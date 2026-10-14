// Package gitlabsrc is the release-train monitor's GitLab source: it plans and
// fetches milestones, MRs, MR details, pipeline jobs, tags, compares and the
// token's own user.
package gitlabsrc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	scmgitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/gitlab"
	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	svc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
)

// Options wires a Source. Tokens builds the credential chain for a host.
type Options struct {
	Tokens func(host string) scmgitlab.TokenSource
	// APIBase defaults to https://<host>/api/v4.
	APIBase   func(host string) string
	Transport http.RoundTripper
}

// Source is the GitLab Source.
type Source struct {
	opts Options

	mu      sync.Mutex
	clients map[string]*scmgitlab.Client
	tokens  map[string]scmgitlab.TokenSource
}

var _ svc.Source = (*Source)(nil)

// New returns a GitLab Source whose requests all go through svc.CountingTransport.
func New(opts Options) *Source {
	if opts.APIBase == nil {
		opts.APIBase = func(host string) string { return "https://" + host + "/api/v4" }
	}
	return &Source{opts: opts, clients: map[string]*scmgitlab.Client{}, tokens: map[string]scmgitlab.TokenSource{}}
}

// System implements svc.Source.
func (s *Source) System() string { return "gitlab" }

func (s *Source) client(host string) (*scmgitlab.Client, scmgitlab.TokenSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.clients[host]; ok {
		return c, s.tokens[host]
	}
	tokens := s.opts.Tokens(host)
	c := scmgitlab.NewClient(scmgitlab.ClientOptions{
		APIBase:    s.opts.APIBase(host),
		Token:      tokens,
		HTTPClient: &http.Client{Timeout: 30 * time.Second, Transport: svc.CountingTransport{Base: s.opts.Transport}},
	})
	s.clients[host], s.tokens[host] = c, tokens
	return c, tokens
}

// Credentials implements svc.Source: a token for the configured host, without a request.
func (s *Source) Credentials(ctx context.Context, view rt.PlanView) (bool, error) {
	_, tokens := s.client(view.Config.GitLabHost)
	if _, err := tokens.Token(ctx); err != nil {
		if errors.Is(err, scmgitlab.ErrNoToken) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Plan implements svc.Source.
func (s *Source) Plan(view rt.PlanView) []rt.ResourceSpec { return svc.Specs(s.jobs(view)) }

// Fetch implements svc.Source.
func (s *Source) Fetch(ctx context.Context, spec rt.ResourceSpec, view rt.PlanView, cursor []byte) (svc.Batch, error) {
	b, err := svc.FetchJob(ctx, s.jobs(view), spec, cursor)
	if err != nil {
		return svc.Batch{}, classify(err)
	}
	return b, nil
}

func (s *Source) jobs(view rt.PlanView) []svc.Job {
	c, _ := s.client(view.Config.GitLabHost)
	r := reader{c: c, view: view}
	var jobs []svc.Job
	for _, a := range view.Apps {
		p, scope := a.GitLabProject, rt.ProjectScope(a.GitLabProject)
		jobs = append(jobs,
			svc.Job{Spec: rt.SpecFor(rt.KindMilestones, scope, a.ID), Fetch: func(ctx context.Context, _ []byte) (svc.Batch, error) { return r.milestones(ctx, p) }},
			svc.Job{Spec: rt.SpecFor(rt.KindMRs, scope, a.ID), Fetch: func(ctx context.Context, cur []byte) (svc.Batch, error) { return r.mrs(ctx, p, cur) }},
			svc.Job{Spec: rt.SpecFor(rt.KindTags, scope, a.ID), Fetch: func(ctx context.Context, _ []byte) (svc.Batch, error) { return r.tags(ctx, p) }},
		)
	}
	seen := map[rt.Scope]bool{}
	add := func(j svc.Job) {
		if !seen[j.Spec.Scope] {
			seen[j.Spec.Scope] = true
			jobs = append(jobs, j)
		}
	}
	for _, v := range view.Versions {
		p, iid, pipeline, tag := v.Project, v.ReleaseMR, v.Pipeline, v.Tag
		if v.Live && iid > 0 {
			add(svc.Job{Spec: rt.SpecFor(rt.KindMRDetail, rt.MRScope(p, iid), v.App), Fetch: func(ctx context.Context, _ []byte) (svc.Batch, error) { return r.detail(ctx, p, iid) }})
		}
		if v.Live && pipeline > 0 {
			add(svc.Job{Spec: rt.SpecFor(rt.KindJobs, rt.PipelineScope(p, pipeline), v.App), Fetch: func(ctx context.Context, _ []byte) (svc.Batch, error) { return r.jobs(ctx, p, pipeline) }})
		}
		if tag != "" {
			add(svc.Job{Spec: rt.SpecFor(rt.KindCompare, rt.CompareScope(p, tag, "main"), v.App), Fetch: func(ctx context.Context, _ []byte) (svc.Batch, error) { return r.compare(ctx, p, tag, "main") }})
		}
	}
	jobs = append(jobs, svc.Job{Spec: rt.SpecFor(rt.KindGitLabUser, rt.MyselfScope, ""), Fetch: r.user})
	return jobs
}

type reader struct {
	c    *scmgitlab.Client
	view rt.PlanView
}

func (r reader) day(t time.Time) rt.Day { return rt.DayOf(t, r.view.Loc) }

func (r reader) historyStart() time.Time {
	d := r.view.HistoryFrom
	return time.Date(d.Y, d.M, d.D, 0, 0, 0, 0, time.UTC)
}

func (r reader) milestones(ctx context.Context, project int) (svc.Batch, error) {
	rows, err := r.c.ListMilestones(ctx, project, scmgitlab.ListMilestonesOptions{UpdatedAfter: r.historyStart(), State: "all"})
	if err != nil {
		return svc.Batch{}, err
	}
	facts := make([]rt.MilestoneFact, 0, len(rows))
	for _, m := range rows {
		due, err := optDay(m.DueDate)
		if err != nil {
			return svc.Batch{}, err
		}
		if due == nil || due.Before(r.view.HistoryFrom) {
			continue
		}
		start, err := optDay(m.StartDate)
		if err != nil {
			return svc.Batch{}, err
		}
		facts = append(facts, rt.MilestoneFact{Project: project, ID: m.ID, Title: m.Title, Description: m.Description, State: m.State, Start: start, Due: due, URL: m.WebURL})
	}
	return svc.SnapshotOf(rt.KindMilestones, facts)
}

func optDay(s string) (*rt.Day, error) {
	if s == "" {
		return nil, nil
	}
	d, err := rt.ParseDay(s)
	return &d, err
}

func (r reader) tags(ctx context.Context, project int) (svc.Batch, error) {
	rows, err := r.c.ListTags(ctx, project, scmgitlab.ListTagsOptions{OrderBy: "updated"})
	if err != nil {
		return svc.Batch{}, err
	}
	facts := make([]rt.TagFact, 0, len(rows))
	for _, t := range rows {
		facts = append(facts, rt.TagFact{Project: project, Name: t.Name, CommitOn: r.day(t.Commit.CreatedAt), URL: t.Commit.WebURL})
	}
	return svc.SnapshotOf(rt.KindTags, facts)
}

func (r reader) detail(ctx context.Context, project, iid int) (svc.Batch, error) {
	d, err := r.c.MergeRequestDetail(ctx, project, iid)
	if err != nil {
		return svc.Batch{}, err
	}
	f := rt.MRDetailFact{Project: project, IID: iid, Conflicts: d.HasConflicts}
	if p := d.HeadPipeline; p != nil {
		f.Pipeline = &rt.PipelineFact{ID: p.ID, Status: p.Status, URL: p.WebURL, Duration: p.Duration}
	}
	return svc.SnapshotOf(rt.KindMRDetail, []rt.MRDetailFact{f})
}

func (r reader) jobs(ctx context.Context, project, pipeline int) (svc.Batch, error) {
	rows, err := r.c.ListPipelineJobs(ctx, project, pipeline, 0)
	if err != nil {
		return svc.Batch{}, err
	}
	f := rt.JobsFact{Project: project, Pipeline: pipeline, Jobs: make([]rt.JobFact, 0, len(rows))}
	for _, j := range rows {
		f.Jobs = append(f.Jobs, rt.JobFact{Name: j.Name, Status: j.Status, AllowFailure: j.AllowFailure})
	}
	return svc.SnapshotOf(rt.KindJobs, []rt.JobsFact{f})
}

func (r reader) compare(ctx context.Context, project int, from, to string) (svc.Batch, error) {
	c, err := r.c.Compare(ctx, project, from, to)
	if err != nil {
		return svc.Batch{}, err
	}
	f := rt.CompareFact{Project: project, From: from, To: to, URL: c.WebURL, Commits: make([]rt.CommitFact, 0, len(c.Commits))}
	for _, cm := range c.Commits {
		f.Commits = append(f.Commits, rt.CommitFact{Title: cm.Title, CreatedOn: r.day(cm.CreatedAt), URL: cm.WebURL})
	}
	return svc.SnapshotOf(rt.KindCompare, []rt.CompareFact{f})
}

func (r reader) user(ctx context.Context, _ []byte) (svc.Batch, error) {
	u, err := r.c.CurrentUser(ctx)
	if err != nil {
		return svc.Batch{}, err
	}
	return svc.SnapshotOf(rt.KindGitLabUser, []rt.GitLabUserFact{{Username: u.Username, Name: u.Name}})
}

// mrCursor is the keyset position: updated_after (inclusive, whole seconds as
// GitLab's filter takes them) and the page within that second's run.
type mrCursor struct {
	UpdatedAfter time.Time `json:"updatedAfter"`
	Page         int       `json:"page"`
}

func (r reader) mrs(ctx context.Context, project int, raw []byte) (svc.Batch, error) {
	cur := mrCursor{UpdatedAfter: r.historyStart(), Page: 1}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cur); err != nil {
			return svc.Batch{}, err
		}
	}
	page, err := r.c.ListMergeRequestsPage(ctx, project, scmgitlab.ListMRsOptions{UpdatedAfter: cur.UpdatedAfter, Page: cur.Page})
	if err != nil {
		return svc.Batch{}, err
	}
	b := svc.Batch{Complete: !page.Full, Items: make([]rt.Item, 0, len(page.Items))}
	for _, m := range page.Items {
		f := rt.MRFact{
			Project: project, IID: m.IID, Title: m.Title, SourceBranch: m.SourceBranch, TargetBranch: m.TargetBranch, State: m.State,
			Author: m.Author.Name, AuthorUsername: m.Author.Username, CreatedOn: r.day(m.CreatedAt), MilestoneID: m.MilestoneID, URL: m.WebURL,
		}
		if m.MergedAt != nil {
			d := r.day(*m.MergedAt)
			f.MergedOn = &d
		}
		updated := m.UpdatedAt.UTC()
		it, err := rt.EncodeItem(rt.KindMRs, f, &updated)
		if err != nil {
			return svc.Batch{}, err
		}
		b.Items = append(b.Items, it)
	}
	b.Cursor, err = json.Marshal(nextCursor(cur, page))
	return b, err
}

func nextCursor(cur mrCursor, page scmgitlab.MRPage) mrCursor {
	if len(page.Items) == 0 {
		return cur
	}
	last := page.Items[len(page.Items)-1].UpdatedAt.UTC().Truncate(time.Second)
	if page.Full && last.Equal(cur.UpdatedAfter) {
		return mrCursor{UpdatedAfter: cur.UpdatedAfter, Page: cur.Page + 1}
	}
	return mrCursor{UpdatedAfter: last, Page: 1}
}

func classify(err error) error {
	var fe *svc.FetchError
	if errors.As(err, &fe) {
		return fe
	}
	var rl *scmgitlab.RateLimitError
	switch {
	case errors.As(err, &rl):
		return &svc.FetchError{Class: rt.ErrRateLimited, RetryAfter: rl.RetryAfter, Err: err}
	case errors.Is(err, scmgitlab.ErrAuthFailed):
		return &svc.FetchError{Class: rt.ErrAuth, Err: err}
	case errors.Is(err, scmgitlab.ErrIncomplete):
		return &svc.FetchError{Class: rt.ErrTruncated, Err: err}
	case errors.Is(err, scmgitlab.ErrNotFound):
		return &svc.FetchError{Class: rt.ErrNotFound, Err: err}
	case svc.IsDecodeError(err):
		return &svc.FetchError{Class: rt.ErrDecode, Err: err}
	}
	return &svc.FetchError{Class: rt.ErrUnavailable, Err: err}
}

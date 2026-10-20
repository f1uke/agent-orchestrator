// Package jirasrc is the release-train monitor's Jira source.
package jirasrc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/atlassian"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira"
	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	svc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
)

// Options configures a Source.
type Options struct {
	Config    atlassian.ConfigSource
	Transport http.RoundTripper
	// Avatars receives each user's avatar on the user read; nil skips the download.
	Avatars *svc.AvatarStore
}

// Source is the Jira Source.
type Source struct {
	config  atlassian.ConfigSource
	client  *jira.Client
	avatars *svc.AvatarStore
}

var _ svc.Source = (*Source)(nil)

// New returns a Jira Source.
func New(opts Options) *Source {
	if opts.Config == nil {
		opts.Config = atlassian.DefaultConfigSource
	}
	httpClient := &http.Client{Timeout: 15 * time.Second, Transport: svc.CountingTransport{Base: opts.Transport}}
	return &Source{config: opts.Config, avatars: opts.Avatars, client: jira.NewClient(jira.WithHTTPDoer(httpClient.Do), jira.WithConfigSource(opts.Config))}
}

// System implements svc.Source.
func (s *Source) System() string { return "jira" }

// Host implements svc.Source: Jira shares the Atlassian budget and pauses with Confluence.
func (s *Source) Host() string { return "atlassian" }

// Credentials implements svc.Source.
func (s *Source) Credentials(context.Context, rt.PlanView) (bool, error) {
	if _, err := s.config(); err != nil {
		if errors.Is(err, atlassian.ErrAuthFailed) || errors.Is(err, atlassian.ErrUnavailable) {
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

func projectScope(key string) rt.Scope { return rt.Scope("project:" + key) }

func (s *Source) jobs(view rt.PlanView) []svc.Job {
	r := reader{c: s.client, view: view, config: s.config, avatars: s.avatars}
	cfg := view.Config.Jira
	jobs := []svc.Job{{Spec: rt.SpecFor(rt.KindJiraVersions, projectScope(cfg.Project), ""), Fetch: r.versions}}
	if cfg.TechRequestProject != "" {
		jobs = append(jobs, svc.Job{Spec: rt.SpecFor(rt.KindTechReq, projectScope(cfg.TechRequestProject), ""), Fetch: r.techReq})
	}
	seen := map[string]bool{}
	for _, v := range view.Versions {
		id, app := v.JiraVersionID, v.App
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		related, cards := rt.SpecFor(rt.KindRelatedWork, rt.VersionScope(id), app), rt.SpecFor(rt.KindCards, rt.VersionScope(id), app)
		if !v.Live {
			related.Budget, cards.Budget = rt.SettledBudget, rt.SettledBudget
		}
		jobs = append(jobs,
			svc.Job{Spec: related, Fetch: func(ctx context.Context, _ []byte) (svc.Batch, error) { return r.relatedWork(ctx, id) }},
			svc.Job{Spec: cards, Fetch: func(ctx context.Context, _ []byte) (svc.Batch, error) { return r.cards(ctx, id) }},
		)
	}
	for _, p := range view.Pages {
		keys, spec := p.Keys, rt.SpecFor(rt.KindPageCards, rt.PageScope(p.ID), "")
		if !p.Live {
			spec.Budget = rt.SettledBudget
		}
		jobs = append(jobs, svc.Job{Spec: spec, Fetch: func(ctx context.Context, _ []byte) (svc.Batch, error) { return r.pageCards(ctx, keys) }})
	}
	for _, id := range view.PersonIDs {
		jobs = append(jobs, svc.Job{Spec: rt.SpecFor(rt.KindJiraUsers, rt.UserScope(id), ""), Fetch: func(ctx context.Context, _ []byte) (svc.Batch, error) { return r.user(ctx, id) }})
	}
	if keys := view.LLBCardKeys; len(keys) > 0 {
		jobs = append(jobs, svc.Job{Spec: rt.SpecFor(rt.KindLLBCards, rt.LLBCardsScope, ""), Fetch: func(ctx context.Context, _ []byte) (svc.Batch, error) {
			return r.issues(ctx, rt.KindLLBCards, "key in ("+strings.Join(keys, ", ")+") ORDER BY key")
		}})
	}
	if me, keys := view.MyAccountID, view.WatchedCardKeys; me != "" && len(keys) > 0 {
		jobs = append(jobs, svc.Job{Spec: rt.SpecFor(rt.KindMySubtasks, rt.MySubtasksScope, ""), Fetch: func(ctx context.Context, _ []byte) (svc.Batch, error) {
			return r.issues(ctx, rt.KindMySubtasks, "parent in ("+strings.Join(keys, ", ")+") AND assignee = "+jqlString(me)+" ORDER BY key")
		}})
	}
	for _, b := range view.Config.Boards {
		board := b.ID
		jobs = append(jobs, svc.Job{Spec: rt.SpecFor(rt.KindBoardSprints, rt.BoardScope(board), ""), Fetch: func(ctx context.Context, _ []byte) (svc.Batch, error) { return r.sprints(ctx, board) }})
	}
	for _, sp := range view.Sprints {
		id := strconv.Itoa(sp.ID)
		jobs = append(jobs,
			svc.Job{Spec: rt.SpecFor(rt.KindSprintCards, rt.SprintScope(sp.ID), ""), Fetch: func(ctx context.Context, _ []byte) (svc.Batch, error) {
				return r.issues(ctx, rt.KindSprintCards, "sprint = "+id+" ORDER BY key")
			}},
			svc.Job{Spec: rt.SpecFor(rt.KindThisSprint, rt.SprintScope(sp.ID), ""), Fetch: func(ctx context.Context, _ []byte) (svc.Batch, error) {
				return r.sprintMembers(ctx, "sprint = "+id+" AND sprint not in closedSprints() ORDER BY key")
			}},
		)
	}
	if keys := view.BranchCardKeys; len(keys) > 0 {
		jobs = append(jobs, svc.Job{Spec: rt.SpecFor(rt.KindBranchCards, rt.BranchCardsScope, ""), Fetch: func(ctx context.Context, _ []byte) (svc.Batch, error) {
			return r.issues(ctx, rt.KindBranchCards, "key in ("+strings.Join(keys, ", ")+") ORDER BY key")
		}})
	}
	return append(jobs, svc.Job{Spec: rt.SpecFor(rt.KindJiraMyself, rt.MyselfScope, ""), Fetch: r.myself})
}

type reader struct {
	c       *jira.Client
	view    rt.PlanView
	config  atlassian.ConfigSource
	avatars *svc.AvatarStore
}

func (r reader) sprints(ctx context.Context, board int) (svc.Batch, error) {
	rows, err := r.c.BoardSprints(ctx, board, []string{string(rt.SprintActive), string(rt.SprintFuture)}, 0)
	if err != nil {
		return svc.Batch{}, err
	}
	cfg, err := r.config()
	if err != nil {
		return svc.Batch{}, err
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	facts := make([]rt.SprintFact, 0, len(rows))
	for _, s := range rows {
		f := rt.SprintFact{Board: board, ID: s.ID, Name: s.Name, State: rt.SprintState(s.State), URL: base + "/issues/?jql=sprint%20%3D%20" + strconv.Itoa(s.ID)}
		if s.StartDate != nil {
			d := rt.DayOf(*s.StartDate, r.view.Loc)
			f.Start = &d
		}
		if s.EndDate != nil {
			d := sprintEndDay(*s.EndDate, r.view.Loc)
			f.End = &d
		}
		facts = append(facts, f)
	}
	return svc.SnapshotOf(rt.KindBoardSprints, facts)
}

// sprintEndDay is the last day a sprint runs: an end instant at exactly local midnight closes the day before.
func sprintEndDay(end time.Time, loc *time.Location) rt.Day {
	local := end.In(loc)
	if local.Hour() == 0 && local.Minute() == 0 && local.Second() == 0 && local.Nanosecond() == 0 {
		return rt.DayOf(local.AddDate(0, 0, -1), loc)
	}
	return rt.DayOf(local, loc)
}

func (r reader) sprintMembers(ctx context.Context, jql string) (svc.Batch, error) {
	rows, err := r.c.SearchAllFields(ctx, jql, []string{"summary"}, 0)
	if err != nil {
		return svc.Batch{}, err
	}
	facts := make([]rt.SprintMemberFact, 0, len(rows))
	for _, row := range rows {
		facts = append(facts, rt.SprintMemberFact{Key: row.Key})
	}
	return svc.SnapshotOf(rt.KindThisSprint, facts)
}

func (r reader) versions(ctx context.Context, _ []byte) (svc.Batch, error) {
	rows, err := r.c.ProjectVersions(ctx, r.view.Config.Jira.Project, 0)
	if err != nil {
		return svc.Batch{}, err
	}
	facts := make([]rt.JiraVersionFact, 0, len(rows))
	for _, v := range rows {
		f := rt.JiraVersionFact{ID: v.ID, Name: v.Name, Description: v.Description, Released: v.Released, URL: v.URL, Approvers: make([]rt.Approver, 0, len(v.Approvers))}
		if v.ReleaseDate != "" {
			d, err := rt.ParseDay(v.ReleaseDate)
			if err != nil {
				return svc.Batch{}, &svc.FetchError{Class: rt.ErrDecode, Err: err}
			}
			if d.Before(r.view.HistoryFrom) {
				continue
			}
			f.ReleaseDate = &d
		}
		for _, a := range v.Approvers {
			f.Approvers = append(f.Approvers, rt.Approver{AccountID: a.AccountID, Status: a.Status})
		}
		facts = append(facts, f)
	}
	return svc.SnapshotOf(rt.KindJiraVersions, facts)
}

func (r reader) relatedWork(ctx context.Context, versionID string) (svc.Batch, error) {
	links, err := r.c.VersionRelatedWork(ctx, versionID)
	if err != nil {
		return svc.Batch{}, err
	}
	f := rt.RelatedWorkFact{VersionID: versionID, Links: make([]rt.RelatedWork, 0, len(links))}
	for _, l := range links {
		f.Links = append(f.Links, rt.RelatedWork{Category: l.Category, Title: l.Title, URL: l.URL})
	}
	return svc.SnapshotOf(rt.KindRelatedWork, []rt.RelatedWorkFact{f})
}

func (r reader) cards(ctx context.Context, versionID string) (svc.Batch, error) {
	return r.issues(ctx, rt.KindCards, "fixVersion = "+versionID+" ORDER BY key")
}

// pageCards reads the cards a train page lists; keys Jira does not know are left out.
func (r reader) pageCards(ctx context.Context, keys []string) (svc.Batch, error) {
	if len(keys) == 0 {
		return svc.SnapshotOf(rt.KindPageCards, nil)
	}
	return r.issues(ctx, rt.KindPageCards, "key in ("+strings.Join(keys, ", ")+") ORDER BY key")
}

func (r reader) issues(ctx context.Context, kind rt.Kind[rt.IssueFact], jql string) (svc.Batch, error) {
	rows, err := r.c.SearchAllFields(ctx, jql, nil, 0)
	if err != nil {
		return svc.Batch{}, err
	}
	ticket := r.releaseTicketPattern()
	facts := make([]rt.IssueFact, 0, len(rows))
	for _, row := range rows {
		f := rt.IssueFact{
			Key: row.Key, Summary: row.Summary, Status: row.Status, StatusCategory: row.StatusCategory, Subtask: row.Subtask,
			ReleaseTicket: ticket != nil && !row.Subtask && ticket.MatchString(row.Summary), FixVersions: row.FixVersionIDs, IssueType: row.IssueType, Parent: row.ParentKey, ParentType: row.ParentType, URL: row.URL,
		}
		if a := row.Assignee; a != nil {
			f.Assignee = &rt.Person{AccountID: a.AccountID, Name: a.DisplayName}
		}
		facts = append(facts, f)
	}
	return svc.SnapshotOf(kind, facts)
}

func (r reader) releaseTicketPattern() *regexp.Regexp {
	var labels []string
	for _, f := range r.view.Config.Families {
		if f.Label != "" {
			labels = append(labels, regexp.QuoteMeta(f.Label))
		}
	}
	if len(labels) == 0 {
		return nil
	}
	return regexp.MustCompile(`(?i)\bRelease\s+(?:` + strings.Join(labels, "|") + `)\s+\d`)
}

var platformRE = regexp.MustCompile(`(?i)\b(iOS|ADR|Android)\b`)

func (r reader) techReq(ctx context.Context, _ []byte) (svc.Batch, error) {
	cfg := r.view.Config.Jira
	jql := fmt.Sprintf(`project = %s AND summary ~ %s AND created >= %q ORDER BY key`, cfg.TechRequestProject, jqlString(cfg.ForceUpdateSummary), r.view.HistoryFrom.String())
	rows, err := r.c.SearchAllFields(ctx, jql, nil, 0)
	if err != nil {
		return svc.Batch{}, err
	}
	facts := make([]rt.ForceUpdateFact, 0, len(rows))
	for _, row := range rows {
		f, ok := r.forceUpdate(row)
		if ok {
			facts = append(facts, f)
		}
	}
	return svc.SnapshotOf(rt.KindTechReq, facts)
}

func (r reader) forceUpdate(row jira.IssueRow) (rt.ForceUpdateFact, bool) {
	if !strings.Contains(strings.ToLower(row.Summary), strings.ToLower(r.view.Config.Jira.ForceUpdateSummary)) {
		return rt.ForceUpdateFact{}, false
	}
	pm := platformRE.FindString(row.Summary)
	v, ok := rt.ParseVersion(row.Summary)
	if pm == "" || !ok {
		return rt.ForceUpdateFact{}, false
	}
	platform := rt.PlatformAndroid
	if strings.EqualFold(pm, "ios") {
		platform = rt.PlatformIOS
	}
	f := rt.ForceUpdateFact{
		Key: row.Key, Status: row.Status, StatusCategory: row.StatusCategory, Platform: platform, Version: v.String(),
		CreatedOn: rt.DayOf(row.Created, r.view.Loc), URL: row.URL,
	}
	if row.DueDate != "" {
		if d, err := rt.ParseDay(row.DueDate); err == nil {
			f.Due = &d
		}
	}
	return f, true
}

func jqlString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func (r reader) user(ctx context.Context, accountID string) (svc.Batch, error) {
	u, err := r.c.User(ctx, accountID)
	if err != nil {
		return svc.Batch{}, err
	}
	r.saveAvatar(ctx, u)
	return svc.SnapshotOf(rt.KindJiraUsers, []rt.JiraUserFact{{AccountID: u.AccountID, DisplayName: u.DisplayName, AvatarURL: u.AvatarURL}})
}

// saveAvatar is best-effort: a missing face falls back to initials, so it never fails the profile read.
func (r reader) saveAvatar(ctx context.Context, u jira.UserProfile) {
	if r.avatars == nil || u.AvatarURL == "" {
		return
	}
	img, err := r.c.Avatar(ctx, u.AvatarURL)
	if err != nil {
		return
	}
	_ = r.avatars.Put(u.AccountID, img.Bytes)
}

func (r reader) myself(ctx context.Context, _ []byte) (svc.Batch, error) {
	u, err := r.c.Myself(ctx)
	if err != nil {
		return svc.Batch{}, err
	}
	return svc.SnapshotOf(rt.KindJiraMyself, []rt.JiraMyselfFact{{AccountID: u.AccountID, DisplayName: u.DisplayName}})
}

// Jira answers a JQL query naming a missing project or version with a 400.
func classify(err error) error {
	var fe *svc.FetchError
	if errors.As(err, &fe) {
		return fe
	}
	var rl *atlassian.RateLimitError
	switch {
	case errors.As(err, &rl):
		return &svc.FetchError{Class: rt.ErrRateLimited, RetryAfter: rl.RetryAfter, Err: err}
	case errors.Is(err, atlassian.ErrAuthFailed):
		return &svc.FetchError{Class: rt.ErrAuth, Err: err}
	case errors.Is(err, jira.ErrIncomplete):
		return &svc.FetchError{Class: rt.ErrTruncated, Err: err}
	case errors.Is(err, jira.ErrNotFound), errors.Is(err, jira.ErrBadQuery), errors.Is(err, jira.ErrBadRequest):
		return &svc.FetchError{Class: rt.ErrNotFound, Err: err}
	case svc.IsDecodeError(err):
		return &svc.FetchError{Class: rt.ErrDecode, Err: err}
	}
	return &svc.FetchError{Class: rt.ErrUnavailable, Err: err}
}

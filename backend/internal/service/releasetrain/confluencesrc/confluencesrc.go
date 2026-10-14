// Package confluencesrc is the release-train monitor's Confluence source.
package confluencesrc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/atlassian"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/confluence"
	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	svc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
)

// Options configures a Source.
type Options struct {
	Config    atlassian.ConfigSource
	Transport http.RoundTripper
}

// Source is the Confluence Source.
type Source struct {
	config atlassian.ConfigSource
	client *confluence.Client
}

var _ svc.Source = (*Source)(nil)

// New returns a Confluence Source.
func New(opts Options) *Source {
	if opts.Config == nil {
		opts.Config = atlassian.DefaultConfigSource
	}
	httpClient := &http.Client{Timeout: 30 * time.Second, Transport: svc.CountingTransport{Base: opts.Transport}}
	return &Source{config: opts.Config, client: confluence.NewClient(confluence.WithHTTPDoer(httpClient.Do), confluence.WithConfigSource(opts.Config))}
}

// System implements svc.Source.
func (s *Source) System() string { return "confluence" }

// Host implements svc.Source: Confluence shares the Atlassian budget and pauses with Jira.
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

// Plan implements svc.Source: one resource for the configured space, none without a confluence section.
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
	cfg := view.Config.Confluence
	if cfg == nil {
		return nil
	}
	fetch := func(ctx context.Context, _ []byte) (svc.Batch, error) { return s.pages(ctx, view) }
	return []svc.Job{{Spec: rt.SpecFor(rt.KindPages, rt.SpaceScope(cfg.Space), ""), Fetch: fetch}}
}

// pageLead is how many days before historyFrom a page may be created and still name a train in range.
const pageLead = 60

// CQL is the query for the configured space's train pages created since historyFrom less 60 days.
func CQL(view rt.PlanView) string {
	cfg := view.Config.Confluence
	return fmt.Sprintf(`space = %q AND title ~ %q AND created >= %q`, cfg.Space, cfg.TrainPageTitle, view.HistoryFrom.Add(-pageLead).String())
}

func (s *Source) pages(ctx context.Context, view rt.PlanView) (svc.Batch, error) {
	raw, err := s.client.SearchPages(ctx, CQL(view), 0)
	if err != nil {
		return svc.Batch{}, err
	}
	facts := make([]rt.PageFact, 0, len(raw))
	for _, p := range raw {
		if f, ok := rt.ParseTrainPage(view.Config, view.Loc, rt.RawPage{ID: p.ID, Title: p.Title, URL: p.URL, Created: p.Created, Body: p.Body}); ok {
			facts = append(facts, f)
		}
	}
	return svc.SnapshotOf(rt.KindPages, facts)
}

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
	case errors.Is(err, confluence.ErrIncomplete):
		return &svc.FetchError{Class: rt.ErrTruncated, Err: err}
	case errors.Is(err, confluence.ErrNotFound), errors.Is(err, confluence.ErrBadQuery):
		return &svc.FetchError{Class: rt.ErrNotFound, Err: err}
	case svc.IsDecodeError(err):
		return &svc.FetchError{Class: rt.ErrDecode, Err: err}
	}
	return &svc.FetchError{Class: rt.ErrUnavailable, Err: err}
}

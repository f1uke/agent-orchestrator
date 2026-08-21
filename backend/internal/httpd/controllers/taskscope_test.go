package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	smokesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/smoke"
)

// The task under test: dev owns the branch, the pull request and the checklist;
// qa shares them. Every task-scoped route named with qa's id must reach the
// service with DEV's.
const (
	scopeDev domain.SessionID = "task-dev"
	scopeQA  domain.SessionID = "task-qa"
	scopeOwn domain.SessionID = "solo-1"
)

// scopeSessions records which id each task-scoped session read was asked for,
// and answers CrewDevOf from a fixed crew map.
type scopeSessions struct {
	*fakeSessionService
	askedPRs      []domain.SessionID
	askedComments []domain.SessionID
	askedSend     []domain.SessionID
	devOf         map[domain.SessionID]domain.SessionID
	devOfErr      error
}

func (s *scopeSessions) TaskDevOf(_ context.Context, id domain.SessionID) (domain.SessionID, error) {
	if s.devOfErr != nil {
		return "", s.devOfErr
	}
	if dev, ok := s.devOf[id]; ok {
		return dev, nil
	}
	return id, nil
}

func (s *scopeSessions) ListPRSummaries(_ context.Context, id domain.SessionID) ([]sessionsvc.PRSummary, error) {
	s.askedPRs = append(s.askedPRs, id)
	return nil, nil
}

func (s *scopeSessions) ListPRCommentThreads(_ context.Context, id domain.SessionID) ([]sessionsvc.PRCommentGroup, error) {
	s.askedComments = append(s.askedComments, id)
	return nil, nil
}

func (s *scopeSessions) SendFrom(_ context.Context, id domain.SessionID, _ string, _ sessionsvc.CrewTalk) (ports.SendOutcome, error) {
	s.askedSend = append(s.askedSend, id)
	return ports.SendOutcome{}, nil
}

// scopeSmoke records which id the checklist was read for.
type scopeSmoke struct {
	*fakeSmokeService
	asked []domain.SessionID
}

func (s *scopeSmoke) List(_ context.Context, id domain.SessionID) (smokesvc.SessionSmoke, error) {
	s.asked = append(s.asked, id)
	return smokesvc.SessionSmoke{Worker: string(id)}, nil
}

// scopeReviews records which id AO's review verdicts were read for.
type scopeReviews struct {
	*fakeReviewService
	asked []domain.SessionID
}

func (s *scopeReviews) List(_ context.Context, id domain.SessionID) (reviewcore.SessionReviews, error) {
	s.asked = append(s.asked, id)
	return reviewcore.SessionReviews{}, nil
}

func newScopeServer(t *testing.T, crew map[domain.SessionID]domain.SessionID) (*httptest.Server, *scopeSessions, *scopeSmoke, *scopeReviews) {
	t.Helper()
	sessions := &scopeSessions{fakeSessionService: newFakeSessionService(), devOf: crew}
	smoke := &scopeSmoke{fakeSmokeService: &fakeSmokeService{}}
	reviews := &scopeReviews{fakeReviewService: &fakeReviewService{}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Sessions: sessions,
		Smoke:    smoke,
		Reviews:  reviews,
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv, sessions, smoke, reviews
}

func lastID(t *testing.T, got []domain.SessionID) domain.SessionID {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("service was asked %d times, want exactly 1: %v", len(got), got)
	}
	return got[0]
}

// The checklist, the pull request, its comment threads and AO's review verdicts
// all belong to the TASK. Opening any of them on a crew's qa must answer with
// the task's - which is dev's - or the tab shows an empty list beside dev's full
// one, and the readiness strip computes a merge verdict for a task that appears
// to have no pull request at all.
func TestTaskScopedReadsResolveToTheTasksDev(t *testing.T) {
	crew := map[domain.SessionID]domain.SessionID{scopeQA: scopeDev}
	for _, tc := range []struct {
		name string
		path string
		got  func(*scopeSessions, *scopeSmoke, *scopeReviews) []domain.SessionID
	}{
		{"smoke checklist", "/smoke-checks", func(_ *scopeSessions, sm *scopeSmoke, _ *scopeReviews) []domain.SessionID {
			return sm.asked
		}},
		{"pull requests", "/pr", func(se *scopeSessions, _ *scopeSmoke, _ *scopeReviews) []domain.SessionID {
			return se.askedPRs
		}},
		{"pr comments", "/pr-comments", func(se *scopeSessions, _ *scopeSmoke, _ *scopeReviews) []domain.SessionID {
			return se.askedComments
		}},
		{"reviews", "/reviews", func(_ *scopeSessions, _ *scopeSmoke, rv *scopeReviews) []domain.SessionID {
			return rv.asked
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, sessions, smoke, reviews := newScopeServer(t, crew)
			body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/"+string(scopeQA)+tc.path, "")
			if status != http.StatusOK {
				t.Fatalf("status = %d body=%s", status, body)
			}
			if id := lastID(t, tc.got(sessions, smoke, reviews)); id != scopeDev {
				t.Fatalf("service asked for %q, want the task's dev %q", id, scopeDev)
			}
		})
	}
}

// A solo session IS its own task, so every read stays byte-identical: the id the
// path names is the id the service is asked for. This is the overwhelming
// majority of real traffic.
func TestTaskScopedReadsLeaveASoloSessionAlone(t *testing.T) {
	srv, sessions, smoke, reviews := newScopeServer(t, nil)
	for _, path := range []string{"/smoke-checks", "/pr", "/pr-comments", "/reviews"} {
		if _, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/"+string(scopeOwn)+path, ""); status != http.StatusOK {
			t.Fatalf("%s: status = %d", path, status)
		}
	}
	for name, got := range map[string][]domain.SessionID{
		"smoke":    smoke.asked,
		"prs":      sessions.askedPRs,
		"comments": sessions.askedComments,
		"reviews":  reviews.asked,
	} {
		if id := lastID(t, got); id != scopeOwn {
			t.Fatalf("%s asked for %q, want the session's own id %q", name, id, scopeOwn)
		}
	}
}

// The opt-in must not leak. A message is delivered to an AGENT, not to a task:
// resolving it would send qa's turn to dev.
func TestAgentScopedRoutesAreNotResolved(t *testing.T) {
	srv, sessions, _, _ := newScopeServer(t, map[domain.SessionID]domain.SessionID{scopeQA: scopeDev})
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/"+string(scopeQA)+"/send", `{"message":"hi"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if id := lastID(t, sessions.askedSend); id != scopeQA {
		t.Fatalf("send reached %q, want the agent it was addressed to, %q", id, scopeQA)
	}
}

// An id the daemon cannot resolve is passed through untouched so the handler
// produces its own answer, rather than the middleware inventing an error of its
// own. A resolver that is down must not take the whole read surface with it.
func TestTaskScopeFallsThroughWhenTheTaskCannotBeResolved(t *testing.T) {
	sessions := &scopeSessions{fakeSessionService: newFakeSessionService(), devOfErr: context.DeadlineExceeded}
	smoke := &scopeSmoke{fakeSmokeService: &fakeSmokeService{}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Sessions: sessions,
		Smoke:    smoke,
		Reviews:  &scopeReviews{fakeReviewService: &fakeReviewService{}},
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	if _, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/"+string(scopeQA)+"/smoke-checks", ""); status != http.StatusOK {
		t.Fatalf("status = %d, want the handler's own answer", status)
	}
	if id := lastID(t, smoke.asked); id != scopeQA {
		t.Fatalf("service asked for %q, want the unresolved path id %q", id, scopeQA)
	}
}

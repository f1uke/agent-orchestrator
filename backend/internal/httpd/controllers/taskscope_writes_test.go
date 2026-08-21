package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

// Answering a task's surfaces for the task is only half of it. What the Tests
// and Reviews tabs OFFER on what they list is a set of WRITES, and a write that
// resolves differently from the read that listed it is worse than the empty tab
// it replaced: qa would play a case whose verdict lands on qa's own empty
// checklist, where dev can never see it, or reply to a thread through a session
// that holds no pull request.
//
// These pin the write half of the same scope, separately from the read tests,
// because the two can drift apart: a controller can be moved into the
// task-scoped group for the GET that motivated it and have the POSTs beside it
// left behind - or a later route can be added to the agent group by habit.

// writeScopeSessions records the session id each task-owned WRITE reached the
// service with.
type writeScopeSessions struct {
	*fakeSessionService
	asked map[string][]domain.SessionID
	devOf map[domain.SessionID]domain.SessionID
}

func (s *writeScopeSessions) note(route string, id domain.SessionID) {
	if s.asked == nil {
		s.asked = map[string][]domain.SessionID{}
	}
	s.asked[route] = append(s.asked[route], id)
}

func (s *writeScopeSessions) TaskDevOf(_ context.Context, id domain.SessionID) (domain.SessionID, error) {
	if dev, ok := s.devOf[id]; ok {
		return dev, nil
	}
	return id, nil
}

func (s *writeScopeSessions) ReplyToThread(_ context.Context, id domain.SessionID, _, _, _ string) (sessionsvc.PRThreadComment, error) {
	s.note("comment-reply", id)
	return sessionsvc.PRThreadComment{}, nil
}

func (s *writeScopeSessions) ResolveThread(_ context.Context, id domain.SessionID, _, _ string) error {
	s.note("comment-resolve", id)
	return nil
}

func (s *writeScopeSessions) DispatchCommentToWorker(_ context.Context, id domain.SessionID, _, _, _ string) error {
	s.note("comment-dispatch", id)
	return nil
}

func (s *writeScopeSessions) ClaimPR(_ context.Context, id domain.SessionID, _ string, _ sessionsvc.ClaimPROptions) (sessionsvc.ClaimPRResult, error) {
	s.note("pr/claim", id)
	return sessionsvc.ClaimPRResult{}, nil
}

// writeScopeSmoke records the session id each checklist write was filed under.
type writeScopeSmoke struct {
	*fakeSmokeService
	asked map[string][]domain.SessionID
}

func (s *writeScopeSmoke) note(route string, id domain.SessionID) {
	if s.asked == nil {
		s.asked = map[string][]domain.SessionID{}
	}
	s.asked[route] = append(s.asked[route], id)
}

func (s *writeScopeSmoke) SetVerdict(_ context.Context, id domain.SessionID, _ string, _ domain.SmokeVerdict, _ string) (domain.SmokeCheck, error) {
	s.note("verdict", id)
	return domain.SmokeCheck{}, nil
}

func (s *writeScopeSmoke) RecordAgentResult(_ context.Context, id domain.SessionID, _ string, _ domain.SmokeAgentResult) (domain.SmokeCheck, error) {
	s.note("agent-result", id)
	return domain.SmokeCheck{}, nil
}

func (s *writeScopeSmoke) Retire(_ context.Context, id domain.SessionID, _, _ string) (domain.SmokeCheck, error) {
	s.note("retire", id)
	return domain.SmokeCheck{}, nil
}

func newWriteScopeServer(t *testing.T, crew map[domain.SessionID]domain.SessionID) (*httptest.Server, *writeScopeSessions, *writeScopeSmoke) {
	t.Helper()
	sessions := &writeScopeSessions{fakeSessionService: newFakeSessionService(), devOf: crew}
	smoke := &writeScopeSmoke{fakeSmokeService: &fakeSmokeService{}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Sessions: sessions,
		Smoke:    smoke,
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv, sessions, smoke
}

// writeCase is one task-owned write, named by the route it posts to.
type writeCase struct {
	route string
	path  string
	body  string
	smoke bool
}

var taskOwnedWrites = []writeCase{
	{route: "verdict", path: "/smoke-checks/c1/verdict", body: `{"verdict":"pass","note":"ok"}`, smoke: true},
	{route: "agent-result", path: "/smoke-checks/c1/agent-result", body: `{"verdict":"pass","note":"ran it"}`, smoke: true},
	{route: "retire", path: "/smoke-checks/c1/retire", body: `{"reason":"covered by a test"}`, smoke: true},
	{route: "comment-reply", path: "/comment-reply", body: `{"prUrl":"https://x/pull/1","threadId":"t1","body":"hi"}`},
	{route: "comment-resolve", path: "/comment-resolve", body: `{"prUrl":"https://x/pull/1","threadId":"t1"}`},
	{route: "comment-dispatch", path: "/comment-dispatch", body: `{"prUrl":"https://x/pull/1","threadId":"t1"}`},
}

func postScoped(t *testing.T, from domain.SessionID, crew map[domain.SessionID]domain.SessionID, tc writeCase) domain.SessionID {
	t.Helper()
	srv, sessions, smoke := newWriteScopeServer(t, crew)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/"+string(from)+tc.path, tc.body)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	got := sessions.asked[tc.route]
	if tc.smoke {
		got = smoke.asked[tc.route]
	}
	if len(got) != 1 {
		t.Fatalf("service was asked %d times, want exactly 1: %v", len(got), got)
	}
	return got[0]
}

// A write launched from qa must land on the row the read that listed it came
// from: the TASK's.
func TestTaskScopedWritesResolveToTheTasksDev(t *testing.T) {
	crew := map[domain.SessionID]domain.SessionID{scopeQA: scopeDev}
	for _, tc := range taskOwnedWrites {
		t.Run(tc.route, func(t *testing.T) {
			if id := postScoped(t, scopeQA, crew, tc); id != scopeDev {
				t.Fatalf("write reached %q, want the task's dev %q", id, scopeDev)
			}
		})
	}
}

// The same writes on a solo session stay byte-identical: the id the path names
// is the id the service is asked for. This is the overwhelming majority of real
// traffic and the thing this change must not touch.
func TestTaskScopedWritesLeaveASoloSessionAlone(t *testing.T) {
	for _, tc := range taskOwnedWrites {
		t.Run(tc.route, func(t *testing.T) {
			if id := postScoped(t, scopeOwn, nil, tc); id != scopeOwn {
				t.Fatalf("write reached %q, want the session's own id %q", id, scopeOwn)
			}
		})
	}
}

// `pr/claim` sits among the PR routes that ARE task-scoped and does the one
// thing on that surface which is not the task's: it BINDS a pull request to the
// claiming session's own row. Resolving it would file qa's claim under dev and
// move the branch's owner without anyone asking, so it must keep the id the path
// names even when that session belongs to a crew.
func TestClaimPRStaysAgentScoped(t *testing.T) {
	srv, sessions, _ := newWriteScopeServer(t, map[domain.SessionID]domain.SessionID{scopeQA: scopeDev})
	raw, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/"+string(scopeQA)+"/pr/claim", `{"pr":"https://x/pull/1"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, raw)
	}
	if got := sessions.asked["pr/claim"]; len(got) != 1 || got[0] != scopeQA {
		t.Fatalf("claim reached %v, want the claiming agent %q", got, scopeQA)
	}
	var got struct {
		SessionID domain.SessionID `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SessionID != scopeQA {
		t.Fatalf("response names %q as the claimer, want %q", got.SessionID, scopeQA)
	}
}

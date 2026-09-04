package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeAliasResolver struct {
	to     domain.SessionID
	handle string
	calls  int
}

func (f *fakeAliasResolver) ResolveSessionAlias(_ context.Context, id domain.SessionID) (domain.SessionID, string, bool) {
	f.calls++
	if f.to == "" || id == f.to {
		return "", "", false
	}
	return f.to, f.handle, true
}

type fakeDevResolver struct{ dev domain.SessionID }

func (f fakeDevResolver) TaskDevOf(_ context.Context, id domain.SessionID) (domain.SessionID, error) {
	if f.dev == "" {
		return id, nil
	}
	return f.dev, nil
}

// serve mounts the middleware the way the daemon does and reports what the
// handler saw.
func serve(t *testing.T, path string, mw ...func(http.Handler) http.Handler) (seen string, rec *httptest.ResponseRecorder) {
	t.Helper()
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		for _, m := range mw {
			r.Use(m)
		}
		r.Get("/sessions/{sessionId}/smoke-checks", func(w http.ResponseWriter, r *http.Request) {
			seen = string(sessionID(r))
			w.WriteHeader(http.StatusOK)
		})
		r.Get("/projects/{projectId}", func(w http.ResponseWriter, r *http.Request) {
			seen = chi.URLParam(r, "projectId")
			w.WriteHeader(http.StatusOK)
		})
	})
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return seen, rec
}

func TestSessionAliasRewritesThePathAndSaysSo(t *testing.T) {
	res := &fakeAliasResolver{to: "advisor-ios-app-9", handle: "advisor-ios-app-feature-MOBILITY-4734"}
	seen, rec := serve(t, "/sessions/mobility-4734-chat-unsafe-url-whitelist-f5/smoke-checks", SessionAlias(res))

	if seen != "advisor-ios-app-9" {
		t.Fatalf("handler saw %q, want the resolved AO id", seen)
	}
	want := "mobility-4734-chat-unsafe-url-whitelist-f5 -> advisor-ios-app-9 (tmux advisor-ios-app-feature-MOBILITY-4734)"
	if got := rec.Header().Get(SessionResolvedHeader); got != want {
		t.Fatalf("header %q, want %q", got, want)
	}
}

func TestSessionAliasLeavesAKnownIDAlone(t *testing.T) {
	res := &fakeAliasResolver{to: "advisor-ios-app-9"}
	seen, rec := serve(t, "/sessions/advisor-ios-app-9/smoke-checks", SessionAlias(res))

	if seen != "advisor-ios-app-9" {
		t.Fatalf("handler saw %q", seen)
	}
	if rec.Header().Get(SessionResolvedHeader) != "" {
		t.Fatal("a request that was never rewritten announced a substitution")
	}
}

func TestSessionAliasIsANoOpWithoutASessionID(t *testing.T) {
	res := &fakeAliasResolver{to: "advisor-ios-app-9"}
	seen, rec := serve(t, "/projects/advisor-ios-app", SessionAlias(res))

	if seen != "advisor-ios-app" {
		t.Fatalf("handler saw %q", seen)
	}
	if res.calls != 0 {
		t.Fatalf("the resolver was consulted %d times on a route with no session", res.calls)
	}
	if rec.Header().Get(SessionResolvedHeader) != "" {
		t.Fatal("a non-session route announced a substitution")
	}
}

func TestSessionAliasNilResolverIsANoOp(t *testing.T) {
	seen, _ := serve(t, "/sessions/whatever/smoke-checks", SessionAlias(nil))
	if seen != "whatever" {
		t.Fatalf("handler saw %q, want the path's id unchanged", seen)
	}
}

// Order matters: TaskScoped reads the same parameter, so the alias has to be
// resolved before it runs or a crew member's alias would never reach dev's
// checklist.
func TestSessionAliasResolvesBeforeTaskScope(t *testing.T) {
	alias := &fakeAliasResolver{to: "advisor-ios-app-10", handle: "advisor-ios-app-10"}
	seen, rec := serve(t,
		"/sessions/mobility-4734-chat-unsafe-url-whitelist-b7/smoke-checks",
		SessionAlias(alias),
		TaskScoped(fakeDevResolver{dev: "advisor-ios-app-9"}),
	)

	if seen != "advisor-ios-app-9" {
		t.Fatalf("handler saw %q; the alias should resolve to qa and task scope should then hand it dev's checklist", seen)
	}
	if rec.Header().Get(SessionResolvedHeader) == "" {
		t.Fatal("the substitution was not announced")
	}
}

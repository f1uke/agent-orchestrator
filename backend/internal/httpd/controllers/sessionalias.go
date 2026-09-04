package controllers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// SessionResolvedHeader announces that the {sessionId} in the path was not an
// AO session id and names the one it stood for, as
// "<given> -> <ao id> (tmux <handle>)".
//
// It is a header rather than a body field on purpose: it applies to every
// session route regardless of what that route returns, and it keeps the typed
// API surface (and therefore the generated OpenAPI document) unchanged.
const SessionResolvedHeader = "X-AO-Session-Resolved"

// SessionAliasResolver answers which AO session a non-AO id names.
// *sessionsvc.Service satisfies it.
type SessionAliasResolver interface {
	ResolveSessionAlias(ctx context.Context, id domain.SessionID) (domain.SessionID, string, bool)
}

// SessionAlias lets a Claude Code session name stand in for an AO session id.
//
// Claude Code names every session after its worktree directory plus a random
// suffix and shows the agent THAT name, so an agent asked to identify itself
// answers with something like "mobility-4734-chat-unsafe-url-whitelist-f5".
// Pasted into `ao send` or `ao smoke list` it resolves to nothing, and the
// message goes nowhere. This resolves it to the AO session that owns the same
// tmux pane and rewrites the path parameter, so every session route - and
// TaskScoped, which runs after this - sees an ordinary AO id.
//
// It NEVER changes what a known id means: an id AO already has wins before the
// registry is even read, and an alias matching zero or several live sessions is
// passed through so the handler returns its own 404.
//
// A resolved request always carries SessionResolvedHeader. That is not
// decoration: a crew's dev and qa share a worktree, a display name and a
// branch, and their Claude names differ only by a random suffix - so a person
// cannot tell from the alias which agent they are about to message. Announcing
// the substitution is what makes a wrong target visible instead of silent.
//
// Mount it once, above TaskScoped, on the group that carries every session
// route (see httpd.API.Register). On a route without a {sessionId} it is a
// no-op, and a nil resolver disables it, which keeps controller tests that wire
// no service able to reach their routes.
func SessionAlias(res SessionAliasResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rctx := chi.RouteContext(r.Context())
			id := domain.SessionID(chi.URLParam(r, "sessionId"))
			if res == nil || rctx == nil || id == "" {
				next.ServeHTTP(w, r)
				return
			}
			resolved, handle, ok := res.ResolveSessionAlias(r.Context(), id)
			if !ok || resolved == "" || resolved == id {
				next.ServeHTTP(w, r)
				return
			}
			setURLParam(rctx, "sessionId", string(resolved))
			w.Header().Set(SessionResolvedHeader, fmt.Sprintf("%s -> %s (tmux %s)", id, resolved, handle))
			next.ServeHTTP(w, r)
		})
	}
}

// setURLParam rewrites a routing parameter in place. chi reads a param from the
// LAST entry with that key, so that is the one to overwrite; a key the router
// never matched is appended, which cannot happen here because the caller has
// already read a non-empty value.
func setURLParam(rctx *chi.Context, key, value string) {
	for i := len(rctx.URLParams.Keys) - 1; i >= 0; i-- {
		if rctx.URLParams.Keys[i] == key {
			rctx.URLParams.Values[i] = value
			return
		}
	}
	rctx.URLParams.Add(key, value)
}

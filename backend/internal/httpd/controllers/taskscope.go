package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TaskResolver answers which TASK a session id belongs to. *sessionsvc.Service
// satisfies it; a solo session resolves to itself.
type TaskResolver interface {
	TaskDevOf(ctx context.Context, id domain.SessionID) (domain.SessionID, error)
}

type taskScopeKey struct{}

// TaskScoped answers the routes it wraps for the TASK rather than for the agent
// whose id the path happens to name.
//
// Some of what a session's routes expose belongs to the AGENT - its pane, its
// process, its messages, its preview, its device lease, the brackets it puts
// around its own runs. The rest belongs to the TASK the agent is on, and is the
// SAME object for every member of it: the branch's pull request, that PR's
// comment threads, AO's review verdicts on it, and the smoke checklist (which
// `ao smoke set` files under $AO_CREW_ID - dev's id - whichever member writes
// it). Read the second kind with a member's own id and a crew's qa gets an empty
// answer: an empty Tests tab beside dev's full one, and a readiness strip
// computing a merge verdict for a task that appears to have no pull request.
//
// Scope is a property of the RESOURCE, so it is declared once where that
// resource's controller is MOUNTED (see httpd.API.Register) rather than in each
// handler. A new route on a task-scoped controller is task-scoped by
// construction; making one wrong again means mounting a task-level surface in
// the agent group deliberately.
//
// It is opt-IN and must stay that way. A task-scoped route left out of the group
// degrades to exactly today's behaviour; an agent-scoped route wrongly swept in
// would send qa's message - or qa's kill - to dev.
//
// Two failures are deliberately quiet. An id that cannot be resolved (unknown
// session, resolver down) passes through unchanged so the handler returns its
// own 404 rather than the middleware inventing an error, and a nil resolver is a
// no-op, which is what keeps a controller test that wires one service still able
// to reach its routes.
func TaskScoped(res TaskResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := domain.SessionID(chi.URLParam(r, "sessionId"))
			if res == nil || id == "" {
				next.ServeHTTP(w, r)
				return
			}
			dev, err := res.TaskDevOf(r.Context(), id)
			if err != nil || dev == "" || dev == id {
				next.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), taskScopeKey{}, dev)))
		})
	}
}

// taskScopeOf returns the task id TaskScoped resolved for this request, if the
// route was mounted task-scoped and the session belongs to a crew.
func taskScopeOf(ctx context.Context) (domain.SessionID, bool) {
	id, ok := ctx.Value(taskScopeKey{}).(domain.SessionID)
	return id, ok && id != ""
}

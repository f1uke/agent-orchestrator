package session

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/claudesessions"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// claudeRegistry is the slice of claudesessions.Registry this file needs, named
// so tests can stand in for it.
type claudeRegistry interface {
	ByName(ctx context.Context, name string) (claudesessions.Session, error)
}

// ResolveSessionAlias reports which AO session a caller meant when the id it
// used is not one of AO's.
//
// The case it exists for: Claude Code gives every session a name of its own
// (derived from the worktree directory plus a random suffix, e.g.
// "mobility-4734-chat-unsafe-url-whitelist-f5"), shows the agent THAT name, and
// an agent asked to identify itself repeats it. It looks enough like an AO id
// to be pasted into `ao send` or `ao smoke list`, and it never resolves - so a
// message goes nowhere, or a checklist reads as empty.
//
// The join is the tmux pane: Claude's registry records the pane its process
// owns, and that pane's session name is exactly AO's runtime handle. Note that
// the WORKTREE cannot be used for this - a crew's dev and qa share one - and
// the two are indistinguishable by display name too, which is why the caller
// announces what it resolved rather than substituting silently.
//
// Three rules keep this from ever changing an existing meaning:
//   - an id AO already knows wins immediately and is returned untouched;
//   - the Claude session must be live and still be a claude process;
//   - the pane must belong to exactly one live AO session.
//
// Anything else is ("", "", false): the caller carries on with the id it was
// given and the handler answers as it always did.
func (s *Service) ResolveSessionAlias(ctx context.Context, id domain.SessionID) (domain.SessionID, string, bool) {
	name := strings.TrimSpace(string(id))
	if name == "" {
		return "", "", false
	}
	// An id AO knows is never an alias, whatever else may share the string.
	if _, ok, err := s.store.GetSession(ctx, id); err != nil || ok {
		return "", "", false
	}
	claude, err := s.claude().ByName(ctx, name)
	if err != nil || claude.TmuxSession == "" {
		return "", "", false
	}
	records, err := s.store.ListAllSessions(ctx)
	if err != nil {
		return "", "", false
	}
	var match domain.SessionRecord
	found := 0
	for _, rec := range records {
		if rec.IsTerminated || rec.Metadata.RuntimeHandleID != claude.TmuxSession {
			continue
		}
		match = rec
		found++
	}
	if found != 1 {
		return "", "", false
	}
	return match.ID, claude.TmuxSession, true
}

// claude returns the Claude session registry, defaulting to the real one on
// first use so the service keeps its existing constructor signature.
func (s *Service) claude() claudeRegistry {
	s.claudeOnce.Do(func() {
		if s.claudeRegistry == nil {
			s.claudeRegistry = claudesessions.New(claudesessions.Options{})
		}
	})
	return s.claudeRegistry
}

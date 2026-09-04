package session

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/claudesessions"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// fakeClaudeRegistry answers as Claude Code's session registry would, without
// reading the developer's real ~/.claude.
type fakeClaudeRegistry struct {
	byName map[string]claudesessions.Session
}

func (f fakeClaudeRegistry) ByName(_ context.Context, name string) (claudesessions.Session, error) {
	session, ok := f.byName[name]
	if !ok {
		return claudesessions.Session{}, claudesessions.NotFound("no-descriptor")
	}
	return session, nil
}

// aliasFixture stands up the crew that made this necessary: dev and qa on one
// task, sharing a worktree, a branch and a display name, told apart only by the
// tmux pane each owns.
func aliasFixture(t *testing.T) *Service {
	t.Helper()
	st := newFakeStore()
	st.sessions["advisor-ios-app-9"] = domain.SessionRecord{
		ID:          "advisor-ios-app-9",
		DisplayName: "chat url whitelist",
		Metadata:    domain.SessionMetadata{RuntimeHandleID: "advisor-ios-app-feature-MOBILITY-4734"},
	}
	st.sessions["advisor-ios-app-10"] = domain.SessionRecord{
		ID:          "advisor-ios-app-10",
		DisplayName: "chat url whitelist",
		Metadata:    domain.SessionMetadata{RuntimeHandleID: "advisor-ios-app-10"},
	}
	return NewWithDeps(Deps{
		Store: st,
		ClaudeRegistry: fakeClaudeRegistry{byName: map[string]claudesessions.Session{
			"mobility-4734-chat-unsafe-url-whitelist-f5": {TmuxSession: "advisor-ios-app-feature-MOBILITY-4734"},
			"mobility-4734-chat-unsafe-url-whitelist-b7": {TmuxSession: "advisor-ios-app-10"},
			"a-session-ao-lost-track-of":                 {TmuxSession: "no-such-pane"},
			"a-session-with-no-pane":                     {TmuxSession: ""},
		}},
	})
}

// The whole point: two Claude names that a human cannot tell apart resolve to
// the two different crew members, because the pane can tell them apart.
func TestResolveSessionAliasPicksTheRightCrewMember(t *testing.T) {
	svc := aliasFixture(t)
	tests := map[string]domain.SessionID{
		"mobility-4734-chat-unsafe-url-whitelist-f5": "advisor-ios-app-9",
		"mobility-4734-chat-unsafe-url-whitelist-b7": "advisor-ios-app-10",
	}
	for alias, want := range tests {
		got, handle, ok := svc.ResolveSessionAlias(context.Background(), domain.SessionID(alias))
		if !ok {
			t.Fatalf("%s did not resolve", alias)
		}
		if got != want {
			t.Fatalf("%s resolved to %s, want %s", alias, got, want)
		}
		if handle == "" {
			t.Fatalf("%s resolved without naming the pane it joined on", alias)
		}
	}
}

// An id AO already knows must never be reinterpreted, whatever else may share
// the string.
func TestResolveSessionAliasLeavesAKnownIDAlone(t *testing.T) {
	svc := aliasFixture(t)
	if _, _, ok := svc.ResolveSessionAlias(context.Background(), "advisor-ios-app-9"); ok {
		t.Fatal("a real AO session id was treated as an alias")
	}
}

func TestResolveSessionAliasDeclines(t *testing.T) {
	svc := aliasFixture(t)
	tests := map[string]string{
		"empty id":                       "",
		"not a Claude session name":      "totally-made-up",
		"Claude session AO does not own": "a-session-ao-lost-track-of",
		"Claude session with no pane":    "a-session-with-no-pane",
	}
	for name, id := range tests {
		t.Run(name, func(t *testing.T) {
			if _, _, ok := svc.ResolveSessionAlias(context.Background(), domain.SessionID(id)); ok {
				t.Fatalf("%q resolved, want a decline", id)
			}
		})
	}
}

// Two live AO sessions on one pane should not happen, but guessing between them
// would message the wrong agent, so it must decline instead.
func TestResolveSessionAliasDeclinesWhenTwoSessionsClaimOnePane(t *testing.T) {
	st := newFakeStore()
	st.sessions["a-1"] = domain.SessionRecord{ID: "a-1", Metadata: domain.SessionMetadata{RuntimeHandleID: "shared-pane"}}
	st.sessions["a-2"] = domain.SessionRecord{ID: "a-2", Metadata: domain.SessionMetadata{RuntimeHandleID: "shared-pane"}}
	svc := NewWithDeps(Deps{
		Store:          st,
		ClaudeRegistry: fakeClaudeRegistry{byName: map[string]claudesessions.Session{"dup": {TmuxSession: "shared-pane"}}},
	})
	if _, _, ok := svc.ResolveSessionAlias(context.Background(), "dup"); ok {
		t.Fatal("resolved an ambiguous pane instead of declining")
	}
}

// A finished session keeps its runtime handle in the record; resolving onto it
// would deliver to a pane nobody is watching.
func TestResolveSessionAliasSkipsTerminatedSessions(t *testing.T) {
	st := newFakeStore()
	st.sessions["a-1"] = domain.SessionRecord{
		ID:           "a-1",
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{RuntimeHandleID: "old-pane"},
	}
	svc := NewWithDeps(Deps{
		Store:          st,
		ClaudeRegistry: fakeClaudeRegistry{byName: map[string]claudesessions.Session{"gone": {TmuxSession: "old-pane"}}},
	})
	if _, _, ok := svc.ResolveSessionAlias(context.Background(), "gone"); ok {
		t.Fatal("resolved onto a terminated session")
	}
}

// A store that cannot answer must not be read as "no such AO session", which
// would send the id down the alias path on a transient failure.
func TestResolveSessionAliasDeclinesWhenTheStoreFails(t *testing.T) {
	st := newFakeStore()
	st.getSessionErr = errors.New("database is locked")
	svc := NewWithDeps(Deps{
		Store:          st,
		ClaudeRegistry: fakeClaudeRegistry{byName: map[string]claudesessions.Session{"x": {TmuxSession: "p"}}},
	})
	if _, _, ok := svc.ResolveSessionAlias(context.Background(), "x"); ok {
		t.Fatal("resolved while the store was failing")
	}
}

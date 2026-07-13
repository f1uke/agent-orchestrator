package tokenusage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeStore struct {
	sessions []domain.SessionRecord
	writes   map[domain.SessionID]domain.TokenUsage
}

func (f *fakeStore) ListAllSessions(_ context.Context) ([]domain.SessionRecord, error) {
	return f.sessions, nil
}

func (f *fakeStore) SetSessionTokenUsage(_ context.Context, id domain.SessionID, usage domain.TokenUsage, _ time.Time) (bool, error) {
	if f.writes == nil {
		f.writes = map[domain.SessionID]domain.TokenUsage{}
	}
	f.writes[id] = usage
	return true, nil
}

func live(id string) domain.SessionRecord {
	return domain.SessionRecord{
		ID:       domain.SessionID(id),
		Harness:  domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/" + id},
		Activity: domain.Activity{LastActivityAt: time.Unix(1000, 0)},
	}
}

func constReader(u domain.TokenUsage, ok bool, err error) UsageReader {
	return func(domain.SessionRecord) (domain.TokenUsage, bool, error) { return u, ok, err }
}

func TestShouldParse(t *testing.T) {
	sample := domain.TokenUsage{Input: 1}
	_ = sample
	cases := []struct {
		name string
		rec  domain.SessionRecord
		want bool
	}{
		{"live claude", live("a"), true},
		{"non-claude", func() domain.SessionRecord { r := live("b"); r.Harness = domain.HarnessCodex; return r }(), false},
		{"no workspace", func() domain.SessionRecord { r := live("c"); r.Metadata.WorkspacePath = ""; return r }(), false},
		{
			"terminated never parsed",
			func() domain.SessionRecord { r := live("d"); r.IsTerminated = true; return r }(),
			true,
		},
		{
			"terminated already finalized",
			func() domain.SessionRecord {
				r := live("e")
				r.IsTerminated = true
				r.TokensUpdatedAt = r.Activity.LastActivityAt.Add(time.Second) // parsed AFTER it ended
				return r
			}(),
			false,
		},
		{
			"suspended with newer activity than last parse",
			func() domain.SessionRecord {
				r := live("f")
				r.IsSuspended = true
				r.TokensUpdatedAt = r.Activity.LastActivityAt.Add(-time.Second) // parsed BEFORE last activity
				return r
			}(),
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldParse(tc.rec); got != tc.want {
				t.Fatalf("shouldParse = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPoll_PersistsParsedUsage(t *testing.T) {
	store := &fakeStore{sessions: []domain.SessionRecord{live("a")}}
	usage := domain.TokenUsage{Input: 10, CacheRead: 20, Output: 5, Turns: 3}
	o := New(store, constReader(usage, true, nil), Config{})
	if err := o.poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if got := store.writes["a"]; got != usage {
		t.Fatalf("persisted usage = %+v, want %+v", got, usage)
	}
}

func TestPoll_SkipsWhenReaderNotOK(t *testing.T) {
	store := &fakeStore{sessions: []domain.SessionRecord{live("a")}}
	o := New(store, constReader(domain.TokenUsage{}, false, nil), Config{})
	if err := o.poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if _, wrote := store.writes["a"]; wrote {
		t.Fatal("persisted a session the reader reported no telemetry for")
	}
}

func TestPoll_ReaderErrorIsNonFatalAndContinues(t *testing.T) {
	store := &fakeStore{sessions: []domain.SessionRecord{live("bad"), live("good")}}
	// Reader errors on the first, succeeds on the second: the whole poll must not
	// abort, and the good session must still be persisted.
	good := domain.TokenUsage{Output: 7, Turns: 1}
	reader := func(rec domain.SessionRecord) (domain.TokenUsage, bool, error) {
		if rec.ID == "bad" {
			return domain.TokenUsage{}, false, errors.New("boom")
		}
		return good, true, nil
	}
	o := New(store, reader, Config{})
	if err := o.poll(context.Background()); err != nil {
		t.Fatalf("poll returned error, want nil (per-session failures are skipped): %v", err)
	}
	if _, wrote := store.writes["bad"]; wrote {
		t.Fatal("persisted the session whose parse errored")
	}
	if got := store.writes["good"]; got != good {
		t.Fatalf("good session usage = %+v, want %+v", got, good)
	}
}

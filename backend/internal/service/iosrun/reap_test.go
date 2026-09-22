package iosrun

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// sessionTable answers for a set of known sessions, so a sweep can be told
// live from terminated from purged.
type sessionTable map[domain.SessionID]bool // id -> isTerminated

func (t sessionTable) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	terminated, ok := t[id]
	if !ok {
		return domain.SessionRecord{}, false, nil
	}
	return domain.SessionRecord{ID: id, IsTerminated: terminated}, true, nil
}

func withRunRecords(t *testing.T, ids ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, id := range ids {
		if err := os.MkdirAll(filepath.Join(dir, "iosrun", id), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The bug, exactly as found: two run panes still building with their owning
// sessions long terminated, and a third whose session is still live.
func TestReapOrphanedRuns_ClosesTheEndedSessionsPanesAndLeavesTheLiveOne(t *testing.T) {
	dir := withRunRecords(t, "advisor-ios-app-13", "advisor-ios-app-14", "nter-ios-app-77")
	rt := &fakeRuntime{}
	svc := New(sessionTable{
		"advisor-ios-app-13": true,
		"advisor-ios-app-14": true,
		"nter-ios-app-77":    false,
	}, rt, "/bin/ao", WithStateDir(dir))

	reaped, err := svc.ReapOrphanedRuns(context.Background())
	if err != nil {
		t.Fatalf("ReapOrphanedRuns: %v", err)
	}
	if reaped != 2 {
		t.Errorf("reaped %d, want 2", reaped)
	}
	got := append([]string(nil), rt.destroys...)
	sort.Strings(got)
	want := []string{"iosrun-advisor-ios-app-13", "iosrun-advisor-ios-app-14"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("destroyed %v, want %v", got, want)
	}
	for _, h := range got {
		if h == "iosrun-nter-ios-app-77" {
			t.Fatal("a LIVE session's build was killed; it may be one somebody is waiting for")
		}
	}
}

// A purged session leaves its record behind. The pane has nobody left to own
// it, so it goes.
func TestReapOrphanedRuns_ReapsARecordWhoseSessionIsGone(t *testing.T) {
	dir := withRunRecords(t, "mer-9")
	rt := &fakeRuntime{}
	svc := New(sessionTable{}, rt, "/bin/ao", WithStateDir(dir))

	reaped, err := svc.ReapOrphanedRuns(context.Background())
	if err != nil {
		t.Fatalf("ReapOrphanedRuns: %v", err)
	}
	if reaped != 1 || len(rt.destroys) != 1 || rt.destroys[0] != "iosrun-mer-9" {
		t.Fatalf("reaped=%d destroyed=%v, want the orphan's pane closed", reaped, rt.destroys)
	}
}

// A daemon that has never run a build has nothing to sweep, and that is not an
// error a boot should log.
func TestReapOrphanedRuns_NoRecordsIsNotAnError(t *testing.T) {
	rt := &fakeRuntime{}
	svc := New(sessionTable{}, rt, "/bin/ao", WithStateDir(t.TempDir()))

	reaped, err := svc.ReapOrphanedRuns(context.Background())
	if err != nil || reaped != 0 || len(rt.destroys) != 0 {
		t.Fatalf("ReapOrphanedRuns = (%d, %v) destroyed %v, want a quiet no-op", reaped, err, rt.destroys)
	}
}

// A service with nowhere to write records has nowhere to read them either.
func TestReapOrphanedRuns_NoStateDirIsANoOp(t *testing.T) {
	rt := &fakeRuntime{}
	svc := New(sessionTable{}, rt, "/bin/ao")

	if reaped, err := svc.ReapOrphanedRuns(context.Background()); err != nil || reaped != 0 {
		t.Fatalf("ReapOrphanedRuns = (%d, %v), want (0, nil)", reaped, err)
	}
}

// Stray files beside the record directories are not sessions.
func TestReapOrphanedRuns_IgnoresAnythingThatIsNotARecordDirectory(t *testing.T) {
	dir := withRunRecords(t, "mer-9")
	if err := os.WriteFile(filepath.Join(dir, "iosrun", "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := &fakeRuntime{}
	svc := New(sessionTable{"mer-9": true}, rt, "/bin/ao", WithStateDir(dir))

	if _, err := svc.ReapOrphanedRuns(context.Background()); err != nil {
		t.Fatalf("ReapOrphanedRuns: %v", err)
	}
	if len(rt.destroys) != 1 || rt.destroys[0] != "iosrun-mer-9" {
		t.Fatalf("destroyed %v, want only the one record directory", rt.destroys)
	}
}

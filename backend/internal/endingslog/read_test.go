package endingslog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestMassEndings_FindsTheIncident(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	base := time.Date(2026, 9, 22, 6, 37, 17, 481_000_000, time.UTC)
	for i, id := range []string{"nter-ios-app-78", "advisor-ios-app-14", "advisor-ios-app-13"} {
		r.RecordEnding(context.Background(), ending(id, base.Add(time.Duration(i)*53*time.Millisecond),
			domain.TerminationSourceAgent, "other"))
	}

	groups, err := MassEndings(dir, base.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("MassEndings: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("found %d mass endings, want 1", len(groups))
	}
	if got := strings.Join(groups[0].IDs(), ","); got != "nter-ios-app-78,advisor-ios-app-14,advisor-ios-app-13" {
		t.Errorf("IDs = %s, want all three in recorded order", got)
	}
	if groups[0].Span() != 106*time.Millisecond {
		t.Errorf("Span = %v, want 106ms", groups[0].Span())
	}
}

// The reader recomputes clusters from the FILE, so a daemon that restarted
// mid-incident still reports it - the in-memory window would have been lost.
func TestMassEndings_SurvivesARestartAndSpansBothGenerations(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 9, 22, 6, 37, 17, 0, time.UTC)

	// First "daemon": two endings, then the journal rolls over.
	r1, _ := New(dir)
	r1.RecordEnding(context.Background(), ending("a", base, domain.TerminationSourceAgent, "other"))
	r1.RecordEnding(context.Background(), ending("b", base.Add(50*time.Millisecond), domain.TerminationSourceAgent, "other"))
	if err := os.Rename(filepath.Join(dir, FileName), filepath.Join(dir, FileName+RotatedSuffix)); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	// Second "daemon": a fresh Recorder with an empty window records the third.
	r2, _ := New(dir)
	r2.RecordEnding(context.Background(), ending("c", base.Add(100*time.Millisecond), domain.TerminationSourceAgent, "other"))
	if got := read(t, dir)[2]; got.MassEnding {
		t.Error("the restarted recorder claimed a mass ending it could not have seen")
	}

	groups, err := MassEndings(dir, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("MassEndings: %v", err)
	}
	if len(groups) != 1 || len(groups[0].Entries) != 3 {
		t.Fatalf("groups = %+v, want one group of three across both generations", groups)
	}
}

func TestMassEndings_IgnoresEndingsAOOrdered(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	base := time.Date(2026, 9, 22, 6, 0, 0, 0, time.UTC)
	for i, id := range []string{"a", "b", "c", "d"} {
		r.RecordEnding(context.Background(), ending(id, base.Add(time.Duration(i)*10*time.Millisecond),
			domain.TerminationSourceAO, domain.TerminationCauseDaemonShutdown))
	}
	groups, err := MassEndings(dir, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("MassEndings: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("a daemon shutdown was reported as %d mass ending(s)", len(groups))
	}
}

func TestMassEndings_TwoTogetherIsBelowTheBar(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	base := time.Date(2026, 9, 22, 6, 0, 0, 0, time.UTC)
	r.RecordEnding(context.Background(), ending("a", base, domain.TerminationSourceAgent, "other"))
	r.RecordEnding(context.Background(), ending("b", base.Add(time.Millisecond), domain.TerminationSourceAgent, "other"))
	groups, _ := MassEndings(dir, base.Add(-time.Hour))
	if len(groups) != 0 {
		t.Fatalf("two endings were reported as a mass ending")
	}
}

func TestMassEndings_OlderThanTheCutoffIsNotReported(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	base := time.Date(2026, 9, 20, 6, 0, 0, 0, time.UTC)
	for i, id := range []string{"a", "b", "c"} {
		r.RecordEnding(context.Background(), ending(id, base.Add(time.Duration(i)*time.Millisecond),
			domain.TerminationSourceAgent, "other"))
	}
	groups, _ := MassEndings(dir, base.Add(24*time.Hour))
	if len(groups) != 0 {
		t.Fatalf("an ending from two days ago was reported inside a one-day window")
	}
}

func TestMassEndings_SeparateBurstsAreSeparateEvents(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	base := time.Date(2026, 9, 22, 6, 0, 0, 0, time.UTC)
	for i, id := range []string{"a", "b", "c"} {
		r.RecordEnding(context.Background(), ending(id, base.Add(time.Duration(i)*time.Millisecond),
			domain.TerminationSourceAgent, "other"))
	}
	later := base.Add(time.Hour)
	for i, id := range []string{"d", "e", "f"} {
		r.RecordEnding(context.Background(), ending(id, later.Add(time.Duration(i)*time.Millisecond),
			domain.TerminationSourceAgent, "other"))
	}
	groups, _ := MassEndings(dir, base.Add(-time.Hour))
	if len(groups) != 2 {
		t.Fatalf("found %d mass endings, want 2 separate bursts", len(groups))
	}
	if !groups[0].At.Before(groups[1].At) {
		t.Error("mass endings are not reported oldest first")
	}
}

func TestRead_MissingJournalIsNotAnError(t *testing.T) {
	entries, err := Read(t.TempDir())
	if err != nil || len(entries) != 0 {
		t.Fatalf("Read on a fresh data dir = (%v, %v), want (empty, nil)", entries, err)
	}
}

package endingslog

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func read(t *testing.T, dir string) []Entry {
	t.Helper()
	entries, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return entries
}

func ending(id string, at time.Time, src domain.TerminationSource, reason string) ports.SessionEnding {
	return ports.SessionEnding{
		At:        at,
		SessionID: domain.SessionID(id),
		ProjectID: "proj",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Source:    src,
		Reason:    reason,
		LastState: domain.ActivityActive,
	}
}

// The incident this package exists for: three sessions ending 105ms apart,
// every one of them `agent`/`other`. The journal has to say they were one event
// and say enough about each to narrow the cause.
func TestRecordEnding_MassEndingIsOneEvent(t *testing.T) {
	dir := t.TempDir()
	r, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	base := time.Date(2026, 9, 22, 6, 37, 17, 481_000_000, time.UTC)
	ids := []string{"nter-ios-app-78", "advisor-ios-app-14", "advisor-ios-app-13"}
	offsets := []time.Duration{0, 53 * time.Millisecond, 105 * time.Millisecond}
	for i, id := range ids {
		r.RecordEnding(context.Background(), ending(id, base.Add(offsets[i]), domain.TerminationSourceAgent, "other"))
	}

	got := read(t, dir)
	if len(got) != 3 {
		t.Fatalf("recorded %d endings, want 3", len(got))
	}
	// The first line cannot know what follows it; the third names both peers and
	// is the one that carries the alarm.
	if len(got[0].Together) != 0 {
		t.Errorf("first ending together = %v, want none", got[0].Together)
	}
	if got[0].MassEnding {
		t.Error("first ending is flagged as a mass ending before its peers exist")
	}
	last := got[2]
	if !last.MassEnding {
		t.Error("third ending within 105ms of two others is not flagged as a mass ending")
	}
	if strings.Join(last.Together, ",") != "nter-ios-app-78,advisor-ios-app-14" {
		t.Errorf("third ending together = %v, want both peers in order", last.Together)
	}
	// Millisecond precision is the whole point: a second-resolution stamp would
	// have shown three endings in one second without showing they were one event.
	if got[2].At.Sub(got[0].At) != 105*time.Millisecond {
		t.Errorf("recorded span = %v, want 105ms", got[2].At.Sub(got[0].At))
	}
}

// AO ends many sessions at once on purpose - the shutdown sweep takes every
// live one. An alarm that fires on that is an alarm nobody reads.
func TestRecordEnding_AOOrderedEndingsNeverRaiseTheAlarm(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	at := time.Date(2026, 9, 22, 6, 0, 0, 0, time.UTC)
	for i, id := range []string{"a-1", "a-2", "a-3", "a-4"} {
		r.RecordEnding(context.Background(), ending(id, at.Add(time.Duration(i)*10*time.Millisecond),
			domain.TerminationSourceAO, domain.TerminationCauseDaemonShutdown))
	}
	for _, e := range read(t, dir) {
		if e.MassEnding {
			t.Fatalf("%s: a daemon shutdown was reported as a mass ending", e.SessionID)
		}
	}
	// They are still RECORDED, and still know they were not alone. Only the
	// alarm is suppressed.
	if got := read(t, dir); len(got) != 4 || len(got[3].Together) != 3 {
		t.Fatalf("shutdown endings lost their peers: %+v", got)
	}
}

// One agent-ordered ending among AO-ordered ones is a crew teardown cascade,
// not an incident.
func TestRecordEnding_OneUnorderedAmongOrderedIsNotAMassEnding(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	at := time.Date(2026, 9, 22, 7, 0, 0, 0, time.UTC)
	r.RecordEnding(context.Background(), ending("dev", at, domain.TerminationSourceAgent, "other"))
	r.RecordEnding(context.Background(), ending("qa", at.Add(20*time.Millisecond), domain.TerminationSourceAO, domain.TerminationCauseDevExited))
	r.RecordEnding(context.Background(), ending("qa2", at.Add(40*time.Millisecond), domain.TerminationSourceAO, domain.TerminationCauseDevExited))
	for _, e := range read(t, dir) {
		if e.MassEnding {
			t.Fatalf("%s: a crew teardown was reported as a mass ending", e.SessionID)
		}
	}
}

// Endings further apart than the window are separate events.
func TestRecordEnding_EndingsOutsideTheWindowAreNotTogether(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	at := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	r.RecordEnding(context.Background(), ending("a", at, domain.TerminationSourceAgent, "other"))
	r.RecordEnding(context.Background(), ending("b", at.Add(MassEndingWindow+time.Second), domain.TerminationSourceAgent, "other"))
	got := read(t, dir)
	if len(got[1].Together) != 0 {
		t.Errorf("second ending together = %v, want none", got[1].Together)
	}
}

// The field that separates an idle timeout from a signal.
func TestRecordEnding_SilenceBeforeTheEndingIsRecorded(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	at := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	e := ending("a", at, domain.TerminationSourceAgent, "other")
	e.LastActivityAt = at.Add(-3 * time.Hour)
	r.RecordEnding(context.Background(), e)

	got := read(t, dir)[0]
	if got.SilentForSeconds == nil || *got.SilentForSeconds != 10800 {
		t.Fatalf("silentForSeconds = %v, want 10800", got.SilentForSeconds)
	}
}

// A session that never reported has no silence to measure, and inventing a
// number would be inventing evidence.
func TestRecordEnding_NoActivityMeansNoSilenceField(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	r.RecordEnding(context.Background(), ending("a", time.Now().UTC(), domain.TerminationSourceAgent, "other"))
	if got := read(t, dir)[0]; got.SilentForSeconds != nil {
		t.Fatalf("silentForSeconds = %v, want absent", *got.SilentForSeconds)
	}
}

// Whether the pane outlived the agent is the fact that stops being true if you
// look later, so it is probed at the ending.
func TestRecordEnding_ProbesThePaneAtTheEnding(t *testing.T) {
	dir := t.TempDir()
	var asked string
	r, _ := New(dir, WithPaneProbe(func(_ context.Context, handle string) (bool, error) {
		asked = handle
		return true, nil
	}))
	e := ending("a", time.Now().UTC(), domain.TerminationSourceAgent, "other")
	e.RuntimeHandleID = "ao-feature-x"
	r.RecordEnding(context.Background(), e)

	if asked != "ao-feature-x" {
		t.Errorf("probed %q, want the session's runtime handle", asked)
	}
	got := read(t, dir)[0]
	if got.PaneAlive == nil || !*got.PaneAlive {
		t.Fatalf("paneAlive = %v, want true", got.PaneAlive)
	}
}

// A probe that cannot answer must record NO answer. Recording "dead" would be
// the one falsehood a reader would build a theory on.
func TestRecordEnding_AFailedPaneProbeRecordsNoAnswer(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir, WithPaneProbe(func(context.Context, string) (bool, error) {
		return false, errors.New("tmux unreachable")
	}))
	e := ending("a", time.Now().UTC(), domain.TerminationSourceAgent, "other")
	e.RuntimeHandleID = "ao-feature-x"
	r.RecordEnding(context.Background(), e)

	if got := read(t, dir)[0]; got.PaneAlive != nil {
		t.Fatalf("paneAlive = %v, want absent on a failed probe", *got.PaneAlive)
	}
}

// A session with no pane is not asked about one.
func TestRecordEnding_NoHandleIsNotProbed(t *testing.T) {
	dir := t.TempDir()
	probed := false
	r, _ := New(dir, WithPaneProbe(func(context.Context, string) (bool, error) { probed = true; return true, nil }))
	r.RecordEnding(context.Background(), ending("a", time.Now().UTC(), domain.TerminationSourceAgent, "other"))
	if probed {
		t.Error("probed a pane for a session that has no runtime handle")
	}
}

// The journal must not grow without bound.
func TestRecordEnding_RollsOverAtTheCap(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	path := filepath.Join(dir, FileName)
	// Put the file just under the cap, then write one more line.
	if err := os.WriteFile(path, make([]byte, maxFileBytes), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r.RecordEnding(context.Background(), ending("a", time.Now().UTC(), domain.TerminationSourceAgent, "other"))

	rotated, err := os.Stat(path + RotatedSuffix)
	if err != nil {
		t.Fatalf("previous generation not kept: %v", err)
	}
	if rotated.Size() != maxFileBytes {
		t.Errorf("rotated size = %d, want the full previous generation", rotated.Size())
	}
	current, err := os.Stat(path)
	if err != nil || current.Size() >= maxFileBytes {
		t.Fatalf("current generation = %v (err %v), want a fresh small file", current, err)
	}
}

// A hard kill can leave a half-written final line. Losing the whole journal to
// it would lose exactly the evidence a crash investigation wants.
func TestRead_SkipsATruncatedFinalLine(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	r.RecordEnding(context.Background(), ending("a", time.Now().UTC(), domain.TerminationSourceAgent, "other"))
	f, err := os.OpenFile(filepath.Join(dir, FileName), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, _ = f.WriteString(`{"at":"2026-09-2`)
	_ = f.Close()

	if got := read(t, dir); len(got) != 1 || got[0].SessionID != "a" {
		t.Fatalf("Read = %+v, want the one intact entry", got)
	}
}

// The line has to be readable by eye and by tools without the database.
func TestRecordEnding_LineIsSelfContained(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	e := ending("advisor-ios-app-13", time.Date(2026, 9, 22, 6, 37, 17, 586_000_000, time.UTC),
		domain.TerminationSourceAgent, "other")
	e.LastActivityAt = e.At.Add(-90 * time.Second)
	e.AgentSessionID = "0199a0bb-1234-7000-8000-abcdefabcdef"
	e.TranscriptPath = "/Users/someone/.claude/projects/p/0199a0bb.jsonl"
	r.RecordEnding(context.Background(), e)

	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw[:len(raw)-1], &decoded); err != nil {
		t.Fatalf("the line is not valid JSON: %v", err)
	}
	for _, key := range []string{"at", "sessionId", "projectId", "kind", "harness", "source", "reason", "lastState", "silentForSeconds", "transcriptPath", "agentSessionId"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("line is missing %q: %s", key, raw)
		}
	}
}

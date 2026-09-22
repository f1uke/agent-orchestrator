package endingslog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// paneExit appends an exit line exactly as a pane's shell writes it (see the
// tmux runtime's exitStatusRecorder, whose own test runs the real shell against
// this reader).
func paneExit(t *testing.T, dir, id string, code int, epoch string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, FileName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintf(f, `{"sessionId":%q,"agentExit":{"code":%d,"epoch":%q}}`+"\n", id, code, epoch); err != nil {
		t.Fatal(err)
	}
}

func epochOf(t time.Time) string {
	return fmt.Sprintf("%d.%09d", t.Unix(), t.Nanosecond())
}

// The usual order: the SessionEnd hook reports while the process is still
// alive, and the pane sees it die about half a second later. Read joins the two
// halves into one record.
func TestRead_JoinsTheExitOntoItsEnding(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	at := time.Date(2026, 9, 22, 15, 6, 43, 807_000_000, time.UTC)
	r.RecordEnding(context.Background(), ending("nter-ios-app-79", at, domain.TerminationSourceAgent, "other"))
	paneExit(t, dir, "nter-ios-app-79", 143, epochOf(at.Add(537*time.Millisecond)))

	got := read(t, dir)
	if len(got) != 1 {
		t.Fatalf("read %d entries, want the ending and its exit as ONE record: %+v", len(got), got)
	}
	x := got[0].Exit
	if x == nil {
		t.Fatal("the exit status was not joined onto the ending")
	}
	if x.Code != 143 || x.Signal != "SIGTERM" {
		t.Errorf("exit = %d/%q, want 143/SIGTERM", x.Code, x.Signal)
	}
	if want := at.Add(537 * time.Millisecond); !x.At.Equal(want) {
		t.Errorf("exit at = %v, want %v to the nanosecond the shell gave", x.At, want)
	}
	if got[0].At != at {
		t.Errorf("the ending keeps its own time; got %v", got[0].At)
	}
}

// A shell with only whole seconds can stamp the exit a fraction of a second
// BEFORE the ending the hook reported; the join must still find it. The same
// for a locale that writes a comma for the decimal point.
func TestRead_JoinsAnExitStampedJustBeforeItsEnding(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	at := time.Date(2026, 9, 22, 6, 37, 17, 481_000_000, time.UTC)
	r.RecordEnding(context.Background(), ending("a", at, domain.TerminationSourceAgent, "other"))
	r.RecordEnding(context.Background(), ending("b", at, domain.TerminationSourceAgent, "other"))
	paneExit(t, dir, "a", 129, fmt.Sprint(at.Unix()))
	paneExit(t, dir, "b", 0, fmt.Sprintf("%d,250000", at.Unix()+1))

	got := read(t, dir)
	if len(got) != 2 {
		t.Fatalf("read %d entries, want 2", len(got))
	}
	for _, e := range got {
		if e.Exit == nil {
			t.Fatalf("%s: exit not joined", e.SessionID)
		}
	}
	byID := map[string]*AgentExit{got[0].SessionID: got[0].Exit, got[1].SessionID: got[1].Exit}
	if byID["a"].Signal != "SIGHUP" {
		t.Errorf("a: signal = %q, want SIGHUP", byID["a"].Signal)
	}
	if byID["b"].Signal != "" || byID["b"].Code != 0 {
		t.Errorf("b: exit = %+v, want a plain 0 with no signal", byID["b"])
	}
	if want := time.Unix(at.Unix()+1, 250_000_000).UTC(); !byID["b"].At.Equal(want) {
		t.Errorf("b: comma-decimal epoch read as %v, want %v", byID["b"].At, want)
	}
}

// A SIGKILL fires no hook, so no ending line is ever written - the exit line
// is the only evidence there is, and it must not vanish for want of a partner.
func TestRead_AnExitNobodyReportedStandsAlone(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 9, 22, 16, 5, 21, 0, time.UTC)
	paneExit(t, dir, "x", 137, epochOf(at))

	got := read(t, dir)
	if len(got) != 1 {
		t.Fatalf("read %d entries, want the orphan exit", len(got))
	}
	if got[0].Source != "" || got[0].Exit == nil || got[0].Exit.Signal != "SIGKILL" || !got[0].At.Equal(at) {
		t.Errorf("orphan = %+v (exit %+v), want an unreported SIGKILL at %v", got[0], got[0].Exit, at)
	}
}

// A session that stops, is resumed and stops again gets each exit on its own
// ending - nearest wins, one exit per ending - and an exit far from any ending
// of its session is not pinned onto one.
func TestRead_EachEndingTakesItsOwnExit(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	first := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	second := first.Add(40 * time.Second)
	r.RecordEnding(context.Background(), ending("s", first, domain.TerminationSourceAgent, "other"))
	r.RecordEnding(context.Background(), ending("s", second, domain.TerminationSourceAgent, "prompt_input_exit"))
	paneExit(t, dir, "s", 143, epochOf(first.Add(500*time.Millisecond)))
	paneExit(t, dir, "s", 0, epochOf(second.Add(300*time.Millisecond)))
	// A different session's exit at the same instant belongs to nobody here.
	paneExit(t, dir, "other", 1, epochOf(first.Add(500*time.Millisecond)))
	// And one of s's exits an hour later is its own event.
	paneExit(t, dir, "s", 2, epochOf(first.Add(time.Hour)))

	got := read(t, dir)
	if len(got) != 4 {
		t.Fatalf("read %d entries, want 2 endings + 2 unclaimed exits: %+v", len(got), got)
	}
	if got[0].Exit == nil || got[0].Exit.Code != 143 {
		t.Errorf("first ending exit = %+v, want 143", got[0].Exit)
	}
	var second0 *Entry
	for i := range got {
		if got[i].Reason == "prompt_input_exit" {
			second0 = &got[i]
		}
	}
	if second0 == nil || second0.Exit == nil || second0.Exit.Code != 0 {
		t.Errorf("second ending exit = %+v, want 0", second0)
	}
	if last := got[3]; last.Source != "" || last.Exit.Code != 2 {
		t.Errorf("late exit = %+v, want it unjoined", last)
	}
}

// A damaged exit line is dropped rather than guessed at; the ending it might
// have belonged to still reads.
func TestRead_DamagedExitLinesAreSkipped(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	at := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	r.RecordEnding(context.Background(), ending("s", at, domain.TerminationSourceAgent, "other"))
	f, _ := os.OpenFile(filepath.Join(dir, FileName), os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(`{"sessionId":"s","agentExit":{"epoch":"1790100478"}}` + "\n")
	_, _ = f.WriteString(`{"sessionId":"s","agentExit":{"code":1,"epoch":""}}` + "\n")
	_, _ = f.WriteString(`{"sessionId":"s","agentExit":{"code":1,"epoch":"soon"}}` + "\n")
	_ = f.Close()

	got := read(t, dir)
	if len(got) != 1 || got[0].Exit != nil {
		t.Errorf("got %+v, want the ending alone", got)
	}
}

// A parked exit is an agent that stopped nobody ordered, so it counts toward a
// mass ending exactly as a terminated one does - on the live alarm and in the
// file. On 2026-09-22 15:06:43 that was five sessions, not the four the journal
// showed.
func TestMassEndings_CountParkedExitsAndUnreportedDeaths(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	base := time.Date(2026, 9, 22, 15, 6, 43, 770_000_000, time.UTC)

	parked := ending("nter-ios-app-79", base, domain.TerminationSourceAgent, "other")
	parked.Outcome = ports.EndingParked
	r.RecordEnding(context.Background(), parked)
	r.RecordEnding(context.Background(), ending("nter-ios-app-77", base.Add(10*time.Millisecond), domain.TerminationSourceAgent, "other"))
	// Two endings are below the bar; the third, a SIGKILL with no hook, is
	// visible only through its pane.
	paneExit(t, dir, "advisor-ios-app-15", 137, epochOf(base.Add(30*time.Millisecond)))

	got := read(t, dir)
	if got[0].Outcome != string(ports.EndingParked) {
		t.Errorf("outcome = %q, want parked on the line", got[0].Outcome)
	}
	if got[1].Outcome != string(ports.EndingTerminated) {
		t.Errorf("outcome = %q, want terminated written explicitly", got[1].Outcome)
	}

	groups, err := MassEndings(dir, base.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("found %d mass endings, want 1", len(groups))
	}
	if ids := strings.Join(groups[0].IDs(), ","); ids != "nter-ios-app-79,nter-ios-app-77,advisor-ios-app-15" {
		t.Errorf("IDs = %s, want the parked one and the unreported one counted", ids)
	}
}

// The live alarm sees parked exits too.
func TestRecordEnding_ParkedExitsRaiseTheLiveAlarm(t *testing.T) {
	dir := t.TempDir()
	r, _ := New(dir)
	base := time.Date(2026, 9, 22, 6, 37, 17, 481_000_000, time.UTC)
	for i, id := range []string{"nter-ios-app-47", "nter-ios-app-78", "advisor-ios-app-14"} {
		e := ending(id, base.Add(time.Duration(i)*time.Millisecond), domain.TerminationSourceAgent, "other")
		if i == 0 {
			e.Outcome = ports.EndingParked
		}
		r.RecordEnding(context.Background(), e)
	}
	if got := read(t, dir); !got[2].MassEnding {
		t.Error("three agent stops in 2ms, one of them parked, did not raise the alarm")
	}
}

func TestSignalFor(t *testing.T) {
	for code, want := range map[int]string{
		0: "", 1: "", 127: "", 128: "", 129: "SIGHUP", 130: "SIGINT", 137: "SIGKILL",
		143: "SIGTERM", 138: "signal 10", 255: "", 192: "signal 64",
	} {
		if got := signalFor(code); got != want {
			t.Errorf("signalFor(%d) = %q, want %q", code, got, want)
		}
	}
}

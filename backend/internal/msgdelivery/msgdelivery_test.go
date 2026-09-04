package msgdelivery_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/msgdelivery"
)

func TestOriginRoundTrips(t *testing.T) {
	ctx := msgdelivery.WithOrigin(context.Background(), msgdelivery.Origin{
		Session: "agent-orchestrator-9", Trigger: msgdelivery.TriggerQueueDrain,
	})
	got := msgdelivery.OriginOf(ctx)
	if got.Session != "agent-orchestrator-9" || got.Trigger != msgdelivery.TriggerQueueDrain {
		t.Fatalf("origin = %+v, want what was set", got)
	}
}

// An unattributed send is a real thing to know about, so a bare context reports
// an empty origin rather than a made-up one.
func TestOriginIsEmptyWhenNobodySaid(t *testing.T) {
	if got := msgdelivery.OriginOf(context.Background()); got != (msgdelivery.Origin{}) {
		t.Fatalf("origin on a bare context = %+v, want empty", got)
	}
}

// "Nothing reported" must stay distinguishable from "reported an empty path":
// it is the difference between a path nobody accounted for and a known one.
func TestCollectorSaysWhenNothingWasReported(t *testing.T) {
	_, collector := msgdelivery.WithCollector(context.Background())
	if _, got := collector.Collected(); got {
		t.Fatal("a collector nothing reported into must say so")
	}
}

func TestRecordReachesTheCollectorAndTheJournal(t *testing.T) {
	journal := &fakeJournal{}
	ctx := msgdelivery.WithOrigin(context.Background(), msgdelivery.Origin{Session: "s1", Trigger: msgdelivery.TriggerNudge})
	ctx, collector := msgdelivery.WithCollector(ctx)

	msgdelivery.Record(ctx, journal, "tmux-1", msgdelivery.Report{
		Path: msgdelivery.PathPane, Reason: "no-descriptor", Sender: "agent-orchestrator-5",
	})

	report, ok := collector.Collected()
	if !ok || report.Path != msgdelivery.PathPane || report.Reason != "no-descriptor" {
		t.Fatalf("collector got %+v (%v), want the transport's own report", report, ok)
	}
	if len(journal.entries) != 1 {
		t.Fatalf("journal holds %d entries, want 1", len(journal.entries))
	}
	entry := journal.entries[0]
	if entry.Session != "s1" || entry.Trigger != msgdelivery.TriggerNudge || entry.Handle != "tmux-1" {
		t.Fatalf("entry lost the caller's half of the story: %+v", entry)
	}
	if entry.Path != msgdelivery.PathPane || entry.Reason != "no-descriptor" || entry.Sender != "agent-orchestrator-5" {
		t.Fatalf("entry lost the transport's half of the story: %+v", entry)
	}
}

// Recording is an observation of a send, never a condition of it: no collector,
// no journal, no origin - and nothing panics or blocks.
func TestRecordIsSafeWithNothingAttached(t *testing.T) {
	msgdelivery.Record(context.Background(), nil, "tmux-1", msgdelivery.Report{Path: msgdelivery.PathSocket})
}

// The first report wins, so a wrapper cannot overwrite the account given by the
// transport that actually did the work.
func TestTheFirstReportWins(t *testing.T) {
	ctx, collector := msgdelivery.WithCollector(context.Background())
	msgdelivery.Record(ctx, nil, "h", msgdelivery.Report{Path: msgdelivery.PathSocket})
	msgdelivery.Record(ctx, nil, "h", msgdelivery.Report{Path: msgdelivery.PathPane, Reason: "made-up"})
	if report, _ := collector.Collected(); report.Path != msgdelivery.PathSocket {
		t.Fatalf("path = %q, want the first report to stand", report.Path)
	}
}

type fakeJournal struct{ entries []msgdelivery.Entry }

func (j *fakeJournal) Append(e msgdelivery.Entry) error {
	j.entries = append(j.entries, e)
	return nil
}

// ---- the file journal ----------------------------------------------------

func readEntries(t *testing.T, path string) []msgdelivery.Entry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []msgdelivery.Entry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e msgdelivery.Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("unparseable journal line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

func TestFileJournalWritesOneLinePerDelivery(t *testing.T) {
	dir := t.TempDir()
	j, err := msgdelivery.NewFileJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	if j.Path() != filepath.Join(dir, msgdelivery.FileName) {
		t.Fatalf("path = %q, want it under the data dir", j.Path())
	}
	for _, e := range []msgdelivery.Entry{
		{Session: "s1", Trigger: msgdelivery.TriggerSend, Path: msgdelivery.PathSocket, Sender: "ao-1", MsgID: "m1"},
		{Session: "s2", Trigger: msgdelivery.TriggerQueueDrain, Path: msgdelivery.PathPane, Reason: "no-descriptor"},
	} {
		if err := j.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	got := readEntries(t, j.Path())
	if len(got) != 2 {
		t.Fatalf("want 2 lines, got %d", len(got))
	}
	if got[0].Path != msgdelivery.PathSocket || got[0].MsgID != "m1" {
		t.Errorf("first entry lost detail: %+v", got[0])
	}
	if got[1].Reason != "no-descriptor" || got[1].Trigger != msgdelivery.TriggerQueueDrain {
		t.Errorf("second entry lost detail: %+v", got[1])
	}
	if got[0].At.IsZero() {
		t.Error("a record with no time cannot answer when a message was delivered")
	}
}

// The journal must survive a daemon restart: the question it answers is about
// this morning, not this process.
func TestFileJournalIsAppendOnlyAcrossWriters(t *testing.T) {
	dir := t.TempDir()
	first, _ := msgdelivery.NewFileJournal(dir)
	if err := first.Append(msgdelivery.Entry{Session: "before-restart", Path: msgdelivery.PathSocket}); err != nil {
		t.Fatal(err)
	}
	second, _ := msgdelivery.NewFileJournal(dir)
	if err := second.Append(msgdelivery.Entry{Session: "after-restart", Path: msgdelivery.PathSocket}); err != nil {
		t.Fatal(err)
	}
	got := readEntries(t, first.Path())
	if len(got) != 2 || got[0].Session != "before-restart" || got[1].Session != "after-restart" {
		t.Fatalf("history was not preserved in order: %+v", got)
	}
}

// One line per message delivered is a rate that would grow a file forever, so
// it rolls over - and the previous generation is kept, because rotating is not
// the same as forgetting.
func TestFileJournalRollsOverAndKeepsThePreviousGeneration(t *testing.T) {
	dir := t.TempDir()
	j, _ := msgdelivery.NewFileJournal(dir)
	// A fat error string makes the bound reachable in a test without writing
	// hundreds of thousands of lines.
	fat := msgdelivery.Entry{Path: msgdelivery.PathPane, Reason: "no-descriptor", Error: strings.Repeat("x", 64*1024)}
	for range 80 {
		if err := j.Append(fat); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(j.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 4<<20 {
		t.Fatalf("journal grew to %d bytes; it must stay bounded", info.Size())
	}
	if _, err := os.Stat(j.Path() + msgdelivery.RotatedSuffix); err != nil {
		t.Fatalf("the previous generation was not kept: %v", err)
	}
	// Both generations are still readable JSON Lines.
	if len(readEntries(t, j.Path())) == 0 {
		t.Fatal("the current journal must still hold records after a rollover")
	}
	if len(readEntries(t, j.Path()+msgdelivery.RotatedSuffix)) == 0 {
		t.Fatal("the rotated journal must still be readable")
	}
}

// Interleaved writes must never produce a half-line: the journal becomes
// unparseable exactly when somebody needs it.
func TestFileJournalConcurrentWritesStayWholeLines(t *testing.T) {
	j, _ := msgdelivery.NewFileJournal(t.TempDir())
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = j.Append(msgdelivery.Entry{Session: "s", Path: msgdelivery.PathSocket, Bytes: i})
		}(i)
	}
	wg.Wait()
	if got := readEntries(t, j.Path()); len(got) != 50 {
		t.Fatalf("want 50 entries, got %d", len(got))
	}
}

func TestFileJournalNeedsADataDir(t *testing.T) {
	if _, err := msgdelivery.NewFileJournal(""); err == nil {
		t.Fatal("want a refusal rather than a journal written to the process cwd")
	}
}

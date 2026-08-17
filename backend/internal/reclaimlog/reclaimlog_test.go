package reclaimlog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func readEntries(t *testing.T, path string) []Entry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("unparseable log line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

func TestAppend_WritesOneJSONLinePerEntry(t *testing.T) {
	dir := t.TempDir()
	w, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if w.Path() != filepath.Join(dir, FileName) {
		t.Fatalf("path = %q", w.Path())
	}

	entries := []Entry{
		{Action: ActionReclaimed, SessionID: "demo-1", Branch: "feature/a", BytesFreed: 1024},
		{Action: ActionSkipped, SessionID: "demo-2", Reason: "workspace_dirty"},
	}
	for _, e := range entries {
		if err := w.Append(e); err != nil {
			t.Fatal(err)
		}
	}

	got := readEntries(t, w.Path())
	if len(got) != 2 {
		t.Fatalf("want 2 lines, got %d", len(got))
	}
	if got[0].Action != ActionReclaimed || got[0].BytesFreed != 1024 || got[0].Branch != "feature/a" {
		t.Errorf("first entry lost detail: %+v", got[0])
	}
	if got[1].Reason != "workspace_dirty" {
		t.Errorf("second entry lost its reason: %+v", got[1])
	}
}

func TestAppend_StampsTheTimeWhenAbsent(t *testing.T) {
	w, _ := New(t.TempDir())
	if err := w.Append(Entry{Action: ActionReclaimed}); err != nil {
		t.Fatal(err)
	}
	got := readEntries(t, w.Path())
	if got[0].At.IsZero() {
		t.Fatal("an entry must always carry a timestamp")
	}
}

func TestAppend_KeepsAnExplicitTime(t *testing.T) {
	w, _ := New(t.TempDir())
	when := time.Date(2026, 8, 17, 9, 30, 0, 0, time.UTC)
	if err := w.Append(Entry{Action: ActionReclaimed, At: when}); err != nil {
		t.Fatal(err)
	}
	if got := readEntries(t, w.Path())[0]; !got.At.Equal(when) {
		t.Fatalf("at = %v, want %v", got.At, when)
	}
}

// TestAppend_IsAppendOnlyAcrossWriters: the log must survive a daemon restart
// and a second process without truncating what is already there.
func TestAppend_IsAppendOnlyAcrossWriters(t *testing.T) {
	dir := t.TempDir()
	w1, _ := New(dir)
	if err := w1.Append(Entry{Action: ActionReclaimed, SessionID: "first"}); err != nil {
		t.Fatal(err)
	}
	// A fresh Writer over the same dir stands in for the restarted daemon.
	w2, _ := New(dir)
	if err := w2.Append(Entry{Action: ActionReclaimed, SessionID: "second"}); err != nil {
		t.Fatal(err)
	}

	got := readEntries(t, w1.Path())
	if len(got) != 2 || got[0].SessionID != "first" || got[1].SessionID != "second" {
		t.Fatalf("history was not preserved in order: %+v", got)
	}
}

// TestAppend_ConcurrentWritesStayWholeLines: interleaved writes must never
// produce a half-line, or the log becomes unparseable exactly when it matters.
func TestAppend_ConcurrentWritesStayWholeLines(t *testing.T) {
	w, _ := New(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = w.Append(Entry{Action: ActionReclaimed, SessionID: "s", BytesFreed: int64(i)})
		}(i)
	}
	wg.Wait()

	got := readEntries(t, w.Path()) // fails the test if any line is unparseable
	if len(got) != 50 {
		t.Fatalf("want 50 entries, got %d", len(got))
	}
}

// TestAppend_SurvivesAPartialFinalLine: a crash mid-write can leave a truncated
// last line. Everything before it must still be readable — that is the point of
// an append-only format.
func TestAppend_SurvivesAPartialFinalLine(t *testing.T) {
	dir := t.TempDir()
	w, _ := New(dir)
	if err := w.Append(Entry{Action: ActionReclaimed, SessionID: "complete"}); err != nil {
		t.Fatal(err)
	}
	// Simulate the interrupted write.
	f, err := os.OpenFile(w.Path(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"action":"reclaimed","sessi`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// The complete line before it is still recoverable.
	data, err := os.ReadFile(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	first := strings.SplitN(string(data), "\n", 2)[0]
	var e Entry
	if err := json.Unmarshal([]byte(first), &e); err != nil {
		t.Fatalf("the completed entry was corrupted by the partial write: %v", err)
	}
	if e.SessionID != "complete" {
		t.Fatalf("entry = %+v", e)
	}

	// And a later append still lands, so the log keeps working.
	if err := w.Append(Entry{Action: ActionReclaimed, SessionID: "after"}); err != nil {
		t.Fatalf("append after a partial line: %v", err)
	}
}

func TestNew_RequiresADataDir(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("want an error for an empty dir")
	}
}

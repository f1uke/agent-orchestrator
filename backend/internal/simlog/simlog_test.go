package simlog

import (
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"
)

// One capture of `log show --style compact` as simctl prints it: the header
// simctl writes before any entry, an entry with a subsystem and category, an
// activity with neither, an entry whose message runs over several lines, and
// two entries from the app under test. Written out rather than captured from a
// device, so it runs on Linux CI and describes a fictional app.
const compactCapture = `Timestamp               Ty Process[PID:TID]
2026-08-17 16:31:59.078 Df Nimbus[4242:714e3fa] [] portfolio response: {"total":1420,"currency":"THB"}
2026-08-17 16:31:59.172 A  runningboardd[63143:714e0d0] (RunningBoard) invalidateAssertionWithIdentifier
2026-08-17 16:31:59.287 Df SpringBoard[63140:7125dce] [com.apple.CoverSheetKit:Common] setting text to value: 16:32
2026-08-17 16:32:00.001 E  Nimbus[4242:714e3fa] [com.example.nimbus:net] request failed
    NSLocalizedDescription=timed out
    NSURL=https://api.example.com/v1/portfolio
2026-08-17 16:32:00.400 Df backboardd[63141:7125dcf] [com.apple.BackBoardServices:Default] display asleep
`

func collect(t *testing.T, capture string, filter Filter) ([]Entry, Scan) {
	t.Helper()
	var got []Entry
	scan, err := Read(strings.NewReader(capture), filter, func(e Entry) error {
		got = append(got, e)
		return nil
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return got, scan
}

func TestRead_ParsesTheCompactFormat(t *testing.T) {
	got, scan := collect(t, compactCapture, Filter{})

	if len(got) != 5 {
		t.Fatalf("entries = %d, want 5: %+v", len(got), got)
	}
	if scan.Entries != 5 {
		t.Fatalf("scanned = %d, want 5", scan.Entries)
	}
	first := got[0]
	if first.Process != "Nimbus" || first.PID != 4242 {
		t.Fatalf("process = %q pid = %d, want Nimbus/4242", first.Process, first.PID)
	}
	if first.Type != "Default" {
		t.Fatalf("type = %q, want the code spelled out", first.Type)
	}
	if !strings.Contains(first.Message, `"currency":"THB"`) {
		t.Fatalf("message = %q, want the payload intact", first.Message)
	}
	if first.Time != "2026-08-17 16:31:59.078" {
		t.Fatalf("time = %q", first.Time)
	}
	// simctl's own column header is not an entry, and neither is anything else
	// that does not parse: dropping it silently beats reporting it as a line
	// from a process called "Timestamp".
	for _, e := range got {
		if strings.HasPrefix(e.Raw, "Timestamp ") {
			t.Fatalf("the column header must not be reported as an entry: %+v", e)
		}
	}
}

func TestRead_KeepsASubsystemAndCategoryApartFromTheMessage(t *testing.T) {
	got, _ := collect(t, compactCapture, Filter{})

	var found bool
	for _, e := range got {
		if e.Subsystem == "com.apple.CoverSheetKit" && e.Category == "Common" {
			found = true
			if strings.HasPrefix(e.Message, "[") {
				t.Fatalf("the subsystem must not be left in the message: %q", e.Message)
			}
		}
	}
	if !found {
		t.Fatal("subsystem and category were not parsed")
	}
}

func TestRead_AMultiLineMessageStaysOneEntry(t *testing.T) {
	// A log entry can carry a payload across several lines. Treating each line
	// as an entry would split a body an agent is reading in half, and a --grep
	// would then match only the fragment it landed on.
	got, _ := collect(t, compactCapture, Filter{Grep: regexp.MustCompile("timed out")})

	if len(got) != 1 {
		t.Fatalf("entries = %d, want the one whose continuation matched: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Message, "request failed") {
		t.Fatalf("the entry must keep its first line: %q", got[0].Message)
	}
	if !strings.Contains(got[0].Message, "api.example.com") {
		t.Fatalf("the entry must keep every continuation line: %q", got[0].Message)
	}
}

func TestRead_FiltersByProcess(t *testing.T) {
	got, scan := collect(t, compactCapture, Filter{Process: "Nimbus"})

	if len(got) != 2 {
		t.Fatalf("entries = %d, want the app's two: %+v", len(got), got)
	}
	for _, e := range got {
		if e.Process != "Nimbus" {
			t.Fatalf("kept an entry from %q", e.Process)
		}
	}
	// Everything read is still counted, so the caller can say "2 of 5".
	if scan.Entries != 5 {
		t.Fatalf("scanned = %d, want every entry counted", scan.Entries)
	}
}

func TestRead_ProcessMatchIsCaseInsensitiveButNotASubstring(t *testing.T) {
	if got, _ := collect(t, compactCapture, Filter{Process: "nimbus"}); len(got) != 2 {
		t.Fatalf("entries = %d, want a case-insensitive match", len(got))
	}
	// "Nim" is not a process on this device, and quietly matching Nimbus would
	// make `--process` mean something different from what it says.
	if got, _ := collect(t, compactCapture, Filter{Process: "Nim"}); len(got) != 0 {
		t.Fatalf("entries = %d, want no partial match", len(got))
	}
}

func TestRead_FiltersByRegexOverTheWholeEntry(t *testing.T) {
	got, _ := collect(t, compactCapture, Filter{Grep: regexp.MustCompile(`"total":\d+`)})

	if len(got) != 1 || !strings.Contains(got[0].Message, "portfolio response") {
		t.Fatalf("entries = %+v, want the one matching entry", got)
	}
	// The process name is part of what an entry looks like, so grepping for it
	// works the way `grep` on the same output would.
	if got, _ := collect(t, compactCapture, Filter{Grep: regexp.MustCompile("SpringBoard")}); len(got) != 1 {
		t.Fatalf("entries = %d, want the grep to see the whole line", len(got))
	}
}

func TestRead_BothFiltersMustMatch(t *testing.T) {
	got, _ := collect(t, compactCapture, Filter{
		Process: "Nimbus",
		Grep:    regexp.MustCompile("request failed"),
	})
	if len(got) != 1 || got[0].Type != "Error" {
		t.Fatalf("entries = %+v, want the app's error only", got)
	}
}

func TestRead_ReportsWhichProcessesDidLog(t *testing.T) {
	// The actionable half of an empty result: a `--process` that matched
	// nothing is nearly always a name that is not what the executable is
	// called, and the answer is in the traffic that WAS there.
	_, scan := collect(t, compactCapture, Filter{Process: "MyApp"})

	names := map[string]int{}
	for _, p := range scan.Processes {
		names[p.Name] = p.Entries
	}
	if names["Nimbus"] != 2 || names["SpringBoard"] != 1 {
		t.Fatalf("processes = %+v, want every process that logged, with its count", scan.Processes)
	}
	// Busiest first, so a truncated list is the useful half.
	if scan.Processes[0].Entries < scan.Processes[len(scan.Processes)-1].Entries {
		t.Fatalf("processes must be ordered busiest first: %+v", scan.Processes)
	}
}

func TestRead_StopsWhenTheCallerStops(t *testing.T) {
	// What a broken pipe looks like from here: the caller's write failed, and
	// reading the rest of a `log stream` would be pointless.
	stop := errors.New("stdout is gone")
	var seen int
	_, err := Read(strings.NewReader(compactCapture), Filter{}, func(Entry) error {
		seen++
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("err = %v, want the caller's own error back", err)
	}
	if seen != 1 {
		t.Fatalf("delivered %d entries after the caller stopped", seen)
	}
}

func TestRead_AnEntryIsDeliveredBeforeTheNextOneArrives(t *testing.T) {
	// An entry is complete when the next one starts or the capture ends, and
	// both entries have to survive that - the last one has nothing after it.
	lines := `2026-08-17 16:31:59.078 Df Nimbus[4242:714e3fa] [] first
2026-08-17 16:31:59.079 Df Nimbus[4242:714e3fa] [] second
`
	got, _ := collect(t, lines, Filter{})
	if len(got) != 2 {
		t.Fatalf("entries = %+v, want both", got)
	}
	if got[1].Message != "second" {
		t.Fatalf("last entry = %q", got[1].Message)
	}
}

func TestNewFilter_RejectsAPatternItCannotCompile(t *testing.T) {
	if _, err := NewFilter("", "(unclosed"); err == nil {
		t.Fatal("a bad regex must be refused, not silently matched")
	}
}

func TestNewFilter_EmptyMeansEverything(t *testing.T) {
	filter, err := NewFilter("", "")
	if err != nil {
		t.Fatalf("NewFilter: %v", err)
	}
	got, _ := collect(t, compactCapture, filter)
	if len(got) != 5 {
		t.Fatalf("entries = %d, want everything", len(got))
	}
}

func TestFollow_DeliversAnEntryWithoutWaitingForTheNextOne(t *testing.T) {
	// The property Read cannot give a live stream: the last thing an app logged
	// before the device went quiet is exactly the entry somebody watching it is
	// waiting for, and holding it until the next one arrives can wait forever.
	r, w := io.Pipe()
	got := make(chan Entry, 4)
	done := make(chan error, 1)
	go func() {
		_, err := Follow(r, Filter{}, func(e Entry) error {
			got <- e
			return nil
		})
		done <- err
	}()

	_, _ = io.WriteString(w, "2026-08-17 16:31:59.078 Df Nimbus[4242:714e3fa] [] alone\n")
	select {
	case entry := <-got:
		if entry.Message != "alone" {
			t.Fatalf("entry = %+v", entry)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a live entry was never delivered; nothing else was going to arrive")
	}
	_ = w.CloseWithError(io.EOF)
	if err := <-done; err != nil {
		t.Fatalf("follow: %v", err)
	}
}

func TestFollow_KeepsAMultiLineMessageTogether(t *testing.T) {
	// The continuation arrives in the same burst as its first line, so it is
	// still one entry.
	got, _ := collectFollow(t, compactCapture, Filter{Grep: regexp.MustCompile("timed out")})
	if len(got) != 1 || !strings.Contains(got[0].Message, "api.example.com") {
		t.Fatalf("entries = %+v, want one entry with its whole message", got)
	}
}

func collectFollow(t *testing.T, capture string, filter Filter) ([]Entry, Scan) {
	t.Helper()
	var got []Entry
	scan, err := Follow(strings.NewReader(capture), filter, func(e Entry) error {
		got = append(got, e)
		return nil
	})
	if err != nil {
		t.Fatalf("follow: %v", err)
	}
	return got, scan
}

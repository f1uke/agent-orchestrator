package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// A slice of what `xcrun simctl spawn <udid> log show --style compact` prints:
// the column header simctl writes first, entries from the app under test, and
// the system traffic that is always there. Fictional app, written out rather
// than captured, so this runs on a Linux CI box with no simulator.
const simLogCapture = `Timestamp               Ty Process[PID:TID]
2026-08-17 16:31:59.078 Df Nimbus[4242:714e3fa] [] portfolio response: {"total":1420}
2026-08-17 16:31:59.172 A  runningboardd[63143:714e0d0] (RunningBoard) invalidateAssertionWithIdentifier
2026-08-17 16:32:00.001 E  Nimbus[4242:714e3fa] [com.example.nimbus:net] request failed
2026-08-17 16:32:00.400 Df SpringBoard[63140:7125dcf] [com.apple.CoverSheetKit:Common] display asleep
`

// fakeStream is one running `log` child. It hands out whatever the test wrote
// into it and records that the command stopped it.
type fakeStream struct {
	reader *io.PipeReader
	writer *io.PipeWriter
	mu     sync.Mutex
	closed int
	err    error
}

func newFakeStream() *fakeStream {
	r, w := io.Pipe()
	return &fakeStream{reader: r, writer: w}
}

func (f *fakeStream) Read(p []byte) (int, error) { return f.reader.Read(p) }

func (f *fakeStream) Close() error {
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
	// A stopped child means end of output, not a broken read.
	return f.reader.CloseWithError(io.EOF)
}

func (f *fakeStream) Err() error { return f.err }

func (f *fakeStream) stops() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// feed writes the capture and ends the stream, the way `log show` does.
func (f *fakeStream) feed(text string) {
	go func() {
		_, _ = io.WriteString(f.writer, text)
		_ = f.writer.CloseWithError(io.EOF)
	}()
}

// logDeps is a booted device whose `log` child hands out capture.
func logDeps(t *testing.T, capture string) (Deps, *fakeStream, *[][]string, *simDaemon) {
	t.Helper()
	cfg := setConfigEnv(t)
	deps := simLeaseDeps(t, bootedProMaxOnly(t), fakePNG)
	daemon := newSimDaemon(t, cfg)
	stream := newFakeStream()
	var starts [][]string
	deps.StartStream = func(_ context.Context, name string, args ...string) (ProcessStream, error) {
		starts = append(starts, append([]string{name}, args...))
		if capture != "" {
			stream.feed(capture)
		}
		return stream, nil
	}
	t.Setenv("AO_SESSION_ID", "mer-9")
	return deps, stream, &starts, daemon
}

func TestSimLog_ReadsTheLastWindowOfTheDevicesUnifiedLog(t *testing.T) {
	deps, _, starts, _ := logDeps(t, simLogCapture)

	out, errOut, err := executeCLI(t, deps, "sim", "log")
	if err != nil {
		t.Fatalf("sim log failed: %v\nstderr=%s", err, errOut)
	}
	if len(*starts) != 1 {
		t.Fatalf("started %d children, want 1: %v", len(*starts), *starts)
	}
	ran := strings.Join((*starts)[0], " ")
	for _, want := range []string{"xcrun simctl spawn", simUDIDProMax, "log show", "--style compact", "--last"} {
		if !strings.Contains(ran, want) {
			t.Fatalf("ran %q, want it to contain %q", ran, want)
		}
	}
	if !strings.Contains(out, "portfolio response") || !strings.Contains(out, "display asleep") {
		t.Fatalf("output must carry the entries:\n%s", out)
	}
	// The column header simctl prints is not a log entry and must not be shown
	// as one.
	if strings.Contains(out, "Ty Process[PID:TID]") {
		t.Fatalf("simctl's own header leaked into the output:\n%s", out)
	}
}

func TestSimLog_NeverFiltersAtTheSource(t *testing.T) {
	// Load-bearing, not a preference: a `log stream` inside the simulator is a
	// child of the guest's launchd, so no signal of ours reaches it. The only
	// thing that ends it is a failed write to the pipe this process holds - and
	// a stream filtered at the source that matches nothing never writes, never
	// notices, and stays running for days. Filtering happens in AO.
	deps, _, starts, _ := logDeps(t, simLogCapture)

	if _, _, err := executeCLI(t, deps, "sim", "log", "--process", "Nimbus", "--grep", "portfolio", "--follow"); err != nil {
		t.Fatalf("sim log --follow: %v", err)
	}
	ran := strings.Join((*starts)[0], " ")
	for _, banned := range []string{"--predicate", "--process", "--grep"} {
		if strings.Contains(ran, banned) {
			t.Fatalf("ran %q, which filters at the source: a filtered stream that matches nothing never notices its reader is gone", ran)
		}
	}
}

func TestSimLog_FiltersByProcess(t *testing.T) {
	deps, _, _, _ := logDeps(t, simLogCapture)

	out, _, err := executeCLI(t, deps, "sim", "log", "--process", "Nimbus")
	if err != nil {
		t.Fatalf("sim log: %v", err)
	}
	if !strings.Contains(out, "portfolio response") || !strings.Contains(out, "request failed") {
		t.Fatalf("the app's own entries must survive the filter:\n%s", out)
	}
	if strings.Contains(out, "display asleep") || strings.Contains(out, "runningboardd") {
		t.Fatalf("another process's entries must not:\n%s", out)
	}
}

func TestSimLog_FiltersByGrep(t *testing.T) {
	deps, _, _, _ := logDeps(t, simLogCapture)

	out, _, err := executeCLI(t, deps, "sim", "log", "--grep", `"total":\d+`)
	if err != nil {
		t.Fatalf("sim log: %v", err)
	}
	if !strings.Contains(out, "portfolio response") {
		t.Fatalf("the matching entry is missing:\n%s", out)
	}
	if strings.Contains(out, "request failed") {
		t.Fatalf("a non-matching entry survived:\n%s", out)
	}
}

func TestSimLog_SinceIsPassedAsAWindow(t *testing.T) {
	deps, _, starts, _ := logDeps(t, simLogCapture)

	if _, _, err := executeCLI(t, deps, "sim", "log", "--since", "5m"); err != nil {
		t.Fatalf("sim log: %v", err)
	}
	ran := strings.Join((*starts)[0], " ")
	if !strings.Contains(ran, "--last 300s") {
		t.Fatalf("ran %q, want the window in units `log show` accepts", ran)
	}
}

func TestSimLog_FollowStreamsInsteadOfReadingHistory(t *testing.T) {
	deps, _, starts, _ := logDeps(t, simLogCapture)

	out, _, err := executeCLI(t, deps, "sim", "log", "--follow")
	if err != nil {
		t.Fatalf("sim log --follow: %v", err)
	}
	ran := strings.Join((*starts)[0], " ")
	if !strings.Contains(ran, "log stream") || strings.Contains(ran, "log show") {
		t.Fatalf("ran %q, want `log stream`", ran)
	}
	if !strings.Contains(out, "portfolio response") {
		t.Fatalf("streamed entries must be printed:\n%s", out)
	}
}

func TestSimLog_FollowPrintsEachEntryAsItArrives(t *testing.T) {
	// The whole point of --follow: an entry that has to wait for the next one
	// is an entry an agent watching a live app cannot use.
	deps, stream, _, _ := logDeps(t, "")
	out, done := executeCLIStreaming(t, deps, "sim", "log", "--follow")

	_, _ = io.WriteString(stream.writer, "2026-08-17 16:31:59.078 Df Nimbus[4242:714e3fa] [] first\n")
	waitFor(t, func() bool { return strings.Contains(out.String(), "first") },
		"the first entry was not printed before the second arrived")

	_, _ = io.WriteString(stream.writer, "2026-08-17 16:31:59.079 Df Nimbus[4242:714e3fa] [] second\n")
	waitFor(t, func() bool { return strings.Contains(out.String(), "second") }, "the second entry never arrived")

	_ = stream.writer.CloseWithError(io.EOF)
	if err := <-done; err != nil {
		t.Fatalf("sim log --follow: %v", err)
	}
}

func TestSimLog_FollowStopsCleanlyOnSignalAndLeavesNoChild(t *testing.T) {
	// A `--follow` that is interrupted must end the command AND stop the child
	// it started. Past `ao sim` slices leaked exactly this.
	deps, stream, _, _ := logDeps(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	out, done := executeCLIStreamingContext(t, ctx, deps, "sim", "log", "--follow")

	_, _ = io.WriteString(stream.writer, "2026-08-17 16:31:59.078 Df Nimbus[4242:714e3fa] [] live\n")
	waitFor(t, func() bool { return strings.Contains(out.String(), "live") }, "nothing was streamed")

	cancel() // what a Ctrl-C, a killed parent or a timed-out harness looks like

	select {
	case err := <-done:
		// An interrupted follow is not a failure: it did its job until it was
		// stopped.
		if err != nil {
			t.Fatalf("an interrupted follow must exit cleanly, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("`ao sim log --follow` did not return after its context was cancelled")
	}
	if stream.stops() == 0 {
		t.Fatal("the `log` child was left running")
	}
}

func TestSimLog_NothingMatchedSaysWhatDidLogAndWhy(t *testing.T) {
	// Silence is the worst answer: the agent cannot tell "the command is
	// broken" from "the name is wrong" from "print never reaches this log".
	deps, _, _, _ := logDeps(t, simLogCapture)

	out, _, err := executeCLI(t, deps, "sim", "log", "--process", "MyApp")
	if err != nil {
		t.Fatalf("nothing matching is not a failure of the command: %v", err)
	}
	for _, want := range []string{
		"MyApp",       // what was asked for
		"Nimbus",      // and what actually logged, so the name can be fixed
		"SpringBoard", //
		"NSLog",       // the limitation that explains most empty results
		"print",       //
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("an empty result must mention %q:\n%s", want, out)
		}
	}
}

func TestSimLog_CapsTheOutputAndSaysWhatItDropped(t *testing.T) {
	deps, _, _, _ := logDeps(t, simLogCapture)

	out, _, err := executeCLI(t, deps, "sim", "log", "--max-lines", "1")
	if err != nil {
		t.Fatalf("sim log: %v", err)
	}
	// The window is history, so the newest entries are the ones worth keeping.
	if !strings.Contains(out, "display asleep") {
		t.Fatalf("a capped read must keep the most recent entries:\n%s", out)
	}
	if strings.Contains(out, "portfolio response") {
		t.Fatalf("more than --max-lines entries were printed:\n%s", out)
	}
	if !strings.Contains(out, "--max-lines") {
		t.Fatalf("a capped read must say it was capped and how to raise it:\n%s", out)
	}
}

func TestSimLog_JSONIsOneObjectWithTheEntries(t *testing.T) {
	deps, _, _, _ := logDeps(t, simLogCapture)

	out, _, err := executeCLI(t, deps, "sim", "log", "--process", "Nimbus", "--json")
	if err != nil {
		t.Fatalf("sim log --json: %v", err)
	}
	var got struct {
		UDID    string `json:"udid"`
		Name    string `json:"name"`
		Since   string `json:"since"`
		Follow  bool   `json:"follow"`
		Process string `json:"process"`
		Entries []struct {
			Time      string `json:"time"`
			Type      string `json:"type"`
			Process   string `json:"process"`
			PID       int    `json:"pid"`
			Subsystem string `json:"subsystem"`
			Message   string `json:"message"`
		} `json:"entries"`
		Matched int          `json:"matched"`
		Scanned int          `json:"scanned"`
		Lease   simLeaseView `json:"lease"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if got.UDID != simUDIDProMax || got.Name != "iPhone 17 Pro Max" {
		t.Fatalf("the result must say which device it came from: %s", out)
	}
	if len(got.Entries) != 2 || got.Matched != 2 || got.Scanned != 4 {
		t.Fatalf("matched/scanned = %d/%d with %d entries: %s", got.Matched, got.Scanned, len(got.Entries), out)
	}
	if got.Entries[0].PID != 4242 || got.Entries[0].Type != "Default" {
		t.Fatalf("entry = %+v, want the parsed fields", got.Entries[0])
	}
	if got.Entries[1].Subsystem != "com.example.nimbus" {
		t.Fatalf("the subsystem must be its own field: %+v", got.Entries[1])
	}
	if got.Lease.State == "" {
		t.Fatalf("a read must still report who holds the device: %s", out)
	}
}

func TestSimLog_FollowJSONIsOneObjectPerLine(t *testing.T) {
	// A stream cannot be one object: it has no end. One entry per line is the
	// only shape that stays readable while it is still arriving.
	deps, _, _, _ := logDeps(t, simLogCapture)

	out, _, err := executeCLI(t, deps, "sim", "log", "--follow", "--json", "--process", "Nimbus")
	if err != nil {
		t.Fatalf("sim log --follow --json: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want one per entry:\n%s", len(lines), out)
	}
	for _, line := range lines {
		var entry struct {
			Process string `json:"process"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("each line must be an entry object: %v\n%s", err, line)
		}
		if entry.Process != "Nimbus" {
			t.Fatalf("entry = %+v", entry)
		}
	}
}

func TestSimLog_TakesNoLeaseAndIsNotBlockedByOne(t *testing.T) {
	// Reading a log touches no HID and cannot corrupt anybody's gesture, so it
	// is not blocked by a lease - and, like every other read, it says who holds
	// the device so reading is never mistaken for permission to drive it.
	deps, _, _, daemon := logDeps(t, simLogCapture)
	daemon.leases[simUDIDProMax] = simLeaseClient{
		UDID: simUDIDProMax, SessionID: "mer-3", AcquiredAt: simFixedNow, ExpiresAt: simFixedNow.Add(7 * time.Minute),
	}

	out, _, err := executeCLI(t, deps, "sim", "log")
	if err != nil {
		t.Fatalf("a read must not be blocked by someone else's lease: %v", err)
	}
	if !strings.Contains(out, "Lease:") {
		t.Fatalf("the read must report the lease:\n%s", out)
	}
	if strings.Contains(daemon.callLog(), "/hold") {
		t.Fatalf("reading must not take a gesture hold: %s", daemon.callLog())
	}
}

func TestSimLog_RefusesADeviceThatIsNotBooted(t *testing.T) {
	deps, _, _, _ := logDeps(t, simLogCapture)

	_, _, err := executeCLI(t, deps, "sim", "log", "--udid", simUDIDPro)
	if err == nil || !strings.Contains(err.Error(), "not booted") {
		t.Fatalf("err = %v, want a not-booted refusal", err)
	}
}

func TestSimLog_UsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "a window and a live stream are different questions",
			args: []string{"sim", "log", "--follow", "--since", "5m"},
			want: "--since",
		},
		{name: "a pattern that will not compile", args: []string{"sim", "log", "--grep", "(unclosed"}, want: "--grep"},
		{name: "a window that is not a duration", args: []string{"sim", "log", "--since", "ages"}, want: "--since"},
		{name: "a window of nothing", args: []string{"sim", "log", "--since", "0s"}, want: "--since"},
		{name: "no room for any output", args: []string{"sim", "log", "--max-lines", "0"}, want: "--max-lines"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, _, _ := logDeps(t, simLogCapture)
			_, _, err := executeCLI(t, deps, tc.args...)
			if !errors.As(err, &usageError{}) {
				t.Fatalf("err = %v, want a usage error (exit 2)", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

func TestSimLog_AChildThatFailedIsReportedWithWhatItSaid(t *testing.T) {
	deps, stream, _, _ := logDeps(t, "")
	stream.err = errors.New("exit status 1: log: unrecognized option `--style'")
	stream.feed("")

	_, _, err := executeCLI(t, deps, "sim", "log")
	if err == nil {
		t.Fatal("a `log` child that failed must fail the command")
	}
	if !strings.Contains(err.Error(), "unrecognized option") {
		t.Fatalf("err = %v, want what the child said", err)
	}
}

func TestSimLog_HelpNamesThePrintLimitationAndTheHazard(t *testing.T) {
	// The limitation an agent will otherwise read as "the command is broken":
	// `print` goes to stdout, which SpringBoard discards, so it never reaches
	// the unified log however long you stare at it.
	deps, _, _, _ := logDeps(t, simLogCapture)

	out, _, err := executeCLI(t, deps, "sim", "log", "--help")
	if err != nil {
		t.Fatalf("sim log --help: %v", err)
	}
	help := strings.ToLower(out)
	for _, want := range []string{"print", "nslog", "stdout", "--console-pipe", "main thread"} {
		if !strings.Contains(help, want) {
			t.Fatalf("the command's own help must mention %q:\n%s", want, out)
		}
	}
}

// --- helpers for a command that does not return on its own ------------------

// syncBuffer is an output buffer a test can read while the command is still
// writing to it.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func executeCLIStreaming(t *testing.T, deps Deps, args ...string) (*syncBuffer, <-chan error) {
	t.Helper()
	return executeCLIStreamingContext(t, context.Background(), deps, args...)
}

func executeCLIStreamingContext(
	t *testing.T, ctx context.Context, deps Deps, args ...string,
) (*syncBuffer, <-chan error) {
	t.Helper()
	out := &syncBuffer{}
	deps.Out, deps.Err = out, &syncBuffer{}
	if deps.Sleep == nil {
		deps.Sleep = func(time.Duration) {}
	}
	cmd := NewRootCommand(deps)
	cmd.SetArgs(args)
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	return out, done
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal(msg)
}

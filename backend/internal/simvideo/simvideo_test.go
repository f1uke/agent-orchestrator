package simvideo

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrecord"
)

// Everything here runs without Xcode, a mac or a device: the Runner is the
// seam, and the promises worth pinning - nothing is left running, a recording
// belongs to one session, a stop finalizes the file - are all promises about
// this package's own bookkeeping rather than about simctl.

const testUDID = "11111111-2222-3333-4444-555555555555"

// fakeProc is a recorder that writes its file when it is interrupted, the way
// simctl does. Writing on interrupt rather than on start is the whole point:
// a test that asserted on a file present before the stop would be asserting
// something the real recorder does not do.
type fakeProc struct {
	pid  int
	path string
	body string

	mu          sync.Mutex
	interrupted bool
	// exitAfterInterrupt delays the exit, so a test can observe a stop that is
	// genuinely waiting for the file.
	exitAfterInterrupt time.Duration

	exited chan struct{}
	once   sync.Once
}

func newFakeProc(pid int, path, body string) *fakeProc {
	return &fakeProc{pid: pid, path: path, body: body, exited: make(chan struct{})}
}

func (p *fakeProc) Pid() int { return p.pid }

func (p *fakeProc) Interrupt() error {
	p.mu.Lock()
	p.interrupted = true
	p.mu.Unlock()
	p.once.Do(func() {
		go func() {
			if p.exitAfterInterrupt > 0 {
				time.Sleep(p.exitAfterInterrupt)
			}
			_ = os.WriteFile(p.path, []byte(p.body), 0o600)
			close(p.exited)
		}()
	})
	return nil
}

func (p *fakeProc) Wait() error {
	<-p.exited
	return nil
}

func (p *fakeProc) wasInterrupted() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.interrupted
}

// fakeRunner hands out fakeProcs and remembers them.
type fakeRunner struct {
	mu    sync.Mutex
	procs []*fakeProc
	body  string
	// err, when set, is what Start fails with.
	err error
}

func (r *fakeRunner) Start(_ context.Context, _, path string) (Process, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	pid := 4000 + len(r.procs)
	body := r.body
	if body == "" {
		body = "a playable video"
	}
	p := newFakeProc(pid, path, body)
	r.procs = append(r.procs, p)
	return p, nil
}

func (r *fakeRunner) last() *fakeProc {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.procs) == 0 {
		return nil
	}
	return r.procs[len(r.procs)-1]
}

func newTestRecorder(t *testing.T, opts ...Option) (*Recorder, *fakeRunner, string) {
	t.Helper()
	dir := t.TempDir()
	runner := &fakeRunner{}
	all := append([]Option{WithRunner(runner), WithReconcileInterval(0)}, opts...)
	v := New(dir, all...)
	t.Cleanup(v.Shutdown)
	return v, runner, dir
}

func TestStart_RecordsIntoTheSessionsOwnVideosDirectory(t *testing.T) {
	v, _, dir := newTestRecorder(t)

	rec, err := v.Start(context.Background(), "sess-1", testUDID, 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	want := simrecord.VideosDir(dir, "sess-1")
	if got := filepath.Dir(rec.Path); got != want {
		t.Errorf("a recording must land in its session's own videos directory, outside every repository:\n got %s\nwant %s", got, want)
	}
	if !strings.HasSuffix(rec.Path, Extension) {
		t.Errorf("the file must be named %s, which is the container simctl actually writes: %s", Extension, rec.Path)
	}
	if !strings.Contains(rec.Path, testUDID) {
		t.Errorf("the file name must name the device it is of: %s", rec.Path)
	}
	if rec.MaxDuration != DefaultMaxDuration {
		t.Errorf("an omitted max duration must become the default, not zero: %s", rec.MaxDuration)
	}
}

func TestStart_RefusesASecondRecorderOnTheSameDeviceAndNamesTheHolder(t *testing.T) {
	v, runner, _ := newTestRecorder(t)

	if _, err := v.Start(context.Background(), "sess-1", testUDID, 0); err != nil {
		t.Fatalf("start: %v", err)
	}
	_, err := v.Start(context.Background(), "sess-2", testUDID, 0)

	var held *HeldError
	if !errors.As(err, &held) {
		t.Fatalf("a second start on one device must be refused, not run two recorders at one file: %v", err)
	}
	if held.Recording.SessionID != "sess-1" {
		t.Errorf("the refusal must name who holds it: %q", held.Recording.SessionID)
	}
	if len(runner.procs) != 1 {
		t.Fatalf("the refused start must not have spawned anything: %d recorders", len(runner.procs))
	}
}

func TestStop_BelongsToTheSessionThatStartedIt(t *testing.T) {
	v, runner, _ := newTestRecorder(t)

	if _, err := v.Start(context.Background(), "sess-1", testUDID, 0); err != nil {
		t.Fatalf("start: %v", err)
	}

	_, err := v.Stop(context.Background(), "sess-2", testUDID)
	var held *HeldError
	if !errors.As(err, &held) {
		t.Fatalf("another session must not be able to stop mine: %v", err)
	}
	if runner.last().wasInterrupted() {
		t.Fatal("a refused stop must not have signalled the recorder")
	}

	if _, err := v.Stop(context.Background(), "sess-1", testUDID); err != nil {
		t.Fatalf("the owning session's stop: %v", err)
	}
}

func TestStop_InterruptsAndWaitsForTheFinishedFile(t *testing.T) {
	v, runner, _ := newTestRecorder(t)
	runner.body = "the finalized video"

	rec, err := v.Start(context.Background(), "sess-1", testUDID, 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// Nothing on disk yet: simctl writes the file when it is interrupted, and a
	// stop that did not wait would report a size of zero for a real recording.
	if _, err := os.Stat(rec.Path); !os.IsNotExist(err) {
		t.Fatalf("the video must not exist before the recording is stopped: %v", err)
	}
	runner.last().exitAfterInterrupt = 20 * time.Millisecond

	stopped, err := v.Stop(context.Background(), "sess-1", testUDID)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !runner.last().wasInterrupted() {
		t.Error("a stop must INTERRUPT the recorder: a killed one leaves a file nothing can open")
	}
	if stopped.Bytes != int64(len("the finalized video")) {
		t.Errorf("stop must report the finished file's size, so it must have waited for it: %d", stopped.Bytes)
	}
	if stopped.StopReason != StopReasonRequested {
		t.Errorf("stop reason = %q, want %q", stopped.StopReason, StopReasonRequested)
	}
	if stopped.StoppedAt == nil {
		t.Error("a finished recording must say when it stopped")
	}
	if _, err := os.Stat(stopped.Path); err != nil {
		t.Errorf("the video must be on disk once stop has returned: %v", err)
	}
}

func TestStop_LeavesNoOpenRecordingBehind(t *testing.T) {
	v, _, _ := newTestRecorder(t)

	if _, err := v.Start(context.Background(), "sess-1", testUDID, 0); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := v.Stop(context.Background(), "sess-1", testUDID); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if _, err := v.Status(testUDID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a stopped recording must not still read as open: %v", err)
	}
	if _, err := v.Stop(context.Background(), "sess-1", testUDID); !errors.Is(err, ErrNotFound) {
		t.Errorf("stopping twice must say there is nothing to stop: %v", err)
	}
	// And the device is free again, which is the point of releasing it.
	if _, err := v.Start(context.Background(), "sess-2", testUDID, 0); err != nil {
		t.Errorf("the device must be recordable again after a stop: %v", err)
	}
}

func TestMaxDuration_StopsItselfAndSaysSo(t *testing.T) {
	v, runner, _ := newTestRecorder(t)

	if _, err := v.Start(context.Background(), "sess-1", testUDID, MinMaxDuration); err != nil {
		t.Fatalf("start: %v", err)
	}
	// The cap is the failure a person actually hits - a recording nobody
	// stopped - so it has to finalize the file exactly as a stop does.
	waitFor(t, 5*time.Second, func() bool { return runner.last().wasInterrupted() })
	waitFor(t, 5*time.Second, func() bool {
		_, err := v.Status(testUDID)
		return errors.Is(err, ErrNotFound)
	})

	rec := runner.last()
	if _, err := os.Stat(rec.path); err != nil {
		t.Errorf("a capped recording must still leave a finalized file: %v", err)
	}
	// Waited for rather than read once: the device is released before the
	// handle is removed, so the recording reads as closed a moment before its
	// bookkeeping is gone. A stale handle is harmless - Sweep tolerates one -
	// but it must not still be there when the dust settles, or every later
	// daemon startup would try to reap a recorder that finished long ago.
	waitFor(t, 5*time.Second, func() bool {
		_, err := os.Stat(handlePath(rec.path))
		return os.IsNotExist(err)
	})
}

func TestStart_RefusesADurationOutsideItsBounds(t *testing.T) {
	v, _, _ := newTestRecorder(t)

	for _, d := range []time.Duration{time.Millisecond, MaxMaxDuration + time.Minute} {
		if _, err := v.Start(context.Background(), "sess-1", testUDID, d); !errors.Is(err, ErrInvalid) {
			t.Errorf("max duration %s must be refused rather than silently clamped: %v", d, err)
		}
	}
}

func TestSessionEnds_StopsTheRecordingItOwned(t *testing.T) {
	var mu sync.Mutex
	ended := map[domain.SessionID]bool{}
	live := func(_ context.Context, id domain.SessionID) bool {
		mu.Lock()
		defer mu.Unlock()
		return !ended[id]
	}
	v, runner, _ := newTestRecorder(t,
		WithSessionLiveness(live), WithReconcileInterval(5*time.Millisecond))

	if _, err := v.Start(context.Background(), "sess-1", testUDID, 0); err != nil {
		t.Fatalf("start: %v", err)
	}
	mu.Lock()
	ended["sess-1"] = true
	mu.Unlock()

	waitFor(t, time.Second, func() bool {
		_, err := v.Status(testUDID)
		return errors.Is(err, ErrNotFound)
	})
	if !runner.last().wasInterrupted() {
		t.Error("a recording must not outlive the session that owns it")
	}
}

func TestSessionThatCannotBeRead_IsNotTreatedAsDead(t *testing.T) {
	// A failed probe is not proof a session is dead (AGENTS.md), and acting on
	// one would destroy the recording somebody asked for.
	v, runner, _ := newTestRecorder(t,
		WithSessionLiveness(func(context.Context, domain.SessionID) bool { return true }),
		WithReconcileInterval(5*time.Millisecond))

	if _, err := v.Start(context.Background(), "sess-1", testUDID, 0); err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if runner.last().wasInterrupted() {
		t.Error("a live session's recording must be left alone")
	}
	if _, err := v.Status(testUDID); err != nil {
		t.Errorf("the recording must still be open: %v", err)
	}
}

func TestShutdown_StopsEveryOpenRecording(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{}
	v := New(dir, WithRunner(runner), WithReconcileInterval(0))

	for i, udid := range []string{testUDID, "22222222-2222-2222-2222-222222222222"} {
		if _, err := v.Start(context.Background(), domain.SessionID("sess-1"), udid, 0); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}

	v.Shutdown()

	for i, p := range runner.procs {
		if !p.wasInterrupted() {
			t.Errorf("recorder %d was left running past the daemon's exit", i)
		}
		if _, err := os.Stat(p.path); err != nil {
			t.Errorf("recorder %d left no finalized file: %v", i, err)
		}
		if _, err := os.Stat(handlePath(p.path)); !os.IsNotExist(err) {
			t.Errorf("recorder %d left its handle file behind: %v", i, err)
		}
	}
}

func TestSweep_ReapsARecorderAPreviousDaemonLeftRunning(t *testing.T) {
	dir := t.TempDir()
	videos := simrecord.VideosDir(dir, "sess-1")
	if err := os.MkdirAll(videos, 0o750); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(videos, "20260101-000000.000Z-"+testUDID+Extension)
	if err := os.WriteFile(video, []byte("truncated"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A process that is alive and ours to signal, standing in for the simctl a
	// SIGKILLed daemon left recording.
	orphan := startSleeper(t)
	body, err := json.Marshal(handle{
		PID: orphan.pid, UDID: testUDID, SessionID: "sess-1", Path: video, StartedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(handlePath(video), body, 0o600); err != nil {
		t.Fatal(err)
	}

	v := New(dir, WithRunner(&fakeRunner{}), WithReconcileInterval(0))
	t.Cleanup(v.Shutdown)

	if !orphan.interrupted(t) {
		t.Error("a recorder left running by a previous daemon must be signalled at startup, not left filling the disk")
	}
	if _, err := os.Stat(handlePath(video)); !os.IsNotExist(err) {
		t.Error("the handle of a reaped recorder must be removed, or every later startup reaps it again")
	}
}

func TestSweep_SurvivesAHandleWhoseProcessIsLongGone(t *testing.T) {
	dir := t.TempDir()
	videos := simrecord.VideosDir(dir, "sess-1")
	if err := os.MkdirAll(videos, 0o750); err != nil {
		t.Fatal(err)
	}
	video := filepath.Join(videos, "20260101-000000.000Z-"+testUDID+Extension)
	// The common case: the daemon and its children died together.
	body, _ := json.Marshal(handle{PID: 999999, UDID: testUDID, SessionID: "sess-1", Path: video})
	if err := os.WriteFile(handlePath(video), body, 0o600); err != nil {
		t.Fatal(err)
	}

	v := New(dir, WithRunner(&fakeRunner{}), WithReconcileInterval(0))
	t.Cleanup(v.Shutdown)

	if _, err := os.Stat(handlePath(video)); !os.IsNotExist(err) {
		t.Error("a handle whose process is gone must still be cleaned up")
	}
}

func TestPrune_KeepsTheNewestAndNeverTheFileJustWritten(t *testing.T) {
	v, runner, dir := newTestRecorder(t)
	videos := simrecord.VideosDir(dir, "sess-1")
	if err := os.MkdirAll(videos, 0o750); err != nil {
		t.Fatal(err)
	}
	// More old recordings than the budget allows, aged so the order is
	// unambiguous.
	old := make([]string, 0, KeepPerSession+3)
	for i := 0; i < KeepPerSession+3; i++ {
		p := filepath.Join(videos, "old-"+string(rune('a'+i))+Extension)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, time.Now().Add(-time.Duration(i+1)*time.Hour), time.Now().Add(-time.Duration(i+1)*time.Hour)); err != nil {
			t.Fatal(err)
		}
		old = append(old, p)
	}

	if _, err := v.Start(context.Background(), "sess-1", testUDID, 0); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := v.Stop(context.Background(), "sess-1", testUDID); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if _, err := os.Stat(runner.last().path); err != nil {
		t.Fatalf("the recording just written must never be pruned: %v", err)
	}
	left := countVideos(t, videos)
	if left != KeepPerSession {
		t.Errorf("a session's videos directory must be trimmed to %d recordings, found %d", KeepPerSession, left)
	}
	// Oldest first: the very oldest must be the ones that went.
	for _, p := range old[len(old)-3:] {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("pruning must take the OLDEST recordings first, but %s survived", filepath.Base(p))
		}
	}
}

func TestPrune_EnforcesTheByteBudgetTooAndLeavesOtherSessionsAlone(t *testing.T) {
	v, _, dir := newTestRecorder(t)
	mine := simrecord.VideosDir(dir, "sess-1")
	theirs := simrecord.VideosDir(dir, "sess-2")
	for _, d := range []string{mine, theirs} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	// One file over the whole byte budget, so the count bound cannot be what
	// removes it.
	fat := filepath.Join(mine, "fat"+Extension)
	if err := os.WriteFile(fat, make([]byte, KeepBytesPerSession+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fat, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(theirs, "other"+Extension)
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := v.Start(context.Background(), "sess-1", testUDID, 0); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := v.Stop(context.Background(), "sess-1", testUDID); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if _, err := os.Stat(fat); !os.IsNotExist(err) {
		t.Error("a recording over the byte budget must be pruned even when the count is within bounds")
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("pruning one session's recordings must never reach another session's: %v", err)
	}
}

func TestStart_ThatCannotSpawnLeavesNothingClaimed(t *testing.T) {
	v, runner, _ := newTestRecorder(t)
	runner.err = errors.New("xcrun exploded")

	if _, err := v.Start(context.Background(), "sess-1", testUDID, 0); err == nil {
		t.Fatal("a start that could not spawn must fail")
	}
	// The device must not read as busy afterwards, or one failed spawn would
	// make it unrecordable until the daemon restarts.
	if _, err := v.Status(testUDID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a failed start must not leave the device claimed: %v", err)
	}
	runner.err = nil
	if _, err := v.Start(context.Background(), "sess-1", testUDID, 0); err != nil {
		t.Errorf("the device must be recordable after a failed start: %v", err)
	}
}

func TestStart_WritesAHandleAFutureDaemonCanReap(t *testing.T) {
	v, runner, _ := newTestRecorder(t)

	rec, err := v.Start(context.Background(), "sess-1", testUDID, 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	body, err := os.ReadFile(handlePath(rec.Path))
	if err != nil {
		t.Fatalf("an open recording must leave a handle, or a killed daemon orphans it forever: %v", err)
	}
	var h handle
	if err := json.Unmarshal(body, &h); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if h.PID != runner.last().Pid() || h.UDID != testUDID || h.Path != rec.Path {
		t.Errorf("the handle must identify the process and what it is recording: %+v", h)
	}
}

// --- helpers ----------------------------------------------------------------

func waitFor(t *testing.T, limit time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition was never met")
}

func countVideos(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), Extension) {
			n++
		}
	}
	return n
}

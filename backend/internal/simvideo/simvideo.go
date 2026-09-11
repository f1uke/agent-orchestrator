// Package simvideo records an iOS Simulator's screen to a video file.
//
// It exists as a daemon-owned package rather than as CLI code because
// `xcrun simctl io <udid> recordVideo` is a process that OUTLIVES the command
// that asked for it: it runs until it is signalled and only then writes the
// file. "Start" and "stop" therefore arrive as two separate process
// invocations - often two separate agent turns - and something has to be alive
// in between to hold the handle. The daemon is the only thing in AO that is.
//
// The precedent is simstream.NewScreen: the daemon constructs it, defers
// Shutdown, and that is what guarantees no capture process outlives the daemon.
// This package follows it, and goes further because a recorder can also be
// orphaned by ways the daemon never sees - see Sweep.
//
// It is deliberately NOT part of internal/service/sim. That service's own doc
// says it never shells out to simctl and runs on any OS, which is what lets the
// lease logic be tested everywhere; a package that spawns simctl cannot promise
// that.
//
// # Nothing is left running
//
// Four ways a recording ends, and all four finalize the file the same way:
//
//   - Stop, by the session that started it.
//   - MaxDuration elapses. A recorder with no stop is the failure a person
//     actually hits, so the cap is part of the feature and not a safety net.
//   - The owning session ends. reconcile() asks, on a ticker, whether the
//     session is still live; one question covers every path that terminates a
//     session, because they all land on the same is_terminated bit.
//   - The daemon stops. Shutdown() stops every open recording.
//
// And one way the daemon cannot see, which is why the handle file exists: the
// daemon is SIGKILLed, or the machine loses power. Sweep(), at construction,
// finds the handle files of recordings nobody closed and signals whatever is
// still alive behind them.
//
// # Why SIGINT and not Kill
//
// simctl writes the moov atom when it is interrupted; a killed recorder leaves
// a file no player will open. Measured on macOS 26.4 / Xcode 26.3: `xcrun`
// EXECS simctl rather than forking it, so the pid we spawn is simctl itself and
// a plain SIGINT to it is enough. The process group is set anyway so a recorder
// never inherits a signal aimed at the daemon's own terminal.
package simvideo

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrecord"
)

// Bounds. Video is not a screenshot: measured on this project's reference
// device (iPhone 17 Pro Max, 1320x2868), a screen with real UI motion in it
// costs about 30 MB a minute, while an idle screen costs almost nothing -
// simctl emits frames only when the picture changes. So the cost is driven by
// how long a BUSY recording runs, and these are the three numbers that bound it.
const (
	// DefaultMaxDuration stops a recording nobody stopped. Ten minutes is long
	// enough for any single scenario a person plays by hand and short enough
	// that forgetting one costs a few hundred megabytes rather than a disk.
	DefaultMaxDuration = 10 * time.Minute
	// MaxMaxDuration is the ceiling on --max-duration. A recording longer than
	// this is not evidence of one thing any more, and it is the point where a
	// single file starts to dominate the retention budget below.
	MaxMaxDuration = 30 * time.Minute
	// MinMaxDuration is one second, matching the lease's MinTTL and for the same
	// reason: a very short bound is a real case (record exactly one animation),
	// not a typo to protect people from.
	MinMaxDuration = time.Second

	// KeepPerSession and KeepBytesPerSession prune a session's videos
	// directory after every stop, oldest first. Two bounds rather than one
	// because they fail differently: a count alone lets ten long recordings
	// hold a gigabyte and a half, and a byte budget alone lets one enormous
	// file evict every small one worth keeping.
	KeepPerSession      = 10
	KeepBytesPerSession = 1 << 30 // 1 GiB

	// Codec is h264 rather than simctl's hevc default, and that is a measured
	// choice, not a portability tax: over the same 6 s of UI motion h264 came
	// back at 2.74 MB against hevc's 4.37 MB. It also plays in Chromium, which
	// is where these files are headed when the desktop app learns to show them.
	Codec = "h264"

	// Extension is .mov because that is what simctl actually writes. Asked for
	// a file named .mp4 it still produced a QuickTime container (ftyp brand
	// `qt  `), so any other extension would be a lie told to whatever opens it.
	Extension = ".mov"

	// stampLayout keeps millisecond precision so two recordings from one
	// session cannot collide on a filename, matching `ao sim shot`.
	stampLayout = "20060102-150405.000"
)

// startedMarker is what simctl writes to stderr once the first frame has been
// processed. Waiting for it is the difference between "the command was spawned"
// and "the device is being recorded": a start that returns before the first
// frame would let a session drive the app and film none of it.
const startedMarker = "Recording started"

// Errors the HTTP layer maps onto status codes.
var (
	// ErrNotFound: no recording is open on that device.
	ErrNotFound = errors.New("simvideo: no recording is open on this device")
	// ErrInvalid: the request itself is wrong (bad duration, empty udid).
	ErrInvalid = errors.New("simvideo: invalid request")
	// ErrUnavailable: this machine cannot record at all.
	ErrUnavailable = errors.New("simvideo: xcrun is not available on this machine")
)

// HeldError says the device is being recorded by somebody else. It carries the
// holder, so no caller has to answer "by whom?" with a second, racy read - the
// same shape sim.HeldError uses for a lease.
type HeldError struct {
	Recording Recording
}

func (e *HeldError) Error() string {
	return fmt.Sprintf("simulator %s is already being recorded by @%s", e.Recording.UDID, e.Recording.SessionID)
}

// StopReason says what ended a recording. It is reported rather than inferred
// because "you stopped it" and "it hit its cap" produce the same file and mean
// different things to whoever reads the video next.
type StopReason string

const (
	// StopReasonRequested is the owning session running stop.
	StopReasonRequested StopReason = "requested"
	// StopReasonMaxDuration is the cap elapsing.
	StopReasonMaxDuration StopReason = "max-duration"
	// StopReasonSessionEnded is the session that owned it terminating.
	StopReasonSessionEnded StopReason = "session-ended"
	// StopReasonDaemonStopped is the daemon going down.
	StopReasonDaemonStopped StopReason = "daemon-stopped"
)

// A recorder reaped by Sweep has no reason of its own on purpose: its
// Recording died with the daemon that held it, so there is nobody left to
// report one to. What survives is the log line and the video itself.

// Recording is one screen recording, open or finished.
type Recording struct {
	UDID        string           `json:"udid"`
	SessionID   domain.SessionID `json:"sessionId"`
	Path        string           `json:"path"`
	StartedAt   time.Time        `json:"startedAt"`
	MaxDuration time.Duration    `json:"-"`
	// MaxDurationMS is MaxDuration on the wire; time.Duration serialises as a
	// nanosecond count that nothing on the other side would read as minutes.
	MaxDurationMS int64 `json:"maxDurationMs"`
	// StoppedAt and StopReason are set once it has finished.
	StoppedAt  *time.Time `json:"stoppedAt,omitempty"`
	StopReason StopReason `json:"stopReason,omitempty"`
	// Bytes is the finished file's size. Zero while it is still open: simctl
	// buffers and writes on exit, so the file on disk says nothing about how
	// much has been recorded until it has been finalized.
	Bytes int64 `json:"bytes"`
}

// Runner spawns the recorder. It is an interface so every path in this package
// can be tested without Xcode, a mac or a device - the same reason simpower
// takes a simctl.Runner.
type Runner interface {
	// Start begins recording udid to path and returns a handle. It must not
	// return until the recorder is actually recording, or until it has failed.
	Start(ctx context.Context, udid, path string) (Process, error)
}

// Process is a running recorder.
type Process interface {
	// Pid identifies it to a future daemon through the handle file.
	Pid() int
	// Interrupt asks it to finalize the file and exit.
	Interrupt() error
	// Wait blocks until it has exited, returning whatever it exited with.
	Wait() error
}

// SessionLiveness answers whether a session is still running. A session that
// cannot be looked up answers true: refusing to answer is not evidence that a
// session is dead, and stopping somebody's recording on a failed read would
// destroy the thing they asked for. The failure mode of the other choice is a
// recorder that runs until its cap, which the cap already bounds.
type SessionLiveness func(ctx context.Context, id domain.SessionID) bool

// Recorder owns every open recording on this machine.
type Recorder struct {
	dataDir string
	runner  Runner
	live    SessionLiveness
	log     *slog.Logger
	now     func() time.Time

	// reconcileEvery is how often open recordings are checked against their
	// owning session. Injected so tests do not sleep.
	reconcileEvery time.Duration

	mu     sync.Mutex
	open   map[string]*recorder // by udid
	closed bool

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

// recorder is one live recording plus the process behind it.
type recorder struct {
	rec  Recording
	proc Process
	// finished closes when the process has exited and rec has been completed.
	finished chan struct{}
	// stopping guards against two callers signalling the same process.
	stopping sync.Once
	// reason is what stopped it, written before finished closes.
	reason StopReason
	// waitErr is what the process exited with, for the log.
	waitErr error
}

// Option configures a Recorder.
type Option func(*Recorder)

// WithRunner replaces the simctl runner. Tests use it; the daemon does not.
func WithRunner(r Runner) Option { return func(v *Recorder) { v.runner = r } }

// WithSessionLiveness teaches the recorder how to ask whether a session is
// still running. Without it, no recording is ever stopped for that reason -
// which is the right default for a Recorder built without a store, not a
// silently weakened guarantee, because the duration cap still bounds it.
func WithSessionLiveness(f SessionLiveness) Option { return func(v *Recorder) { v.live = f } }

// WithClock replaces time.Now.
func WithClock(now func() time.Time) Option { return func(v *Recorder) { v.now = now } }

// WithLogger sets the logger.
func WithLogger(l *slog.Logger) Option { return func(v *Recorder) { v.log = l } }

// WithReconcileInterval sets how often open recordings are checked against
// their owning sessions.
func WithReconcileInterval(d time.Duration) Option {
	return func(v *Recorder) { v.reconcileEvery = d }
}

// New builds a Recorder and reaps whatever a previous daemon left behind.
//
// The sweep happens here, at construction, rather than lazily on first use:
// a machine whose daemon was killed mid-recording has a simctl process writing
// frames right now, and waiting for somebody to run `ao sim record` before
// noticing would mean the orphan runs until the machine reboots.
func New(dataDir string, opts ...Option) *Recorder {
	v := &Recorder{
		dataDir:        dataDir,
		runner:         simctlRunner{},
		log:            slog.Default(),
		now:            time.Now,
		reconcileEvery: 15 * time.Second,
		open:           map[string]*recorder{},
		stop:           make(chan struct{}),
		done:           make(chan struct{}),
	}
	for _, opt := range opts {
		opt(v)
	}
	v.Sweep()
	go v.loop()
	return v
}

// Start opens a recording on udid for sessionID.
func (v *Recorder) Start(ctx context.Context, sessionID domain.SessionID, udid string, maxDuration time.Duration) (Recording, error) {
	udid = strings.TrimSpace(udid)
	if udid == "" {
		return Recording{}, fmt.Errorf("%w: no simulator udid", ErrInvalid)
	}
	if strings.TrimSpace(string(sessionID)) == "" {
		return Recording{}, fmt.Errorf("%w: no session id", ErrInvalid)
	}
	switch {
	case maxDuration == 0:
		maxDuration = DefaultMaxDuration
	case maxDuration < MinMaxDuration:
		return Recording{}, fmt.Errorf("%w: a recording must be allowed at least %s", ErrInvalid, MinMaxDuration)
	case maxDuration > MaxMaxDuration:
		return Recording{}, fmt.Errorf("%w: a recording may not run longer than %s", ErrInvalid, MaxMaxDuration)
	}

	startedAt := v.now().UTC()
	path := filepath.Join(
		simrecord.VideosDir(v.dataDir, string(sessionID)),
		startedAt.Format(stampLayout)+"Z-"+udid+Extension,
	)

	// Claim the device BEFORE spawning anything. Two starts racing on one udid
	// would otherwise both spawn a recorder and both write to the same
	// directory, which is exactly the "two recorders fighting over one file"
	// this refusal exists to prevent.
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return Recording{}, errors.New("simvideo: the daemon is shutting down")
	}
	if existing, ok := v.open[udid]; ok {
		rec := existing.rec
		v.mu.Unlock()
		return Recording{}, &HeldError{Recording: rec}
	}
	pending := &recorder{
		rec: Recording{
			UDID: udid, SessionID: sessionID, Path: path, StartedAt: startedAt,
			MaxDuration: maxDuration, MaxDurationMS: maxDuration.Milliseconds(),
		},
		finished: make(chan struct{}),
	}
	v.open[udid] = pending
	v.mu.Unlock()

	proc, err := v.spawn(ctx, udid, path)
	if err != nil {
		v.mu.Lock()
		// Only drop our own claim: a slower path must never release a
		// recording somebody else has since started.
		if v.open[udid] == pending {
			delete(v.open, udid)
		}
		v.mu.Unlock()
		close(pending.finished)
		return Recording{}, err
	}

	pending.proc = proc
	if err := writeHandle(pending.rec, proc.Pid()); err != nil {
		// A recording whose handle could not be written is a recording a future
		// daemon could not reap, which is the one thing this must not ship. Undo it.
		_ = proc.Interrupt()
		_ = proc.Wait()
		v.mu.Lock()
		if v.open[udid] == pending {
			delete(v.open, udid)
		}
		v.mu.Unlock()
		close(pending.finished)
		return Recording{}, fmt.Errorf("record the handle for the recording on %s: %w", udid, err)
	}

	go v.watch(pending)
	return pending.rec, nil
}

// Status reports a device's open recording.
func (v *Recorder) Status(udid string) (Recording, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	r, ok := v.open[strings.TrimSpace(udid)]
	if !ok {
		return Recording{}, ErrNotFound
	}
	return r.rec, nil
}

// Stop ends the recording sessionID holds on udid and returns the finished
// file. A recording belongs to the session that started it: another session's
// stop is refused rather than honoured, the same way a lease is.
func (v *Recorder) Stop(ctx context.Context, sessionID domain.SessionID, udid string) (Recording, error) {
	v.mu.Lock()
	r, ok := v.open[strings.TrimSpace(udid)]
	v.mu.Unlock()
	if !ok {
		return Recording{}, ErrNotFound
	}
	if r.rec.SessionID != sessionID {
		return Recording{}, &HeldError{Recording: r.rec}
	}
	return v.finish(ctx, r, StopReasonRequested)
}

// Shutdown stops every open recording and waits for the files to finalize. It
// is what the daemon defers, and it is the reason a normal daemon exit cannot
// leave a recorder running.
func (v *Recorder) Shutdown() {
	v.stopOnce.Do(func() { close(v.stop) })
	<-v.done

	v.mu.Lock()
	v.closed = true
	open := make([]*recorder, 0, len(v.open))
	for _, r := range v.open {
		open = append(open, r)
	}
	v.mu.Unlock()

	var wg sync.WaitGroup
	for _, r := range open {
		wg.Add(1)
		go func(r *recorder) {
			defer wg.Done()
			// Background context on purpose: shutdown is exactly when the
			// request contexts are already cancelled, and a cancelled stop is
			// a truncated video.
			if _, err := v.finish(context.Background(), r, StopReasonDaemonStopped); err != nil {
				v.log.Warn("simvideo: could not stop a recording on the way out",
					"udid", r.rec.UDID, "error", err)
			}
		}(r)
	}
	wg.Wait()
}

// finish signals the recorder, waits for it to write the file, and prunes.
func (v *Recorder) finish(ctx context.Context, r *recorder, reason StopReason) (Recording, error) {
	r.stopping.Do(func() {
		r.reason = reason
		if err := r.proc.Interrupt(); err != nil {
			v.log.Warn("simvideo: could not interrupt the recorder; the video may be truncated",
				"udid", r.rec.UDID, "error", err)
		}
	})
	select {
	case <-r.finished:
	case <-ctx.Done():
		// The file is still being written by a process we have already
		// signalled; watch() will complete it. Reporting the caller's own
		// cancellation is honest, and nothing is orphaned by it.
		return Recording{}, ctx.Err()
	}

	v.mu.Lock()
	rec := r.rec
	v.mu.Unlock()
	return rec, nil
}

// watch waits for one recorder to exit, completes its Recording, and cleans up.
func (v *Recorder) watch(r *recorder) {
	timer := time.NewTimer(r.rec.MaxDuration)
	defer timer.Stop()

	exited := make(chan error, 1)
	go func() { exited <- r.proc.Wait() }()

	select {
	case err := <-exited:
		r.waitErr = err
	case <-timer.C:
		r.stopping.Do(func() {
			r.reason = StopReasonMaxDuration
			if err := r.proc.Interrupt(); err != nil {
				v.log.Warn("simvideo: could not interrupt a recording at its cap",
					"udid", r.rec.UDID, "error", err)
			}
		})
		r.waitErr = <-exited
	}

	stoppedAt := v.now().UTC()
	reason := r.reason
	if reason == "" {
		// It exited without being asked: simctl failed, or somebody killed it
		// from outside. Say so rather than reporting a stop nobody requested.
		reason = StopReasonRequested
	}

	v.mu.Lock()
	r.rec.StoppedAt = &stoppedAt
	r.rec.StopReason = reason
	if info, err := os.Stat(r.rec.Path); err == nil {
		r.rec.Bytes = info.Size()
	}
	if v.open[r.rec.UDID] == r {
		delete(v.open, r.rec.UDID)
	}
	rec := r.rec
	v.mu.Unlock()

	removeHandle(rec.Path)
	if r.waitErr != nil {
		v.log.Warn("simvideo: the recorder exited with an error",
			"udid", rec.UDID, "path", rec.Path, "error", r.waitErr)
	}
	v.prune(string(rec.SessionID), rec.Path)
	close(r.finished)
}

// loop reconciles open recordings against their owning sessions.
func (v *Recorder) loop() {
	defer close(v.done)
	if v.reconcileEvery <= 0 {
		<-v.stop
		return
	}
	ticker := time.NewTicker(v.reconcileEvery)
	defer ticker.Stop()
	for {
		select {
		case <-v.stop:
			return
		case <-ticker.C:
			v.reconcile()
		}
	}
}

// reconcile stops the recordings whose owning session has ended.
//
// One question - "is this session still live?" - rather than a hook on each of
// the many paths that end a session. Every one of them lands on the same
// is_terminated bit, so asking the bit cannot be bypassed by a path somebody
// adds later, which a set of hooks silently could.
func (v *Recorder) reconcile() {
	if v.live == nil {
		return
	}
	v.mu.Lock()
	open := make([]*recorder, 0, len(v.open))
	for _, r := range v.open {
		open = append(open, r)
	}
	v.mu.Unlock()

	ctx := context.Background()
	for _, r := range open {
		if v.live(ctx, r.rec.SessionID) {
			continue
		}
		v.log.Info("simvideo: stopping a recording whose session has ended",
			"udid", r.rec.UDID, "session", r.rec.SessionID, "path", r.rec.Path)
		if _, err := v.finish(ctx, r, StopReasonSessionEnded); err != nil {
			v.log.Warn("simvideo: could not stop the recording of an ended session",
				"udid", r.rec.UDID, "error", err)
		}
	}
}

// spawn starts the recorder process, creating the directory it writes into.
func (v *Recorder) spawn(ctx context.Context, udid, path string) (Process, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create the recording directory: %w", err)
	}
	proc, err := v.runner.Start(ctx, udid, path)
	if err != nil {
		// A spawn that failed may still have created an empty file; leaving it
		// would put a zero-byte "recording" in a session's directory.
		if info, statErr := os.Stat(path); statErr == nil && info.Size() == 0 {
			_ = os.Remove(path)
		}
		return nil, err
	}
	return proc, nil
}

// --- the handle file --------------------------------------------------------

// handle is what a future daemon needs to reap a recorder this one started. It
// sits beside the video rather than in a registry of its own so that a person
// who finds a truncated .mov also finds the note explaining what happened to it.
type handle struct {
	PID       int              `json:"pid"`
	UDID      string           `json:"udid"`
	SessionID domain.SessionID `json:"sessionId"`
	Path      string           `json:"path"`
	StartedAt time.Time        `json:"startedAt"`
}

// handlePath is the video's path with the handle suffix appended, so the two
// sort together and the glob that finds one can never match a video.
func handlePath(videoPath string) string { return videoPath + ".recording.json" }

func writeHandle(rec Recording, pid int) error {
	body, err := json.Marshal(handle{
		PID: pid, UDID: rec.UDID, SessionID: rec.SessionID, Path: rec.Path, StartedAt: rec.StartedAt,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(handlePath(rec.Path), body, 0o600)
}

func removeHandle(videoPath string) { _ = os.Remove(handlePath(videoPath)) }

// Sweep reaps recorders a previous daemon left running.
//
// This is the path nothing else covers: Shutdown handles a daemon that exits,
// and reconcile handles a session that ends, but a daemon that is SIGKILLed
// runs neither - and its simctl children keep recording, because a child does
// not die when its parent does. The handle files are what is left of them.
func (v *Recorder) Sweep() {
	pattern := filepath.Join(v.dataDir, "sim", "*", "videos", "*"+Extension+".recording.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		v.log.Warn("simvideo: could not look for orphaned recordings", "error", err)
		return
	}
	for _, match := range matches {
		body, err := os.ReadFile(match)
		if err != nil {
			v.log.Warn("simvideo: could not read an orphaned recording's handle", "path", match, "error", err)
			continue
		}
		var h handle
		if err := json.Unmarshal(body, &h); err != nil {
			v.log.Warn("simvideo: an orphaned recording's handle is unreadable; removing it", "path", match, "error", err)
			_ = os.Remove(match)
			continue
		}
		if h.PID > 0 {
			if err := interruptPID(h.PID); err != nil {
				// Already gone is the common case and not worth a warning: the
				// daemon and its children usually die together.
				v.log.Debug("simvideo: an orphaned recorder was no longer running",
					"pid", h.PID, "udid", h.UDID, "error", err)
			} else {
				v.log.Info("simvideo: interrupted a recorder left over from a previous daemon",
					"pid", h.PID, "udid", h.UDID, "path", h.Path)
				// Give it a moment to write the moov atom. Not waited on
				// properly because it is not our child - we cannot wait4 it -
				// and blocking daemon startup on somebody else's process is
				// worse than a video that finalizes a beat after we looked.
				time.Sleep(500 * time.Millisecond)
			}
		}
		_ = os.Remove(match)
	}
}

// --- retention --------------------------------------------------------------

// prune keeps a session's videos directory inside both bounds, oldest first.
// keep is the file just written, which is never a candidate however large it is:
// deleting the recording somebody is about to read would be the worst possible
// way to enforce a budget.
func (v *Recorder) prune(sessionID, keep string) {
	dir := simrecord.VideosDir(v.dataDir, sessionID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type video struct {
		path string
		mod  time.Time
		size int64
	}
	videos := make([]video, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), Extension) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		videos = append(videos, video{path: filepath.Join(dir, e.Name()), mod: info.ModTime(), size: info.Size()})
	}
	// Newest first, so the walk below spends the budget on what a person is
	// most likely to still want.
	sort.Slice(videos, func(i, j int) bool { return videos[i].mod.After(videos[j].mod) })

	var kept int
	var bytes int64
	for _, vid := range videos {
		kept++
		bytes += vid.size
		if vid.path == keep {
			continue
		}
		if kept <= KeepPerSession && bytes <= KeepBytesPerSession {
			continue
		}
		if err := os.Remove(vid.path); err != nil {
			v.log.Warn("simvideo: could not prune an old recording", "path", vid.path, "error", err)
			continue
		}
		removeHandle(vid.path)
		kept--
		bytes -= vid.size
		v.log.Info("simvideo: pruned an old recording", "path", vid.path, "session", sessionID)
	}
}

// --- the real runner --------------------------------------------------------

// simctlRunner is the production Runner: `xcrun simctl io <udid> recordVideo`.
type simctlRunner struct{}

func (simctlRunner) Start(ctx context.Context, udid, path string) (Process, error) {
	bin, err := exec.LookPath(simctl.Binary)
	if err != nil {
		return nil, fmt.Errorf("%w: %s is not on PATH, so `ao sim record` cannot record anything", ErrUnavailable, simctl.Binary)
	}
	// NOT CommandContext: the request context dies with the HTTP request that
	// started the recording, and a recorder that dies with its request records
	// nothing. Its lifetime is this package's business, not the request's.
	cmd := exec.Command(bin, "simctl", "io", udid, "recordVideo",
		"--codec", Codec, "--force", path)
	// Its own process group, so a signal aimed at the daemon's terminal (a
	// Ctrl-C in a foreground `ao daemon`) cannot reach a recorder and kill it
	// the one way that truncates the file.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("read the recorder's output: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start `simctl io %s recordVideo`: %w", udid, err)
	}

	p := &simctlProcess{cmd: cmd}
	// simctl prints "Recording started" once the first frame is in. Returning
	// before that would let a session drive the app and film none of it.
	if err := awaitStart(ctx, stderr); err != nil {
		_ = p.Interrupt()
		_ = p.Wait()
		return nil, err
	}
	return p, nil
}

// awaitStart blocks until the recorder says it is recording, it exits, or ctx
// is done. Whatever it read is kept, so a failure can say what simctl said.
func awaitStart(ctx context.Context, stderr io.ReadCloser) error {
	type result struct {
		started bool
		output  string
	}
	done := make(chan result, 1)
	go func() {
		var seen strings.Builder
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			seen.WriteString(line)
			seen.WriteString("\n")
			if strings.Contains(line, startedMarker) {
				done <- result{started: true, output: seen.String()}
				// Keep draining: a full stderr pipe would block the recorder
				// mid-recording, and nothing else is reading it.
				for scanner.Scan() {
				}
				return
			}
		}
		done <- result{output: seen.String()}
	}()

	select {
	case r := <-done:
		if r.started {
			return nil
		}
		return fmt.Errorf("`simctl io recordVideo` stopped before it started recording: %s",
			strings.TrimSpace(simctl.Output([]byte(r.output))))
	case <-ctx.Done():
		return ctx.Err()
	}
}

type simctlProcess struct {
	cmd      *exec.Cmd
	waitOnce sync.Once
	waitErr  error
	waited   chan struct{}
	initOnce sync.Once
}

func (p *simctlProcess) init() {
	p.initOnce.Do(func() { p.waited = make(chan struct{}) })
}

func (p *simctlProcess) Pid() int { return p.cmd.Process.Pid }

// Interrupt sends SIGINT, which is the only signal that leaves a playable file:
// simctl writes the container's index when it is interrupted and exits.
func (p *simctlProcess) Interrupt() error { return interruptPID(p.cmd.Process.Pid) }

func (p *simctlProcess) Wait() error {
	p.init()
	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
		close(p.waited)
	})
	<-p.waited
	return p.waitErr
}

// interruptPID signals one process. Measured on macOS 26.4 / Xcode 26.3, xcrun
// EXECS simctl rather than forking it, so the pid we started is the recorder
// and this reaches it directly.
func interruptPID(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGINT)
}

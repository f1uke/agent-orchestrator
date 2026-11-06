package sessionhost

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// A "!" command's output is logged in notes at most every bashFlushEvery, or
// sooner once bashFlushBytes wait, and only its first bashDisplayCap bytes.
const (
	bashFlushEvery = 100 * time.Millisecond
	bashFlushBytes = 16 << 10
	bashDisplayCap = 1 << 20
)

var (
	errBashBusy = errors.New(`a "!" command is still running: wait for it to finish or stop it`)
	errBashTurn = errors.New(`claude is busy: wait for the turn to end, then run the command`)
)

// bashStop says whether, and why, the host ended a run before it finished.
type bashStop uint8

const (
	bashNotStopped bashStop = iota
	// bashStoppedByPerson: the person pressed stop. Claude reads the result.
	bashStoppedByPerson
	// bashStoppedClaudeGone: claude exited or the host is shutting down, so no
	// result is sent.
	bashStoppedClaudeGone
)

// bashRun is the process of the one "!" command a host runs at a time. The
// folder owns where the run stands; this holds what the host needs to end it.
type bashRun struct {
	id      string
	command string
	proc    *os.Process
	out     *bashSink
	exited  chan struct{} // closed once the process is reaped and its output read
	done    chan struct{} // closed once bash_end and the result are written
	stop    bashStop      // under host.mu
}

type bashResponse struct {
	OK bool   `json:"ok"`
	ID string `json:"id"`
}

func bashCommand(command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return process.Command("cmd", "/C", command)
	}
	return process.Command("/bin/sh", "-c", command)
}

// runBash starts command in the session's worktree, as the person, with the
// host's own environment. It refuses while claude is not running, a turn is
// open or about to open, a permission is pending, or an earlier run has not
// settled.
func (h *host) runBash(command string) (any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.stdin == nil {
		return nil, errNotRunning
	}
	switch t := h.folder.Transcript(); {
	case t.Bash != nil:
		return nil, errBashBusy
	case t.Busy || len(t.Pending) > 0 || len(t.Queued) > 0 || h.promptAfterInit != "":
		return nil, errBashTurn
	}
	r := &bashRun{id: uuid.NewString(), command: command, out: newBashSink(), exited: make(chan struct{}), done: make(chan struct{})}
	cmd := bashCommand(command)
	process.Detach(cmd)
	cmd.Dir = h.dir
	cmd.Stdout, cmd.Stderr = r.out.writer(chatproto.BashStdout), r.out.writer(chatproto.BashStderr)
	// A background child the command left behind may keep the output open.
	cmd.WaitDelay = stdoutDrain
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start the command: %w", err)
	}
	r.proc = cmd.Process
	h.bash = r
	h.noteLocked(bashStartNote{Kind: chatproto.NoteBashStart, ID: r.id, Command: command, CWD: h.dir})
	go func() {
		_ = cmd.Wait()
		close(r.exited)
	}()
	go h.superviseBash(r, cmd)
	return bashResponse{OK: true, ID: r.id}, nil
}

// superviseBash logs the run's output as it comes, then its end, then sends
// claude the result. It is the only writer of the run's notes, so they keep
// their order.
func (h *host) superviseBash(r *bashRun, cmd *exec.Cmd) {
	defer close(r.done)
	tick := time.NewTicker(bashFlushEvery)
	defer tick.Stop()
	for running := true; running; {
		select {
		case <-tick.C:
		case <-r.out.full:
		case <-r.exited:
			running = false
		}
		h.flushBash(r, !running)
	}
	code, signal, ret := exitStatus(cmd.ProcessState)
	stdout, stderr, truncated := r.out.result()

	h.mu.Lock()
	defer h.mu.Unlock()
	h.bash = nil
	h.noteLocked(bashEndNote{
		Kind: chatproto.NoteBashEnd, ID: r.id, Code: code, Signal: signal,
		Stopped: r.stop != bashNotStopped, Truncated: truncated,
	})
	if r.stop == bashStoppedClaudeGone {
		return
	}
	res := chatproto.BashResult{
		UUID: r.id, Command: r.command, Stdout: stdout, Stderr: stderr,
		ExitCode: ret, Stopped: r.stop == bashStoppedByPerson,
	}
	if err := h.writeThenLogLocked(res); err != nil {
		h.pane.line(dim(`sessionhost: send the "!" result: ` + err.Error()))
	}
}

func (h *host) flushBash(r *bashRun, final bool) {
	chunks := r.out.take(final)
	if len(chunks) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range chunks {
		h.noteLocked(bashOutputNote{Kind: chatproto.NoteBashOutput, ID: r.id, Stream: c.Stream, Text: c.Text})
	}
}

// stopBash ends the run id. A finished or unknown id is a no-op.
func (h *host) stopBash(id string) okResponse {
	h.mu.Lock()
	r := h.bash
	if r == nil || r.id != id || r.stop != bashNotStopped {
		h.mu.Unlock()
		return okResponse{OK: true}
	}
	r.stop = bashStoppedByPerson
	h.mu.Unlock()
	go terminate(r.proc, r.exited, h.bashGrace)
	return okResponse{OK: true}
}

// endBash ends the run, if one is left, because its claude is gone, and
// returns once its end is logged.
func (h *host) endBash() {
	h.mu.Lock()
	r := h.bash
	if r != nil {
		r.stop = bashStoppedClaudeGone
	}
	h.mu.Unlock()
	if r == nil {
		return
	}
	terminate(r.proc, r.exited, h.bashGrace)
	<-r.done
}

// bashSink takes a run's stdout and stderr from the process and holds what
// the next note should carry, and the copy the model will read. The process's
// two output copiers write it; superviseBash drains it.
type bashSink struct {
	mu        sync.Mutex
	pending   []chatproto.BashChunk
	size      int // bytes in pending
	shown     int // bytes logged or pending, at most bashDisplayCap
	truncated bool
	// partial holds a character a write split, until the rest of it comes.
	partial map[chatproto.BashStream][]byte
	model   map[chatproto.BashStream]*chatproto.BashOutput
	full    chan struct{}
}

func newBashSink() *bashSink {
	return &bashSink{
		partial: map[chatproto.BashStream][]byte{},
		model:   map[chatproto.BashStream]*chatproto.BashOutput{chatproto.BashStdout: {}, chatproto.BashStderr: {}},
		full:    make(chan struct{}, 1),
	}
}

type bashStreamWriter struct {
	s      *bashSink
	stream chatproto.BashStream
}

func (s *bashSink) writer(stream chatproto.BashStream) bashStreamWriter {
	return bashStreamWriter{s: s, stream: stream}
}

func (w bashStreamWriter) Write(p []byte) (int, error) {
	s := w.s
	s.mu.Lock()
	_, _ = s.model[w.stream].Write(p)
	text := append(slices.Clone(s.partial[w.stream]), p...)
	text, s.partial[w.stream] = splitPartialRune(text)
	s.addLocked(w.stream, text)
	full := s.size >= bashFlushBytes
	s.mu.Unlock()
	if full {
		select {
		case s.full <- struct{}{}:
		default:
		}
	}
	return len(p), nil
}

func (s *bashSink) addLocked(stream chatproto.BashStream, text []byte) {
	if s.truncated || len(text) == 0 {
		return
	}
	if room := bashDisplayCap - s.shown; len(text) > room {
		n := room
		for n > 0 && !utf8.RuneStart(text[n]) {
			n--
		}
		text, s.truncated = text[:n], true
	}
	if n := len(s.pending); n > 0 && s.pending[n-1].Stream == stream {
		s.pending[n-1].Text += string(text)
	} else {
		s.pending = append(s.pending, chatproto.BashChunk{Stream: stream, Text: string(text)})
	}
	s.size += len(text)
	s.shown += len(text)
}

// take returns the output not yet logged. final also lets out a character
// left split at the end, as it will never be whole.
func (s *bashSink) take(final bool) []chatproto.BashChunk {
	s.mu.Lock()
	defer s.mu.Unlock()
	if final {
		for _, stream := range []chatproto.BashStream{chatproto.BashStdout, chatproto.BashStderr} {
			s.addLocked(stream, s.partial[stream])
			s.partial[stream] = nil
		}
	}
	out := s.pending
	s.pending, s.size = nil, 0
	return out
}

func (s *bashSink) result() (stdout, stderr chatproto.BashOutput, truncated bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.model[chatproto.BashStdout], *s.model[chatproto.BashStderr], s.truncated
}

// splitPartialRune splits off the start of a UTF-8 character that b ends in
// the middle of.
func splitPartialRune(b []byte) (whole, rest []byte) {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if utf8.FullRune(b[i:]) {
				return b, nil
			}
			return b[:i], slices.Clone(b[i:])
		}
	}
	return b, nil
}

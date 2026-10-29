// Package sessionhost hosts one claude process in stream-json mode. It logs
// every line claude writes and every message it is sent to an append-only
// event log, serves the session over a local socket, and shows a read-only
// rendering of it on the pane it runs in. `ao session-host` is its command.
package sessionhost

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Config says what to host and where.
type Config struct {
	Command    []string
	Dir        string
	LogPath    string
	SocketPath string
	PromptFile string

	stdinGrace time.Duration
	killGrace  time.Duration
}

const (
	defaultStdinGrace = 5 * time.Second
	defaultKillGrace  = 2 * time.Second
	// stdoutDrain bounds the wait for claude's stdout to reach EOF after it
	// exits, in case a leftover subprocess still holds the pipe open.
	stdoutDrain = 2 * time.Second
)

var protocolFlags = []string{
	"-p",
	"--input-format", "stream-json",
	"--output-format", "stream-json",
	"--verbose",
	"--replay-user-messages",
	"--include-partial-messages",
	"--permission-prompt-tool=stdio",
}

var errNotRunning = errors.New("claude is not running")

// Run hosts one claude process until it exits or ctx is cancelled, and returns
// claude's exit code (128+signal when a signal ended it on Unix). The code is
// meaningful only when the error is nil.
func Run(ctx context.Context, cfg Config, pane io.Writer) (int, error) {
	if len(cfg.Command) == 0 || cfg.Command[0] == "" {
		return 0, errors.New("sessionhost: no agent command")
	}
	if cfg.LogPath == "" || cfg.SocketPath == "" {
		return 0, errors.New("sessionhost: log and socket paths are required")
	}
	prompt, err := readPrompt(cfg.PromptFile)
	if err != nil {
		return 0, err
	}
	ln, err := listen(ctx, cfg.SocketPath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.Remove(cfg.SocketPath) }()
	defer func() { _ = ln.Close() }()

	lg, folder, err := openLog(cfg.LogPath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = lg.file.Close() }()

	t := folder.Transcript()
	resume := ""
	if n := len(t.ClaudeSessionIDs); n > 0 {
		resume = t.ClaudeSessionIDs[n-1]
	}
	if hasUserInput(t) {
		prompt = ""
	}
	h := &host{
		pane:            &paneWriter{w: pane},
		log:             lg,
		folder:          folder,
		subs:            map[*subscriber]struct{}{},
		promptAfterInit: prompt,
	}
	return h.run(ctx, cfg, ln, resume)
}

func readPrompt(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("sessionhost: read prompt file: %w", err)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return "", nil
	}
	return string(b), nil
}

func hasUserInput(t chatproto.Transcript) bool {
	for _, it := range t.Items {
		if _, ok := it.(chatproto.UserItem); ok {
			return true
		}
	}
	return false
}

func listen(ctx context.Context, socketPath string) (net.Listener, error) {
	d := net.Dialer{Timeout: time.Second}
	if c, err := d.DialContext(ctx, "unix", socketPath); err == nil {
		_ = c.Close()
		return nil, fmt.Errorf("sessionhost: session host already running on %s", socketPath)
	}
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("sessionhost: remove stale socket: %w", err)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("sessionhost: listen: %w", err)
	}
	return ln, nil
}

type host struct {
	pane *paneWriter

	mu              sync.Mutex
	log             *eventLog
	folder          *chatproto.Folder
	subs            map[*subscriber]struct{}
	closed          bool
	stdin           io.Writer
	pid             int
	initID          string
	promptAfterInit string
}

func (h *host) run(ctx context.Context, cfg Config, ln net.Listener, resume string) (int, error) {
	argv := append(append([]string{}, cfg.Command...), protocolFlags...)
	if resume != "" {
		argv = append(argv, "--resume", resume)
	}
	cmd := process.Command(argv[0], argv[1:]...)
	process.Detach(cmd)
	cmd.Dir = cfg.Dir
	cmd.Stderr = h.pane
	cmd.WaitDelay = stdoutDrain
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return 0, fmt.Errorf("sessionhost: stdin pipe: %w", err)
	}
	// An os.Pipe instead of StdoutPipe, so that Wait can report the exit while
	// the reader is still draining what claude wrote before it.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return 0, fmt.Errorf("sessionhost: stdout pipe: %w", err)
	}
	defer func() { _ = stdoutR.Close() }()
	cmd.Stdout = stdoutW
	if err := cmd.Start(); err != nil {
		_ = stdoutW.Close()
		return 0, fmt.Errorf("sessionhost: start %s: %w", argv[0], err)
	}
	_ = stdoutW.Close()

	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	h.start(stdin, cmd.Process.Pid, argv, resume)

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		h.readStdout(stdoutR)
	}()
	srv := newServer(h, ln)
	go srv.serve()

	select {
	case <-exited:
	case <-ctx.Done():
		stop(cmd.Process, stdin, exited, orDefault(cfg.stdinGrace, defaultStdinGrace), orDefault(cfg.killGrace, defaultKillGrace))
		<-exited
	}
	select {
	case <-drained:
	case <-time.After(stdoutDrain):
		_ = stdoutR.Close()
		<-drained
	}

	code, signal, ret := exitStatus(cmd.ProcessState)
	h.finish(code, signal)
	srv.close()
	return ret, nil
}

func orDefault(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}

func (h *host) start(stdin io.Writer, pid int, argv []string, resume string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stdin, h.pid = stdin, pid
	h.noteLocked(spawnNote{Kind: chatproto.NoteSpawn, Argv: argv, Resume: resume, PID: pid})
	h.initID = uuid.NewString()
	if err := h.writeThenLogLocked(chatproto.Initialize{RequestID: h.initID}); err != nil {
		h.pane.line(dim("sessionhost: " + err.Error()))
	}
}

func stop(p *os.Process, stdin io.Closer, exited <-chan struct{}, stdinGrace, killGrace time.Duration) {
	_ = stdin.Close()
	if waitFor(exited, stdinGrace) {
		return
	}
	terminate(p, exited, killGrace)
}

func waitFor(done <-chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}

func (h *host) finish(code *int, signal *string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stdin, h.pid = nil, 0
	h.noteLocked(exitNote{Kind: chatproto.NoteExit, Code: code, Signal: signal})
	h.closed = true
	for s := range h.subs {
		h.dropLocked(s)
	}
}

func (h *host) readStdout(r io.Reader) {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if line = bytes.TrimRight(line, "\r\n"); len(bytes.TrimSpace(line)) > 0 {
			h.handleLine(line)
		}
		if err != nil {
			return
		}
	}
}

func (h *host) handleLine(line []byte) {
	ev, parseErr := chatproto.ParseLine(line)
	h.mu.Lock()
	defer h.mu.Unlock()
	if parseErr != nil {
		h.noteLocked(nonJSONNote{Kind: noteNonJSON, Text: string(line)})
		return
	}
	if _, ok := ev.(chatproto.StreamEvent); ok {
		h.broadcastLocked(frame("delta", line))
		return
	}
	if err := h.appendLocked(chatproto.DirOut, line); err != nil {
		h.pane.line(dim("sessionhost: " + err.Error()))
		return
	}
	if r, ok := ev.(chatproto.ControlResponse); ok && h.initID != "" && r.RequestID == h.initID {
		h.initID = ""
		if h.promptAfterInit != "" {
			if err := h.writeThenLogLocked(chatproto.UserInput{UUID: uuid.NewString(), Text: h.promptAfterInit}); err != nil {
				h.pane.line(dim("sessionhost: send prompt: " + err.Error()))
			}
			h.promptAfterInit = ""
		}
	}
}

func (h *host) writeThenLogLocked(o chatproto.Outbound) error {
	b, err := chatproto.Encode(o)
	if err != nil {
		return err
	}
	if h.stdin == nil {
		return errNotRunning
	}
	if _, err := h.stdin.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("%w: %w", errNotRunning, err)
	}
	return h.appendLocked(chatproto.DirIn, b)
}

func (h *host) noteLocked(note any) {
	b, err := marshal(note)
	if err == nil {
		err = h.appendLocked(chatproto.DirHost, b)
	}
	if err != nil {
		h.pane.line(dim("sessionhost: " + err.Error()))
	}
}

func (h *host) appendLocked(dir chatproto.Direction, payload []byte) error {
	e := chatproto.Entry{Seq: h.log.seq + 1, TS: time.Now().UTC(), Dir: dir, Payload: payload}
	line, err := h.log.append(e)
	if err != nil {
		return err
	}
	if err := h.folder.Apply(e); err != nil {
		h.pane.line(dim("sessionhost: " + err.Error()))
	}
	h.broadcastLocked(frame("entry", bytes.TrimSuffix(line, []byte("\n"))))
	for _, l := range RenderPane(e) {
		h.pane.line(l)
	}
	return nil
}

type paneWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (p *paneWriter) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.w.Write(b)
}

func (p *paneWriter) line(s string) { _, _ = p.Write([]byte(s + "\n")) }

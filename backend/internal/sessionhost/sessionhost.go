// Package sessionhost hosts one claude process in stream-json mode. It logs
// every line claude writes and every message it is sent to an append-only
// event log, serves the session over a local socket, and shows a read-only
// rendering of it on the pane it runs in. `ao session-host` is its command.
package sessionhost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Config says what to host and where.
type Config struct {
	Command    []string
	Dir        string
	LogPath    string
	SocketPath string
	PromptFile string
	// NoIdleCompact stops the host from compacting a large idle conversation.
	NoIdleCompact bool
	// NoPromptSuggestions stops the host from asking claude for a guess at the
	// user's next message after each turn.
	NoPromptSuggestions bool

	stdinGrace     time.Duration
	killGrace      time.Duration
	controlTimeout time.Duration
	contextTimeout time.Duration
	// predecessorWait bounds how long listen waits for a host that still holds the socket.
	predecessorWait time.Duration
}

const (
	defaultStdinGrace     = 5 * time.Second
	defaultKillGrace      = 2 * time.Second
	defaultControlTimeout = requestTimeout / 2
	// Claude may not know get_context_usage, so a missing answer is dropped, not retried.
	defaultContextTimeout = 5 * time.Second
	// A restarted session starts its new host while the old one is still stopping claude.
	defaultPredecessorWait = 10 * time.Second
	predecessorPoll        = 50 * time.Millisecond
	contextRequestPrefix   = "ctx-"
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
	"--allowedTools", mcpAllowedTools(),
}

// envFileCheckpointing turns on the file checkpoints that rewind_files restores;
// claude keeps them off when it runs under an SDK host.
const envFileCheckpointing = "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"

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
	ln, err := listen(ctx, cfg.SocketPath, orDefault(cfg.predecessorWait, defaultPredecessorWait))
	if err != nil {
		return 0, err
	}
	// Close unlinks the socket. A later Remove could delete the one a successor host just made.
	defer func() { _ = ln.Close() }()

	lg, folder, err := openLog(cfg.LogPath)
	if err != nil {
		return 0, err
	}
	// claude is given this folder with --add-dir, so it must exist before claude starts.
	if err := os.MkdirAll(attachmentsBeside(cfg.LogPath), 0o700); err != nil {
		_ = lg.file.Close()
		return 0, fmt.Errorf("sessionhost: attachments: %w", err)
	}
	defer func() { _ = lg.file.Close() }()

	dir := cfg.Dir
	if dir == "" {
		if dir, err = os.Getwd(); err != nil {
			return 0, fmt.Errorf("sessionhost: working directory: %w", err)
		}
	}
	t := folder.Transcript()
	first := launch{attachDir: attachmentsBeside(cfg.LogPath), suggestions: !cfg.NoPromptSuggestions}.from(t, t.Chrome)
	if hasUserInput(t) {
		prompt = ""
	}
	h := &host{
		pane:            &paneWriter{w: pane},
		log:             lg,
		folder:          folder,
		subs:            map[*subscriber]struct{}{},
		answers:         map[string]chan chatproto.ControlResponse{},
		controlTimeout:  orDefault(cfg.controlTimeout, defaultControlTimeout),
		contextTimeout:  orDefault(cfg.contextTimeout, defaultContextTimeout),
		promptAfterInit: prompt,
		idleOff:         cfg.NoIdleCompact,
		afterFunc:       realAfterFunc,
		launched:        first,
		relaunch:        make(chan launch, 1),
		dir:             dir,
		bashGrace:       orDefault(cfg.killGrace, defaultKillGrace),
	}
	return h.run(ctx, cfg, ln, first)
}

type launch struct {
	resume      string
	model       string
	attachDir   string
	suggestions bool
	chrome      bool
}

func (l launch) from(t chatproto.Transcript, chrome bool) launch {
	l.model, l.resume, l.chrome = t.ChosenModel, "", chrome
	if n := len(t.ClaudeSessionIDs); n > 0 {
		l.resume = t.ClaudeSessionIDs[n-1]
	}
	return l
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

// listen takes over socketPath. A host that answers on it gets up to wait to
// shut down before listen reports it as still running.
func listen(ctx context.Context, socketPath string, wait time.Duration) (net.Listener, error) {
	d := net.Dialer{Timeout: time.Second}
	deadline := time.Now().Add(wait)
	for {
		c, err := d.DialContext(ctx, "unix", socketPath)
		if err != nil {
			break
		}
		_ = c.Close()
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("sessionhost: session host already running on %s", socketPath)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(predecessorPoll):
		}
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
	answers         map[string]chan chatproto.ControlResponse
	controlTimeout  time.Duration
	contextTimeout  time.Duration
	contextID       string
	idleOff         bool
	idle            idleState
	afterFunc       afterFunc
	launched        launch
	tasks           map[string]struct{}
	// claude reads --chrome only at start, so switching it replaces the process.
	relaunch chan launch
	// dir is where claude and "!" commands run.
	dir       string
	bashGrace time.Duration
	bash      *bashRun
}

func (h *host) run(ctx context.Context, cfg Config, ln net.Listener, l launch) (int, error) {
	var srv *server
	for {
		ps, next, err := h.runClaude(ctx, cfg, l, func() {
			if srv == nil {
				srv = newServer(h, ln)
				go srv.serve()
			}
		})
		h.endBash()
		if err != nil {
			if srv != nil {
				h.finish(nil, nil)
				srv.close()
			}
			return 0, err
		}
		if next == nil {
			code, signal, ret := exitStatus(ps)
			h.finish(code, signal)
			srv.close()
			return ret, nil
		}
		h.mu.Lock()
		h.endProcessLocked()
		h.mu.Unlock()
		l = *next
	}
}

func (h *host) runClaude(ctx context.Context, cfg Config, l launch, serve func()) (*os.ProcessState, *launch, error) {
	argv := claudeArgv(cfg.Command, l)
	cmd := process.Command(argv[0], argv[1:]...)
	process.Detach(cmd)
	cmd.Dir = cfg.Dir
	cmd.Env = append(os.Environ(), ports.EnvAgentParentPID+"="+strconv.Itoa(os.Getpid()), envFileCheckpointing+"=1")
	cmd.Stderr = h.pane
	cmd.WaitDelay = stdoutDrain
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("sessionhost: stdin pipe: %w", err)
	}
	// An os.Pipe instead of StdoutPipe, so that Wait can report the exit while
	// the reader is still draining what claude wrote before it.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, nil, fmt.Errorf("sessionhost: stdout pipe: %w", err)
	}
	defer func() { _ = stdoutR.Close() }()
	cmd.Stdout = stdoutW
	if err := cmd.Start(); err != nil {
		_ = stdoutW.Close()
		return nil, nil, fmt.Errorf("sessionhost: start %s: %w", argv[0], err)
	}
	_ = stdoutW.Close()

	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	h.start(stdin, cmd.Process.Pid, argv, l)

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		h.readStdout(stdoutR)
	}()
	serve()

	var next *launch
	select {
	case <-exited:
	case <-ctx.Done():
		stop(cmd.Process, stdin, exited, orDefault(cfg.stdinGrace, defaultStdinGrace), orDefault(cfg.killGrace, defaultKillGrace))
		<-exited
	case l := <-h.relaunch:
		next = &l
		stop(cmd.Process, stdin, exited, orDefault(cfg.stdinGrace, defaultStdinGrace), orDefault(cfg.killGrace, defaultKillGrace))
		<-exited
	}
	select {
	case <-drained:
	case <-time.After(stdoutDrain):
		_ = stdoutR.Close()
		<-drained
	}

	return cmd.ProcessState, next, nil
}

// claudeArgv puts the protocol flags and --resume before any "--", after
// which claude reads prompt text. Claude refuses --session-id beside --resume,
// so a resume drops the pinned id the agent command carries. A model chosen
// with set_model replaces the command's --model, so a respawn keeps it.
func claudeArgv(command []string, l launch) []string {
	resume, model, attachDir, suggestions := l.resume, l.model, l.attachDir, l.suggestions
	flags, rest := command, []string(nil)
	if i := slices.Index(command, "--"); i >= 0 {
		flags, rest = command[:i], command[i:]
	}
	argv := make([]string, 0, len(command)+len(protocolFlags)+2)
	for i := 0; i < len(flags); i++ {
		switch {
		case resume != "" && flags[i] == "--session-id":
			i++
		case resume != "" && strings.HasPrefix(flags[i], "--session-id="):
		case model != "" && flags[i] == "--model":
			i++
		case model != "" && strings.HasPrefix(flags[i], "--model="):
		case flags[i] == chatproto.ChromeFlag || flags[i] == "--no-chrome":
		default:
			argv = append(argv, flags[i])
		}
	}
	if model != "" {
		argv = append(argv, "--model", model)
	}
	argv = append(argv, protocolFlags...)
	if suggestions {
		argv = append(argv, "--prompt-suggestions")
	}
	if l.chrome {
		argv = append(argv, chatproto.ChromeFlag)
	}
	if attachDir != "" {
		argv = append(argv, "--add-dir", attachDir)
	}
	if resume != "" {
		argv = append(argv, "--resume", resume)
	}
	return append(argv, rest...)
}

func orDefault(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}

func (h *host) start(stdin io.Writer, pid int, argv []string, l launch) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stdin, h.pid, h.launched = stdin, pid, l
	h.noteLocked(spawnNote{Kind: chatproto.NoteSpawn, Argv: argv, Resume: l.resume, PID: pid})
	h.initID = uuid.NewString()
	if err := h.writeThenLogLocked(chatproto.Initialize{RequestID: h.initID, SDKMCPServers: []string{mcpServerName}}); err != nil {
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
	h.endProcessLocked()
	h.noteLocked(exitNote{Kind: chatproto.NoteExit, Code: code, Signal: signal})
	h.closed = true
	for s := range h.subs {
		h.dropLocked(s)
	}
}

func (h *host) endProcessLocked() {
	h.stdin, h.pid = nil, 0
	h.contextID = ""
	h.tasks = nil
	h.disarmIdleLocked()
	for id, ch := range h.answers {
		close(ch)
		delete(h.answers, id)
	}
}

var errChromeBusy = errors.New(`claude is busy: wait for the turn, its background tasks and any "!" command to end, then turn Chrome on or off`)

func (h *host) setChrome(enabled bool) (any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.stdin == nil {
		return nil, errNotRunning
	}
	if h.launched.chrome == enabled {
		return okResponse{OK: true}, nil
	}
	t := h.folder.Transcript()
	if t.Busy || len(t.Pending) > 0 || len(h.tasks) > 0 || t.Bash != nil {
		return nil, errChromeBusy
	}
	select {
	case h.relaunch <- h.launched.from(t, enabled):
		return okResponse{OK: true}, nil
	default:
		return nil, errors.New("claude is already restarting")
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
	mcp, isMCP := mcpRequest(ev)
	logged := line
	if isMCP {
		logged = mcp.logLine
	}
	r, isResponse := ev.(chatproto.ControlResponse)
	isContext := isResponse && h.contextID != "" && r.RequestID == h.contextID
	if isContext {
		h.contextID = ""
		logged = withoutGridRows(line)
	}
	if err := h.appendLocked(chatproto.DirOut, logged); err != nil {
		h.pane.line(dim("sessionhost: " + err.Error()))
		return
	}
	h.observeIdleLocked(ev, line)
	if isMCP {
		if err := h.writeThenLogLocked(chatproto.MCPReply{RequestID: mcp.requestID, Message: mcpReply(mcp.server, mcp.message)}); err != nil {
			h.pane.line(dim("sessionhost: mcp reply: " + err.Error()))
		}
		return
	}
	if _, ok := ev.(chatproto.Result); ok {
		h.askContextLocked()
	}
	if ch := h.answers[r.RequestID]; isResponse && ch != nil {
		ch <- r
		delete(h.answers, r.RequestID)
	}
	if isResponse && h.initID != "" && r.RequestID == h.initID {
		h.initID = ""
		h.askContextLocked()
		if h.promptAfterInit != "" {
			if err := h.writeThenLogLocked(chatproto.UserInput{UUID: uuid.NewString(), Text: h.promptAfterInit}); err != nil {
				h.pane.line(dim("sessionhost: send prompt: " + err.Error()))
			}
			h.promptAfterInit = ""
		}
	}
}

func (h *host) askContextLocked() {
	if h.contextID != "" {
		return
	}
	id := contextRequestPrefix + uuid.NewString()
	if err := h.writeThenLogLocked(chatproto.GetContextUsage{RequestID: id}); err != nil {
		h.pane.line(dim("sessionhost: get_context_usage: " + err.Error()))
		return
	}
	h.contextID = id
	time.AfterFunc(h.contextTimeout, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.contextID == id {
			h.contextID = ""
		}
	})
}

// The CLI's own /context drawing repeats the categories square by square and is
// most of a get_context_usage answer.
func withoutGridRows(line []byte) []byte {
	var w struct {
		Type     string `json:"type"`
		Response struct {
			Subtype   string                     `json:"subtype"`
			RequestID string                     `json:"request_id"`
			Error     json.RawMessage            `json:"error,omitempty"`
			Response  map[string]json.RawMessage `json:"response,omitempty"`
		} `json:"response"`
	}
	if json.Unmarshal(line, &w) != nil || w.Response.Response["gridRows"] == nil {
		return line
	}
	delete(w.Response.Response, "gridRows")
	b, err := json.Marshal(w)
	if err != nil {
		return line
	}
	return b
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
	line, e, err := h.log.append(e)
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

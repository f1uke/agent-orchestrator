// Package tmux implements ports.Runtime using tmux sessions on Darwin/Linux.
package tmux

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/ptyexec"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	defaultTimeout    = 5 * time.Second
	defaultChunkBytes = 16 * 1024

	// defaultChunkDelay is the pause between literal send-keys chunks, and
	// defaultEnterDelay the pause before the submitting Enter. They give the
	// TUI in the pane (e.g. Claude Code) time to ingest the whole message
	// before the submit key arrives, so the Enter registers as "submit"
	// instead of being swallowed into the paste-burst and left unsubmitted.
	// Values mirror the conpty/pty-client.ts reference path
	// (PTY_INPUT_CHUNK_DELAY_MS / PTY_INPUT_ENTER_DELAY_MS).
	defaultChunkDelay = 15 * time.Millisecond
	defaultEnterDelay = 300 * time.Millisecond
)

var sessionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

var getenv = os.Getenv

// Options configures a tmux Runtime. Every field has a sensible default (see
// New), so the zero value is usable.
type Options struct {
	Binary     string        // default "tmux" (resolved via exec.LookPath)
	Shell      string        // default $SHELL else /bin/sh
	Timeout    time.Duration // default 5s
	ChunkSize  int           // default 16*1024
	ChunkDelay time.Duration // pause between send-keys chunks; default 15ms
	EnterDelay time.Duration // pause before the submitting Enter; default 300ms
}

// Runtime runs agent sessions inside tmux sessions, driving them via the tmux
// CLI. It implements ports.Runtime.
type Runtime struct {
	binary     string
	shell      string
	timeout    time.Duration
	chunkSize  int
	chunkDelay time.Duration
	enterDelay time.Duration
	runner     runner
	sleep      func(time.Duration) // seam for tests; defaults to time.Sleep
	// hasLiveChild reports whether pid has at least one live child process. It is
	// the seam AgentAlive uses to see past the keep-alive shell; tests fake it.
	hasLiveChild func(ctx context.Context, pid int) (bool, error)
}

var _ ports.Runtime = (*Runtime)(nil)
var _ ports.Attacher = (*Runtime)(nil)
var _ ports.AgentLivenessProber = (*Runtime)(nil)

type runner interface {
	Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

// daemonOnlyEnvKeys are AO_* vars that configure the DAEMON and must not leak into
// the worker panes tmux spawns from the daemon's environment. A worker inheriting
// the daemon's AO_SESSION_IDLE_CLOSE re-seeds that value back into the next
// app-from-worker-shell launch (a self-perpetuating stale-config loop); AO_OWNER
// only means "app-spawned daemon" and is meaningless on a worker. Per-session env
// the daemon sets intentionally (AO_SESSION_ID, AO_DATA_DIR, …) is exported by
// buildLaunchCommand, not inherited here, so stripping these is safe.
var daemonOnlyEnvKeys = []string{"AO_SESSION_IDLE_CLOSE", "AO_OWNER"}

// stripEnvKeys returns env (a KEY=VALUE slice) without any entry whose key is in
// keys. The input slice is not mutated.
func stripEnvKeys(env, keys []string) []string {
	if len(keys) == 0 {
		return env
	}
	drop := make(map[string]bool, len(keys))
	for _, k := range keys {
		drop[k] = true
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if drop[name] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func (execRunner) Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// stripEnvKeys returns a fresh slice, so appending env into it is safe (no
	// aliasing of the caller's os.Environ()).
	cmd.Env = append(stripEnvKeys(os.Environ(), daemonOnlyEnvKeys), env...)
	return cmd.CombinedOutput()
}

// New builds a tmux Runtime, filling unset Options with defaults: binary "tmux"
// (resolved via exec.LookPath), shell from $SHELL (else /bin/sh), and the
// default timeout and output chunk size.
func New(opts Options) *Runtime {
	binary := opts.Binary
	if binary == "" {
		if path, err := exec.LookPath("tmux"); err == nil {
			binary = path
		} else {
			binary = "tmux"
		}
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	shellPath := opts.Shell
	if shellPath == "" {
		shellPath = getenv("SHELL")
	}
	if shellPath == "" {
		shellPath = "/bin/sh"
	}
	chunkSize := opts.ChunkSize
	if chunkSize <= 0 {
		chunkSize = defaultChunkBytes
	}
	chunkDelay := opts.ChunkDelay
	if chunkDelay <= 0 {
		chunkDelay = defaultChunkDelay
	}
	enterDelay := opts.EnterDelay
	if enterDelay <= 0 {
		enterDelay = defaultEnterDelay
	}
	return &Runtime{
		binary:       binary,
		shell:        shellPath,
		timeout:      timeout,
		chunkSize:    chunkSize,
		chunkDelay:   chunkDelay,
		enterDelay:   enterDelay,
		runner:       execRunner{},
		sleep:        time.Sleep,
		hasLiveChild: defaultHasLiveChild,
	}
}

// Create starts a new tmux session in the workspace, running the agent's
// launch command with a keep-alive shell, and returns a handle to it.
func (r *Runtime) Create(ctx context.Context, cfg ports.RuntimeConfig) (_ ports.RuntimeHandle, err error) {
	id, err := SessionNameFor(string(cfg.ProjectID), cfg.Branch, string(cfg.SessionID))
	if err != nil {
		return ports.RuntimeHandle{}, err
	}
	if cfg.WorkspacePath == "" {
		return ports.RuntimeHandle{}, errors.New("tmux runtime: workspace path is required")
	}
	if len(cfg.Argv) == 0 {
		return ports.RuntimeHandle{}, errors.New("tmux runtime: launch command is required")
	}
	if err := validateEnvKeys(cfg.Env); err != nil {
		return ports.RuntimeHandle{}, err
	}

	launchCmd := buildLaunchCommand(cfg)
	args, scriptPath, err := r.launchInvocation(id, cfg.WorkspacePath, launchCmd, cfg.Env)
	if err != nil {
		return ports.RuntimeHandle{}, err
	}
	// A launch command too long for tmux's inline limit is delivered via a
	// self-deleting script file (see launchInvocation). On success the shell
	// removes it; if new-session ultimately fails, the shell never runs it, so
	// remove it here rather than leak it. Inline launches leave scriptPath "".
	defer func() {
		if err != nil && scriptPath != "" {
			_ = os.Remove(scriptPath)
		}
	}()
	if out, err := r.run(ctx, args...); err != nil {
		// A pre-existing session with this deterministic name blocks new-session
		// with "duplicate session". If it is a STALE orphan (its agent has exited —
		// e.g. left by a terminated/restarted session, the re-import→open case),
		// reap it and retry. If a LIVE agent occupies it, refuse rather than clobber
		// a session the user may be using.
		if !duplicateSessionOutput(string(out)) {
			return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: create session %s: %w", id, err)
		}
		stale := ports.RuntimeHandle{ID: id}
		alive, aErr := r.AgentAlive(ctx, stale)
		if aErr != nil || alive {
			return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: create session %s: a live agent already occupies that name: %w", id, err)
		}
		if dErr := r.Destroy(ctx, stale); dErr != nil {
			return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: create session %s: reap stale: %w", id, dErr)
		}
		if _, err := r.run(ctx, args...); err != nil {
			return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: create session %s (after reaping stale): %w", id, err)
		}
	}

	// Hide the status bar in the embedded terminal: it clutters the view and
	// was not designed for the in-browser display context.
	if _, err := r.run(ctx, setStatusOffArgs(id)...); err != nil {
		_ = r.Destroy(context.Background(), ports.RuntimeHandle{ID: id})
		return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: set status %s: %w", id, err)
	}

	// Enable mouse mode so the embedded terminal's SGR wheel reports scroll the
	// pane (see setMouseOnArgs). Without it, wheel scrolling silently no-ops.
	if _, err := r.run(ctx, setMouseOnArgs(id)...); err != nil {
		_ = r.Destroy(context.Background(), ports.RuntimeHandle{ID: id})
		return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: set mouse %s: %w", id, err)
	}

	handle := ports.RuntimeHandle{ID: id}
	alive, err := r.IsAlive(ctx, handle)
	if err != nil {
		_ = r.Destroy(context.Background(), handle)
		return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: verify session %s: %w", id, err)
	}
	if !alive {
		_ = r.Destroy(context.Background(), handle)
		return ports.RuntimeHandle{}, fmt.Errorf("tmux runtime: session %s exited before ready", id)
	}
	return handle, nil
}

// Destroy kills the handle's tmux session. An already-gone session is treated
// as success (idempotent).
func (r *Runtime) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	id, err := handleID(handle)
	if err != nil {
		return err
	}
	out, err := r.run(ctx, killSessionArgs(id)...)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && killSessionMissingOutput(string(out)) {
			return nil
		}
		return fmt.Errorf("tmux runtime: destroy session %s: %w", id, err)
	}
	return nil
}

// IsAlive reports whether the handle's session still exists via `tmux
// has-session`. Exit 0 means alive. A non-zero exit with output indicating the
// session or server is missing is a definitive false, nil. Any other non-zero
// exit is a probe error (not proof of death) so callers (the reaper feeding
// the LCM) treat it as a failed probe and never kill a session on a transient
// error.
func (r *Runtime) IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	id, err := handleID(handle)
	if err != nil {
		return false, err
	}
	out, err := r.run(ctx, hasSessionArgs(id)...)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && sessionMissingOutput(string(out)) {
			return false, nil
		}
		return false, fmt.Errorf("tmux runtime: probe session %s: %w", id, err)
	}
	return true, nil
}

// AgentAlive reports whether the agent process is still running in the session's
// pane, seeing past the keep-alive shell that IsAlive cannot. The pane is
// launched as `<shell> -c '<exports>; <agent argv>; exec <shell> -i'`: while the
// agent runs it is a child of the pane's leader process; once it exits the leader
// execs into a bare interactive shell with no children. So "the pane leader has a
// live child" is a runtime-agnostic proxy for "the agent is alive" — a leaked
// dev server the agent double-forked reparents to init and is NOT a child, so it
// does not count. A definitively-missing session is (false, nil); any other
// non-zero tmux exit is a probe error so a reaper never reaps on a failed probe.
func (r *Runtime) AgentAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error) {
	id, err := handleID(handle)
	if err != nil {
		return false, err
	}
	out, err := r.run(ctx, listPanePIDArgs(id)...)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && sessionMissingOutput(string(out)) {
			return false, nil
		}
		return false, fmt.Errorf("tmux runtime: agent-alive probe %s: %w", id, err)
	}
	pid, err := parseFirstPID(string(out))
	if err != nil {
		return false, fmt.Errorf("tmux runtime: agent-alive probe %s: %w", id, err)
	}
	if pid <= 0 {
		return false, nil
	}
	return r.hasLiveChild(ctx, pid)
}

// parseFirstPID reads the first positive integer from `tmux list-panes` pane_pid
// output. Empty output (no panes) yields 0, nil so AgentAlive reports not-alive.
func parseFirstPID(out string) (int, error) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil {
			return 0, fmt.Errorf("unexpected pane_pid output %q", line)
		}
		return pid, nil
	}
	return 0, nil
}

// defaultHasLiveChild reports whether pid has at least one direct child process,
// via `pgrep -P`. pgrep exits 1 with no output when nothing matches, which is a
// clean "no child" (false, nil), not an error.
func defaultHasLiveChild(ctx context.Context, pid int) (bool, error) {
	out, err := exec.CommandContext(ctx, "pgrep", "-P", strconv.Itoa(pid)).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("tmux runtime: pgrep -P %d: %w", pid, err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// SendMessage sends literal text to the session (chunked via send-keys -l),
// then — after a short settle pause — presses Enter to submit.
//
// The pause is load-bearing, not cosmetic. A TUI in the pane (e.g. Claude Code)
// treats a burst of input as a paste; if the submitting Enter arrives in that
// same burst it is folded into the pasted block instead of acting as submit, so
// the message lands as an un-submitted "[Pasted text]" and just sits there
// (worst for multi-line messages). Delivering the whole message, waiting
// enterDelay, then sending a distinct Enter keeps the submit key outside the
// paste burst so it reliably submits. chunkDelay does the same between chunks
// of a large message. This mirrors the conpty/pty-client.ts reference path
// (PTY_INPUT_CHUNK_DELAY_MS / PTY_INPUT_ENTER_DELAY_MS), which the tmux port
// had dropped.
//
// ponytail: send-keys -l chunked is simpler than load-buffer/paste-buffer; the
// ceiling is very large messages may be slower, but chunk size defaults to 16 KB
// which is ample for agent prompts.
func (r *Runtime) SendMessage(ctx context.Context, handle ports.RuntimeHandle, message string) error {
	id, err := handleID(handle)
	if err != nil {
		return err
	}
	parts := chunks(message, r.chunkSize)
	for i, chunk := range parts {
		if _, err := r.run(ctx, sendKeysLiteralArgs(id, chunk)...); err != nil {
			return fmt.Errorf("tmux runtime: send message %s: %w", id, err)
		}
		if i < len(parts)-1 {
			r.sleep(r.chunkDelay) // between chunks only, not after the last
		}
	}
	// Let the pane ingest the whole message before the submit key arrives.
	r.sleep(r.enterDelay)
	if _, err := r.run(ctx, sendEnterArgs(id)...); err != nil {
		return fmt.Errorf("tmux runtime: send enter %s: %w", id, err)
	}
	return nil
}

// GetOutput returns the last `lines` lines of the session pane's captured
// output.
func (r *Runtime) GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error) {
	id, err := handleID(handle)
	if err != nil {
		return "", err
	}
	if lines <= 0 {
		return "", errors.New("tmux runtime: lines must be positive")
	}
	out, err := r.run(ctx, capturePaneArgs(id, lines)...)
	if err != nil {
		return "", fmt.Errorf("tmux runtime: capture output %s: %w", id, err)
	}
	return tailLines(trimTrailingBlankLines(string(out)), lines), nil
}

// Attach opens a fresh attach Stream by spawning `tmux attach-session` on a
// local PTY, sized rows x cols from birth when known. ctx cancellation closes
// the PTY.
func (r *Runtime) Attach(ctx context.Context, handle ports.RuntimeHandle, rows, cols uint16) (ports.Stream, error) {
	argv, err := r.attachCommand(handle)
	if err != nil {
		return nil, err
	}
	return ptyexec.Spawn(ctx, argv, attachEnv(os.Environ()), rows, cols)
}

// attachCommand returns the argv to attach a terminal to the session.
// tmux needs no per-session env block.
func (r *Runtime) attachCommand(handle ports.RuntimeHandle) ([]string, error) {
	id, err := handleID(handle)
	if err != nil {
		return nil, err
	}
	return []string{r.binary, "attach-session", "-t", id}, nil
}

func attachEnv(base []string) []string {
	env := append([]string(nil), base...)
	for i, kv := range env {
		if strings.HasPrefix(kv, "TERM=") {
			env[i] = "TERM=xterm-256color"
			return env
		}
	}
	return append(env, "TERM=xterm-256color")
}

// run wraps runner.Run with a per-call timeout context.
func (r *Runtime) run(ctx context.Context, args ...string) ([]byte, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	out, err := r.runner.Run(cmdCtx, nil, r.binary, args...)
	if cmdCtx.Err() != nil {
		return out, cmdCtx.Err()
	}
	if err != nil {
		return out, commandError{err: err, output: strings.TrimSpace(string(out))}
	}
	return out, nil
}

// -- session name helpers --

func tmuxSessionName(id domain.SessionID) (string, error) {
	raw := string(id)
	if raw == "" {
		return "", errors.New("tmux runtime: session id is required")
	}
	return SessionName(raw), nil
}

// SessionName returns the tmux session name the runtime registers for a given
// session id, applying the same sanitisation Create does. Callers that print an
// attach hint must use this rather than the raw id.
func SessionName(id string) string {
	if sessionIDPattern.MatchString(id) && len(id) <= 48 {
		return id
	}
	return sanitizedSessionName(id)
}

// sanitizeName collapses raw into tmux's safe charset ([A-Za-z0-9_-]): every
// other run of characters (including "." and ":", which tmux treats as target
// syntax) becomes a single dash. Dashes are trimmed, an empty result becomes
// "session", and the result is capped at maxLen.
func sanitizeName(raw string, maxLen int) string {
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		valid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" {
		base = "session"
	}
	if len(base) > maxLen {
		base = strings.TrimRight(base[:maxLen], "-")
	}
	return base
}

func sanitizedSessionName(raw string) string {
	base := sanitizeName(raw, 32)
	sum := sha256.Sum256([]byte(raw))
	return base + "-" + hex.EncodeToString(sum[:4])
}

// branchNameMaxLen caps a branch-mirroring tmux name. It is looser than the 32
// used for hashed ids because these names carry no hash suffix and readability
// (the whole branch) is the point.
const branchNameMaxLen = 64

// branchSessionName mirrors a session's branch into a tmux session name: the
// project id joined to the branch, sanitized and flattened with dashes. tmux's
// namespace is flat and global, so the project id prefix keeps names unique
// across projects (a branch is unique within a project).
func branchSessionName(projectID, branch string) string {
	return sanitizeName(projectID+"/"+branch, branchNameMaxLen)
}

// SessionNameFor returns the tmux session name for a session. A session with a
// branch (worker or orchestrator) gets a branch-mirroring name so `tmux ls`
// lines up with the branch and its worktree directory. A session without a
// branch (e.g. the reviewer pane, which uses a synthetic id) falls back to
// session-id naming, which also carries the empty-id guard.
func SessionNameFor(projectID, branch, sessionID string) (string, error) {
	if projectID != "" && branch != "" {
		return branchSessionName(projectID, branch), nil
	}
	return tmuxSessionName(domain.SessionID(sessionID))
}

func handleID(handle ports.RuntimeHandle) (string, error) {
	id := handle.ID
	if id == "" {
		return "", errors.New("tmux runtime: session id is required")
	}
	if !sessionIDPattern.MatchString(id) {
		return "", fmt.Errorf("tmux runtime: invalid handle id %q", id)
	}
	return id, nil
}

// -- output detection helpers --

// sessionMissingOutput reports whether a non-zero `tmux has-session` or
// `tmux kill-session` exit is definitively "session does not exist" rather
// than a transient probe failure.
func sessionMissingOutput(out string) bool {
	s := strings.ToLower(out)
	return strings.Contains(s, "can't find session") ||
		strings.Contains(s, "no server running") ||
		strings.Contains(s, "error connecting") ||
		strings.Contains(s, "session not found")
}

// killSessionMissingOutput reports whether a non-zero `tmux kill-session`
// failed because the session was already gone.
func killSessionMissingOutput(out string) bool {
	return sessionMissingOutput(out)
}

// duplicateSessionOutput reports whether a non-zero `tmux new-session` exit
// failed because a session with that name already exists.
func duplicateSessionOutput(out string) bool {
	return strings.Contains(strings.ToLower(out), "duplicate session")
}

// -- text helpers --

func chunks(s string, maxBytes int) []string {
	if s == "" {
		return []string{""}
	}
	if maxBytes <= 0 || len(s) <= maxBytes {
		return []string{s}
	}
	parts := []string{}
	for s != "" {
		if len(s) <= maxBytes {
			parts = append(parts, s)
			break
		}
		end := maxBytes
		for end > 0 && !utf8.ValidString(s[:end]) {
			end--
		}
		if end == 0 {
			_, size := utf8.DecodeRuneInString(s)
			end = size
		}
		parts = append(parts, s[:end])
		s = s[end:]
	}
	return parts
}

func tailLines(s string, n int) string {
	if n <= 0 || s == "" {
		return ""
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "")
}

func trimTrailingBlankLines(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for len(lines) > 0 && strings.TrimRight(lines[len(lines)-1], "\r\n") == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "")
}

// -- env / quoting helpers --

func validateEnvKeys(env map[string]string) error {
	for key := range env {
		if !validEnvKey(key) {
			return fmt.Errorf("tmux runtime: invalid env key %q", key)
		}
	}
	return nil
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// maxInlineLaunchLen is the largest launch command tmux accepts inline via
// `new-session … <shell> -c <cmd>`. tmux caps a command argument near 16 KiB and
// fails with "command too long" beyond it, so a bigger command (a large prompt
// and/or system prompt) is delivered via a script FILE instead — the prompt then
// reaches the agent as a normal argv element, bounded only by ARG_MAX (~1 MiB).
// The margin below 16384 leaves room for the other new-session args.
const maxInlineLaunchLen = 15000

// launchInvocation returns the `tmux new-session` args for cfg's launch command.
// A command within tmux's inline limit uses `<shell> -c <cmd>` (unchanged
// behavior). An oversized command is written to a self-deleting script under the
// session data dir and launched as `<shell> <script>`. The returned scriptPath is
// non-empty only in that case, so Create can remove it if new-session fails (on
// success the script removes itself — see writeLaunchScript).
func (r *Runtime) launchInvocation(id, cwd, launchCmd string, env map[string]string) (args []string, scriptPath string, err error) {
	if len(launchCmd) <= maxInlineLaunchLen {
		return newSessionArgs(id, cwd, r.shell, launchCmd), "", nil
	}
	scriptPath, err = writeLaunchScript(id, launchCmd, env)
	if err != nil {
		return nil, "", err
	}
	return newSessionScriptArgs(id, cwd, r.shell, scriptPath), scriptPath, nil
}

// writeLaunchScript writes launchCmd to a self-deleting shell script so an
// oversized launch command bypasses tmux's inline command-length limit. The
// script removes itself as its first action; the shell keeps the file open by
// fd, so the unlink is safe and nothing is left behind after launch. It lives
// under AO_DATA_DIR (all app state stays under ~/.ao) — never the worktree, where
// it could be committed onto the branch.
func writeLaunchScript(id, launchCmd string, env map[string]string) (string, error) {
	dir := filepath.Join(launchScriptBaseDir(env), "runtime", "launch")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("tmux runtime: prepare launch dir: %w", err)
	}
	f, err := os.CreateTemp(dir, "launch-"+id+"-*.sh")
	if err != nil {
		return "", fmt.Errorf("tmux runtime: create launch script: %w", err)
	}
	// `$0` is the script path under `sh <script>`, so `rm -f -- "$0"` deletes it.
	if _, err := f.WriteString("rm -f -- \"$0\"\n" + launchCmd + "\n"); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("tmux runtime: write launch script: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("tmux runtime: write launch script: %w", err)
	}
	return f.Name(), nil
}

// launchScriptBaseDir returns the base dir for launch scripts: the session's
// AO_DATA_DIR when set (keeping app state under ~/.ao), else a temp dir.
func launchScriptBaseDir(env map[string]string) string {
	if d := strings.TrimSpace(env["AO_DATA_DIR"]); d != "" {
		return d
	}
	return os.TempDir()
}

// buildLaunchCommand builds the shell command string passed to `sh -c`. It
// exports env vars, then runs argv, then execs a keep-alive interactive shell
// so the tmux session survives the agent exiting.
//
// PATH from cfg.Env is exported last, after all other keys, so an explicit
// override takes effect.
func buildLaunchCommand(cfg ports.RuntimeConfig) string {
	path := cfg.Env["PATH"]
	if path == "" {
		path = getenv("PATH")
	}

	var b strings.Builder
	for _, key := range sortedKeys(cfg.Env) {
		if key == "PATH" {
			continue
		}
		b.WriteString("export ")
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(shellQuote(cfg.Env[key]))
		b.WriteString("; ")
	}
	if path != "" {
		b.WriteString("export PATH=")
		b.WriteString(shellQuote(path))
		b.WriteString("; ")
	}
	// Quote each argv word so spaces inside a word are preserved.
	parts := make([]string, len(cfg.Argv))
	for i, a := range cfg.Argv {
		parts[i] = shellQuote(a)
	}
	b.WriteString(strings.Join(parts, " "))
	// Keep the tmux session alive after the agent exits so the operator can
	// inspect the terminal. The shell variable expansion picks up $SHELL from
	// the process env if set, otherwise falls back to /bin/sh.
	b.WriteString(`; exec "${SHELL:-/bin/sh}" -i`)
	return b.String()
}

// -- error type --

type commandError struct {
	err    error
	output string
}

func (e commandError) Error() string {
	if e.output == "" {
		return e.err.Error()
	}
	return e.err.Error() + ": " + e.output
}

func (e commandError) Unwrap() error { return e.err }

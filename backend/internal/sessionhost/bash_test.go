package sessionhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

// A bash result on stdin, and claude's empty answer to it.
var answeredBashResult = []fixtureLine{
	{chatproto.DirIn, `{"type":"user","uuid":"run","shouldQuery":false,"message":{"role":"user","content":[]}}`},
	{chatproto.DirOut, `{"type":"result","subtype":"success","is_error":false,"num_turns":0,"result":"","session_id":"s","total_cost_usd":0}`},
}

type bashHost struct {
	cfg   Config
	run   *running
	c     Client
	stdin string
}

func startBashHost(t *testing.T, extra ...fixtureLine) *bashHost {
	t.Helper()
	dir := shortDir(t)
	cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir, extra...))
	cfg.NoIdleCompact = true
	cfg.Dir = filepath.Join(dir, "work")
	if err := os.Mkdir(cfg.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stdin := filepath.Join(dir, "stdin.jsonl")
	t.Setenv(envFakeStdin, stdin)
	r := startHost(t, cfg)
	return &bashHost{cfg: cfg, run: r, c: NewClient(cfg.SocketPath), stdin: stdin}
}

// transcript folds the log as it is on disk now, or reports false while the
// host is part way through writing a line.
func (b *bashHost) transcript() (chatproto.Transcript, bool) {
	data, err := os.ReadFile(b.cfg.LogPath)
	if err != nil {
		return chatproto.Transcript{}, false
	}
	if i := bytes.LastIndexByte(data, '\n'); i >= 0 {
		data = data[:i+1]
	}
	entries, err := chatproto.DecodeLog(bytes.NewReader(data))
	if err != nil {
		return chatproto.Transcript{}, false
	}
	tr, err := chatproto.Fold(entries)
	return tr, err == nil
}

// runOf waits until the run id reaches phase and returns its row.
func (b *bashHost) runOf(t *testing.T, id string, phase chatproto.BashPhase) chatproto.BashItem {
	t.Helper()
	var run chatproto.BashItem
	eventually(t, "run "+id+" is "+string(phase), func() bool {
		tr, ok := b.transcript()
		if !ok {
			return false
		}
		for _, it := range tr.Items {
			if r, ok := it.(chatproto.BashItem); ok && r.ID == id && r.Phase == phase {
				run = r
				return true
			}
		}
		return false
	})
	return run
}

func outputText(run chatproto.BashItem, stream chatproto.BashStream) string {
	var s strings.Builder
	for _, c := range run.Output {
		if c.Stream == stream {
			s.WriteString(c.Text)
		}
	}
	return s.String()
}

// bashResultsSent returns the shouldQuery:false messages claude read on stdin.
func (b *bashHost) bashResultsSent(t *testing.T) []any {
	t.Helper()
	data, err := os.ReadFile(b.stdin)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	var out []any
	for _, line := range bytes.Split(data, []byte("\n")) {
		if !bytes.Contains(line, []byte(`"shouldQuery":false`)) {
			continue
		}
		var v any
		if err := json.Unmarshal(line, &v); err != nil {
			t.Fatalf("stdin line %s: %v", line, err)
		}
		out = append(out, v)
	}
	return out
}

func wantBashResult(t *testing.T, id, input, result string) any {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"type": "user", "uuid": id, "shouldQuery": false,
		"message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": input},
			map[string]any{"type": "text", "text": result},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func refusal(t *testing.T, err error) string {
	t.Helper()
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("error = %v, want a refusal", err)
	}
	return refused.Reason
}

func TestRunBashLogsTheRunAndSendsClaudeTheResult(t *testing.T) {
	b := startBashHost(t, answeredBashResult...)
	if err := os.WriteFile(filepath.Join(b.cfg.Dir, "note.txt"), []byte("from the worktree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, err := b.c.RunBash(ctx, "echo hi && cat note.txt")
	if err != nil {
		t.Fatal(err)
	}

	run := b.runOf(t, id, chatproto.BashSettled)
	if run.Command != "echo hi && cat note.txt" || run.CWD != b.cfg.Dir {
		t.Errorf("run = %+v, want the command in %s", run, b.cfg.Dir)
	}
	if got := outputText(run, chatproto.BashStdout); got != "hi\nfrom the worktree\n" {
		t.Errorf("stdout logged = %q", got)
	}
	if run.End == nil || run.End.Code == nil || *run.End.Code != 0 || run.End.Stopped || run.End.Truncated {
		t.Errorf("end = %+v, want exit 0", run.End)
	}
	want := wantBashResult(t, id, "<bash-input>echo hi &amp;&amp; cat note.txt</bash-input>",
		"<bash-stdout>hi\nfrom the worktree\n</bash-stdout><bash-stderr></bash-stderr><bash-exit-code>0</bash-exit-code>")
	if got := b.bashResultsSent(t); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("claude read %v on stdin, want exactly %v", got, want)
	}
	if _, err := b.c.Send(ctx, chatproto.UserInput{Text: "next"}, nil); err != nil {
		t.Errorf("send after the run settled: %v", err)
	}
}

func TestStopBashEndsTheRunAndClaudeReadsItAsStopped(t *testing.T) {
	b := startBashHost(t, answeredBashResult...)
	ctx := context.Background()
	id, err := b.c.RunBash(ctx, "echo started; sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the run printed", func() bool {
		tr, ok := b.transcript()
		return ok && tr.Bash != nil && outputText(*tr.Bash, chatproto.BashStdout) == "started\n"
	})
	if err := b.c.StopBash(ctx, id); err != nil {
		t.Fatal(err)
	}

	run := b.runOf(t, id, chatproto.BashSettled)
	if run.End == nil || !run.End.Stopped || run.End.Code != nil || run.End.Signal != "SIGTERM" {
		t.Errorf("end = %+v, want stopped by SIGTERM", run.End)
	}
	want := wantBashResult(t, id, "<bash-input>echo started; sleep 30</bash-input>",
		"<bash-stdout>started\n</bash-stdout><bash-stderr>[stopped]</bash-stderr><bash-exit-code>130</bash-exit-code>")
	if got := b.bashResultsSent(t); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("claude read %v on stdin, want exactly %v", got, want)
	}
	if err := b.c.StopBash(ctx, id); err != nil {
		t.Errorf("stopping a finished run: %v, want a no-op", err)
	}
}

func TestRunBashIsRefusedWhileATurnIsOpen(t *testing.T) {
	first := loadEntries(t, fixturePath(t, "first_session.jsonl"))
	b := startBashHost(t,
		fixtureLine{chatproto.DirOut, string(first[4].Payload)},
		fixtureLine{chatproto.DirOut, string(first[5].Payload)},
	)
	eventually(t, "the turn is running", func() bool { return state(t, b.cfg.SocketPath).Busy })
	_, err := b.c.RunBash(context.Background(), "echo hi")
	if got := refusal(t, err); got != errBashTurn.Error() {
		t.Errorf("refused with %q, want %q", got, errBashTurn)
	}
	if tr, _ := b.transcript(); tr.Bash != nil || strings.Contains(mustRead(t, b.cfg.LogPath), `"bash_start"`) {
		t.Error("a refused command was started")
	}
}

func TestABusyRunRefusesWhatWouldRaceIt(t *testing.T) {
	b := startBashHost(t)
	ctx := context.Background()
	pidFile := filepath.Join(b.cfg.Dir, "sleep.pid")
	if _, err := b.c.RunBash(ctx, "sleep 30 & echo $! > sleep.pid; wait"); err != nil {
		t.Fatal(err)
	}
	pid := readPID(t, pidFile)

	_, err := b.c.Send(ctx, chatproto.UserInput{Text: "hi"}, nil)
	if got := refusal(t, err); got != errBashBusy.Error() {
		t.Errorf("send refused with %q, want %q", got, errBashBusy)
	}
	_, err = b.c.RunBash(ctx, "echo again")
	if got := refusal(t, err); got != errBashBusy.Error() {
		t.Errorf("a second command refused with %q, want %q", got, errBashBusy)
	}
	if got := refusal(t, b.c.Rewind(ctx, "u-1", false)); got != errBashBusy.Error() {
		t.Errorf("rewind refused with %q, want %q", got, errBashBusy)
	}
	if got := refusal(t, b.c.SetChrome(ctx, true)); got != errChromeBusy.Error() {
		t.Errorf("set_chrome refused with %q, want %q", got, errChromeBusy)
	}
	if got := loggedInputs(t, b.cfg.LogPath, "user/"); len(got) != 0 {
		t.Errorf("a refused send reached claude: %v", got)
	}

	b.run.cancel()
	b.run.wait(t)
	assertKilledRun(t, b, pid)
}

func TestClaudeExitingKillsTheRun(t *testing.T) {
	b := startBashHost(t,
		fixtureLine{chatproto.DirIn, `{"type":"control_request","request_id":"i","request":{"subtype":"interrupt"}}`},
		fixtureLine{chatproto.DirHost, `{"kind":"exit","code":0}`},
	)
	ctx := context.Background()
	if _, err := b.c.RunBash(ctx, "sleep 30 & echo $! > sleep.pid; wait"); err != nil {
		t.Fatal(err)
	}
	pid := readPID(t, filepath.Join(b.cfg.Dir, "sleep.pid"))
	if err := b.c.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	if res := b.run.wait(t); res.err != nil || res.code != 0 {
		t.Fatalf("Run = %d, %v", res.code, res.err)
	}
	assertKilledRun(t, b, pid)
}

// assertKilledRun checks that the host that ran b's one command has gone,
// that it killed the command's whole process group, logged the end before
// claude's exit, and sent claude no result.
func assertKilledRun(t *testing.T, b *bashHost, pid int) {
	t.Helper()
	tr, ok := b.transcript()
	if !ok {
		t.Fatal("log does not fold")
	}
	var runs []chatproto.BashItem
	for _, it := range tr.Items {
		if r, ok := it.(chatproto.BashItem); ok {
			runs = append(runs, r)
		}
	}
	if len(runs) != 1 || runs[0].End == nil || !runs[0].End.Stopped || runs[0].End.Signal == "" {
		t.Fatalf("runs = %+v, want one stopped by a signal the host logged", runs)
	}
	if got := b.bashResultsSent(t); len(got) != 0 {
		t.Errorf("claude was sent a result after it went away: %v", got)
	}
	eventually(t, "the command's background child is gone", func() bool {
		return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	})
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	var pid int
	eventually(t, "the command wrote its child's pid", func() bool {
		b, err := os.ReadFile(path)
		if err != nil || !bytes.HasSuffix(b, []byte("\n")) {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(b)))
		return err == nil
	})
	return pid
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunBashCapsTheLoggedOutputButClaudeReadsTheTrueEnd(t *testing.T) {
	b := startBashHost(t, answeredBashResult...)
	id, err := b.c.RunBash(context.Background(), `head -c 2000000 /dev/zero | tr '\000' a; echo; echo END`)
	if err != nil {
		t.Fatal(err)
	}
	run := b.runOf(t, id, chatproto.BashSettled)
	if got := len(outputText(run, chatproto.BashStdout)); got != bashDisplayCap {
		t.Errorf("logged %d bytes of stdout, want the %d byte cap", got, bashDisplayCap)
	}
	if !run.End.Truncated {
		t.Error("the end does not say the output was cut")
	}
	if n := len(run.Output); n > bashDisplayCap/(8<<10) {
		t.Errorf("%d output notes for 1 MiB, want them coalesced", n)
	}
	sent := b.bashResultsSent(t)
	if len(sent) != 1 {
		t.Fatalf("claude read %d results, want 1", len(sent))
	}
	result := sent[0].(map[string]any)["message"].(map[string]any)["content"].([]any)[1].(map[string]any)["text"].(string)
	if !strings.HasSuffix(result, "aaa\nEND\n</bash-stdout><bash-stderr></bash-stderr><bash-exit-code>0</bash-exit-code>") {
		t.Errorf("claude's copy does not end with the command's last line: ...%q", result[len(result)-120:])
	}
}

func TestRunBashKeepsACharacterSplitAcrossWritesWhole(t *testing.T) {
	b := startBashHost(t, answeredBashResult...)
	id, err := b.c.RunBash(context.Background(), `printf '\340\270'; sleep 0.3; printf '\201\n'`)
	if err != nil {
		t.Fatal(err)
	}
	run := b.runOf(t, id, chatproto.BashSettled)
	if got := outputText(run, chatproto.BashStdout); got != "ก\n" {
		t.Errorf("stdout logged = %q, want %q", got, "ก\n")
	}
	for _, c := range run.Output {
		if strings.ContainsRune(c.Text, '�') {
			t.Errorf("a note carries half a character: %q", c.Text)
		}
	}
}

func TestBashRequestsNeedTheirFields(t *testing.T) {
	for _, line := range []string{`{"op":"bash"}`, `{"op":"bash","command":"  \n"}`, `{"op":"bash_stop"}`} {
		if _, err := parseRequest([]byte(line)); err == nil {
			t.Errorf("parseRequest(%s) = nil error", line)
		}
	}
}

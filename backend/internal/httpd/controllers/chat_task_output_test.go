package controllers_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

type taskOutputBody struct {
	Text       string `json:"text"`
	Size       int64  `json:"size"`
	Truncated  bool   `json:"truncated"`
	ModifiedAt string `json:"modifiedAt"`
}

type taskOutputEnv struct {
	srv     *httptest.Server
	root    string
	outside string
	log     string
}

const (
	sid1 = "11111111-1111-1111-1111-111111111111"
	sid2 = "22222222-2222-2222-2222-222222222222"
)

func taskLog(sid string, lines ...string) string {
	all := []string{fmt.Sprintf(`{"type":"system","subtype":"init","session_id":%q}`, sid)}
	all = append(all, lines...)
	var b strings.Builder
	for i, payload := range all {
		fmt.Fprintf(&b, `{"seq":%d,"ts":"2026-10-30T10:55:36Z","dir":"out","payload":%s}`+"\n", i+1, payload)
	}
	return b.String()
}

func started(taskID, toolUseID string) string {
	return fmt.Sprintf(`{"type":"system","subtype":"task_started","task_id":%q,"tool_use_id":%q,"task_type":"local_bash"}`, taskID, toolUseID)
}

func backgroundResult(toolUseID, taskID, path string) string {
	text := fmt.Sprintf("Command running in background with ID: %s. Output is being written to: %s. You will be notified when it completes.", taskID, path)
	b, _ := json.Marshal(text)
	return fmt.Sprintf(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":%s}]}}`, toolUseID, b)
}

func notified(taskID, path string) string {
	return fmt.Sprintf(`{"type":"system","subtype":"task_notification","task_id":%q,"status":"completed","output_file":%q}`, taskID, path)
}

func newTaskOutputEnv(t *testing.T) taskOutputEnv {
	t.Helper()
	base := t.TempDir()
	tmp := filepath.Join(base, "tmp")
	t.Setenv("CLAUDE_CODE_TMPDIR", tmp)
	env := taskOutputEnv{
		root:    filepath.Join(tmp, "claude-"+strconv.Itoa(os.Getuid())),
		outside: filepath.Join(base, "outside"),
	}
	tasks1 := filepath.Join(env.root, "-work-project", sid1, "tasks")
	tasks2 := filepath.Join(env.root, "-work-project", sid2, "tasks")

	writeFile(t, filepath.Join(tasks1, "run1.output"), "line 1\nline 2\n")
	writeFile(t, filepath.Join(tasks1, "empty.output"), "")
	writeFile(t, filepath.Join(env.outside, sid1, "tasks", "outside.output"), "secret")
	writeFile(t, filepath.Join(env.outside, sid1, "tasks", "escape.output"), "secret")
	if err := os.Symlink(filepath.Join(env.outside, sid1, "tasks", "escape.output"), filepath.Join(tasks1, "escape.output")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(env.root, "-work-project", "not-a-session", "tasks", "foreign.output"), "secret")
	writeFile(t, filepath.Join(env.root, "-work-project", sid1, "logs", "loose.output"), "secret")
	if err := os.MkdirAll(filepath.Join(tasks1, "adir.output"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(tasks1, "run.1.output"), "dotted")
	writeFile(t, filepath.Join(tasks1, "...output"), "dots")
	writeFile(t, filepath.Join(tasks2, "other.output"), "the other session's output")

	data := filepath.Join(base, "data")
	env.log = filepath.Join(data, "sessions", "ao-1", "events.jsonl")
	writeFile(t, env.log, taskLog(sid1,
		started("run1", "toolu_1"),
		backgroundResult("toolu_1", "run1", filepath.Join(tasks1, "run1.output")),
		started("empty", "toolu_2"),
		backgroundResult("toolu_2", "empty", filepath.Join(tasks1, "empty.output")),
		notified("outside", filepath.Join(env.outside, sid1, "tasks", "outside.output")),
		notified("escape", filepath.Join(tasks1, "escape.output")),
		notified("wrongname", filepath.Join(tasks1, "run1.output")),
		notified("foreign", filepath.Join(env.root, "-work-project", "not-a-session", "tasks", "foreign.output")),
		notified("loose", filepath.Join(env.root, "-work-project", sid1, "logs", "loose.output")),
		notified("adir", filepath.Join(tasks1, "adir.output")),
		notified("run.1", filepath.Join(tasks1, "run.1.output")),
		notified("..", filepath.Join(tasks1, "...output")),
		notified("big", filepath.Join(tasks1, "big.output")),
	))
	writeFile(t, filepath.Join(data, "sessions", "ao-2", "events.jsonl"), taskLog(sid2,
		notified("other", filepath.Join(tasks2, "other.output")),
	))

	svc := newFakeSessionService()
	o := svc.sessions["ao-1"]
	o.ID = "ao-2"
	svc.sessions["ao-2"] = o
	cfg := config.Config{DataDir: data}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	env.srv = httptest.NewServer(httpd.NewRouterWithControl(cfg, log, nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{}))
	t.Cleanup(env.srv.Close)
	return env
}

func (e taskOutputEnv) get(t *testing.T, session, taskID string) (taskOutputBody, int, http.Header) {
	t.Helper()
	resp, err := e.srv.Client().Get(e.srv.URL + "/api/v1/sessions/" + session + "/chat/tasks/" + taskID + "/output")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var body taskOutputBody
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
	}
	return body, resp.StatusCode, resp.Header
}

func TestChatTaskOutputServesTheTailOfARunningTask(t *testing.T) {
	env := newTaskOutputEnv(t)
	mtime := time.Date(2026, 10, 30, 10, 56, 0, 0, time.UTC)
	path := filepath.Join(env.root, "-work-project", sid1, "tasks", "run1.output")
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	body, status, h := env.get(t, "ao-1", "run1")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	want := taskOutputBody{Text: "line 1\nline 2\n", Size: 14, Truncated: false, ModifiedAt: "2026-10-30T10:56:00Z"}
	if body != want {
		t.Errorf("body = %+v, want %+v", body, want)
	}
	if got := h.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}

	body, status, _ = env.get(t, "ao-2", "other")
	if status != http.StatusOK || body.Text != "the other session's output" {
		t.Errorf("ao-2 other = %d %+v, want 200 with its own output", status, body)
	}
}

func TestChatTaskOutputOfAnEmptyFileIsEmptyText(t *testing.T) {
	env := newTaskOutputEnv(t)
	body, status, _ := env.get(t, "ao-1", "empty")
	if status != http.StatusOK || body.Text != "" || body.Size != 0 || body.Truncated {
		t.Errorf("empty = %d %+v, want 200 with no text", status, body)
	}
}

func TestChatTaskOutputReturnsOnlyTheLast64KiB(t *testing.T) {
	env := newTaskOutputEnv(t)
	const total = 200 << 10
	content := strings.Repeat("a", total-1024) + strings.Repeat("z", 1024)
	writeFile(t, filepath.Join(env.root, "-work-project", sid1, "tasks", "big.output"), content)
	body, status, _ := env.get(t, "ao-1", "big")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if !body.Truncated || body.Size != total {
		t.Errorf("truncated = %v size = %d, want true %d", body.Truncated, body.Size, total)
	}
	if body.Text != content[total-64<<10:] {
		t.Errorf("text is %d bytes ending %q, want the last 65536 bytes", len(body.Text), body.Text[max(0, len(body.Text)-8):])
	}
}

func TestChatTaskOutputRefusesWithNotFound(t *testing.T) {
	env := newTaskOutputEnv(t)
	cases := []struct{ name, session, taskID string }{
		{"a task id from another session", "ao-1", "other"},
		{"a task id of .., though the log names it and its file exists", "ao-1", ".."},
		{"a task id with an encoded slash", "ao-1", "run1%2F..%2Fescape"},
		{"a task id with a dot, though the log names it and its file exists", "ao-1", "run.1"},
		{"a log path outside the temp root", "ao-1", "outside"},
		{"a symlink inside tasks that points outside", "ao-1", "escape"},
		{"a file named for a different task id", "ao-1", "wrongname"},
		{"a tasks directory under a non-session parent", "ao-1", "foreign"},
		{"an output file in a session directory but not in tasks", "ao-1", "loose"},
		{"a directory, not a regular file", "ao-1", "adir"},
		{"a task whose file is not there yet", "ao-1", "big"},
		{"a task the log never names", "ao-1", "nosuchtask"},
		{"an unknown session", "ao-404", "run1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if body, status, _ := env.get(t, tc.session, tc.taskID); status != http.StatusNotFound {
				t.Errorf("GET %s/%s = %d %+v, want 404", tc.session, tc.taskID, status, body)
			}
		})
	}
}

func TestChatTaskOutputRefusesABadTaskIDBeforeAnyLookup(t *testing.T) {
	env := newTaskOutputEnv(t)
	resp, err := env.srv.Client().Get(env.srv.URL + "/api/v1/sessions/ao-404/chat/tasks/run.1/output")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != http.StatusNotFound || body.Code != "TASK_OUTPUT_NOT_FOUND" {
		t.Errorf("bad id on an unknown session = %d %q, want 404 TASK_OUTPUT_NOT_FOUND before the session is looked up", resp.StatusCode, body.Code)
	}
}

func TestChatTaskOutputFindsATaskOnceThenFollowsItsFile(t *testing.T) {
	env := newTaskOutputEnv(t)
	if _, status, _ := env.get(t, "ao-1", "run1"); status != http.StatusOK {
		t.Fatalf("first read = %d, want 200", status)
	}
	if err := os.WriteFile(env.log, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(env.root, "-work-project", sid1, "tasks", "run1.output"), "line 1\nline 2\nline 3\n")
	body, status, _ := env.get(t, "ao-1", "run1")
	if status != http.StatusOK || body.Text != "line 1\nline 2\nline 3\n" {
		t.Errorf("poll after the log was emptied = %d %+v, want 200 with the file's new text and no log rescan", status, body)
	}

	if err := os.Remove(filepath.Join(env.root, "-work-project", sid1, "tasks", "run1.output")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(env.outside, sid1, "tasks", "escape.output"), filepath.Join(env.root, "-work-project", sid1, "tasks", "run1.output")); err != nil {
		t.Fatal(err)
	}
	if _, status, _ := env.get(t, "ao-1", "run1"); status != http.StatusNotFound {
		t.Errorf("a remembered task whose file became a link outside = %d, want 404", status)
	}
}

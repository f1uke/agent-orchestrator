package learncapture_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learncapture"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

type harness struct {
	t        *testing.T
	store    *sqlite.Store
	projects string // stands in for ~/.claude/projects
	now      time.Time
	obs      *learncapture.Observer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := &harness{t: t, store: st, projects: t.TempDir(), now: time.Now().UTC().Add(time.Hour)}
	h.obs = learncapture.New(st, learncapture.Locator{
		WorkspaceDir: func(ws string) (string, error) { return filepath.Join(h.projects, slug(ws)), nil },
		Pinned: func(ws, id string) (string, error) {
			return filepath.Join(h.projects, slug(ws), id+".jsonl"), nil
		},
	}, learncapture.Config{
		Clock:   func() time.Time { return h.now },
		Environ: func() []string { return nil },
	})
	return h
}

func slug(ws string) string {
	return strings.NewReplacer("/", "-", ".", "-", "_", "-").Replace(ws)
}

func (h *harness) project(id string, learnOn bool) {
	h.t.Helper()
	if err := h.store.UpsertProject(context.Background(), domain.ProjectRecord{
		ID: id, Path: "/repo/" + id, RegisteredAt: time.Now().UTC(),
		Config: domain.ProjectConfig{LearnFromSessions: learnOn},
	}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) session(project, workspace, prompt string) domain.SessionRecord {
	h.t.Helper()
	now := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	rec, err := h.store.CreateSession(context.Background(), domain.SessionRecord{
		ProjectID: domain.ProjectID(project),
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		Metadata:  domain.SessionMetadata{Branch: "feat/x", WorkspacePath: workspace, Prompt: prompt},
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return rec
}

func turn(uuid, cwd, text string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "user", "uuid": uuid, "timestamp": "2026-10-01T10:00:00Z", "cwd": cwd,
		"origin": map[string]any{"kind": "human"}, "promptSource": "typed",
		"message": map[string]any{"role": "user", "content": text},
	})
	return string(b) + "\n"
}

func reply(uuid, text string) string {
	b, _ := json.Marshal(map[string]any{
		"type": "assistant", "uuid": uuid, "timestamp": "2026-10-01T10:00:05Z",
		"message": map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": text}}},
	})
	return string(b) + "\n"
}

func (h *harness) write(workspace, name string, lines ...string) string {
	h.t.Helper()
	dir := filepath.Join(h.projects, slug(workspace))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		h.t.Fatal(err)
	}
	p := filepath.Join(dir, name+".jsonl")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(strings.Join(lines, "")); err != nil {
		h.t.Fatal(err)
	}
	return p
}

func (h *harness) poll() {
	h.t.Helper()
	if err := h.obs.Poll(context.Background()); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) kept(project string) []string {
	h.t.Helper()
	rows, err := h.store.ListLearnExcerpts(context.Background(), domain.ProjectID(project), 100)
	if err != nil {
		h.t.Fatal(err)
	}
	out := make([]string, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		out = append(out, rows[i].HumanText)
	}
	return out
}

func TestPoll_CapturesOnlyOptedInProjects(t *testing.T) {
	h := newHarness(t)
	h.project("on", true)
	h.project("off", false)
	wsOn, wsOff := "/wt/on/feat-x", "/wt/off/feat-y"
	on := h.session("on", wsOn, "the brief")
	off := h.session("off", wsOff, "")
	h.write(wsOn, string(on.ID), turn("u1", wsOn, "the brief"), reply("a1", "working"), turn("u2", wsOn, "use the script, not taps"))
	h.write(wsOff, string(off.ID), turn("v1", wsOff, "my customer's address is ..."))

	h.poll()

	if got := h.kept("on"); strings.Join(got, "|") != "use the script, not taps" {
		t.Errorf("on kept %q; the brief is AO's, not the human's", got)
	}
	if got := h.kept("off"); len(got) != 0 {
		t.Errorf("a project with learning off was read: %q", got)
	}
	if cursors, _ := h.store.ListLearnCursors(context.Background(), "off"); len(cursors) != 0 {
		t.Errorf("a project with learning off has %d cursors; it must not even be opened", len(cursors))
	}
}

func TestPoll_FollowsAClearedConversationButNotAnotherDirectory(t *testing.T) {
	h := newHarness(t)
	h.project("on", true)
	ws := "/wt/on/feat-x"
	s := h.session("on", ws, "")
	h.write(ws, string(s.ID), turn("u1", ws, "first conversation"))
	// `/clear` starts a new file under a new id, in the same worktree.
	h.write(ws, "0b7595a6-0461-4f6a-9f64-9bed878e4dfa", turn("c1", ws, "after clear"))
	// A conversation started in a subdirectory (a test's scratch repo, say)
	// lands in the same project directory but is not the session's.
	h.write(ws, "aa9d2bf7-b967-4000-8000-000000000000", turn("x1", ws+"/backend/internal", "e2e probe text"))

	h.poll()

	got := h.kept("on")
	if strings.Join(got, "|") != "first conversation|after clear" && strings.Join(got, "|") != "after clear|first conversation" {
		t.Errorf("kept %q", got)
	}
}

func TestPoll_ReadsOnlyWhatIsNew(t *testing.T) {
	h := newHarness(t)
	h.project("on", true)
	ws := "/wt/on/feat-x"
	s := h.session("on", ws, "")
	path := h.write(ws, string(s.ID), turn("u1", ws, "one"), reply("a1", "ok"))
	h.poll()
	first, _, _ := h.store.GetLearnCursor(context.Background(), path)

	h.poll() // nothing changed
	again, _, _ := h.store.GetLearnCursor(context.Background(), path)
	if !again.UpdatedAt.Equal(first.UpdatedAt) {
		t.Error("an unchanged file was read again")
	}

	h.write(ws, string(s.ID), turn("u2", ws, "two"), reply("a2", "done"))
	h.now = h.now.Add(time.Minute)
	h.poll()
	if got := h.kept("on"); strings.Join(got, "|") != "one|two" {
		t.Errorf("kept %q", got)
	}
}

func TestPoll_TellsTheHumansAppMessageFromAnAgentsSend(t *testing.T) {
	h := newHarness(t)
	h.project("on", true)
	ws := "/wt/on/feat-x"
	s := h.session("on", ws, "")
	ctx := context.Background()
	for _, d := range []struct{ trigger, body string }{
		{"send", "from the app: keep PRs small"},
		{"send", "[from @on-2] qa passed"},
	} {
		if err := h.store.RecordDeliveredFingerprint(ctx, learn.DeliveredFingerprintFor(s, d.trigger, d.body, time.Now().UTC())); err != nil {
			t.Fatal(err)
		}
	}
	h.write(ws, string(s.ID),
		turn("u1", ws, "from the app: keep PRs small"),
		turn("u2", ws, "[from @on-2] qa passed"),
	)
	h.poll()
	rows, _ := h.store.ListLearnExcerpts(ctx, "on", 10)
	if len(rows) != 1 || rows[0].HumanText != "from the app: keep PRs small" || rows[0].SourceClass != domain.LearnSourceAppSend {
		t.Fatalf("kept %+v", rows)
	}
}

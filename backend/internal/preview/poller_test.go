package preview

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakePreviewSessions struct {
	sessions []domain.SessionRecord
	sets     []previewSet
	// previewRefusal stands in for a project whose "Web UI" setting is off.
	previewRefusal error
}

type previewSet struct {
	id  domain.SessionID
	url string
}

func (f *fakePreviewSessions) ListAllSessions(_ context.Context) ([]domain.SessionRecord, error) {
	return append([]domain.SessionRecord(nil), f.sessions...), nil
}

func (f *fakePreviewSessions) EnsurePreviewAllowed(_ context.Context, _ domain.SessionID) error {
	return f.previewRefusal
}

func (f *fakePreviewSessions) SetPreview(_ context.Context, id domain.SessionID, previewURL string) (domain.Session, error) {
	f.sets = append(f.sets, previewSet{id: id, url: previewURL})
	for i, sess := range f.sessions {
		if sess.ID == id {
			sess.Metadata.PreviewURL = previewURL
			f.sessions[i] = sess
			return domain.Session{SessionRecord: sess}, nil
		}
	}
	return domain.Session{}, nil
}

func TestPollerSetsPreviewWhenActiveWorkerEntryAppears(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>hello</main>")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "")}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	assertSets(t, svc.sets, previewSet{
		id:  "ao-1",
		url: "http://127.0.0.1:3001/api/v1/sessions/ao-1/preview/files/index.html",
	})
}

func TestPollerUsesFirstExistingEntrypoint(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "dist", "index.html"), "<main>dist</main>")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "")}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	assertSets(t, svc.sets, previewSet{
		id:  "ao-1",
		url: "http://127.0.0.1:3001/api/v1/sessions/ao-1/preview/files/dist/index.html",
	})
}

func TestPollerPreservesEntrypointPriority(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "public", "index.html"), "<main>public</main>")
	writeFile(t, filepath.Join(workspace, "dist", "index.html"), "<main>dist</main>")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "")}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	assertSets(t, svc.sets, previewSet{
		id:  "ao-1",
		url: "http://127.0.0.1:3001/api/v1/sessions/ao-1/preview/files/public/index.html",
	})
}

func TestPollerRefreshesOnlyWhenEntrypointChanges(t *testing.T) {
	workspace := t.TempDir()
	entry := filepath.Join(workspace, "index.html")
	writeFile(t, entry, "<main>v1</main>")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "")}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("first Poll: %v", err)
	}
	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("second Poll: %v", err)
	}
	if len(svc.sets) != 1 {
		t.Fatalf("sets after unchanged entry = %#v, want one set", svc.sets)
	}

	writeFile(t, entry, "<main>v2 changed</main>")
	nextMod := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(entry, nextMod, nextMod); err != nil {
		t.Fatalf("chtimes entry: %v", err)
	}
	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("third Poll: %v", err)
	}

	if len(svc.sets) != 2 {
		t.Fatalf("sets after changed entry = %#v, want refresh set", svc.sets)
	}
}

func TestPollerRediscoverEntryAfterDeleteAndRecreate(t *testing.T) {
	workspace := t.TempDir()
	entry := filepath.Join(workspace, "index.html")
	writeFile(t, entry, "<main>v1</main>")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "")}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	// First poll discovers the entry and sets the preview.
	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("first Poll: %v", err)
	}
	wantURL := "http://127.0.0.1:3001/api/v1/sessions/ao-1/preview/files/index.html"
	assertSets(t, svc.sets, previewSet{id: "ao-1", url: wantURL})

	// Delete the entry — poller must clear the preview and mark the session cleared.
	if err := os.Remove(entry); err != nil {
		t.Fatalf("remove index.html: %v", err)
	}
	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("second Poll (delete): %v", err)
	}
	if len(svc.sets) != 2 {
		t.Fatalf("sets after delete = %#v, want clear + set", svc.sets)
	}
	if svc.sets[1].url != "" {
		t.Fatalf("second set.url = %q, want empty (clear)", svc.sets[1].url)
	}

	// Recreate the entry — poller must re-discover.
	writeFile(t, entry, "<main>v2</main>")
	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("third Poll (recreate): %v", err)
	}
	if len(svc.sets) != 3 {
		t.Fatalf("sets after recreate = %#v, want 3 sets (discover + clear + rediscover)", svc.sets)
	}
	if svc.sets[2].url != wantURL {
		t.Fatalf("third set.url = %q, want %q", svc.sets[2].url, wantURL)
	}
}

func TestPollerDoesNotRestoreClearedPreviewAfterRestart(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>hello</main>")
	sess := workerSession("ao-1", workspace, "")
	sess.Metadata.PreviewRevision = 2
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{sess}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	if len(svc.sets) != 0 {
		t.Fatalf("sets = %#v, want cleared preview to remain empty after restart", svc.sets)
	}
}

func TestPollerDoesNotOverrideExplicitPreviewTarget(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>hello</main>")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "file:///C:/tmp/other.html")}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	if len(svc.sets) != 0 {
		t.Fatalf("sets = %#v, want no automatic override", svc.sets)
	}
}

func TestPollerSkipsNonWorkerSessions(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>hello</main>")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{{
		ID:   "ao-orch",
		Kind: domain.KindOrchestrator,
		Metadata: domain.SessionMetadata{
			WorkspacePath: workspace,
		},
	}}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	if len(svc.sets) != 0 {
		t.Fatalf("sets = %#v, want no preview updates for orchestrator sessions", svc.sets)
	}
}

// A worker writing its own plan/diagnosis notes must never take over the
// Browser tab: the poller reveals unprompted, so it only acts on a real
// entrypoint, not on "the newest file in the tree".
func TestPollerIgnoresScratchMarkdownAndLooseHTML(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "PLAN.md"), "# plan")
	writeFile(t, filepath.Join(workspace, "docs", "diagnosis.md"), "# diagnosis")
	writeFile(t, filepath.Join(workspace, "coverage", "report.html"), "<main>coverage</main>")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "")}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	if len(svc.sets) != 0 {
		t.Fatalf("sets = %#v, want no preview for a workspace with no entrypoint", svc.sets)
	}
}

// An explicit `ao preview <url>` target belongs to the agent; the poller must not
// even touch the filesystem for it, let alone overwrite it.
func TestPollerSkipsDiscoveryForExplicitPreview(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>hello</main>")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "http://localhost:5173")}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})
	discoveries := 0
	poller.discover = func(ws string) (Entry, bool) {
		discoveries++
		return DiscoverEntrypoint(ws)
	}

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	if discoveries != 0 {
		t.Fatalf("discoveries = %d, want 0 for an explicitly previewed session", discoveries)
	}
	if len(svc.sets) != 0 {
		t.Fatalf("sets = %#v, want no automatic override", svc.sets)
	}
}

// A bare `ao preview` can point the panel at any workspace file (a report, a
// design doc) that the poller would never discover on its own. That target is
// still live, so the poller must leave it alone — and must still retract it once
// the file is gone and the panel would 404.
func TestPollerKeepsExplicitWorkspaceFilePreviewUntilItIsGone(t *testing.T) {
	workspace := t.TempDir()
	report := filepath.Join(workspace, "REPORT.md")
	writeFile(t, report, "# report")
	current := "http://127.0.0.1:3001/api/v1/sessions/ao-1/preview/files/REPORT.md"
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, current)}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("first Poll: %v", err)
	}
	if len(svc.sets) != 0 {
		t.Fatalf("sets = %#v, want an explicitly previewed workspace file left alone", svc.sets)
	}

	if err := os.Remove(report); err != nil {
		t.Fatalf("remove report: %v", err)
	}
	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("second Poll: %v", err)
	}
	assertSets(t, svc.sets, previewSet{id: "ao-1", url: ""})
}

// `ao preview` is refused outright for a project with no web UI, so the poller
// must not do behind the agent's back what the agent is forbidden to ask for.
func TestPollerRespectsProjectWithoutWebUI(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>hello</main>")
	svc := &fakePreviewSessions{
		sessions:       []domain.SessionRecord{workerSession("ao-1", workspace, "")},
		previewRefusal: errors.New("WEB_PREVIEW_DISABLED"),
	}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("first Poll: %v", err)
	}
	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("second Poll: %v", err)
	}

	if len(svc.sets) != 0 {
		t.Fatalf("sets = %#v, want no preview for a project with no web UI", svc.sets)
	}

	// Turning "Web UI" on must take effect on the next tick, without waiting for
	// the entrypoint to change again.
	svc.previewRefusal = nil
	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("third Poll: %v", err)
	}
	assertSets(t, svc.sets, previewSet{
		id:  "ao-1",
		url: "http://127.0.0.1:3001/api/v1/sessions/ao-1/preview/files/index.html",
	})
}

// Clearing a stale preview is deliberately not gated (see EnsurePreviewAllowed):
// emptying the panel can never mislead, and it is how a target left over from
// before a project opted out gets dropped.
func TestPollerClearsStalePreviewEvenWithoutWebUI(t *testing.T) {
	workspace := t.TempDir()
	stale := "http://127.0.0.1:3001/api/v1/sessions/ao-1/preview/files/index.html"
	svc := &fakePreviewSessions{
		sessions:       []domain.SessionRecord{workerSession("ao-1", workspace, stale)},
		previewRefusal: errors.New("WEB_PREVIEW_DISABLED"),
	}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	assertSets(t, svc.sets, previewSet{id: "ao-1", url: ""})
}

// The interval guards a filesystem poll across every session; sub-second ticking
// is what pinned a CPU core.
func TestDefaultPollIntervalIsNotAHotLoop(t *testing.T) {
	if DefaultPollInterval < time.Second {
		t.Fatalf("DefaultPollInterval = %v, want >= 1s", DefaultPollInterval)
	}
	if got := NewPoller(nil, nil, "", PollerConfig{Logger: discardLogger()}).interval; got != DefaultPollInterval {
		t.Fatalf("poller interval = %v, want %v", got, DefaultPollInterval)
	}
}

func workerSession(id domain.SessionID, workspace, previewURL string) domain.SessionRecord {
	return domain.SessionRecord{
		ID:   id,
		Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{
			WorkspacePath: workspace,
			PreviewURL:    previewURL,
		},
	}
}

func writeFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertSets(t *testing.T, got []previewSet, want ...previewSet) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("sets = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sets[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

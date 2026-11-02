package controllers_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionhost"
)

type uploaded struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func newAttachmentsEnv(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dataDir := t.TempDir()
	svc := newFakeSessionService()
	s := svc.sessions["ao-1"]
	s.Transport = domain.TransportChat
	svc.sessions["ao-1"] = s
	term := s
	term.ID = "ao-2"
	term.Transport = domain.TransportTerminal
	svc.sessions["ao-2"] = term
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{DataDir: dataDir}, log, nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv, dataDir
}

func upload(t *testing.T, srv *httptest.Server, session, name, body string) ([]byte, int) {
	t.Helper()
	u := srv.URL + "/api/v1/sessions/" + session + "/chat/attachments?name=" + url.QueryEscape(name)
	resp, err := http.Post(u, "application/octet-stream", strings.NewReader(body)) //nolint:noctx // test client
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return out, resp.StatusCode
}

func TestChatAttachmentsSavesTheFileOutsideTheWorktreeForClaudeToRead(t *testing.T) {
	srv, dataDir := newAttachmentsEnv(t)
	body, status := upload(t, srv, "ao-1", "spec v2.pdf", "%PDF-1.4 fake")
	if status != http.StatusOK {
		t.Fatalf("upload = %d %s, want 200", status, body)
	}
	var got uploaded
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	dir := sessionhost.AttachmentsDir(dataDir, "ao-1")
	if got.Name != "spec v2.pdf" || got.Size != 13 || filepath.Base(got.Path) != "spec v2.pdf" {
		t.Fatalf("upload answered %+v", got)
	}
	if rel, err := filepath.Rel(dir, got.Path); err != nil || strings.HasPrefix(rel, "..") || !filepath.IsAbs(got.Path) {
		t.Fatalf("path %q is not an absolute path inside %q", got.Path, dir)
	}
	data, err := os.ReadFile(got.Path)
	if err != nil || string(data) != "%PDF-1.4 fake" {
		t.Fatalf("file on disk = %q, %v", data, err)
	}
	if info, _ := os.Stat(got.Path); info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", info.Mode().Perm())
	}

	again, _ := upload(t, srv, "ao-1", "spec v2.pdf", "second")
	var second uploaded
	_ = json.Unmarshal(again, &second)
	if second.Path == got.Path {
		t.Errorf("two uploads of one name share %q", got.Path)
	}
}

func TestChatAttachmentsRefusesBadNamesOtherSessionsAndOversizeFiles(t *testing.T) {
	srv, dataDir := newAttachmentsEnv(t)
	for _, name := range []string{"", ".", "..", "../x.txt", "a/b.txt", `a\b.txt`, "x\x00.txt", "x\n.txt", strings.Repeat("a", 256)} {
		body, status := upload(t, srv, "ao-1", name, "data")
		if status != http.StatusBadRequest {
			t.Errorf("name %q = %d %s, want 400", name, status, body)
		}
	}
	if body, status := upload(t, srv, "ao-2", "a.txt", "data"); status != http.StatusNotFound {
		t.Errorf("terminal session = %d %s, want 404", status, body)
	}
	if body, status := upload(t, srv, "ao-9", "a.txt", "data"); status != http.StatusNotFound {
		t.Errorf("unknown session = %d %s, want 404", status, body)
	}
	if body, status := upload(t, srv, "ao-1", "big.bin", strings.Repeat("x", 50<<20+1)); status != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize = %d %.200s, want 413", status, body)
	}
	if entries, _ := os.ReadDir(sessionhost.AttachmentsDir(dataDir, "ao-1")); len(entries) != 0 {
		t.Errorf("refused uploads left %d entries behind", len(entries))
	}
}

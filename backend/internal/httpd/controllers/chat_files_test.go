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
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

const fileCSP = "sandbox; default-src 'none'; img-src data:; style-src 'unsafe-inline'"

type chatFilesEnv struct {
	srv       *httptest.Server
	worktree  string
	knowledge string
	other     string
	outside   string
}

func newChatFilesEnv(t *testing.T) chatFilesEnv {
	t.Helper()
	base := t.TempDir()
	env := chatFilesEnv{
		worktree:  filepath.Join(base, "wt"),
		knowledge: filepath.Join(base, "knowledge", "ao"),
		other:     filepath.Join(base, "wt-other"),
		outside:   filepath.Join(base, "outside"),
	}
	for _, d := range []string{env.worktree, env.knowledge, env.other, env.outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	svc := newFakeSessionService()
	s := svc.sessions["ao-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: env.worktree}
	svc.sessions["ao-1"] = s
	o := s
	o.ID = "ao-2"
	o.ProjectID = "elsewhere"
	o.Metadata = domain.SessionMetadata{WorkspacePath: env.other}
	svc.sessions["ao-2"] = o
	cfg := config.Config{KnowledgeDir: filepath.Join(base, "knowledge")}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	env.srv = httptest.NewServer(httpd.NewRouterWithControl(cfg, log, nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{}))
	t.Cleanup(env.srv.Close)
	return env
}

func (e chatFilesEnv) get(t *testing.T, method, path string) ([]byte, int, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return body, resp.StatusCode, resp.Header
}

type resolvedMatch struct {
	Root       string `json:"root"`
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	MIME       string `json:"mime"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modifiedAt"`
}

type resolvedRef struct {
	Ref     string          `json:"ref"`
	Matches []resolvedMatch `json:"matches"`
}

func (e chatFilesEnv) resolve(t *testing.T, session string, refs ...string) ([]resolvedRef, int, []byte) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"refs": refs})
	if err != nil {
		t.Fatal(err)
	}
	body, status, _ := doRequest(t, e.srv, http.MethodPost, "/api/v1/sessions/"+session+"/chat/files/resolve", string(payload))
	var out struct {
		Files []resolvedRef `json:"files"`
	}
	if status == http.StatusOK {
		mustJSON(t, body, &out)
	}
	return out.Files, status, body
}

func matchPaths(m []resolvedMatch) []string {
	out := make([]string, 0, len(m))
	for _, x := range m {
		out = append(out, x.Root+":"+x.Path)
	}
	return out
}

func TestChatFilesServesWorktreeAndKnowledgeFilesWithSafeHeaders(t *testing.T) {
	env := newChatFilesEnv(t)
	writeFile(t, filepath.Join(env.worktree, "shots", "a.png"), "PNGBYTES")
	writeFile(t, filepath.Join(env.knowledge, "plans", "n.md"), "# plan")
	writeFile(t, filepath.Join(env.worktree, "page.html"), "<script>fetch('/api/v1/sessions')</script>")

	cases := []struct{ path, body, contentType string }{
		{"/api/v1/sessions/ao-1/chat/files/worktree/shots/a.png", "PNGBYTES", "image/png"},
		{"/api/v1/sessions/ao-1/chat/files/knowledge/plans/n.md", "# plan", "text/markdown; charset=utf-8"},
		{"/api/v1/sessions/ao-1/chat/files/worktree/page.html", "<script>fetch('/api/v1/sessions')</script>", "text/html; charset=utf-8"},
	}
	for _, tc := range cases {
		body, status, h := env.get(t, http.MethodGet, tc.path)
		if status != http.StatusOK || string(body) != tc.body {
			t.Fatalf("GET %s = %d %q, want 200 %q", tc.path, status, body, tc.body)
		}
		if got := h.Get("Content-Type"); got != tc.contentType {
			t.Errorf("GET %s Content-Type = %q, want %q", tc.path, got, tc.contentType)
		}
		if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET %s X-Content-Type-Options = %q", tc.path, got)
		}
		if got := h.Get("Content-Security-Policy"); got != fileCSP {
			t.Errorf("GET %s CSP = %q, want %q", tc.path, got, fileCSP)
		}
		if got := h.Get("Cache-Control"); got != "private, no-cache" {
			t.Errorf("GET %s Cache-Control = %q", tc.path, got)
		}
	}
}

func TestChatFilesAnswersHeadAndRangeRequests(t *testing.T) {
	env := newChatFilesEnv(t)
	writeFile(t, filepath.Join(env.worktree, "doc.pdf"), "0123456789")
	path := "/api/v1/sessions/ao-1/chat/files/worktree/doc.pdf"

	body, status, h := env.get(t, http.MethodHead, path)
	if status != http.StatusOK || len(body) != 0 || h.Get("Content-Length") != "10" || h.Get("Content-Security-Policy") != fileCSP {
		t.Fatalf("HEAD = %d len %d Content-Length %q CSP %q", status, len(body), h.Get("Content-Length"), h.Get("Content-Security-Policy"))
	}

	req, err := http.NewRequest(http.MethodGet, env.srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=2-5")
	resp, err := env.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	part, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusPartialContent || string(part) != "2345" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("range GET = %d %q nosniff=%q", resp.StatusCode, part, resp.Header.Get("X-Content-Type-Options"))
	}
}

func TestChatFilesRefusesPathsThatEscapeTheRoot(t *testing.T) {
	env := newChatFilesEnv(t)
	writeFile(t, filepath.Join(env.worktree, "ok.png"), "OK")
	writeFile(t, filepath.Join(env.outside, "secret.png"), "OUTSIDE-SECRET")
	writeFile(t, filepath.Join(env.outside, "dir", "page.html"), "OUTSIDE-SECRET")
	writeFile(t, filepath.Join(filepath.Dir(env.knowledge), "other-project", "x.png"), "OTHER-PROJECT")
	writeFile(t, filepath.Join(env.other, "theirs.png"), "OTHER-SESSION")
	if err := os.Symlink(filepath.Join(env.outside, "secret.png"), filepath.Join(env.worktree, "leak.png")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(env.outside, "dir"), filepath.Join(env.worktree, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(env.outside, "secret.png"), filepath.Join(env.knowledge, "leak.png")); err != nil {
		t.Fatal(err)
	}

	base := "/api/v1/sessions/ao-1/chat/files/"
	if _, status, _ := env.get(t, http.MethodGet, base+"worktree/ok.png"); status != http.StatusOK {
		t.Fatalf("control GET = %d, want 200", status)
	}
	refused := []string{
		base + "worktree/../outside/secret.png",
		base + "worktree/%2e%2e/outside/secret.png",
		base + "worktree/%2E%2E/outside/secret.png",
		base + "worktree/sub/..%2f..%2foutside/secret.png",
		base + "worktree/..%2foutside%2fsecret.png",
		base + "worktree/" + filepath.Join(env.outside, "secret.png"),
		base + "worktree//" + strings.TrimPrefix(filepath.Join(env.outside, "secret.png"), "/"),
		base + "worktree/%2F" + strings.TrimPrefix(filepath.Join(env.outside, "secret.png"), "/"),
		base + "nowhere/ok.png",
		base + "worktree/leak.png",
		base + "worktree/linked/page.html",
		base + "knowledge/leak.png",
		base + "knowledge/../other-project/x.png",
		base + "knowledge/%2e%2e/other-project/x.png",
		base + "worktree/theirs.png",
		base + "worktree/",
		base + "worktree",
	}
	for _, p := range refused {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			body, status, _ := env.get(t, method, p)
			if status != http.StatusNotFound || strings.Contains(string(body), "SECRET") || strings.Contains(string(body), "OTHER") {
				t.Errorf("%s %s = %d %q, want 404 without the file", method, p, status, body)
			}
		}
	}
	if _, status, _ := env.get(t, http.MethodGet, "/api/v1/sessions/ao-9/chat/files/worktree/ok.png"); status != http.StatusNotFound {
		t.Errorf("unknown session = %d, want 404", status)
	}
}

func TestChatFilesSessionsCannotReachEachOthersFiles(t *testing.T) {
	env := newChatFilesEnv(t)
	writeFile(t, filepath.Join(env.other, "theirs.png"), "OTHER-SESSION")
	writeFile(t, filepath.Join(env.worktree, "mine.png"), "MINE")

	if body, status, _ := env.get(t, http.MethodGet, "/api/v1/sessions/ao-1/chat/files/worktree/theirs.png"); status != http.StatusNotFound {
		t.Fatalf("ao-1 reading ao-2's file = %d %q, want 404", status, body)
	}
	if body, status, _ := env.get(t, http.MethodGet, "/api/v1/sessions/ao-2/chat/files/worktree/theirs.png"); status != http.StatusOK || string(body) != "OTHER-SESSION" {
		t.Fatalf("ao-2 reading its own file = %d %q, want 200", status, body)
	}
	if _, status, _ := env.get(t, http.MethodGet, "/api/v1/sessions/ao-2/chat/files/worktree/mine.png"); status != http.StatusNotFound {
		t.Fatalf("ao-2 reading ao-1's file = %d, want 404", status)
	}
	writeFile(t, filepath.Join(env.knowledge, "note.md"), "AO-NOTE")
	if _, status, _ := env.get(t, http.MethodGet, "/api/v1/sessions/ao-2/chat/files/knowledge/note.md"); status != http.StatusNotFound {
		t.Fatalf("ao-2 reading project ao's knowledge = %d, want 404", status)
	}
}

func TestChatFilesServesFromASymlinkedWorktree(t *testing.T) {
	env := newChatFilesEnv(t)
	writeFile(t, filepath.Join(env.worktree, "a.png"), "VIA-LINK")
	alias := filepath.Join(t.TempDir(), "wt-link")
	if err := os.Symlink(env.worktree, alias); err != nil {
		t.Fatal(err)
	}
	svc := newFakeSessionService()
	s := svc.sessions["ao-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: alias}
	svc.sessions["ao-1"] = s
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/ao-1/chat/files/worktree/a.png", "")
	if status != http.StatusOK || string(body) != "VIA-LINK" {
		t.Fatalf("GET through a symlinked worktree = %d %q, want 200", status, body)
	}
}

func TestChatFilesRefusesTypesOffTheList(t *testing.T) {
	env := newChatFilesEnv(t)
	writeFile(t, filepath.Join(env.worktree, "run.sh"), "echo")
	writeFile(t, filepath.Join(env.worktree, ".env"), "SECRET=1")
	writeFile(t, filepath.Join(env.worktree, "Makefile"), "all:")
	writeFile(t, filepath.Join(env.worktree, "ok.png"), "png")
	writeFile(t, filepath.Join(env.worktree, "ok.md"), "md")

	for _, name := range []string{"run.sh", ".env", "Makefile"} {
		body, status, _ := env.get(t, http.MethodGet, "/api/v1/sessions/ao-1/chat/files/worktree/"+name)
		if status != http.StatusNotFound || strings.Contains(string(body), "SECRET") {
			t.Errorf("GET %s = %d %q, want 404", name, status, body)
		}
	}
	files, status, body := env.resolve(t, "ao-1", "run.sh", ".env", "Makefile", "*")
	if status != http.StatusOK {
		t.Fatalf("resolve = %d %s", status, body)
	}
	for _, f := range files[:3] {
		if len(f.Matches) != 0 {
			t.Errorf("resolve %q = %v, want no match", f.Ref, matchPaths(f.Matches))
		}
	}
	if got := matchPaths(files[3].Matches); !slices.Equal(got, []string{"worktree:ok.md", "worktree:ok.png"}) {
		t.Errorf("resolve * = %v, want only the servable files", got)
	}
}

func TestChatFilesAnswers413ForAnOversizeFileAndResolveOmitsIt(t *testing.T) {
	env := newChatFilesEnv(t)
	big := filepath.Join(env.worktree, "big.svg")
	writeFile(t, big, "x")
	if err := os.Truncate(big, 2<<20+1); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(env.worktree, "small.svg"), "<svg/>")

	body, status, _ := env.get(t, http.MethodGet, "/api/v1/sessions/ao-1/chat/files/worktree/big.svg")
	assertErrorCode(t, body, status, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE")
	if _, status, _ := env.get(t, http.MethodGet, "/api/v1/sessions/ao-1/chat/files/worktree/small.svg"); status != http.StatusOK {
		t.Fatalf("small svg = %d, want 200", status)
	}
	files, _, _ := env.resolve(t, "ao-1", "big.svg", "*.svg")
	if len(files[0].Matches) != 0 || !slices.Equal(matchPaths(files[1].Matches), []string{"worktree:small.svg"}) {
		t.Fatalf("resolve = %+v, want the oversize file left out", files)
	}
}

func TestChatFilesResolveFindsAbsoluteTildeRelativeAndGlobRefs(t *testing.T) {
	env := newChatFilesEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, filepath.Join(env.worktree, "shots", "b.png"), "png-b")
	writeFile(t, filepath.Join(env.worktree, "shots", "a.png"), "png-a")
	writeFile(t, filepath.Join(env.knowledge, "plans", "only-store.md"), "# store")
	writeFile(t, filepath.Join(env.outside, "x.png"), "OUTSIDE")

	files, status, body := env.resolve(t, "ao-1",
		filepath.Join(env.worktree, "shots", "a.png"),
		"plans/only-store.md",
		"shots/*.png",
		"../*",
		filepath.Join(env.outside, "x.png"),
		"missing.png",
	)
	if status != http.StatusOK || len(files) != 6 {
		t.Fatalf("resolve = %d %s", status, body)
	}
	want := [][]string{
		{"worktree:shots/a.png"},
		{"knowledge:plans/only-store.md"},
		{"worktree:shots/a.png", "worktree:shots/b.png"},
		{}, {}, {},
	}
	for i, w := range want {
		if got := matchPaths(files[i].Matches); !slices.Equal(got, w) {
			t.Errorf("ref %q = %v, want %v", files[i].Ref, got, w)
		}
	}
	m := files[0].Matches[0]
	if m.Kind != "image" || m.MIME != "image/png" || m.Size != 5 || m.ModifiedAt == "" {
		t.Errorf("match = %+v, want kind image, mime image/png, size 5 and modifiedAt", m)
	}
	if files[1].Matches[0].Kind != "markdown" {
		t.Errorf("knowledge match kind = %q", files[1].Matches[0].Kind)
	}
	if string(body) == "" || !strings.Contains(string(body), `"matches":[]`) {
		t.Errorf("an unmatched ref must serialize matches as [], body=%s", body)
	}

	writeFile(t, filepath.Join(home, "note.md"), "# home")
	files, _, _ = env.resolve(t, "ao-1", "~/note.md")
	if len(files[0].Matches) != 0 {
		t.Errorf("a ~ path outside both roots matched %v", matchPaths(files[0].Matches))
	}
}

func TestChatFilesResolveCapsGlobMatchesAt24(t *testing.T) {
	env := newChatFilesEnv(t)
	for i := range 30 {
		writeFile(t, filepath.Join(env.worktree, "many", fmt.Sprintf("%02d.png", i)), "p")
	}
	files, _, _ := env.resolve(t, "ao-1", "many/*.png")
	if got := len(files[0].Matches); got != 24 {
		t.Fatalf("glob matches = %d, want 24", got)
	}
}

func TestChatFilesResolveValidatesTheRequest(t *testing.T) {
	env := newChatFilesEnv(t)
	post := func(session, body string) ([]byte, int) {
		out, status, _ := doRequest(t, env.srv, http.MethodPost, "/api/v1/sessions/"+session+"/chat/files/resolve", body)
		return out, status
	}

	if body, status := post("ao-9", `{"refs":["a.png"]}`); status != http.StatusNotFound {
		t.Errorf("unknown session = %d %s, want 404", status, body)
	}
	tooMany := `{"refs":[` + strings.TrimSuffix(strings.Repeat(`"a.png",`, 65), ",") + `]}`
	if body, status := post("ao-1", tooMany); status != http.StatusBadRequest {
		t.Errorf("65 refs = %d %s, want 400", status, body)
	}
	atLimit := `{"refs":[` + strings.TrimSuffix(strings.Repeat(`"a.png",`, 64), ",") + `]}`
	if body, status := post("ao-1", atLimit); status != http.StatusOK {
		t.Errorf("64 refs = %d %s, want 200", status, body)
	}
	for name, body := range map[string]string{
		"no refs":     `{"refs":[]}`,
		"missing":     `{}`,
		"long ref":    `{"refs":["` + strings.Repeat("a", 1025) + `"]}`,
		"bad json":    `{"refs":`,
		"wrong shape": `{"refs":"a.png"}`,
	} {
		if out, status := post("ao-1", body); status != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", name, status, out)
		}
	}
	if out, status := post("ao-1", `{"refs":["`+strings.Repeat("a", 1024)+`"]}`); status != http.StatusOK {
		t.Errorf("1024-byte ref = %d %s, want 200", status, out)
	}
}

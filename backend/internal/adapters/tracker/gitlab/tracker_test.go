package gitlab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// staticToken is a literal TokenSource for tests.
type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }

func TestTrackerGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// NOTE: asserts on r.URL.EscapedPath(), not r.URL.Path — Go's net/url
		// decodes "%2F" back into a literal "/" in the Path field (only
		// EscapedPath()/RawPath preserve the wire encoding), so a suffix
		// check against Path could never observe the "%2F" this test is
		// pinning. See TestTrackerGet_NestedGroupPathIsSingleEncoded below
		// for the fuller single-vs-double-encoding regression test.
		if !strings.HasSuffix(r.URL.EscapedPath(), "/projects/group%2Fproj/issues/5") {
			t.Fatalf("path %s", r.URL.EscapedPath())
		}
		_, _ = w.Write([]byte(`{"iid":5,"title":"Bug","description":"desc","state":"opened","web_url":"https://gl/5","labels":["bug"],"assignees":[{"username":"fluke"}]}`))
	}))
	defer srv.Close()
	tr, _ := New(Options{APIBase: srv.URL, Token: staticToken("t")})
	iss, err := tr.Get(context.Background(), domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/proj#5"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if iss.Title != "Bug" || iss.State != domain.IssueOpen || iss.ID.Provider != domain.TrackerProviderGitLab {
		t.Fatalf("issue wrong: %+v", iss)
	}
	if len(iss.Labels) != 1 || iss.Labels[0] != "bug" || len(iss.Assignees) != 1 {
		t.Fatalf("labels/assignees wrong: %+v", iss)
	}
}

// TestTrackerGet_NestedGroupPathIsSingleEncoded pins the exact encoding
// concern the SCM gitlab client hit: a nested-group project path
// ("group/sub/proj") url.PathEscape's the "/" separators to "%2F" before
// being joined onto the request path. If the adapter builds the URL via
// net/url and assigns the pre-escaped segment straight to url.URL.Path,
// url.URL.String() re-escapes it, turning "%2F" into "%252F" on the wire.
// This test asserts on r.URL.EscapedPath() (the actual bytes the server
// received) rather than r.URL.Path (which Go has already unescaped once),
// so a regression here fails loud instead of silently 404ing against a
// real GitLab.
func TestTrackerGet_NestedGroupPathIsSingleEncoded(t *testing.T) {
	var gotEscapedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEscapedPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"iid":7,"title":"T","description":"","state":"opened","web_url":"https://gl/7"}`))
	}))
	defer srv.Close()
	tr, _ := New(Options{APIBase: srv.URL, Token: staticToken("t")})
	_, err := tr.Get(context.Background(), domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/sub/proj#7"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	wantEscaped := "/projects/" + url.PathEscape("group/sub/proj") + "/issues/7"
	if gotEscapedPath != wantEscaped {
		t.Fatalf("EscapedPath() = %q, want %q", gotEscapedPath, wantEscaped)
	}
	if strings.Contains(gotEscapedPath, "%252F") {
		t.Fatalf("EscapedPath() = %q, project path was double-escaped", gotEscapedPath)
	}
	if !strings.Contains(gotEscapedPath, "group%2Fsub%2Fproj") {
		t.Fatalf("EscapedPath() = %q, want it to contain group%%2Fsub%%2Fproj", gotEscapedPath)
	}
}

func TestNewRejectsMissingToken(t *testing.T) {
	if _, err := New(Options{Token: StaticTokenSource("")}); !errors.Is(err, ErrNoToken) {
		t.Fatalf("New with empty token = %v, want ErrNoToken", err)
	}
	if _, err := New(Options{}); !errors.Is(err, ErrNoToken) {
		t.Fatalf("New with no source = %v, want ErrNoToken", err)
	}
}

func TestParseGitLabID(t *testing.T) {
	cases := []struct {
		name        string
		native      string
		wantProject string
		wantIID     int
		wantErr     bool
	}{
		{"simple", "group/proj#5", "group/proj", 5, false},
		{"nested group", "group/sub/proj#7", "group/sub/proj", 7, false},
		{"missing hash", "group/proj", "", 0, true},
		{"empty project", "#5", "", 0, true},
		{"empty segment", "group//proj#5", "", 0, true},
		{"non-numeric iid", "group/proj#abc", "", 0, true},
		{"zero iid", "group/proj#0", "", 0, true},
		{"negative iid", "group/proj#-1", "", 0, true},
		{"space in project", "group/pro j#5", "", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project, iid, err := parseGitLabID(tc.native)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %s#%d", project, iid)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if project != tc.wantProject || iid != tc.wantIID {
				t.Fatalf("got %s#%d, want %s#%d", project, iid, tc.wantProject, tc.wantIID)
			}
		})
	}
}

func TestGet_StateMapping(t *testing.T) {
	cases := []struct {
		glState   string
		wantState domain.NormalizedIssueState
	}{
		{"opened", domain.IssueOpen},
		{"closed", domain.IssueDone},
	}
	for _, tc := range cases {
		t.Run(tc.glState, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"iid":1,"title":"t","description":"","state":"` + tc.glState + `","web_url":"https://gl/1"}`))
			}))
			defer srv.Close()
			tr, _ := New(Options{APIBase: srv.URL, Token: staticToken("t")})
			iss, err := tr.Get(context.Background(), domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "g/p#1"})
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if iss.State != tc.wantState {
				t.Fatalf("state = %q, want %q", iss.State, tc.wantState)
			}
		})
	}
}

func TestGet_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"404 Not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	tr, _ := New(Options{APIBase: srv.URL, Token: staticToken("t")})
	_, err := tr.Get(context.Background(), domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "g/p#1"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestGet_AuthFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"401 Unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	tr, _ := New(Options{APIBase: srv.URL, Token: staticToken("t")})
	_, err := tr.Get(context.Background(), domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "g/p#1"})
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
}

func TestGet_RejectsWrongProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request %s", r.URL.Path)
	}))
	defer srv.Close()
	tr, _ := New(Options{APIBase: srv.URL, Token: staticToken("t")})
	_, err := tr.Get(context.Background(), domain.TrackerID{Provider: domain.TrackerProviderGitHub, Native: "g/p#1"})
	if !errors.Is(err, ErrWrongProvider) {
		t.Fatalf("err = %v, want ErrWrongProvider", err)
	}
}

func TestList_HappyPathWithFilters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.EscapedPath(), "/projects/group%2Fproj/issues") {
			t.Fatalf("path %s", r.URL.EscapedPath())
		}
		q := r.URL.Query()
		if got := q.Get("state"); got != "opened" {
			t.Errorf("state = %q, want opened", got)
		}
		if got := q.Get("labels"); got != "bug,help" {
			t.Errorf("labels = %q, want bug,help", got)
		}
		if got := q.Get("assignee_username"); got != "fluke" {
			t.Errorf("assignee_username = %q, want fluke", got)
		}
		_, _ = w.Write([]byte(`[
			{"iid":1,"title":"first","description":"","state":"opened","web_url":"https://gl/1"},
			{"iid":2,"title":"second","description":"","state":"closed","web_url":"https://gl/2","assignees":[{"username":"fluke"}]}
		]`))
	}))
	defer srv.Close()
	tr, _ := New(Options{APIBase: srv.URL, Token: staticToken("t")})
	issues, err := tr.List(context.Background(), domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/proj"}, domain.ListFilter{
		State: domain.ListOpen, Labels: []string{"bug", "help"}, Assignee: "fluke",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("len = %d, want 2", len(issues))
	}
	if issues[0].ID.Native != "group/proj#1" || issues[0].State != domain.IssueOpen {
		t.Fatalf("issues[0] = %#v", issues[0])
	}
	if issues[1].ID.Native != "group/proj#2" || issues[1].State != domain.IssueDone || len(issues[1].Assignees) != 1 {
		t.Fatalf("issues[1] = %#v", issues[1])
	}
}

func TestList_RejectsWrongProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request %s", r.URL.Path)
	}))
	defer srv.Close()
	tr, _ := New(Options{APIBase: srv.URL, Token: staticToken("t")})
	_, err := tr.List(context.Background(), domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "group/proj"}, domain.ListFilter{})
	if !errors.Is(err, ErrWrongProvider) {
		t.Fatalf("err = %v, want ErrWrongProvider", err)
	}
}

func TestPreflight_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/user") {
			t.Fatalf("path %s", r.URL.Path)
		}
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "t" {
			t.Errorf("PRIVATE-TOKEN = %q, want t", got)
		}
		_, _ = w.Write([]byte(`{"username":"fluke","id":1}`))
	}))
	defer srv.Close()
	tr, _ := New(Options{APIBase: srv.URL, Token: staticToken("t")})
	if err := tr.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
}

func TestPreflight_AuthFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"401 Unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	tr, _ := New(Options{APIBase: srv.URL, Token: staticToken("t")})
	err := tr.Preflight(context.Background())
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
}

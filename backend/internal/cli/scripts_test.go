package cli

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const scriptsWorktreeJSON = `{"owner":"nter-1","store":"/store","path":"/data/store-worktrees/store/nter-1","branch":"ao/nter-1","baseBranch":"main","state":"active","uncommitted":["projects/nter/draft.yaml"],"unpublished":2}`

func newScriptsServer(t *testing.T, publish string) (*[]string, *httptest.Server) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			paths = append(paths, r.Method+" "+r.URL.Path)
		}
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/scripts"):
			_, _ = io.WriteString(w, `{"worktree":`+scriptsWorktreeJSON+`,"storeDirty":["README.md"]}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/scripts/publish"):
			_, _ = io.WriteString(w, `{"worktree":`+scriptsWorktreeJSON+`,`+publish+`}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &paths, srv
}

func TestScriptsStatusDefaultsToTheCallersSession(t *testing.T) {
	cfg := setConfigEnv(t)
	paths, srv := newScriptsServer(t, "")
	writeRunFileFor(t, cfg, srv)
	t.Setenv("AO_SESSION_ID", "nter-2")

	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "scripts", "status")
	if err != nil {
		t.Fatal(err)
	}
	if (*paths)[0] != "GET /api/v1/sessions/nter-2/scripts" {
		t.Fatalf("requests = %v, want the caller's session", *paths)
	}
	for _, want := range []string{"/data/store-worktrees/store/nter-1", "ao/nter-1 (publishes into main of /store)", "unpublished: 2 commit(s)", "projects/nter/draft.yaml", "README.md", "ao scripts publish"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output misses %q:\n%s", want, out)
		}
	}

	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "scripts", "status", "--session", "nter-7", "--json"); err != nil {
		t.Fatal(err)
	}
	if (*paths)[1] != "GET /api/v1/sessions/nter-7/scripts" {
		t.Fatalf("requests = %v, want --session to win", *paths)
	}
}

func TestScriptsWithoutASessionIsAUsageError(t *testing.T) {
	setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "scripts", "publish")
	var usage usageError
	if !errors.As(err, &usage) {
		t.Fatalf("err = %v, want a usage error", err)
	}
}

func TestScriptsPublishSaysWhatItDid(t *testing.T) {
	cases := []struct {
		name, body string
		wantErr    bool
		want       []string
	}{
		{"fast forward", `"outcome":"fast_forward","sha":"0123456789abcdef","commits":2`, false,
			[]string{"Published 2 commit(s) from ao/nter-1 into main of /store (fast forward to 0123456789ab)", "1 uncommitted file(s) stay"}},
		{"merge", `"outcome":"merged","sha":"fedcba9876543210","commits":1`, false,
			[]string{"with merge commit fedcba987654"}},
		{"nothing", `"outcome":"nothing","commits":0`, false,
			[]string{"Nothing to publish"}},
		{"conflict", `"outcome":"refused","hold":"publish_conflict","detail":"ao/nter-1 conflicts with main","files":["projects/nter/login.yaml"]`, true,
			[]string{"publish refused (publish_conflict)", "projects/nter/login.yaml", "merge main into your branch in $AO_SCRIPTS_STORE", "ao scripts publish"}},
		{"dirty overlap", `"outcome":"refused","hold":"store_dirty_overlap","detail":"the store's main checkout has uncommitted changes","files":["projects/nter/a.yaml"]`, true,
			[]string{"store_dirty_overlap", "projects/nter/a.yaml", "never edit the main checkout"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			_, srv := newScriptsServer(t, tc.body)
			writeRunFileFor(t, cfg, srv)
			out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "scripts", "publish", "--session", "nter-1")
			text := out
			if err != nil {
				text = err.Error()
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Fatalf("output misses %q:\n%s", want, text)
				}
			}
		})
	}
}

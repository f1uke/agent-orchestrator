package gitlab

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestGlabAuthStatusCapturesStderr reproduces the real-world failure: `glab auth
// status --show-token` prints its status — including the token line — to STDERR,
// not stdout. glabAuthStatus must capture stderr, or the token never resolves and
// the GitLab provider disables itself with "no token configured". A fake glab on
// PATH that writes only to stderr fails the test if glabAuthStatus reads stdout
// alone.
func TestGlabAuthStatusCapturesStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake glab is a /bin/sh script")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "glab")
	// Mirror real glab: everything, including the token, goes to stderr; stdout empty.
	body := "#!/bin/sh\necho 'gitlab.example.com' >&2\necho '  ✓ Token: glpat-from-stderr' >&2\nexit 0\n"
	if err := os.WriteFile(fake, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	out, err := glabAuthStatus(context.Background(), "gitlab.example.com")
	if err != nil {
		t.Fatalf("glabAuthStatus: %v", err)
	}
	tok, err := parseGlabToken(out)
	if err != nil || tok != "glpat-from-stderr" {
		t.Fatalf("token=%q err=%v, want glpat-from-stderr (stderr not captured?)", tok, err)
	}
}

func TestParseGlabToken(t *testing.T) {
	sample := `gitlab.finnomena.com
  ✓ Logged in to gitlab.finnomena.com as fluke.s (config.yml)
  ✓ Git operations for gitlab.finnomena.com configured to use https protocol.
  ✓ Token: glpat-abc123DEF
`
	got, err := parseGlabToken(sample)
	if err != nil {
		t.Fatalf("parseGlabToken: %v", err)
	}
	if got != "glpat-abc123DEF" {
		t.Fatalf("token = %q, want glpat-abc123DEF", got)
	}
}

func TestParseGlabTokenSkipsMasked(t *testing.T) {
	sample := `gitlab.finnomena.com
  ✓ Logged in to gitlab.finnomena.com as fluke.s (config.yml)
  ✓ Token: ****************
`
	if _, err := parseGlabToken(sample); !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}

func TestParseGlabTokenNoMatch(t *testing.T) {
	sample := "gitlab.finnomena.com\n  x Not logged in\n"
	if _, err := parseGlabToken(sample); !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}

func TestEnvTokenSourcePrecedence(t *testing.T) {
	t.Setenv("AO_GITLAB_TOKEN", "ao-tok")
	t.Setenv("GITLAB_TOKEN", "generic-tok")
	tok, err := EnvTokenSource{EnvVars: []string{"AO_GITLAB_TOKEN"}}.Token(context.Background())
	if err != nil || tok != "ao-tok" {
		t.Fatalf("token=%q err=%v, want ao-tok", tok, err)
	}
}

func TestEnvTokenSourceFallsBackToGitlabToken(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "generic-tok")
	tok, err := EnvTokenSource{EnvVars: []string{"AO_GITLAB_TOKEN"}}.Token(context.Background())
	if err != nil || tok != "generic-tok" {
		t.Fatalf("token=%q err=%v, want generic-tok", tok, err)
	}
}

func TestEnvTokenSourceNoneSetReturnsErrNoToken(t *testing.T) {
	if _, err := (EnvTokenSource{EnvVars: []string{"AO_GITLAB_TOKEN"}}).Token(context.Background()); !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}

func TestStaticTokenSourceRejectsBlank(t *testing.T) {
	if _, err := StaticTokenSource("").Token(context.Background()); !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
	if _, err := StaticTokenSource("   ").Token(context.Background()); !errors.Is(err, ErrNoToken) {
		t.Fatalf("blank-with-spaces: err = %v, want ErrNoToken", err)
	}
}

func TestFallbackTokenSourceSkipsErrNoToken(t *testing.T) {
	src := FallbackTokenSource{
		StaticTokenSource(""),
		StaticTokenSource("second-tok"),
	}
	tok, err := src.Token(context.Background())
	if err != nil || tok != "second-tok" {
		t.Fatalf("token=%q err=%v, want second-tok", tok, err)
	}
}

func TestGlabTokenSourceUsesInjectedHook(t *testing.T) {
	src := &GlabTokenSource{Host: "gitlab.finnomena.com", Glab: func(ctx context.Context, host string) (string, error) {
		return "  ✓ Token: glpat-XYZ\n", nil
	}}
	tok, err := src.Token(context.Background())
	if err != nil || tok != "glpat-XYZ" {
		t.Fatalf("token=%q err=%v, want glpat-XYZ", tok, err)
	}
}

func TestGlabTokenSourceMemoizesAndInvalidates(t *testing.T) {
	calls := 0
	src := &GlabTokenSource{
		Host: "gitlab.finnomena.com",
		Glab: func(ctx context.Context, host string) (string, error) {
			calls++
			return "  ✓ Token: glpat-cached\n", nil
		},
		TokenTTL: time.Hour,
	}
	tok, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != "glpat-cached" {
		t.Fatalf("Token = %q, want glpat-cached", tok)
	}
	// Second call within TTL must be cached.
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatalf("second Token: %v", err)
	}
	if calls != 1 {
		t.Fatalf("Glab called %d times; want 1 (cache miss only)", calls)
	}
	// Invalidate and the next call must re-run.
	src.InvalidateToken()
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatalf("third Token: %v", err)
	}
	if calls != 2 {
		t.Fatalf("after invalidate, Glab called %d times; want 2", calls)
	}
}

func TestGlabTokenSourcePassesHost(t *testing.T) {
	var gotHost string
	src := &GlabTokenSource{Host: "gitlab.finnomena.com", Glab: func(ctx context.Context, host string) (string, error) {
		gotHost = host
		return "  ✓ Token: glpat-abc\n", nil
	}}
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if gotHost != "gitlab.finnomena.com" {
		t.Fatalf("host passed to Glab = %q, want gitlab.finnomena.com", gotHost)
	}
}

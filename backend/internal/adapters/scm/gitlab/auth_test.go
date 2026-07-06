package gitlab

import (
	"context"
	"errors"
	"testing"
	"time"
)

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

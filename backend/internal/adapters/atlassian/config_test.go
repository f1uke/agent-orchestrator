package atlassian

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"AO_JIRA_URL", "JIRA_SERVER", "AO_JIRA_EMAIL", "JIRA_LOGIN", "AO_JIRA_TOKEN", "JIRA_API_TOKEN"} {
		t.Setenv(k, "")
	}
}

func TestConfigFormattingRedactsToken(t *testing.T) {
	const token = "sentinel-token-4f1c"
	cfg := Config{BaseURL: "https://example.atlassian.net", Email: "alex@example.com", Token: token}
	for _, verb := range []string{"%v", "%+v", "%s"} {
		for _, v := range []any{cfg, &cfg} {
			got := fmt.Sprintf(verb, v)
			if strings.Contains(got, token) {
				t.Errorf("Sprintf(%q, %T) = %q, leaks the token", verb, v, got)
			}
			if !strings.Contains(got, "https://example.atlassian.net") || !strings.Contains(got, "alex@example.com") {
				t.Errorf("Sprintf(%q, %T) = %q, want the base URL and email kept", verb, v, got)
			}
		}
	}
}

func TestDefaultConfigSource_Resolution(t *testing.T) {
	clearEnv(t)
	cfgPath := filepath.Join(t.TempDir(), ".config.yml")
	if err := os.WriteFile(cfgPath, []byte("server: https://example.atlassian.net\nlogin: alex@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JIRA_CONFIG_FILE", cfgPath)

	if _, err := DefaultConfigSource(); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("no token: err = %v, want ErrAuthFailed", err)
	}

	t.Setenv("JIRA_API_TOKEN", "tok-xyz")
	cfg, err := DefaultConfigSource()
	if err != nil {
		t.Fatalf("with token: %v", err)
	}
	if cfg != (Config{BaseURL: "https://example.atlassian.net", Email: "alex@example.com", Token: "tok-xyz"}) {
		t.Errorf("cfg = %+v", cfg)
	}

	t.Setenv("AO_JIRA_URL", "https://override.example.net/")
	t.Setenv("AO_JIRA_TOKEN", "tok-ao")
	cfg, err = DefaultConfigSource()
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	if cfg.BaseURL != "https://override.example.net" || cfg.Token != "tok-ao" {
		t.Errorf("cfg = %+v, want the AO_ overrides with the slash trimmed", cfg)
	}
}

func TestDefaultConfigSource_NoServerIsUnavailable(t *testing.T) {
	clearEnv(t)
	t.Setenv("JIRA_CONFIG_FILE", filepath.Join(t.TempDir(), "does-not-exist.yml"))
	if _, err := DefaultConfigSource(); !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

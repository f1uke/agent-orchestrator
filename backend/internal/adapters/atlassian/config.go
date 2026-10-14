// Package atlassian holds the Atlassian Cloud site identity and the
// authenticated request building that the Jira and Confluence clients share.
//
// The base URL and login come from env or jira-cli's config file, and the API
// token from AO_JIRA_TOKEN or JIRA_API_TOKEN. The host is never hardcoded.
package atlassian

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The "jira:" text predates this package; the Browse Jira page shows err.Error()
// verbatim, so the prefix stays until a caller needs a neutral one.
var (
	// ErrAuthFailed is a rejected or absent credential.
	ErrAuthFailed = errors.New("jira: authentication failed")
	// ErrUnavailable is a transport or configuration failure: the site is
	// unreachable, not configured, or returns unparseable output.
	ErrUnavailable = errors.New("jira: unavailable")
)

// Config is the site base URL and the basic-auth identity.
type Config struct {
	BaseURL string
	Email   string
	Token   string
}

// String redacts the token so a logged or formatted Config never leaks it.
func (c Config) String() string {
	token := ""
	if c.Token != "" {
		token = "[redacted]"
	}
	return fmt.Sprintf("{BaseURL:%s Email:%s Token:%s}", c.BaseURL, c.Email, token)
}

// ConfigSource yields the site config. A seam so tests point a client at an
// httptest server with a static identity.
type ConfigSource func() (Config, error)

// DefaultConfigSource resolves the config from env, then jira-cli's config
// file for the non-secret base URL and login. Mirrors the SCM adapters'
// precedence (an AO_-prefixed override, then the generic value jira-cli honors).
func DefaultConfigSource() (Config, error) {
	cfg := Config{
		BaseURL: firstEnv("AO_JIRA_URL", "JIRA_SERVER"),
		Email:   firstEnv("AO_JIRA_EMAIL", "JIRA_LOGIN"),
		Token:   firstEnv("AO_JIRA_TOKEN", "JIRA_API_TOKEN"),
	}
	if cfg.BaseURL == "" || cfg.Email == "" {
		server, login := readCLIConfig()
		if cfg.BaseURL == "" {
			cfg.BaseURL = server
		}
		if cfg.Email == "" {
			cfg.Email = login
		}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.BaseURL == "" || cfg.Email == "" {
		return Config{}, fmt.Errorf("%w: Jira server/login not configured — run `jira init` or set AO_JIRA_URL and AO_JIRA_EMAIL", ErrUnavailable)
	}
	if cfg.Token == "" {
		return Config{}, fmt.Errorf("%w: no Jira API token — set JIRA_API_TOKEN (or AO_JIRA_TOKEN) so AO can read transitions and move status", ErrAuthFailed)
	}
	return cfg, nil
}

// firstEnv returns the first non-empty (trimmed) env var from names.
func firstEnv(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}

// readCLIConfig line-scans jira-cli's config for the top-level `server:`/`login:`
// values (no YAML dependency). Honors $JIRA_CONFIG_FILE, else the default path.
func readCLIConfig() (server, login string) {
	path := strings.TrimSpace(os.Getenv("JIRA_CONFIG_FILE"))
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", ""
		}
		path = filepath.Join(home, ".config", ".jira", ".config.yml")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "server:"):
			server = unquote(strings.TrimSpace(strings.TrimPrefix(line, "server:")))
		case strings.HasPrefix(line, "login:"):
			login = unquote(strings.TrimSpace(strings.TrimPrefix(line, "login:")))
		}
	}
	return server, login
}

func unquote(s string) string {
	return strings.Trim(s, `"'`)
}

package daemon

import (
	"io"
	"log/slog"
	"testing"
)

// TestGitlabHostsParsing pins gitlabHosts' comma-split + trim behavior and
// its empty-env fast path (AO_GITLAB_HOST unset/blank must disable GitLab
// wiring entirely).
func TestGitlabHostsParsing(t *testing.T) {
	t.Setenv("AO_GITLAB_HOST", " gitlab.finnomena.com , gl.example.com ")
	got := gitlabHosts()
	if len(got) != 2 || got[0] != "gitlab.finnomena.com" || got[1] != "gl.example.com" {
		t.Fatalf("hosts=%v", got)
	}

	t.Setenv("AO_GITLAB_HOST", "")
	if len(gitlabHosts()) != 0 {
		t.Fatalf("empty env should yield no hosts")
	}
}

// TestBuildSCMEntries_NoGitlabHostIsGithubOnly pins the no-GitLab-host path:
// with AO_GITLAB_HOST unset, the composite entries must contain exactly one
// entry named "github" — behavior identical to the pre-GitLab code that
// passed the GitHub provider directly to scmobserve.New.
func TestBuildSCMEntries_NoGitlabHostIsGithubOnly(t *testing.T) {
	t.Setenv("AO_GITLAB_HOST", "")
	// No GitHub token env vars set either; newGitHubSCMProvider uses
	// SkipTokenPreflight so construction still succeeds without network.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	entries, err := buildSCMEntries(log)
	if err != nil {
		t.Fatalf("buildSCMEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want exactly 1 (github only)", entries)
	}
	if entries[0].Name != "github" {
		t.Fatalf("entries[0].Name = %q, want %q", entries[0].Name, "github")
	}
}

// TestBuildSCMEntries_GitlabHostPrependsGitlabEntry asserts that when
// AO_GITLAB_HOST is set, a "gitlab" entry is prepended before "github" so it
// claims its host first in ParseRepository.
func TestBuildSCMEntries_GitlabHostPrependsGitlabEntry(t *testing.T) {
	t.Setenv("AO_GITLAB_HOST", "gitlab.example.com")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	entries, err := buildSCMEntries(log)
	if err != nil {
		t.Fatalf("buildSCMEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %v, want exactly 2 (gitlab + github)", entries)
	}
	if entries[0].Name != "gitlab" {
		t.Fatalf("entries[0].Name = %q, want %q (gitlab must come first)", entries[0].Name, "gitlab")
	}
	if entries[1].Name != "github" {
		t.Fatalf("entries[1].Name = %q, want %q", entries[1].Name, "github")
	}
}

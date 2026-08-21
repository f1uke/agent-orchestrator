package sessionmanager

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// "qa COMMITS ONLY TO TEST PATHS" HAS TO BE ENFORCED, and these are the tests
// that it is rather than asked for.
//
// Two members write one worktree, which means one git index. A wide `git add`
// by either sweeps up the other's work in progress and commits it under the
// wrong name, and `git log` is supposed to say who wrote a test. Both real crew
// runs showed a prompt-level rule being overridden by a brief, so this one is a
// git hook and git refuses the commit.

// TestCrewGitEnv_OnlyQAIsPoliced is the preservation guard. dev's scope is the
// whole tree and a solo session is not a crew member at all, so neither gets any
// git configuration injected - their environment is byte-for-byte what it was.
func TestCrewGitEnv_OnlyQAIsPoliced(t *testing.T) {
	dataDir := t.TempDir()
	for _, role := range []domain.CrewRole{"", domain.CrewRoleDev} {
		if env := crewGitEnv(role, dataDir); len(env) != 0 {
			t.Fatalf("role %q was given git configuration %v; only qa is policed", role, env)
		}
	}
	env := crewGitEnv(domain.CrewRoleQA, dataDir)
	if env["GIT_CONFIG_KEY_0"] != "core.hooksPath" || env["GIT_CONFIG_COUNT"] != "1" {
		t.Fatalf("qa's git environment = %v, want core.hooksPath injected", env)
	}
	// The hooks live under the DATA DIR, never in the repository: hooks are shared
	// by every worktree of a repo, so writing one into the checkout would police
	// the human's own tree and every other AO worktree of that project.
	if !strings.HasPrefix(env["GIT_CONFIG_VALUE_0"], dataDir) {
		t.Fatalf("hooks path %q is outside the data dir", env["GIT_CONFIG_VALUE_0"])
	}
	if _, err := os.Stat(filepath.Join(env["GIT_CONFIG_VALUE_0"], "pre-commit")); err != nil {
		t.Fatalf("the hook was not installed: %v", err)
	}
}

// TestQAPreCommitHook_RefusesANonTestPath drives the REAL hook through the REAL
// git, because a hook that is not exercised by git is a string literal.
func TestQAPreCommitHook_RefusesANonTestPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	env := crewGitEnv(domain.CrewRoleQA, t.TempDir())
	repo := hookRepo(t)

	// The shape this exists for: a wide `git add` that sweeps up dev's work in
	// progress along with qa's test.
	write(t, repo, "internal/app.go", "package app // dev is mid-change\n")
	write(t, repo, "internal/app_test.go", "package app // qa's test\n")
	git(t, repo, nil, "add", "-A")

	out, err := commit(t, repo, env, "test: sweep everything up")
	if err == nil {
		t.Fatalf("the commit was allowed:\n%s", out)
	}
	if !strings.Contains(out, "internal/app.go") {
		t.Fatalf("the refusal does not name the offending path:\n%s", out)
	}
	if !strings.Contains(out, "ao send --crew dev") {
		t.Fatalf("the refusal does not say what to do instead:\n%s", out)
	}
	// Nothing landed: the refusal costs the tree nothing.
	if out, _ := git(t, repo, nil, "log", "--oneline"); strings.Count(out, "\n") != 1 {
		t.Fatalf("a refused commit changed history:\n%s", out)
	}
}

// TestQAPreCommitHook_AllowsTestPaths: the rule has to let qa do its job, and the
// job is a wide range of shapes - Go tests, vitest specs, Maestro flows, fixtures.
func TestQAPreCommitHook_AllowsTestPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	env := crewGitEnv(domain.CrewRoleQA, t.TempDir())
	repo := hookRepo(t)

	for _, path := range []string{
		"internal/app_test.go",
		"frontend/src/renderer/lib/crew.test.ts",
		"frontend/src/renderer/components/Card.spec.tsx",
		"ios/AppUITests/LoginTests.swift",
		"maestro/login.yaml",
		"internal/service/testdata/fixture.json",
	} {
		write(t, repo, path, "// a test\n")
	}
	git(t, repo, nil, "add", "-A")
	if out, err := commit(t, repo, env, "test: the whole spread of test paths"); err != nil {
		t.Fatalf("qa was refused its own tests:\n%s", out)
	}
}

// TestQAPreCommitHook_ChainsToTheProjectsOwnHook. Replacing a repository's hooks
// path for one session must not take that repository's formatter or linter away
// from one agent and not the other.
func TestQAPreCommitHook_ChainsToTheProjectsOwnHook(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	env := crewGitEnv(domain.CrewRoleQA, t.TempDir())
	repo := hookRepo(t)

	marker := filepath.Join(repo, "project-hook-ran")
	projectHook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(projectHook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectHook, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	write(t, repo, "internal/app_test.go", "package app\n")
	git(t, repo, nil, "add", "-A")
	if out, err := commit(t, repo, env, "test: with a project hook in place"); err != nil {
		t.Fatalf("the commit failed:\n%s", out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the project's own pre-commit hook was not run")
	}
}

func hookRepo(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git(t, root, nil, "init", "-q")
	git(t, root, nil, "config", "user.email", "t@example.com")
	git(t, root, nil, "config", "user.name", "t")
	write(t, root, "README.md", "seed\n")
	git(t, root, nil, "add", "-A")
	if out, err := commit(t, root, nil, "init"); err != nil {
		t.Fatalf("seed commit: %s", out)
	}
	return root
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, root string, env map[string]string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func commit(t *testing.T, root string, env map[string]string, message string) (string, error) {
	t.Helper()
	return git(t, root, env, "commit", "-m", message)
}

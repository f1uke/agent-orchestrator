package knowledgestore

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestPlansDir_UnderDataDirKnowledge(t *testing.T) {
	got := PlansDir("/home/u/.ao", "demo-ios-app")
	want := filepath.Join("/home/u/.ao", "knowledge", "demo-ios-app", "plans")
	if got != want {
		t.Fatalf("PlansDir = %q, want %q", got, want)
	}
}

// writeFile creates parent dirs and writes content, failing the test on error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func destBaseNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func TestPreserveStrayDocs_CopiesPlanProposalAndDocsPlans(t *testing.T) {
	wt := t.TempDir()
	dest := filepath.Join(t.TempDir(), "plans")

	writeFile(t, filepath.Join(wt, "PLAN.md"), "root plan")
	writeFile(t, filepath.Join(wt, "design-proposal.md"), "a proposal")
	writeFile(t, filepath.Join(wt, "docs", "plans", "auth.md"), "docs-plans doc")
	// Unrelated files that must NOT be copied.
	writeFile(t, filepath.Join(wt, "README.md"), "readme")
	writeFile(t, filepath.Join(wt, "plan.txt"), "not markdown")
	writeFile(t, filepath.Join(wt, "docs", "architecture.md"), "arch doc, not under plans/")

	written, err := PreserveStrayDocs(wt, "feat/knowledge-store", dest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 3 {
		t.Fatalf("want 3 preserved files, got %d: %v", len(written), written)
	}

	got := destBaseNames(t, dest)
	want := []string{
		"feat-knowledge-store--PLAN.md",
		"feat-knowledge-store--design-proposal.md",
		"feat-knowledge-store--docs-plans-auth.md",
	}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("preserved names = %v, want %v", got, want)
	}

	// Content must be copied verbatim.
	body, err := os.ReadFile(filepath.Join(dest, "feat-knowledge-store--docs-plans-auth.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "docs-plans doc" {
		t.Fatalf("copied content = %q", body)
	}
}

func TestPreserveStrayDocs_SkipsHeavyAndHiddenDirs(t *testing.T) {
	wt := t.TempDir()
	dest := filepath.Join(t.TempDir(), "plans")

	// A real plan at the root must be found.
	writeFile(t, filepath.Join(wt, "implementation-plan.md"), "keep me")
	// Plans buried in dirs the scan must never descend into.
	writeFile(t, filepath.Join(wt, "node_modules", "pkg", "PLAN.md"), "vendored")
	writeFile(t, filepath.Join(wt, ".git", "PLAN.md"), "git internal")
	writeFile(t, filepath.Join(wt, ".claude", "worktrees", "x", "PLAN.md"), "claude scaffold")

	written, err := PreserveStrayDocs(wt, "main", dest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("want 1 preserved file (only the root plan), got %d: %v", len(written), written)
	}
	if names := destBaseNames(t, dest); len(names) != 1 || names[0] != "main--implementation-plan.md" {
		t.Fatalf("preserved names = %v", names)
	}
}

func TestPreserveStrayDocs_CaseInsensitiveMatch(t *testing.T) {
	wt := t.TempDir()
	dest := filepath.Join(t.TempDir(), "plans")
	writeFile(t, filepath.Join(wt, "MyPlan.MD"), "x")
	writeFile(t, filepath.Join(wt, "Refactor-Proposal.md"), "y")

	written, err := PreserveStrayDocs(wt, "b", dest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 2 {
		t.Fatalf("want 2, got %d: %v", len(written), written)
	}
}

func TestPreserveStrayDocs_NeverOverwrites(t *testing.T) {
	wt := t.TempDir()
	dest := filepath.Join(t.TempDir(), "plans")
	writeFile(t, filepath.Join(wt, "PLAN.md"), "version one")

	// First run writes the file.
	first, err := PreserveStrayDocs(wt, "feat/x", dest)
	if err != nil || len(first) != 1 {
		t.Fatalf("first run: written=%v err=%v", first, err)
	}

	// Second run with identical content is idempotent: nothing new written.
	second, err := PreserveStrayDocs(wt, "feat/x", dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("identical re-run must be a no-op, got %v", second)
	}
	if names := destBaseNames(t, dest); len(names) != 1 {
		t.Fatalf("idempotent run must not duplicate, got %v", names)
	}

	// Different content for the same source name must be preserved under a
	// suffix, never overwriting the original.
	writeFile(t, filepath.Join(wt, "PLAN.md"), "version two")
	third, err := PreserveStrayDocs(wt, "feat/x", dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != 1 {
		t.Fatalf("changed content must be preserved, got %v", third)
	}
	names := destBaseNames(t, dest)
	if len(names) != 2 {
		t.Fatalf("want original + suffixed copy, got %v", names)
	}
	orig, err := os.ReadFile(filepath.Join(dest, "feat-x--PLAN.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(orig) != "version one" {
		t.Fatalf("original was overwritten: %q", orig)
	}
}

func TestPreserveStrayDocs_MissingWorktreeIsNoError(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "plans")
	written, err := PreserveStrayDocs(filepath.Join(t.TempDir(), "does-not-exist"), "b", dest)
	if err != nil {
		t.Fatalf("missing worktree must be a benign no-op, got err=%v", err)
	}
	if len(written) != 0 {
		t.Fatalf("want no writes, got %v", written)
	}
	// Must not create the dest dir when there was nothing to copy.
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("dest dir should not be created when nothing is preserved")
	}
}

func TestPreserveStrayDocs_EmptyBranchStillCopies(t *testing.T) {
	wt := t.TempDir()
	dest := filepath.Join(t.TempDir(), "plans")
	writeFile(t, filepath.Join(wt, "PLAN.md"), "x")
	written, err := PreserveStrayDocs(wt, "", dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 {
		t.Fatalf("want 1, got %v", written)
	}
	if names := destBaseNames(t, dest); len(names) != 1 || !strings.HasSuffix(names[0], "--PLAN.md") {
		t.Fatalf("empty branch should still produce a prefixed name, got %v", names)
	}
}

package knowledgestore

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// migrationFixture is a project repo, a stranded root and a real store, laid
// out the way the old data-dir-relative store left them.
type migrationFixture struct {
	repo, stranded, store string
}

func (f migrationFixture) strandedPlans() string { return filepath.Join(f.stranded, "proj", "plans") }
func (f migrationFixture) storePlans() string    { return PlansDir(f.store, "proj") }

func (f migrationFixture) options(dryRun bool) MigrateOptions {
	return MigrateOptions{
		StrandedRoot: f.stranded,
		StoreRoot:    f.store,
		RepoPath: func(id string) (string, error) {
			if id == "proj" {
				return f.repo, nil
			}
			return "", nil
		},
		DryRun: dryRun,
	}
}

// newMigrationFixture strands one file per case the migration must tell apart,
// next to a store that already holds an INDEX.md and two clashing names.
func newMigrationFixture(t *testing.T) migrationFixture {
	t.Helper()
	f := migrationFixture{repo: newRepo(t), stranded: filepath.Join(t.TempDir(), "knowledge"), store: t.TempDir()}
	commitFiles(t, f.repo, map[string]string{"docs/plans/lifecycle.md": "v1"})
	commitFiles(t, f.repo, map[string]string{"docs/plans/lifecycle.md": "v2"})
	git(t, f.repo, "checkout", "-q", "-b", "side")
	commitFiles(t, f.repo, map[string]string{"docs/auth-PLAN.md": "side branch"})
	git(t, f.repo, "checkout", "-q", "main")

	sp := f.strandedPlans()
	// Copies of committed docs: an older version, the current one under a
	// collision suffix, and one committed only on another branch.
	writeFile(t, filepath.Join(sp, "feature-a--docs-plans-lifecycle.md"), "v1")
	writeFile(t, filepath.Join(sp, "bugfix-b--docs-plans-lifecycle-2.md"), "v2")
	writeFile(t, filepath.Join(sp, "feature-c--docs-auth-PLAN.md"), "side branch")
	// Genuine rescues: an edit nobody committed, an untracked doc, and
	// committed content under a name it was never committed at.
	writeFile(t, filepath.Join(sp, "feature-d--docs-plans-lifecycle.md"), "uncommitted edit")
	writeFile(t, filepath.Join(sp, "feature-e--PLAN.md"), "untracked plan")
	writeFile(t, filepath.Join(sp, "feature-f--design-proposal.md"), "v2")
	// Clashes with what the store already holds.
	writeFile(t, filepath.Join(sp, "feature-g--notes-plan.md"), "same bytes")
	writeFile(t, filepath.Join(sp, "feature-h--rollout-plan.md"), "stranded version")

	writeFile(t, filepath.Join(f.store, "proj", "INDEX.md"), "curated index")
	writeFile(t, filepath.Join(f.storePlans(), "feature-g--notes-plan.md"), "same bytes")
	writeFile(t, filepath.Join(f.storePlans(), "feature-h--rollout-plan.md"), "store version")
	return f
}

// actions maps each stranded file's base name to what the report says
// happened to it.
func actions(r MigrationReport) map[string]MigrationAction {
	out := map[string]MigrationAction{}
	for _, e := range r.Entries {
		out[filepath.Base(e.From)] = e.Action
	}
	return out
}

var wantFixtureActions = map[string]MigrationAction{
	"feature-a--docs-plans-lifecycle.md":  ActionDropCommitted,
	"bugfix-b--docs-plans-lifecycle-2.md": ActionDropCommitted,
	"feature-c--docs-auth-PLAN.md":        ActionDropCommitted,
	"feature-d--docs-plans-lifecycle.md":  ActionMove,
	"feature-e--PLAN.md":                  ActionMove,
	"feature-f--design-proposal.md":       ActionMove,
	"feature-g--notes-plan.md":            ActionDropDuplicate,
	"feature-h--rollout-plan.md":          ActionMove,
}

func assertActions(t *testing.T, r MigrationReport) {
	t.Helper()
	got := actions(r)
	if len(got) != len(wantFixtureActions) {
		t.Fatalf("report covers %d files, want %d: %v", len(got), len(wantFixtureActions), got)
	}
	for name, want := range wantFixtureActions {
		if got[name] != want {
			t.Errorf("%s: action %q, want %q", name, got[name], want)
		}
	}
}

func readString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMigrateStranded_DropsCommittedCopiesAndMovesRescues(t *testing.T) {
	f := newMigrationFixture(t)

	report, err := MigrateStranded(context.Background(), f.options(false))
	if err != nil {
		t.Fatalf("MigrateStranded: %v", err)
	}
	assertActions(t, report)

	// The stranded root is emptied and removed.
	if _, err := os.Stat(f.stranded); !os.IsNotExist(err) {
		t.Fatalf("stranded root should be gone, stat err = %v", err)
	}
	// Rescues land under their own names; the differing clash takes -2 and the
	// store's own file is untouched.
	wantStore := map[string]string{
		"feature-d--docs-plans-lifecycle.md": "uncommitted edit",
		"feature-e--PLAN.md":                 "untracked plan",
		"feature-f--design-proposal.md":      "v2",
		"feature-g--notes-plan.md":           "same bytes",
		"feature-h--rollout-plan.md":         "store version",
		"feature-h--rollout-plan-2.md":       "stranded version",
	}
	names := destBaseNames(t, f.storePlans())
	if len(names) != len(wantStore) {
		t.Fatalf("store plans = %v, want %d files", names, len(wantStore))
	}
	for name, content := range wantStore {
		if got := readString(t, filepath.Join(f.storePlans(), name)); got != content {
			t.Errorf("%s = %q, want %q", name, got, content)
		}
	}
	if got := readString(t, filepath.Join(f.store, "proj", "INDEX.md")); got != "curated index" {
		t.Fatalf("INDEX.md changed: %q", got)
	}
}

func TestMigrateStranded_DryRunTouchesNothing(t *testing.T) {
	f := newMigrationFixture(t)
	before := snapshot(t, f.stranded, f.store)

	report, err := MigrateStranded(context.Background(), f.options(true))
	if err != nil {
		t.Fatalf("MigrateStranded: %v", err)
	}
	assertActions(t, report)
	for _, e := range report.Entries {
		if filepath.Base(e.From) == "feature-h--rollout-plan.md" && filepath.Base(e.To) != "feature-h--rollout-plan-2.md" {
			t.Fatalf("dry run must report the suffixed name it would use, got %q", e.To)
		}
	}
	if after := snapshot(t, f.stranded, f.store); after != before {
		t.Fatalf("dry run changed the disk:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestMigrateStranded_IsIdempotent(t *testing.T) {
	f := newMigrationFixture(t)
	if _, err := MigrateStranded(context.Background(), f.options(false)); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, f.store)
	report, err := MigrateStranded(context.Background(), f.options(false))
	if err != nil || len(report.Entries) != 0 {
		t.Fatalf("second run: entries=%v err=%v, want nothing", report.Entries, err)
	}
	if after := snapshot(t, f.store); after != before {
		t.Fatalf("second run changed the store")
	}
}

// TestMigrateStranded_UnknownRepoMovesEverything: with no repo to compare
// against nothing can be classified as committed, so nothing is dropped.
func TestMigrateStranded_UnknownRepoMovesEverything(t *testing.T) {
	stranded, store := filepath.Join(t.TempDir(), "knowledge"), t.TempDir()
	writeFile(t, filepath.Join(stranded, "gone", "plans", "b--PLAN.md"), "x")
	writeFile(t, filepath.Join(stranded, "nogit", "plans", "b--PLAN.md"), "y")
	nonGit := t.TempDir()

	report, err := MigrateStranded(context.Background(), MigrateOptions{
		StrandedRoot: stranded, StoreRoot: store,
		RepoPath: func(id string) (string, error) {
			if id == "nogit" {
				return nonGit, nil
			}
			return "", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Count(ActionMove) != 2 || len(report.Entries) != 2 {
		t.Fatalf("want both moved, got %+v", report.Entries)
	}
	for _, p := range []string{"gone", "nogit"} {
		if _, err := os.Stat(filepath.Join(PlansDir(store, p), "b--PLAN.md")); err != nil {
			t.Fatalf("%s not moved: %v", p, err)
		}
	}
}

// TestMigrateStranded_LeavesWhatItCannotClassify: a failed project lookup and
// unexpected entries are left exactly where they are, with their dirs.
func TestMigrateStranded_LeavesWhatItCannotClassify(t *testing.T) {
	stranded, store := filepath.Join(t.TempDir(), "knowledge"), t.TempDir()
	writeFile(t, filepath.Join(stranded, "broken", "plans", "b--PLAN.md"), "x")
	writeFile(t, filepath.Join(stranded, "odd", "INDEX.md"), "not a plan")
	writeFile(t, filepath.Join(stranded, "odd", "plans", "nested", "b--PLAN.md"), "nested")
	writeFile(t, filepath.Join(stranded, "stray.md"), "top level")

	report, err := MigrateStranded(context.Background(), MigrateOptions{
		StrandedRoot: stranded, StoreRoot: store,
		RepoPath: func(id string) (string, error) {
			if id == "broken" {
				return "", errors.New("store is closed")
			}
			return "", nil
		},
	})
	if err == nil {
		t.Fatal("want the lookup failure reported")
	}
	if report.Count(ActionLeave) != len(report.Entries) || len(report.Entries) != 4 {
		t.Fatalf("want 4 entries, all left: %+v", report.Entries)
	}
	for _, p := range []string{
		"broken/plans/b--PLAN.md", "odd/INDEX.md", "odd/plans/nested/b--PLAN.md", "stray.md",
	} {
		if _, err := os.Stat(filepath.Join(stranded, filepath.FromSlash(p))); err != nil {
			t.Fatalf("%s must be left in place: %v", p, err)
		}
	}
	if names := destBaseNames(t, store); len(names) != 0 {
		t.Fatalf("store must be untouched, got %v", names)
	}
}

// TestMigrateStranded_AllCommittedLeavesNoTrace: a project whose strays are all
// committed copies gets nothing in the store, not even an empty plans dir.
func TestMigrateStranded_AllCommittedLeavesNoTrace(t *testing.T) {
	repo := newRepo(t)
	commitFiles(t, repo, map[string]string{"docs/batch-redemption-ui/PLAN.md": "tracked"})
	stranded, store := filepath.Join(t.TempDir(), "knowledge"), t.TempDir()
	for _, b := range []string{"feature-a", "feature-b", "release-1.0"} {
		writeFile(t, filepath.Join(stranded, "proj", "plans", b+"--docs-batch-redemption-ui-PLAN.md"), "tracked")
	}

	report, err := MigrateStranded(context.Background(), MigrateOptions{
		StrandedRoot: stranded, StoreRoot: store,
		RepoPath: func(string) (string, error) { return repo, nil },
	})
	if err != nil || report.Count(ActionDropCommitted) != 3 {
		t.Fatalf("entries=%+v err=%v, want 3 committed copies dropped", report.Entries, err)
	}
	if names := destBaseNames(t, store); len(names) != 0 {
		t.Fatalf("store must stay empty, got %v", names)
	}
	if _, err := os.Stat(stranded); !os.IsNotExist(err) {
		t.Fatalf("stranded root should be gone, stat err = %v", err)
	}
}

func TestMigrateStranded_SameRootIsNoop(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "proj", "plans", "b--PLAN.md"), "x")
	report, err := MigrateStranded(context.Background(), MigrateOptions{
		StrandedRoot: root, StoreRoot: root + string(filepath.Separator),
		RepoPath: func(string) (string, error) { return "", nil },
	})
	if err != nil || len(report.Entries) != 0 {
		t.Fatalf("entries=%v err=%v, want a no-op", report.Entries, err)
	}
}

func TestMigrateStranded_MissingStrandedRootIsNoop(t *testing.T) {
	report, err := MigrateStranded(context.Background(), MigrateOptions{
		StrandedRoot: filepath.Join(t.TempDir(), "absent"), StoreRoot: t.TempDir(),
		RepoPath: func(string) (string, error) { return "", nil },
	})
	if err != nil || len(report.Entries) != 0 {
		t.Fatalf("entries=%v err=%v, want a no-op", report.Entries, err)
	}
}

func TestPreservedFrom(t *testing.T) {
	cases := []struct {
		name, slug string
		want       bool
	}{
		{"feature-x--docs-plans-a.md", "docs-plans-a.md", true},
		{"feature-x--docs-plans-a-2.md", "docs-plans-a.md", true},
		{"feature-x--docs-plans-a-17.md", "docs-plans-a.md", true},
		{"a--b--docs-plans-a.md", "docs-plans-a.md", true},      // "--" inside the branch
		{"--docs-plans-a.md", "docs-plans-a.md", false},         // no branch
		{"feature-x-docs-plans-a.md", "docs-plans-a.md", false}, // no separator
		{"feature-x--my-docs-plans-a.md", "docs-plans-a.md", false},
		{"feature-x--docs-plans-a-v2.md", "docs-plans-a.md", false},
		{"feature-x--docs-plans-a-.md", "docs-plans-a.md", false},
		{"feature-x--PLAN", "PLAN", true},
	}
	for _, c := range cases {
		if got := preservedFrom(c.name, c.slug); got != c.want {
			t.Errorf("preservedFrom(%q, %q) = %v, want %v", c.name, c.slug, got, c.want)
		}
	}
}

// snapshot renders every file under dirs as "path=content" lines, so two
// snapshots compare equal exactly when nothing on disk changed.
func snapshot(t *testing.T, dirs ...string) string {
	t.Helper()
	var lines []string
	for _, d := range dirs {
		err := filepath.WalkDir(d, func(p string, e os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() {
				lines = append(lines, p+"/")
				return nil
			}
			lines = append(lines, p+"="+readString(t, p))
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

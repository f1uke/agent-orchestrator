package chatfiles_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatfiles"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sparse(t *testing.T, path string, size int64) {
	t.Helper()
	write(t, path, "x")
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
}

func link(t *testing.T, target, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, name); err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	worktree, knowledge, outside string
	roots                        chatfiles.Roots
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	base := t.TempDir()
	f := fixture{
		worktree:  filepath.Join(base, "wt"),
		knowledge: filepath.Join(base, "knowledge", "proj"),
		outside:   filepath.Join(base, "outside"),
	}
	for _, d := range []string{f.worktree, f.knowledge, f.outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.roots = chatfiles.NewRoots(f.worktree, f.knowledge)
	return f
}

func paths(files []chatfiles.File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, string(f.Root)+":"+f.Path)
	}
	return out
}

func TestOpenDescribesEachAllowedType(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name, kind, mime string
	}{
		{"a.png", "image", "image/png"},
		{"a.JPG", "image", "image/jpeg"},
		{"a.jpeg", "image", "image/jpeg"},
		{"a.gif", "image", "image/gif"},
		{"a.webp", "image", "image/webp"},
		{"a.svg", "image", "image/svg+xml"},
		{"a.md", "markdown", "text/markdown; charset=utf-8"},
		{"a.markdown", "markdown", "text/markdown; charset=utf-8"},
		{"a.html", "html", "text/html; charset=utf-8"},
		{"a.htm", "html", "text/html; charset=utf-8"},
		{"a.pdf", "pdf", "application/pdf"},
		{"a.json", "json", "application/json"},
		{"a.txt", "text", "text/plain; charset=utf-8"},
		{"a.csv", "text", "text/plain; charset=utf-8"},
		{"a.log", "log", "text/plain; charset=utf-8"},
		{"a.diff", "diff", "text/plain; charset=utf-8"},
		{"a.patch", "diff", "text/plain; charset=utf-8"},
	}
	for _, tc := range cases {
		write(t, filepath.Join(f.worktree, "docs", tc.name), "content")
		file, abs, err := f.roots.Open(chatfiles.Worktree, "docs/"+tc.name)
		if err != nil {
			t.Fatalf("Open %s: %v", tc.name, err)
		}
		if string(file.Kind) != tc.kind || file.MIME != tc.mime || file.Size != 7 || file.Path != "docs/"+tc.name || file.Root != chatfiles.Worktree {
			t.Errorf("Open %s = %+v, want kind %s mime %s", tc.name, file, tc.kind, tc.mime)
		}
		if file.ModTime.IsZero() {
			t.Errorf("Open %s: ModTime is zero", tc.name)
		}
		if !strings.HasSuffix(abs, filepath.FromSlash("docs/"+tc.name)) {
			t.Errorf("Open %s abs = %q", tc.name, abs)
		}
	}
}

func TestOpenReadsTheKnowledgeRoot(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.knowledge, "plans", "x.md"), "# plan")
	file, _, err := f.roots.Open(chatfiles.Knowledge, "plans/x.md")
	if err != nil || file.Root != chatfiles.Knowledge || file.Path != "plans/x.md" {
		t.Fatalf("Open knowledge = %+v, %v", file, err)
	}
}

func TestOpenRefusesWhatItShouldNotServe(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.worktree, "ok.png"), "png")
	write(t, filepath.Join(f.worktree, "run.sh"), "echo")
	write(t, filepath.Join(f.worktree, ".env"), "SECRET=1")
	write(t, filepath.Join(f.worktree, "Makefile"), "all:")
	write(t, filepath.Join(f.worktree, "dir.png", "inner.png"), "png")
	write(t, filepath.Join(f.outside, "secret.png"), "OUTSIDE")
	write(t, filepath.Join(f.outside, "dir", "page.html"), "OUTSIDE")
	write(t, filepath.Join(f.knowledge, "..", "other-project", "x.png"), "OTHER")
	link(t, filepath.Join(f.outside, "secret.png"), filepath.Join(f.worktree, "leak.png"))
	link(t, filepath.Join(f.outside, "dir"), filepath.Join(f.worktree, "linked"))
	link(t, filepath.Join(f.worktree, "run.sh"), filepath.Join(f.worktree, "alias.png"))
	link(t, "../../../outside/secret.png", filepath.Join(f.worktree, "deep", "rel", "leak.png"))

	if _, _, err := f.roots.Open(chatfiles.Worktree, "ok.png"); err != nil {
		t.Fatalf("control file ok.png: %v", err)
	}
	refused := []struct{ root, rel string }{
		{"worktree", ".."},
		{"worktree", "../outside/secret.png"},
		{"worktree", "a/../ok.png"},
		{"worktree", "..\\outside\\secret.png"},
		{"worktree", "/etc/hosts.txt"},
		{"worktree", filepath.Join(f.outside, "secret.png")},
		{"worktree", ""},
		{"worktree", "."},
		{"worktree", "ok.png\x00.sh"},
		{"worktree", "run.sh"},
		{"worktree", ".env"},
		{"worktree", "Makefile"},
		{"worktree", "dir.png"},
		{"worktree", "missing.png"},
		{"worktree", "leak.png"},
		{"worktree", "linked/page.html"},
		{"worktree", "alias.png"},
		{"worktree", "deep/rel/leak.png"},
		{"knowledge", "../other-project/x.png"},
		{"knowledge", "ok.png"},
		{"nowhere", "ok.png"},
		{"", "ok.png"},
	}
	for _, tc := range refused {
		_, _, err := f.roots.Open(chatfiles.Root(tc.root), tc.rel)
		if !errors.Is(err, chatfiles.ErrNotFound) {
			t.Errorf("Open(%q, %q) err = %v, want ErrNotFound", tc.root, tc.rel, err)
		}
	}
}

func TestOpenFollowsASymlinkedRootAndInRootLinks(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.worktree, "docs", "real.png"), "png")
	link(t, filepath.Join(f.worktree, "docs", "real.png"), filepath.Join(f.worktree, "alias.png"))
	aliased := filepath.Join(t.TempDir(), "wt-link")
	link(t, f.worktree, aliased)
	roots := chatfiles.NewRoots(aliased, "")

	file, _, err := roots.Open(chatfiles.Worktree, "alias.png")
	if err != nil || file.Path != "docs/real.png" {
		t.Fatalf("Open via symlinked root = %+v, %v, want docs/real.png", file, err)
	}
	if _, _, err := roots.Open(chatfiles.Knowledge, "x.png"); !errors.Is(err, chatfiles.ErrNotFound) {
		t.Fatalf("Open on a missing root = %v, want ErrNotFound", err)
	}
}

func TestOpenSiblingDirectorySharingTheRootPrefixIsOutside(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.worktree+"-evil", "x.png"), "EVIL")
	link(t, f.worktree+"-evil", filepath.Join(f.worktree, "sib"))
	if _, _, err := f.roots.Open(chatfiles.Worktree, "sib/x.png"); !errors.Is(err, chatfiles.ErrNotFound) {
		t.Fatalf("Open sibling-prefix dir = %v, want ErrNotFound", err)
	}
}

func TestOpenEnforcesTheSizeCapOfTheType(t *testing.T) {
	f := newFixture(t)
	const mib = 1 << 20
	cases := []struct {
		name        string
		atCap, over int64
	}{
		{"a.png", 25 * mib, 25*mib + 1},
		{"a.svg", 2 * mib, 2*mib + 1},
		{"a.md", 2 * mib, 2*mib + 1},
		{"a.pdf", 50 * mib, 50*mib + 1},
	}
	for _, tc := range cases {
		sparse(t, filepath.Join(f.worktree, "at-"+tc.name), tc.atCap)
		sparse(t, filepath.Join(f.worktree, "over-"+tc.name), tc.over)
		if _, _, err := f.roots.Open(chatfiles.Worktree, "at-"+tc.name); err != nil {
			t.Errorf("Open at-%s: %v, want served at the cap", tc.name, err)
		}
		if _, _, err := f.roots.Open(chatfiles.Worktree, "over-"+tc.name); !errors.Is(err, chatfiles.ErrTooLarge) {
			t.Errorf("Open over-%s = %v, want ErrTooLarge", tc.name, err)
		}
	}
}

func TestResolveAbsoluteAndTildePaths(t *testing.T) {
	f := newFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	knowledge := filepath.Join(home, ".ao", "knowledge", "proj")
	write(t, filepath.Join(knowledge, "plans", "n.md"), "# n")
	write(t, filepath.Join(f.worktree, "shots", "a.png"), "png")
	write(t, filepath.Join(f.outside, "b.png"), "png")
	roots := chatfiles.NewRoots(f.worktree, knowledge)

	got := paths(roots.Resolve(filepath.Join(f.worktree, "shots", "a.png")))
	if !slices.Equal(got, []string{"worktree:shots/a.png"}) {
		t.Errorf("absolute worktree path = %v", got)
	}
	got = paths(roots.Resolve("~/.ao/knowledge/proj/plans/n.md"))
	if !slices.Equal(got, []string{"knowledge:plans/n.md"}) {
		t.Errorf("tilde knowledge path = %v", got)
	}
	if got := roots.Resolve(filepath.Join(f.outside, "b.png")); len(got) != 0 {
		t.Errorf("absolute path outside both roots = %v, want none", paths(got))
	}
	if got := roots.Resolve("/etc/hosts"); len(got) != 0 {
		t.Errorf("/etc/hosts = %v, want none", paths(got))
	}
}

func TestResolveAbsolutePathThroughASymlinkedRoot(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.worktree, "a.png"), "png")
	alias := filepath.Join(t.TempDir(), "alias")
	link(t, f.worktree, alias)
	roots := chatfiles.NewRoots(alias, "")

	if got := paths(roots.Resolve(filepath.Join(alias, "a.png"))); !slices.Equal(got, []string{"worktree:a.png"}) {
		t.Errorf("path via the link = %v", got)
	}
	if got := paths(roots.Resolve(filepath.Join(f.worktree, "a.png"))); !slices.Equal(got, []string{"worktree:a.png"}) {
		t.Errorf("path via the real dir = %v", got)
	}
}

func TestResolveRelativeRefsTryTheWorktreeThenTheKnowledgeRoot(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.knowledge, "plans", "only-store.png"), "png")
	write(t, filepath.Join(f.knowledge, "plans", "both.png"), "store")
	write(t, filepath.Join(f.worktree, "plans", "both.png"), "tree")

	if got := paths(f.roots.Resolve("plans/only-store.png")); !slices.Equal(got, []string{"knowledge:plans/only-store.png"}) {
		t.Errorf("knowledge-only ref = %v", got)
	}
	if got := paths(f.roots.Resolve("./plans/both.png")); !slices.Equal(got, []string{"worktree:plans/both.png"}) {
		t.Errorf("ref in both roots = %v, want the worktree copy", got)
	}
	for _, ref := range []string{"", "  ", "../outside/x.png", "plans/../plans/both.png", "plans/missing.png", "run.sh"} {
		if got := f.roots.Resolve(ref); len(got) != 0 {
			t.Errorf("Resolve(%q) = %v, want none", ref, paths(got))
		}
	}
}

func TestResolveGlobsReturnEveryServableMatchSorted(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"c.png", "a.png", "b.png"} {
		write(t, filepath.Join(f.worktree, "shots", name), "png")
	}
	write(t, filepath.Join(f.worktree, "shots", "notes.sh"), "echo")
	sparse(t, filepath.Join(f.worktree, "shots", "huge.svg"), 3<<20)
	write(t, filepath.Join(f.knowledge, "shots", "d.png"), "png")
	link(t, filepath.Join(f.outside, "x.png"), filepath.Join(f.worktree, "shots", "z.png"))
	write(t, filepath.Join(f.outside, "x.png"), "OUTSIDE")

	want := []string{"worktree:shots/a.png", "worktree:shots/b.png", "worktree:shots/c.png", "knowledge:shots/d.png"}
	if got := paths(f.roots.Resolve("shots/*.png")); !slices.Equal(got, want) {
		t.Errorf("glob shots/*.png = %v, want %v", got, want)
	}
	if got := paths(f.roots.Resolve("shots/?.png")); !slices.Equal(got, want) {
		t.Errorf("glob shots/?.png = %v, want %v", got, want)
	}
	if got := paths(f.roots.Resolve("shots/[ab].png")); !slices.Equal(got, want[:2]) {
		t.Errorf("glob shots/[ab].png = %v", got)
	}
	if got := paths(f.roots.Resolve(filepath.Join(f.worktree, "shots", "*.png"))); !slices.Equal(got, want[:3]) {
		t.Errorf("absolute glob = %v, want %v", got, want[:3])
	}
}

func TestResolveGlobCannotEscape(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.worktree, "ok.png"), "png")
	write(t, filepath.Join(f.outside, "secret.png"), "OUTSIDE")
	if got := paths(f.roots.Resolve("*.png")); !slices.Equal(got, []string{"worktree:ok.png"}) {
		t.Fatalf("control glob = %v", got)
	}
	for _, ref := range []string{
		"../*", "../outside/*.png", "*/../*.png", "sub/../../*.png",
		filepath.Join(f.worktree, "..", "outside", "*.png"),
		filepath.Join(f.outside, "*.png"),
		"/*/*.png",
		"[", "*[",
	} {
		if got := f.roots.Resolve(ref); len(got) != 0 {
			t.Errorf("Resolve(%q) = %v, want none", ref, paths(got))
		}
	}
}

func TestResolveGlobInARootWhoseOwnPathHoldsGlobCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wt[1]*")
	write(t, filepath.Join(dir, "x.png"), "png")
	write(t, filepath.Join(dir, "x.sh"), "echo")
	roots := chatfiles.NewRoots(dir, "")
	if got := paths(roots.Resolve("*")); !slices.Equal(got, []string{"worktree:x.png"}) {
		t.Errorf("relative glob = %v", got)
	}
}

func TestResolveCapsGlobMatches(t *testing.T) {
	f := newFixture(t)
	for i := range 30 {
		write(t, filepath.Join(f.worktree, "many", fmt.Sprintf("%02d.png", i)), "png")
	}
	got := paths(f.roots.Resolve("many/*.png"))
	if len(got) != 24 || got[0] != "worktree:many/00.png" || got[23] != "worktree:many/23.png" {
		t.Errorf("capped glob = %d matches %v, want the first 24 sorted", len(got), got)
	}
}

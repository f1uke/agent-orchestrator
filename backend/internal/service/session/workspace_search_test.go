package session

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// searchRepo is the fixture every case below narrows: a small tree with the
// shapes that break a search engine — mixed case, a word inside a longer word,
// a non-ASCII line, a binary file, a nested directory.
func searchRepo(t *testing.T) string {
	t.Helper()
	return gitRepo(t, map[string]string{
		"App/Feature/Login.swift":    "import UIKit\nclass LoginViewModel {\n    let viewModel = 1\n}\n",
		"App/Feature/Signup.swift":   "class SignupViewModel {}\n",
		"App/Legacy/old.m":           "// viewmodel, lowercase\n",
		"Docs/readme.md":             "See the ViewModel docs.\nNothing else here.\n",
		"Pods/Vendor/vendor.swift":   "class VendorViewModel {}\n",
		"App/Feature/emoji.swift":    "let 🎉 = \"ViewModel\"\n",
		"App/Feature/binary.bin":     "abc\x00defViewModel\n",
		"App/Feature/notfound.swift": "nothing to see\n",
	})
}

func search(t *testing.T, dir string, q SearchQuery) SearchResult {
	t.Helper()
	return searchWorkspaceTree(context.Background(), dir, q)
}

// samePaths compares two path lists. The package's own equalStrings takes
// ResolveCandidates, not strings.
func samePaths(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// paths is the set of files a result names, for assertions that do not care
// about the matches themselves.
func paths(res SearchResult) []string {
	out := make([]string, 0, len(res.Files))
	for _, f := range res.Files {
		out = append(out, f.Path)
	}
	return out
}

func TestSearchWorkspace_EmptyQueryIsAvailableAndEmpty(t *testing.T) {
	dir := searchRepo(t)
	res := search(t, dir, SearchQuery{Query: "  "})
	if !res.Available {
		t.Fatalf("available = false, want true — an empty box is not a failure")
	}
	if len(res.Files) != 0 || res.TotalMatches != 0 {
		t.Fatalf("files = %v, matches = %d, want empty", paths(res), res.TotalMatches)
	}
}

func TestSearchWorkspace_CaseInsensitiveByDefault(t *testing.T) {
	dir := searchRepo(t)
	res := search(t, dir, SearchQuery{Query: "viewmodel"})
	// Every text file mentioning it in any case, and NOT the binary one.
	want := []string{
		"App/Feature/Login.swift",
		"App/Feature/Signup.swift",
		"App/Feature/emoji.swift",
		"App/Legacy/old.m",
		"Docs/readme.md",
		"Pods/Vendor/vendor.swift",
	}
	if got := paths(res); !samePaths(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	// Login.swift says it twice: `LoginViewModel` and `viewModel`.
	if res.Files[0].Total != 2 {
		t.Fatalf("Login.swift total = %d, want 2", res.Files[0].Total)
	}
	if res.TotalMatches != 7 {
		t.Fatalf("total matches = %d, want 7", res.TotalMatches)
	}
}

func TestSearchWorkspace_MatchCase(t *testing.T) {
	dir := searchRepo(t)
	res := search(t, dir, SearchQuery{Query: "viewmodel", MatchCase: true})
	if got, want := paths(res), []string{"App/Legacy/old.m"}; !samePaths(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func TestSearchWorkspace_WholeWord(t *testing.T) {
	dir := searchRepo(t)
	// `LoginViewModel` is not a whole-word hit for `ViewModel` — only the two
	// places it stands on its own are.
	res := search(t, dir, SearchQuery{Query: "ViewModel", WholeWord: true, MatchCase: true})
	if got, want := paths(res), []string{"App/Feature/emoji.swift", "Docs/readme.md"}; !samePaths(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	res = search(t, dir, SearchQuery{Query: "viewModel", WholeWord: true, MatchCase: true})
	if got, want := paths(res), []string{"App/Feature/Login.swift"}; !samePaths(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	if n := res.Files[0].Total; n != 1 {
		t.Fatalf("whole-word matches = %d, want 1 (LoginViewModel must not count)", n)
	}
}

func TestSearchWorkspace_Regex(t *testing.T) {
	dir := searchRepo(t)
	res := search(t, dir, SearchQuery{Query: `class \w+ViewModel`, Regex: true, MatchCase: true})
	want := []string{"App/Feature/Login.swift", "App/Feature/Signup.swift", "Pods/Vendor/vendor.swift"}
	if got := paths(res); !samePaths(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func TestSearchWorkspace_RegexAlternationBindsInsideWholeWord(t *testing.T) {
	dir := searchRepo(t)
	// `\bclass|let\b` would mean `(\bclass)|(let\b)`. Wrapped correctly it is
	// `\b(?:class|let)\b`, so both alternatives are whole words.
	res := search(t, dir, SearchQuery{Query: "class|let", Regex: true, WholeWord: true, MatchCase: true})
	want := []string{
		"App/Feature/Login.swift",
		"App/Feature/Signup.swift",
		"App/Feature/emoji.swift",
		"Pods/Vendor/vendor.swift",
	}
	if got := paths(res); !samePaths(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func TestSearchWorkspace_RegexAnchorsAreLineAnchors(t *testing.T) {
	dir := searchRepo(t)
	// `^class` must mean "start of a LINE", not "start of the file" — otherwise
	// Signup.swift is the only hit and Login.swift is silently missed.
	res := search(t, dir, SearchQuery{Query: "^class", Regex: true, MatchCase: true})
	want := []string{"App/Feature/Login.swift", "App/Feature/Signup.swift", "Pods/Vendor/vendor.swift"}
	if got := paths(res); !samePaths(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	if line := res.Files[0].Matches[0].Line; line != 2 {
		t.Fatalf("Login.swift first hit line = %d, want 2", line)
	}
}

func TestSearchWorkspace_InvalidRegexIsAStateNotAnError(t *testing.T) {
	dir := searchRepo(t)
	res := search(t, dir, SearchQuery{Query: "class (", Regex: true})
	if !res.Available {
		t.Fatalf("available = false, want true")
	}
	if res.InvalidRegex == "" {
		t.Fatalf("invalidRegex is empty, want the compile error")
	}
	if len(res.Files) != 0 {
		t.Fatalf("files = %v, want none", paths(res))
	}
}

func TestSearchWorkspace_ZeroWidthMatchesAreNotResults(t *testing.T) {
	dir := searchRepo(t)
	// `x*` matches the empty string at every position. Reporting those would
	// make every file in the tree a hit, one per byte.
	res := search(t, dir, SearchQuery{Query: "zzz*", Regex: true})
	if len(res.Files) != 0 {
		t.Fatalf("files = %v, want none — zero-width matches are not results", paths(res))
	}
}

func TestSearchWorkspace_LineColumnAndPreview(t *testing.T) {
	dir := searchRepo(t)
	res := search(t, dir, SearchQuery{Query: "viewModel", MatchCase: true})
	if len(res.Files) != 1 {
		t.Fatalf("files = %v, want just Login.swift", paths(res))
	}
	m := res.Files[0].Matches[0]
	if m.Line != 3 {
		t.Fatalf("line = %d, want 3", m.Line)
	}
	// "    let viewModel = 1" — the match starts at the 9th character.
	if m.Column != 9 {
		t.Fatalf("column = %d, want 9", m.Column)
	}
	if m.EndColumn != 18 {
		t.Fatalf("endColumn = %d, want 18", m.EndColumn)
	}
	if m.Preview != "    let viewModel = 1" {
		t.Fatalf("preview = %q", m.Preview)
	}
	if m.PreviewStart != 8 || m.PreviewEnd != 17 {
		t.Fatalf("preview range = %d..%d, want 8..17", m.PreviewStart, m.PreviewEnd)
	}
}

func TestSearchWorkspace_ColumnIsUTF16NotBytes(t *testing.T) {
	dir := searchRepo(t)
	// `let 🎉 = "ViewModel"` — the emoji is 4 BYTES but 2 UTF-16 units, which is
	// what Monaco counts. Byte columns would land the caret four characters off.
	res := search(t, dir, SearchQuery{Query: "ViewModel", MatchCase: true, Include: "emoji.swift"})
	if len(res.Files) != 1 {
		t.Fatalf("files = %v, want emoji.swift", paths(res))
	}
	// `let 🎉 = "ViewModel"` — V is the 11th UTF-16 unit (the emoji is two) and
	// the 13th byte (it is four).
	if got := res.Files[0].Matches[0].Column; got != 11 {
		t.Fatalf("column = %d, want 11 (UTF-16); 13 would mean bytes", got)
	}
}

func TestSearchWorkspace_SkipsBinaryFiles(t *testing.T) {
	dir := searchRepo(t)
	res := search(t, dir, SearchQuery{Query: "ViewModel"})
	for _, p := range paths(res) {
		if strings.HasSuffix(p, ".bin") {
			t.Fatalf("binary file %s is in the results", p)
		}
	}
}

func TestSearchWorkspace_SkipsFilesOverTheSizeCap(t *testing.T) {
	dir := gitRepo(t, map[string]string{"small.txt": "needle\n"})
	writeRepoFile(t, dir, "huge.txt", strings.Repeat("needle padding padding\n", (maxSearchFileBytes/23)+64))
	res := search(t, dir, SearchQuery{Query: "needle"})
	if got, want := paths(res), []string{"small.txt"}; !samePaths(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func TestSearchWorkspace_LongLineIsWindowedAroundTheMatch(t *testing.T) {
	dir := gitRepo(t, map[string]string{
		"min.js": strings.Repeat("x", 4000) + "needle" + strings.Repeat("y", 4000) + "\n",
	})
	res := search(t, dir, SearchQuery{Query: "needle"})
	m := res.Files[0].Matches[0]
	if len(m.Preview) != searchPreview {
		t.Fatalf("preview length = %d, want %d", len(m.Preview), searchPreview)
	}
	// The column still addresses the REAL line, so opening the hit lands on it.
	if m.Column != 4001 {
		t.Fatalf("column = %d, want 4001", m.Column)
	}
	// The highlight addresses the PREVIEW, which is a different number.
	if got := m.Preview[m.PreviewStart:m.PreviewEnd]; got != "needle" {
		t.Fatalf("preview[%d:%d] = %q, want %q", m.PreviewStart, m.PreviewEnd, got, "needle")
	}
}

func TestSearchWorkspace_PerFileMatchCapIsReported(t *testing.T) {
	dir := gitRepo(t, map[string]string{
		"many.txt": strings.Repeat("needle\n", maxSearchFileMatches+40),
	})
	res := search(t, dir, SearchQuery{Query: "needle"})
	f := res.Files[0]
	if len(f.Matches) != maxSearchFileMatches {
		t.Fatalf("matches = %d, want %d", len(f.Matches), maxSearchFileMatches)
	}
	if f.Total != maxSearchFileMatches+40 {
		t.Fatalf("total = %d, want %d — the honest count survives the cap", f.Total, maxSearchFileMatches+40)
	}
	if !f.Truncated || !res.Truncated {
		t.Fatalf("truncated = %v/%v, want both true", f.Truncated, res.Truncated)
	}
}

func TestSearchWorkspace_FileCapIsReported(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < maxSearchFiles+5; i++ {
		files[fmt.Sprintf("d%04d/f.txt", i)] = "needle\n"
	}
	dir := gitRepo(t, files)
	res := search(t, dir, SearchQuery{Query: "needle"})
	if len(res.Files) != maxSearchFiles {
		t.Fatalf("files = %d, want %d", len(res.Files), maxSearchFiles)
	}
	if res.TotalFiles != maxSearchFiles+5 {
		t.Fatalf("totalFiles = %d, want %d — the honest count survives the cap", res.TotalFiles, maxSearchFiles+5)
	}
	if !res.Truncated {
		t.Fatalf("truncated = false, want true")
	}
}

func TestSearchWorkspace_ResultsAreInPathOrder(t *testing.T) {
	dir := searchRepo(t)
	res := search(t, dir, SearchQuery{Query: "ViewModel"})
	for i := 1; i < len(res.Files); i++ {
		if res.Files[i-1].Path >= res.Files[i].Path {
			t.Fatalf("not sorted: %q before %q", res.Files[i-1].Path, res.Files[i].Path)
		}
	}
}

func TestSearchWorkspace_IncludeAndExcludeGlobs(t *testing.T) {
	dir := searchRepo(t)
	cases := []struct {
		name string
		q    SearchQuery
		want []string
	}{
		{
			// A bare pattern with no slash matches any path SEGMENT, so an
			// extension glob works wherever the file lives.
			name: "include by extension",
			q:    SearchQuery{Query: "ViewModel", Include: "*.swift"},
			want: []string{"App/Feature/Login.swift", "App/Feature/Signup.swift", "App/Feature/emoji.swift", "Pods/Vendor/vendor.swift"},
		},
		{
			// ...and a bare DIRECTORY name excludes everything under it, which is
			// what someone typing "Pods" into exclude means.
			name: "exclude a directory by bare name",
			q:    SearchQuery{Query: "ViewModel", Include: "*.swift", Exclude: "Pods"},
			want: []string{"App/Feature/Login.swift", "App/Feature/Signup.swift", "App/Feature/emoji.swift"},
		},
		{
			name: "include a directory path",
			q:    SearchQuery{Query: "ViewModel", Include: "App/Legacy"},
			want: []string{"App/Legacy/old.m"},
		},
		{
			name: "double star crosses separators",
			q:    SearchQuery{Query: "ViewModel", Include: "App/**/*.m"},
			want: []string{"App/Legacy/old.m"},
		},
		{
			name: "several patterns, comma separated",
			q:    SearchQuery{Query: "ViewModel", Include: "*.md, *.m"},
			want: []string{"App/Legacy/old.m", "Docs/readme.md"},
		},
		{
			name: "an unparseable glob narrows nothing",
			q:    SearchQuery{Query: "ViewModel", Include: "App/Legacy, ["},
			want: []string{"App/Legacy/old.m"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := paths(search(t, dir, tc.q)); !samePaths(got, tc.want) {
				t.Fatalf("paths = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSearchWorkspace_CancelledContextReturnsPartialAndSaysSo(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 400; i++ {
		files[fmt.Sprintf("d%03d/f.txt", i)] = strings.Repeat("needle\n", 200)
	}
	dir := gitRepo(t, files)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := searchWorkspaceTree(ctx, dir, SearchQuery{Query: "needle"})
	if !res.Truncated {
		t.Fatalf("truncated = false — a cancelled search must not claim it saw the whole tree")
	}
	if res.FilesSearched >= 400 {
		t.Fatalf("filesSearched = %d, want well under 400 — cancellation must actually stop the scan", res.FilesSearched)
	}
}

func TestSearchWorkspace_NonGitWorkspaceStillSearches(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, dir, "notes/todo.txt", "the needle is here\n")
	res := searchWorkspaceTree(context.Background(), dir, SearchQuery{Query: "needle"})
	if got, want := paths(res), []string{"notes/todo.txt"}; !samePaths(got, want) {
		t.Fatalf("paths = %v, want %v — the walk fallback must serve a non-repo workspace", got, want)
	}
}

func TestSearchWorkspace_UnknownSessionIsNotFound(t *testing.T) {
	svc := serviceForRepo(t, searchRepo(t))
	if _, err := svc.SearchWorkspace(context.Background(), "nope", SearchQuery{Query: "x"}); err == nil {
		t.Fatalf("err = nil, want NotFound")
	}
}

func TestSearchWorkspace_MissingWorktreeDegradesLikeChanges(t *testing.T) {
	fake := newFakeStore()
	fake.putSessionWithWorkspace("s1", "/no/such/worktree")
	svc := newServiceWithStore(t, fake)
	res, err := svc.SearchWorkspace(context.Background(), "s1", SearchQuery{Query: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Available || res.Reason != ChangesNoWorkspace {
		t.Fatalf("available = %v, reason = %q, want false / %q", res.Available, res.Reason, ChangesNoWorkspace)
	}
}

func TestSearchWorkspace_DeadlineIsBounded(t *testing.T) {
	// Not a timing assertion — just that the deadline is short enough to be a
	// guard rather than a hang, and long enough to cover the measured 123 ms
	// full-tree search by two orders of magnitude.
	if searchDeadline < time.Second || searchDeadline > time.Minute {
		t.Fatalf("searchDeadline = %v, want between 1s and 1m", searchDeadline)
	}
}

package wiki

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/wikisettings"
)

// taskVault builds a vault with the given files and a service pointed at it.
func taskVault(t *testing.T, cfg wikisettings.TaskSettings, files map[string]string) (*Service, string, *fakeSettings) {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	set := &fakeSettings{vault: dir, tasks: cfg}
	return New(Deps{Settings: set}), dir, set
}

// --- the scan -------------------------------------------------------------

func TestListTasks_NoFolderConfigured_ScansNothing(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{}, map[string]string{
		"Areas/a.md": "- [ ] never read\n",
	})
	got, err := svc.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Configured {
		t.Fatal("Configured = true with no folder set")
	}
	if len(got.Rows) != 0 || got.ScannedNotes != 0 {
		t.Fatalf("unconfigured scan read something: %d rows, %d notes", len(got.Rows), got.ScannedNotes)
	}
}

func TestListTasks_ReadsOnlyTheConfiguredSubtree(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"Areas"}}, map[string]string{
		"Areas/a.md":    "- [ ] inside\n",
		"Projects/b.md": "- [ ] outside\n",
		"c.md":          "- [ ] root\n",
	})
	got, err := svc.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Rows[0].Text != "inside" {
		t.Fatalf("rows = %+v, want only the subtree's row", got.Rows)
	}
}

func TestListTasks_MissingFolderIsRefusedNotWidened(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"Nope"}}, map[string]string{
		"Areas/a.md": "- [ ] must not appear\n",
	})
	_, err := svc.ListTasks(context.Background())
	if got := codeOf(t, err); got != "WIKI_TASKS_FOLDER_MISSING" {
		t.Fatalf("code = %s, want WIKI_TASKS_FOLDER_MISSING", got)
	}
}

// A folder setting must never be usable to read outside the vault.
func TestListTasks_FolderCannotEscapeTheVault(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"../.."}}, map[string]string{
		"Areas/a.md": "- [ ] x\n",
	})
	if _, err := svc.ListTasks(context.Background()); err == nil {
		t.Fatal("an escaping folder was accepted")
	}
}

func TestListTasks_SkipsCheckedRows(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{
		"A/a.md": "- [x] done\n- [X] also done\n- [ ] open\n",
	})
	got, _ := svc.ListTasks(context.Background())
	if len(got.Rows) != 1 || got.Rows[0].Text != "open" {
		t.Fatalf("rows = %+v, want only the open row", got.Rows)
	}
}

func TestListTasks_TracksSectionAndSubsection(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{
		"A/a.md": "# Title\n\n## Alpha\n\n### Sprint 1\n\n- [ ] one\n\n## Beta\n\n- [ ] two\n",
	})
	got, _ := svc.ListTasks(context.Background())
	if len(got.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(got.Rows))
	}
	if got.Rows[0].Section != "Alpha" || got.Rows[0].Subsection != "Sprint 1" {
		t.Fatalf("row 0 headings = %q/%q", got.Rows[0].Section, got.Rows[0].Subsection)
	}
	// A new "## " clears the "### " under the old one.
	if got.Rows[1].Section != "Beta" || got.Rows[1].Subsection != "" {
		t.Fatalf("row 1 headings = %q/%q", got.Rows[1].Section, got.Rows[1].Subsection)
	}
}

func TestListTasks_SectionFilterIsCaseInsensitiveAndExcludes(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}, Sections: []string{"mine"}}, map[string]string{
		"A/a.md": "## Mine\n- [ ] keep\n\n## Theirs\n- [ ] drop\n",
	})
	got, _ := svc.ListTasks(context.Background())
	if len(got.Rows) != 1 || got.Rows[0].Text != "keep" {
		t.Fatalf("rows = %+v, want only the filtered section", got.Rows)
	}
}

func TestListTasks_ParsesOwnerDueAndStripsThemFromText(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{
		"A/a.md": "- [ ] [@Some One] send the doc due:2026-05-09\n" +
			"- [ ] @bare chase it\n" +
			"- [ ] unowned row\n" +
			"- [ ] email someone@example.com about it\n",
	})
	got, _ := svc.ListTasks(context.Background())
	if len(got.Rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(got.Rows))
	}
	if got.Rows[0].Owner != "Some One" || got.Rows[0].Due != "2026-05-09" || got.Rows[0].Text != "send the doc" {
		t.Fatalf("row 0 = %+v", got.Rows[0])
	}
	if got.Rows[1].Owner != "bare" || got.Rows[1].Text != "chase it" {
		t.Fatalf("row 1 = %+v", got.Rows[1])
	}
	if got.Rows[2].Owner != "" {
		t.Fatalf("row 2 owner = %q, want unowned", got.Rows[2].Owner)
	}
	// An "@" mid-sentence is prose, not an assignment.
	if got.Rows[3].Owner != "" {
		t.Fatalf("row 3 owner = %q, want unowned", got.Rows[3].Owner)
	}
	if len(got.Owners) != 2 || got.Owners[0] != "Some One" || got.Owners[1] != "bare" {
		t.Fatalf("Owners = %v", got.Owners)
	}
}

// A row's own dates: the `created:` field and the date inside its "(from: …)"
// provenance tag. Both describe the ROW, which is the whole reason the note's
// mtime plays no part — see the resolution order in the renderer's wiki-tasks.
func TestListTasks_ReadsTheRowsOwnDates(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{
		"A/a.md": "- [ ] confirm the timeline (from: 2026-05-07 standup)\n" +
			"- [ ] review the proposal (from: chat 2026-05-07, Mobility HQ) due:2026-05-09\n" +
			"- [ ] a new row created:2026-08-20 (from: 2026-05-07 standup)\n" +
			"- [ ] no provenance at all\n" +
			"- [ ] the tag names no date (from: My active items)\n" +
			"- [ ] shipped on 2026-05-07 to production\n" +
			"- [ ] two tags (from: My active items) (from: 2026-06-01 review)\n" +
			"- [ ] an impossible day created:2026-02-30 (from: 2026-13-45 nowhere)\n",
	})
	got, _ := svc.ListTasks(context.Background())
	want := []struct {
		created, from, text string
	}{
		{"", "2026-05-07", "confirm the timeline (from: 2026-05-07 standup)"},
		{"", "2026-05-07", "review the proposal (from: chat 2026-05-07, Mobility HQ)"},
		{"2026-08-20", "2026-05-07", "a new row (from: 2026-05-07 standup)"},
		{"", "", "no provenance at all"},
		{"", "", "the tag names no date (from: My active items)"},
		// A date in the task's own sentence is a date the task TALKS about. The
		// row is only ever dated from inside a from-tag.
		{"", "", "shipped on 2026-05-07 to production"},
		{"", "2026-06-01", "two tags (from: My active items) (from: 2026-06-01 review)"},
		// Shaped like a date, and not one. An absent field, not an error.
		{"", "", "an impossible day (from: 2026-13-45 nowhere)"},
	}
	if len(got.Rows) != len(want) {
		t.Fatalf("rows = %d, want %d", len(got.Rows), len(want))
	}
	for i, w := range want {
		row := got.Rows[i]
		if row.Created != w.created || row.FromDate != w.from || row.Text != w.text {
			t.Errorf("row %d = {created:%q from:%q text:%q}, want {created:%q from:%q text:%q}",
				i, row.Created, row.FromDate, row.Text, w.created, w.from, w.text)
		}
	}
}

// The raw line is the row's identity, so it must survive parsing untouched.
func TestListTasks_RawIsTheLineVerbatim(t *testing.T) {
	line := "  - [ ] [@Someone] indented row due:2026-01-02  (from: somewhere)"
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{
		"A/a.md": line + "\n",
	})
	got, _ := svc.ListTasks(context.Background())
	if got.Rows[0].Raw != line {
		t.Fatalf("Raw = %q, want the line verbatim", got.Rows[0].Raw)
	}
}

func TestListTasks_IgnoresCheckboxesInsideCodeFences(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{
		"A/a.md": "- [ ] real\n\n```md\n- [ ] a sample in a fence\n```\n\n- [ ] also real\n",
	})
	got, _ := svc.ListTasks(context.Background())
	if len(got.Rows) != 2 {
		t.Fatalf("rows = %d (%+v), want 2 — the fenced sample is not a task", len(got.Rows), got.Rows)
	}
}

func TestListTasks_LineNumbersAre1Based(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{
		"A/a.md": "# Title\n\n- [ ] third line\n",
	})
	got, _ := svc.ListTasks(context.Background())
	if got.Rows[0].Line != 3 {
		t.Fatalf("Line = %d, want 3", got.Rows[0].Line)
	}
}

func TestListTasks_SkipsNonMarkdownAndDotDirs(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{
		"A/a.md":            "- [ ] kept\n",
		"A/notes.txt":       "- [ ] not markdown\n",
		"A/.obsidian/x.md":  "- [ ] hidden dir\n",
		"A/sub/deep/why.md": "- [ ] nested is fine\n",
	})
	got, _ := svc.ListTasks(context.Background())
	if len(got.Rows) != 2 {
		t.Fatalf("rows = %+v, want the two markdown rows", got.Rows)
	}
}

// --- ticking --------------------------------------------------------------

func TestCompleteTask_TicksExactlyOneLineAndNothingElse(t *testing.T) {
	before := "# Title\n\n## Mine\n\n- [ ] first\n- [ ] second\n- [x] third\n\ntrailing prose\n"
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": before})
	res, err := svc.CompleteTask(context.Background(), CompleteTaskInput{Path: "A/a.md", Line: 6, Raw: "- [ ] second"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved {
		t.Fatal("Moved = true for a row that had not moved")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	want := strings.Replace(before, "- [ ] second", "- [x] second", 1)
	if string(after) != want {
		t.Fatalf("note after tick =\n%q\nwant\n%q", after, want)
	}
}

func TestCompleteTask_MovedRowIsTickedAndReported(t *testing.T) {
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{
		// The row the caller names line 1 for now sits on line 3.
		"A/a.md": "- [ ] new row\n- [ ] another new row\n- [ ] the one we asked for\n",
	})
	res, err := svc.CompleteTask(context.Background(), CompleteTaskInput{
		Path: "A/a.md", Line: 1, Raw: "- [ ] the one we asked for",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Moved || res.Line != 3 {
		t.Fatalf("res = %+v, want moved to line 3", res)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if want := "- [ ] new row\n- [ ] another new row\n- [x] the one we asked for\n"; string(after) != want {
		t.Fatalf("note = %q, want %q", after, want)
	}
}

// 🗝 The failure mode this whole design exists to prevent: a stale row must
// NEVER tick whatever now sits at its old line number.
func TestCompleteTask_StaleRowFailsClosed(t *testing.T) {
	before := "- [ ] somebody else's row\n"
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": before})
	_, err := svc.CompleteTask(context.Background(), CompleteTaskInput{
		Path: "A/a.md", Line: 1, Raw: "- [ ] the row I was actually shown",
	})
	if got := codeOf(t, err); got != "WIKI_TASK_NOT_FOUND" {
		t.Fatalf("code = %s, want WIKI_TASK_NOT_FOUND", got)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if string(after) != before {
		t.Fatalf("a refused tick still wrote: %q", after)
	}
}

func TestCompleteTask_TwoIdenticalRowsIsRefused(t *testing.T) {
	before := "- [ ] duplicate\n- [ ] duplicate\n"
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": before})
	// Line 5 does not exist, so the exact-line branch cannot settle it and the
	// whole-note search finds two candidates.
	_, err := svc.CompleteTask(context.Background(), CompleteTaskInput{Path: "A/a.md", Line: 5, Raw: "- [ ] duplicate"})
	if got := codeOf(t, err); got != "WIKI_TASK_AMBIGUOUS" {
		t.Fatalf("code = %s, want WIKI_TASK_AMBIGUOUS", got)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if string(after) != before {
		t.Fatalf("an ambiguous tick still wrote: %q", after)
	}
}

// Two identical rows are still tickable when the named line is one of them:
// the caller pointed at a specific line and its text still matches.
func TestCompleteTask_DuplicateRowsAreTickableByExactLine(t *testing.T) {
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{
		"A/a.md": "- [ ] duplicate\n- [ ] duplicate\n",
	})
	if _, err := svc.CompleteTask(context.Background(), CompleteTaskInput{Path: "A/a.md", Line: 2, Raw: "- [ ] duplicate"}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if want := "- [ ] duplicate\n- [x] duplicate\n"; string(after) != want {
		t.Fatalf("note = %q, want %q", after, want)
	}
}

func TestCompleteTask_AlreadyTickedIsSaidPlainly(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{
		"A/a.md": "- [x] the row\n",
	})
	_, err := svc.CompleteTask(context.Background(), CompleteTaskInput{Path: "A/a.md", Line: 1, Raw: "- [ ] the row"})
	if got := codeOf(t, err); got != "WIKI_TASK_ALREADY_DONE" {
		t.Fatalf("code = %s, want WIKI_TASK_ALREADY_DONE", got)
	}
}

func TestCompleteTask_EmptyRawIsRefused(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": "- [ ] x\n"})
	_, err := svc.CompleteTask(context.Background(), CompleteTaskInput{Path: "A/a.md", Line: 1})
	if got := codeOf(t, err); got != "WIKI_TASK_RAW_REQUIRED" {
		t.Fatalf("code = %s, want WIKI_TASK_RAW_REQUIRED", got)
	}
}

func TestCompleteTask_PreservesIndentationMarkerAndTrailingText(t *testing.T) {
	line := "   * [ ] [@Someone] a row due:2026-01-02  (from: a meeting)"
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{
		"A/a.md": "intro\n" + line + "\noutro\n",
	})
	if _, err := svc.CompleteTask(context.Background(), CompleteTaskInput{Path: "A/a.md", Line: 2, Raw: line}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	want := "intro\n" + strings.Replace(line, "[ ]", "[x]", 1) + "\noutro\n"
	if string(after) != want {
		t.Fatalf("note = %q, want %q", after, want)
	}
}

// CRLF notes must round-trip unchanged apart from the one box.
func TestCompleteTask_PreservesCRLFEndings(t *testing.T) {
	before := "intro\r\n- [ ] a row\r\noutro\r\n"
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": before})
	got, err := svc.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows[0].Raw != "- [ ] a row" {
		t.Fatalf("Raw = %q, want the \\r stripped", got.Rows[0].Raw)
	}
	if _, err := svc.CompleteTask(context.Background(), CompleteTaskInput{
		Path: got.Rows[0].Path, Line: got.Rows[0].Line, Raw: got.Rows[0].Raw,
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if want := "intro\r\n- [x] a row\r\noutro\r\n"; string(after) != want {
		t.Fatalf("note = %q, want %q", after, want)
	}
}

// A tick is addressed by note path, and that path may not escape the vault.
func TestCompleteTask_PathCannotEscapeTheVault(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": "- [ ] x\n"})
	if _, err := svc.CompleteTask(context.Background(), CompleteTaskInput{
		Path: "../../etc/hosts", Line: 1, Raw: "- [ ] x",
	}); err == nil {
		t.Fatal("an escaping path was accepted")
	}
}

// The tick and the list agree: a row read out of ListTasks ticks with the
// values ListTasks gave, with no reinterpretation in between.
func TestListThenComplete_RoundTrips(t *testing.T) {
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}, Sections: []string{"Mine"}}, map[string]string{
		"A/a.md": "## Mine\n\n- [ ] [@Me] write it up due:2026-03-04\n- [ ] second\n",
	})
	listed, err := svc.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Rows) != 2 {
		t.Fatalf("rows = %d", len(listed.Rows))
	}
	row := listed.Rows[0]
	if _, err := svc.CompleteTask(context.Background(), CompleteTaskInput{Path: row.Path, Line: row.Line, Raw: row.Raw}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if want := "## Mine\n\n- [x] [@Me] write it up due:2026-03-04\n- [ ] second\n"; string(after) != want {
		t.Fatalf("note = %q, want %q", after, want)
	}
	again, _ := svc.ListTasks(context.Background())
	if len(again.Rows) != 1 || again.Rows[0].Text != "second" {
		t.Fatalf("re-read = %+v, want only the untouched row", again.Rows)
	}
}

func TestTaskID_ChangesWithEveryPartOfTheAddress(t *testing.T) {
	base := TaskID("a.md", 1, "- [ ] x")
	if TaskID("b.md", 1, "- [ ] x") == base {
		t.Fatal("id ignores the path")
	}
	if TaskID("a.md", 2, "- [ ] x") == base {
		t.Fatal("id ignores the line")
	}
	if TaskID("a.md", 1, "- [ ] y") == base {
		t.Fatal("id ignores the text")
	}
}

// A row's Path must be VAULT-relative, not subtree-relative and not absolute —
// the renderer opens the note with it, and the tick addresses the note with it.
//
// Regression guard: the vault here lives under a symlinked temp dir
// (/var -> /private/var on macOS), and deriving the path from the resolved
// walk root against the unresolved vault produced a path full of "..".
func TestListTasks_PathIsVaultRelative(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"Areas/work"}}, map[string]string{
		"Areas/work/deep/a.md": "- [ ] a row\n",
	})
	got, err := svc.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows = %d", len(got.Rows))
	}
	if got.Rows[0].Path != "Areas/work/deep/a.md" {
		t.Fatalf("Path = %q, want Areas/work/deep/a.md", got.Rows[0].Path)
	}
	// And it must be readable back by that exact path.
	if _, err := svc.ReadNote(context.Background(), got.Rows[0].Path); err != nil {
		t.Fatalf("the listed path does not read back: %v", err)
	}
}

// A folder spelled with slashes at either end is the same folder.
func TestListTasks_FolderSpellingIsForgiving(t *testing.T) {
	for _, spelling := range []string{"Areas", "Areas/", "/Areas", "./Areas"} {
		svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{spelling}}, map[string]string{
			"Areas/a.md": "- [ ] row\n",
		})
		got, err := svc.ListTasks(context.Background())
		if err != nil {
			t.Fatalf("folder %q: %v", spelling, err)
		}
		if len(got.Rows) != 1 || got.Rows[0].Path != "Areas/a.md" {
			t.Fatalf("folder %q gave %+v", spelling, got.Rows)
		}
	}
}

// A vault that separates ongoing areas from delivered projects keeps live rows
// in both, with the folders it wants left alone sitting alongside them.
func TestListTasks_ReadsEverySubtreeAndNothingElse(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"Areas", "Projects"}}, map[string]string{
		"Areas/a.md":    "- [ ] from areas\n",
		"Projects/b.md": "- [ ] from projects\n",
		"Archives/c.md": "- [ ] archived, not wanted\n",
		"raw/d.md":      "- [ ] a raw capture, not a task\n",
	})
	got, err := svc.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 {
		t.Fatalf("rows = %+v, want the two configured subtrees", got.Rows)
	}
	// Sorted by path across the folders, not grouped by configuration order.
	if got.Rows[0].Path != "Areas/a.md" || got.Rows[1].Path != "Projects/b.md" {
		t.Fatalf("paths = %q, %q", got.Rows[0].Path, got.Rows[1].Path)
	}
	if got.ScannedNotes != 2 {
		t.Fatalf("ScannedNotes = %d, want 2", got.ScannedNotes)
	}
}

// One bad subtree fails the whole read rather than quietly returning the rest:
// a half-empty list that looks complete is worse than an error that names the
// folder to fix.
func TestListTasks_OneMissingSubtreeIsRefused(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"Areas", "Gone"}}, map[string]string{
		"Areas/a.md": "- [ ] from areas\n",
	})
	_, err := svc.ListTasks(context.Background())
	if got := codeOf(t, err); got != "WIKI_TASKS_FOLDER_MISSING" {
		t.Fatalf("code = %s, want WIKI_TASKS_FOLDER_MISSING", got)
	}
}

// A subtree listed twice, however it is spelled, is read once.
func TestListTasks_DuplicateSubtreesAreReadOnce(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"Areas", "/Areas/", "Areas"}}, map[string]string{
		"Areas/a.md": "- [ ] only once\n",
	})
	got, err := svc.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows = %+v, want one", got.Rows)
	}
}

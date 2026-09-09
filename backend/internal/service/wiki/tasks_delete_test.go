package wiki

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/wikisettings"
)

// Every test here asserts on the WHOLE FILE, not on a row count. A delete that
// removed the right line and also normalised a line ending, dropped a trailing
// newline or reflowed a blank line would pass any count-based assertion and
// would still have damaged somebody's notes.

func TestDeleteTask_RemovesExactlyOneLineFromTheMiddle(t *testing.T) {
	before := "# Title\n\n## Mine\n\n- [ ] first\n- [ ] second\n- [x] third\n\ntrailing prose\n"
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": before})
	res, err := svc.DeleteTask(context.Background(), DeleteTaskInput{Path: "A/a.md", Line: 6, Raw: "- [ ] second"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved || res.Line != 6 || res.Raw != "- [ ] second" {
		t.Fatalf("res = %+v", res)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	want := strings.Replace(before, "- [ ] second\n", "", 1)
	if string(after) != want {
		t.Fatalf("note after delete =\n%q\nwant\n%q", after, want)
	}
	if got, wantN := strings.Count(string(after), "\n"), strings.Count(before, "\n")-1; got != wantN {
		t.Fatalf("newline count = %d, want %d", got, wantN)
	}
}

// The first row under a heading, which is the one a "delete the line and the
// blank line with it" implementation damages.
func TestDeleteTask_FirstRowOfASectionLeavesTheHeadingAlone(t *testing.T) {
	before := "## Mine\n\n- [ ] first\n- [ ] second\n\n## Others\n\n- [ ] theirs\n"
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": before})
	if _, err := svc.DeleteTask(context.Background(), DeleteTaskInput{Path: "A/a.md", Line: 3, Raw: "- [ ] first"}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if want := "## Mine\n\n- [ ] second\n\n## Others\n\n- [ ] theirs\n"; string(after) != want {
		t.Fatalf("note = %q, want %q", after, want)
	}
}

// The last line of a note that ends WITH a newline: the newline stays.
func TestDeleteTask_LastLineKeepsTheNotesTrailingNewline(t *testing.T) {
	before := "## Mine\n\n- [ ] first\n- [ ] last\n"
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": before})
	if _, err := svc.DeleteTask(context.Background(), DeleteTaskInput{Path: "A/a.md", Line: 4, Raw: "- [ ] last"}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if want := "## Mine\n\n- [ ] first\n"; string(after) != want {
		t.Fatalf("note = %q, want %q", after, want)
	}
}

// ...and the last line of a note that ends WITHOUT one: none is invented. The
// note had no final newline before and has none after, which is the point — the
// line that leaves takes the separator in front of it, not a byte belonging to
// the note's own shape.
func TestDeleteTask_LastLineOfANoteWithNoTrailingNewline(t *testing.T) {
	before := "## Mine\n\n- [ ] first\n- [ ] last"
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": before})
	if _, err := svc.DeleteTask(context.Background(), DeleteTaskInput{Path: "A/a.md", Line: 4, Raw: "- [ ] last"}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if want := "## Mine\n\n- [ ] first"; string(after) != want {
		t.Fatalf("note = %q, want %q", after, want)
	}
}

func TestDeleteTask_PreservesCRLFEndings(t *testing.T) {
	before := "intro\r\n- [ ] a row\r\noutro\r\n"
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": before})
	got, err := svc.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeleteTask(context.Background(), DeleteTaskInput{
		Path: got.Rows[0].Path, Line: got.Rows[0].Line, Raw: got.Rows[0].Raw,
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if want := "intro\r\noutro\r\n"; string(after) != want {
		t.Fatalf("note = %q, want %q", after, want)
	}
}

// Indentation, an unusual list marker and trailing whitespace all belong to the
// line, and the line goes whole. Its neighbours keep theirs.
func TestDeleteTask_TakesTheWholeLineAndNothingAdjacent(t *testing.T) {
	before := "- [ ] above  \n\t*   [ ] [@Sun] ทดสอบ [[roadmap]] created:2026-09-04 due:2026-09-09  \n- [ ] below\t\n"
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": before})
	raw := "\t*   [ ] [@Sun] ทดสอบ [[roadmap]] created:2026-09-04 due:2026-09-09  "
	if _, err := svc.DeleteTask(context.Background(), DeleteTaskInput{Path: "A/a.md", Line: 2, Raw: raw}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if want := "- [ ] above  \n- [ ] below\t\n"; string(after) != want {
		t.Fatalf("note = %q, want %q", after, want)
	}
}

func TestDeleteTask_MovedRowIsDeletedAndReported(t *testing.T) {
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{
		"A/a.md": "- [ ] new row\n- [ ] another new row\n- [ ] the one we asked for\n",
	})
	res, err := svc.DeleteTask(context.Background(), DeleteTaskInput{
		Path: "A/a.md", Line: 1, Raw: "- [ ] the one we asked for",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Moved || res.Line != 3 {
		t.Fatalf("res = %+v, want moved to line 3", res)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if want := "- [ ] new row\n- [ ] another new row\n"; string(after) != want {
		t.Fatalf("note = %q, want %q", after, want)
	}
}

// 🗝 The failure this whole design exists to prevent, and the one that cannot be
// undone: a stale row must NEVER delete whatever now sits at its old line.
func TestDeleteTask_StaleRowFailsClosed(t *testing.T) {
	before := "- [ ] somebody else's row\n"
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": before})
	_, err := svc.DeleteTask(context.Background(), DeleteTaskInput{
		Path: "A/a.md", Line: 1, Raw: "- [ ] the row I was actually shown",
	})
	if got := codeOf(t, err); got != "WIKI_TASK_NOT_FOUND" {
		t.Fatalf("code = %s, want WIKI_TASK_NOT_FOUND", got)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if string(after) != before {
		t.Fatalf("a refused delete still wrote: %q", after)
	}
}

// A row somebody already removed. The refusal is the same one a reworded row
// gets, which is the truth: the note no longer holds that text, and this
// package cannot tell which of the two happened.
func TestDeleteTask_AlreadyDeletedRowIsRefusedNotAnError(t *testing.T) {
	before := "- [ ] still here\n"
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": before})
	_, err := svc.DeleteTask(context.Background(), DeleteTaskInput{Path: "A/a.md", Line: 2, Raw: "- [ ] already gone"})
	if got := codeOf(t, err); got != "WIKI_TASK_NOT_FOUND" {
		t.Fatalf("code = %s, want WIKI_TASK_NOT_FOUND", got)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if string(after) != before {
		t.Fatalf("a refused delete still wrote: %q", after)
	}
}

func TestDeleteTask_TwoIdenticalRowsIsRefused(t *testing.T) {
	before := "- [ ] duplicate\n- [ ] duplicate\n"
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": before})
	// Line 5 does not exist, so the exact-line branch cannot settle it and the
	// whole-note search finds two candidates.
	_, err := svc.DeleteTask(context.Background(), DeleteTaskInput{Path: "A/a.md", Line: 5, Raw: "- [ ] duplicate"})
	if got := codeOf(t, err); got != "WIKI_TASK_AMBIGUOUS" {
		t.Fatalf("code = %s, want WIKI_TASK_AMBIGUOUS", got)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if string(after) != before {
		t.Fatalf("an ambiguous delete still wrote: %q", after)
	}
}

func TestDeleteTask_AlreadyTickedIsSaidPlainly(t *testing.T) {
	before := "- [x] the row\n"
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": before})
	_, err := svc.DeleteTask(context.Background(), DeleteTaskInput{Path: "A/a.md", Line: 1, Raw: "- [ ] the row"})
	if got := codeOf(t, err); got != "WIKI_TASK_ALREADY_DONE" {
		t.Fatalf("code = %s, want WIKI_TASK_ALREADY_DONE", got)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if string(after) != before {
		t.Fatalf("a refused delete still wrote: %q", after)
	}
}

func TestDeleteTask_EmptyRawIsRefused(t *testing.T) {
	before := "- [ ] x\n"
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": before})
	_, err := svc.DeleteTask(context.Background(), DeleteTaskInput{Path: "A/a.md", Line: 1, Raw: "   "})
	if got := codeOf(t, err); got != "WIKI_TASK_RAW_REQUIRED" {
		t.Fatalf("code = %s, want WIKI_TASK_RAW_REQUIRED", got)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if string(after) != before {
		t.Fatalf("a refused delete still wrote: %q", after)
	}
}

// The tab lists unchecked task rows and nothing else, so an exact-text match on
// a line that is not one did not come from a row anybody was looking at. This
// endpoint takes no session, so the guard is not theoretical.
func TestDeleteTask_RefusesALineThatIsNotATaskRow(t *testing.T) {
	before := "# Title\n\nsome prose the caller would rather not have\n- [ ] a real row\n"
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": before})
	_, err := svc.DeleteTask(context.Background(), DeleteTaskInput{
		Path: "A/a.md", Line: 3, Raw: "some prose the caller would rather not have",
	})
	if got := codeOf(t, err); got != "WIKI_TASK_NOT_A_TASK" {
		t.Fatalf("code = %s, want WIKI_TASK_NOT_A_TASK", got)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if string(after) != before {
		t.Fatalf("a refused delete still wrote: %q", after)
	}
}

// A delete is addressed by note path, and that path may not escape the vault.
func TestDeleteTask_PathCannotEscapeTheVault(t *testing.T) {
	svc, _, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{"A/a.md": "- [ ] x\n"})
	if _, err := svc.DeleteTask(context.Background(), DeleteTaskInput{
		Path: "../../etc/hosts", Line: 1, Raw: "- [ ] x",
	}); err == nil {
		t.Fatal("an escaping path was accepted")
	}
}

// The delete and the list agree: a row read out of ListTasks deletes with the
// values ListTasks gave, with no reinterpretation in between — and the rows
// around it are still listed, unchanged.
func TestListThenDelete_RoundTrips(t *testing.T) {
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}, Sections: []string{"Mine"}}, map[string]string{
		"A/a.md": "## Mine\n\n- [ ] [@Me] write it up due:2026-03-04\n- [ ] second\n",
	})
	listed, err := svc.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	row := listed.Rows[0]
	if _, err := svc.DeleteTask(context.Background(), DeleteTaskInput{Path: row.Path, Line: row.Line, Raw: row.Raw}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if want := "## Mine\n\n- [ ] second\n"; string(after) != want {
		t.Fatalf("note = %q, want %q", after, want)
	}
	again, err := svc.ListTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Rows) != 1 || again.Rows[0].Raw != "- [ ] second" {
		t.Fatalf("rows after delete = %+v", again.Rows)
	}
}

// Ticking still works exactly as it did, next to a delete of another row in the
// same note. Neither write reinterprets the other's lines.
func TestDeleteThenComplete_LeaveEachOtherAlone(t *testing.T) {
	svc, dir, _ := taskVault(t, wikisettings.TaskSettings{Folders: []string{"A"}}, map[string]string{
		"A/a.md": "- [ ] drop me\n- [ ] tick me\n- [ ] leave me\n",
	})
	if _, err := svc.DeleteTask(context.Background(), DeleteTaskInput{Path: "A/a.md", Line: 1, Raw: "- [ ] drop me"}); err != nil {
		t.Fatal(err)
	}
	// The tick still names line 2, which the delete has renumbered to line 1.
	// The text is the key, so it lands anyway and says it moved.
	res, err := svc.CompleteTask(context.Background(), CompleteTaskInput{Path: "A/a.md", Line: 2, Raw: "- [ ] tick me"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Moved || res.Line != 1 {
		t.Fatalf("res = %+v, want moved to line 1", res)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "A", "a.md"))
	if want := "- [x] tick me\n- [ ] leave me\n"; string(after) != want {
		t.Fatalf("note = %q, want %q", after, want)
	}
}

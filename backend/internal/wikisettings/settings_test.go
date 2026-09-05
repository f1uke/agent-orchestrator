package wikisettings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewStore_AbsentFile_IsUnconfigured(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Get(); got.VaultPath != "" || got.Harness != "" {
		t.Fatalf("defaults = %+v, want zero", got)
	}
}

func TestSet_PersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	vault := filepath.Join(dir, "vault")
	st, _ := NewStore(dir)
	if err := st.Set(Settings{VaultPath: vault, Harness: "claude-code"}); err != nil {
		t.Fatal(err)
	}
	st2, _ := NewStore(dir)
	if got := st2.Get(); got.VaultPath != vault || got.Harness != "claude-code" {
		t.Fatalf("reloaded = %+v, want %s/claude-code", got, vault)
	}
}

func TestSet_TrimsAndExpandsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	st, _ := NewStore(t.TempDir())
	if err := st.Set(Settings{VaultPath: "  ~/Notes  "}); err != nil {
		t.Fatal(err)
	}
	if got := st.VaultPath(); got != filepath.Join(home, "Notes") {
		t.Fatalf("VaultPath() = %q, want %q", got, filepath.Join(home, "Notes"))
	}
}

func TestSetHarness_KeepsVaultPath(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	if err := st.Set(Settings{VaultPath: "/tmp/vault"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetHarness("codex"); err != nil {
		t.Fatal(err)
	}
	if got := st.Get(); got.VaultPath != "/tmp/vault" || got.Harness != "codex" {
		t.Fatalf("after SetHarness = %+v", got)
	}
}

func TestNewStore_EmptyDir_Errors(t *testing.T) {
	if _, err := NewStore(""); err == nil {
		t.Fatal("want error for empty dir")
	}
}

func TestSetTasks_KeepsVaultPathAndHarness(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	if err := st.Set(Settings{VaultPath: "/tmp/vault", Harness: "codex"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTasks(TaskSettings{Folders: []string{"Areas"}, Cutoff: "2026-01-01"}); err != nil {
		t.Fatal(err)
	}
	got := st.Get()
	if got.VaultPath != "/tmp/vault" || got.Harness != "codex" {
		t.Fatalf("SetTasks disturbed the rest: %+v", got)
	}
	if len(got.Tasks.Folders) != 1 || got.Tasks.Folders[0] != "Areas" || got.Tasks.Cutoff != "2026-01-01" {
		t.Fatalf("tasks = %+v", got.Tasks)
	}
}

func TestSetHarness_KeepsTasks(t *testing.T) {
	st, _ := NewStore(t.TempDir())
	if err := st.SetTasks(TaskSettings{Folders: []string{"Areas"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetHarness("claude-code"); err != nil {
		t.Fatal(err)
	}
	if got := st.Tasks().Folders; len(got) != 1 || got[0] != "Areas" {
		t.Fatalf("Tasks().Folders = %v after SetHarness, want [Areas]", got)
	}
}

func TestTasks_PersistAndReload(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	if err := st.SetTasks(TaskSettings{
		Folders:      []string{"Areas/work"},
		Sections:     []string{"My items"},
		Cutoff:       "2026-06-01",
		OwnerAliases: []string{"me"},
	}); err != nil {
		t.Fatal(err)
	}
	st2, _ := NewStore(dir)
	got := st2.Tasks()
	if len(got.Folders) != 1 || got.Folders[0] != "Areas/work" || got.Cutoff != "2026-06-01" {
		t.Fatalf("reloaded = %+v", got)
	}
	if len(got.Sections) != 1 || got.Sections[0] != "My items" {
		t.Fatalf("sections = %v", got.Sections)
	}
	if len(got.OwnerAliases) != 1 || got.OwnerAliases[0] != "me" {
		t.Fatalf("ownerAliases = %v", got.OwnerAliases)
	}
}

// RequireCreated hides every row that has not been tagged yet, so the one
// property worth a test of its own is that it is never ON unless somebody
// asked: a settings file written before the field existed must reload as
// false, not as "whatever the zero value happened to be this release".
func TestTasks_RequireCreated_DefaultsOffAndPersistsWhenSet(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	if err := st.SetTasks(TaskSettings{Folders: []string{"Areas"}}); err != nil {
		t.Fatal(err)
	}
	if reloaded, _ := NewStore(dir); reloaded.Tasks().RequireCreated {
		t.Fatal("a settings file that never mentioned requireCreated reloaded as true")
	}

	if err := st.SetTasks(TaskSettings{Folders: []string{"Areas"}, RequireCreated: true}); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := NewStore(dir)
	if !reloaded.Tasks().RequireCreated {
		t.Fatal("requireCreated did not survive a reload")
	}

	// And it must be turn-off-able: `omitempty` drops the key on the way out,
	// which is only correct because absent reads as false above.
	if err := st.SetTasks(TaskSettings{Folders: []string{"Areas"}, RequireCreated: false}); err != nil {
		t.Fatal(err)
	}
	if again, _ := NewStore(dir); again.Tasks().RequireCreated {
		t.Fatal("requireCreated could not be turned back off")
	}
}

func TestNormalizeTasks_TrimsFolderSlashesAndDropsBlankEntries(t *testing.T) {
	st, _ := NewStore(t.TempDir())
	if err := st.SetTasks(TaskSettings{
		Folders:      []string{"  /Areas/work/  ", "", "Areas/work"},
		Sections:     []string{" Mine ", "", "   "},
		OwnerAliases: []string{"", " me "},
	}); err != nil {
		t.Fatal(err)
	}
	got := st.Tasks()
	// Trimmed, de-duplicated, and blanks dropped: "/Areas/work/" and
	// "Areas/work" are the same folder and must not be scanned twice.
	if len(got.Folders) != 1 || got.Folders[0] != "Areas/work" {
		t.Fatalf("Folders = %#v, want [Areas/work]", got.Folders)
	}
	if len(got.Sections) != 1 || got.Sections[0] != "Mine" {
		t.Fatalf("Sections = %#v, want [Mine]", got.Sections)
	}
	if len(got.OwnerAliases) != 1 || got.OwnerAliases[0] != "me" {
		t.Fatalf("OwnerAliases = %#v, want [me]", got.OwnerAliases)
	}
}

// An all-blank list must come back nil rather than an empty slice, so the
// settings file does not accumulate "sections": [] for a filter nobody set.
func TestNormalizeTasks_AllBlankListIsNil(t *testing.T) {
	st, _ := NewStore(t.TempDir())
	if err := st.SetTasks(TaskSettings{Sections: []string{"", "  "}}); err != nil {
		t.Fatal(err)
	}
	if got := st.Tasks().Sections; got != nil {
		t.Fatalf("Sections = %#v, want nil", got)
	}
}

// A settings file written before the Tasks tab existed must still load.
func TestNewStore_FileWithoutTasks_LoadsClean(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(`{"vaultPath":"/tmp/v","harness":"codex"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := st.Get()
	if got.VaultPath != "/tmp/v" || got.Harness != "codex" {
		t.Fatalf("legacy file = %+v", got)
	}
	if got.Tasks.Folders != nil || got.Tasks.Sections != nil {
		t.Fatalf("tasks = %+v, want zero", got.Tasks)
	}
}

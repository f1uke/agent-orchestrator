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

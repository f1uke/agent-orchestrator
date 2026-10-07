package simpower

import (
	"os"
	"path/filepath"
	"testing"
)

// A machine that never touched the setting gets the cap the human chose.
func TestNewSettingsStore_AbsentFileIsACapOfFour(t *testing.T) {
	st, err := NewSettingsStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Get().MaxBooted; got != 4 {
		t.Fatalf("MaxBooted = %d, want 4", got)
	}
}

func TestNewSettingsStore_ReadsTheCapFromTheFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, settingsFileName), []byte(`{"maxBooted":6}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ := NewSettingsStore(dir)
	if got := st.Get().MaxBooted; got != 6 {
		t.Fatalf("MaxBooted = %d, want 6", got)
	}
}

// A file that would refuse every boot, or cannot be read, is not honoured: a
// machine where no simulator can come up deadlocks qa.
func TestNewSettingsStore_AnInvalidOrCorruptFileFallsBackToTheDefault(t *testing.T) {
	for name, body := range map[string]string{
		"corrupt": `{"maxBooted":`,
		"zero":    `{"maxBooted":0}`,
		"absent":  `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, settingsFileName), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			st, _ := NewSettingsStore(dir)
			if got := st.Get().MaxBooted; got != DefaultMaxBooted {
				t.Fatalf("MaxBooted = %d, want the default %d", got, DefaultMaxBooted)
			}
		})
	}
}

func TestSettingsStore_SetPersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewSettingsStore(dir)
	if err := st.Set(Settings{MaxBooted: 2}); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := NewSettingsStore(dir)
	if got := reloaded.Get().MaxBooted; got != 2 {
		t.Fatalf("reloaded MaxBooted = %d, want 2", got)
	}
}

func TestSettingsStore_SetRefusesACapBelowOne(t *testing.T) {
	st, _ := NewSettingsStore(t.TempDir())
	if err := st.Set(Settings{MaxBooted: 0}); err == nil {
		t.Fatal("a cap of 0 was accepted; no simulator could ever boot")
	}
	if got := st.Get().MaxBooted; got != DefaultMaxBooted {
		t.Fatalf("MaxBooted = %d after a refused Set, want it unchanged", got)
	}
}

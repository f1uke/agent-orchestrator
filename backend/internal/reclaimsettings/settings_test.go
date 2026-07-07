package reclaimsettings

import (
	"path/filepath"
	"testing"
)

func TestNewStore_AbsentFile_ReturnsDefaults(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Get(); !got.Enabled || got.GraceMinutes != 15 {
		t.Fatalf("defaults = %+v, want {true 15}", got)
	}
}

func TestSet_PersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	if err := st.Set(Settings{Enabled: false, GraceMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	if got := st.Get(); got.Enabled || got.GraceMinutes != 30 {
		t.Fatalf("in-memory = %+v", got)
	}
	// A fresh store over the same dir reloads the persisted value.
	st2, _ := NewStore(dir)
	if got := st2.Get(); got.Enabled || got.GraceMinutes != 30 {
		t.Fatalf("reloaded = %+v, want {false 30}", got)
	}
	_ = filepath.Join(dir, "reclaim-settings.json")
}

func TestSet_NegativeGrace_Rejected(t *testing.T) {
	st, _ := NewStore(t.TempDir())
	if err := st.Set(Settings{Enabled: true, GraceMinutes: -1}); err == nil {
		t.Fatal("want error for negative grace")
	}
}

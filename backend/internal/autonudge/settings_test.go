package autonudge

import "testing"

func TestNewStore_AbsentFile_DefaultsToDisabled(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Get(); got.Enabled {
		t.Fatalf("defaults = %+v, want {Enabled:false}", got)
	}
}

func TestSet_PersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	if err := st.Set(Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if got := st.Get(); !got.Enabled {
		t.Fatalf("in-memory = %+v, want enabled", got)
	}
	// A fresh store over the same dir reloads the persisted value.
	st2, _ := NewStore(dir)
	if got := st2.Get(); !got.Enabled {
		t.Fatalf("reloaded = %+v, want enabled", got)
	}
}

func TestNewStore_EmptyDir_Errors(t *testing.T) {
	if _, err := NewStore(""); err == nil {
		t.Fatal("want error for empty dir")
	}
}

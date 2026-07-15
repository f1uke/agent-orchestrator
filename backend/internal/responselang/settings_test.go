package responselang

import "testing"

func TestNewStore_AbsentFile_DefaultsToEnglish(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Get(); got.Language != "English" {
		t.Fatalf("defaults = %+v, want {Language:English}", got)
	}
}

func TestSet_PersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	if err := st.Set(Settings{Language: "Thai"}); err != nil {
		t.Fatal(err)
	}
	if got := st.Get(); got.Language != "Thai" {
		t.Fatalf("in-memory = %+v, want Thai", got)
	}
	// A fresh store over the same dir reloads the persisted value.
	st2, _ := NewStore(dir)
	if got := st2.Get(); got.Language != "Thai" {
		t.Fatalf("reloaded = %+v, want Thai", got)
	}
}

func TestSet_TrimsWhitespace(t *testing.T) {
	st, _ := NewStore(t.TempDir())
	if err := st.Set(Settings{Language: "  Japanese  "}); err != nil {
		t.Fatal(err)
	}
	if got := st.Language(); got != "Japanese" {
		t.Fatalf("Language() = %q, want %q", got, "Japanese")
	}
}

func TestNewStore_EmptyDir_Errors(t *testing.T) {
	if _, err := NewStore(""); err == nil {
		t.Fatal("want error for empty dir")
	}
}

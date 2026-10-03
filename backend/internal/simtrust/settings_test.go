package simtrust

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// A machine that never touched the setting trusts the known proxy CAs.
func TestNewStore_AbsentFileIsTheDefault(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := st.CAFiles(); !reflect.DeepEqual(got, DefaultCAFiles) {
		t.Fatalf("CAFiles = %v, want the default %v", got, DefaultCAFiles)
	}
}

// An explicit empty list is "trust nothing", and must survive a restart rather
// than decoding back into the default.
func TestNewStore_AnExplicitEmptyListIsHonoured(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(`{"caFiles":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ := NewStore(dir)
	if got := st.CAFiles(); len(got) != 0 {
		t.Fatalf("CAFiles = %v, want none", got)
	}
}

func TestNewStore_ACorruptOrInvalidFileFallsBackToTheDefault(t *testing.T) {
	for name, body := range map[string]string{
		"corrupt":  `{"caFiles":`,
		"relative": `{"caFiles":["ca.pem"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, fileName), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			st, _ := NewStore(dir)
			if got := st.CAFiles(); !reflect.DeepEqual(got, DefaultCAFiles) {
				t.Fatalf("CAFiles = %v, want the default", got)
			}
		})
	}
}

func TestSet_PersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	want := []string{"~/charles-ca.pem", "/opt/mitm/ca.pem"}
	if err := st.Set(Settings{CAFiles: want}); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := NewStore(dir)
	if got := reloaded.CAFiles(); !reflect.DeepEqual(got, want) {
		t.Fatalf("reloaded = %v, want %v", got, want)
	}
}

// A relative path would resolve against wherever the daemon runs, which is a
// CA that is quietly never found.
func TestSet_RejectsAPathThatCannotNameAFileOnThisMac(t *testing.T) {
	st, _ := NewStore(t.TempDir())
	for _, bad := range []string{"ca.pem", "", " /ca.pem"} {
		if err := st.Set(Settings{CAFiles: []string{bad}}); err == nil {
			t.Errorf("Set(%q) succeeded, want a validation error", bad)
		}
	}
	if got := st.CAFiles(); !reflect.DeepEqual(got, DefaultCAFiles) {
		t.Fatalf("a refused Set changed the setting to %v", got)
	}
}

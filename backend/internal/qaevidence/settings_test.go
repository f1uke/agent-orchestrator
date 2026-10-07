package qaevidence

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewStoreWithoutAFileIsOff(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Get(); got.DriveFolder != "" || got.Remote() != "" {
		t.Fatalf("defaults = %+v, want upload off", got)
	}
}

func TestSetNormalizesPersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	if err := st.Set(Settings{DriveFolder: "  finnomena:QA/ "}); err != nil {
		t.Fatal(err)
	}
	if got := st.Get().DriveFolder; got != "finnomena:QA" {
		t.Fatalf("in memory = %q, want finnomena:QA", got)
	}
	st2, _ := NewStore(dir)
	if got := st2.Get().DriveFolder; got != "finnomena:QA" {
		t.Fatalf("reloaded = %q, want finnomena:QA", got)
	}
	if err := st.Set(Settings{}); err != nil {
		t.Fatal(err)
	}
	if got := st.Get().DriveFolder; got != "" {
		t.Fatalf("after clearing = %q, want empty", got)
	}
}

func TestNormalizeAcceptsRclonePaths(t *testing.T) {
	for in, want := range map[string]string{
		"finnomena:QA":                 "finnomena:QA",
		"finnomena:":                   "finnomena:",
		"finnomena:/":                  "finnomena:",
		"my drive:QA/[AO-TEST]":        "my drive:QA/[AO-TEST]",
		"team.drive+2@x:Shared/QA//":   "team.drive+2@x:Shared/QA",
		"ไดรฟ:หลักฐาน":                 "ไดรฟ:หลักฐาน",
		"finnomena:QA/MOBILITY: notes": "finnomena:QA/MOBILITY: notes",
	} {
		got, err := Normalize(Settings{DriveFolder: in})
		if err != nil || got.DriveFolder != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got.DriveFolder, err, want)
		}
	}
}

func TestNormalizeRefusesWhatIsNotAnRclonePath(t *testing.T) {
	for _, in := range []string{
		"QA",
		"/Users/me/QA",
		":drive:QA",
		"-remote:QA",
		" :QA",
		"bad/remote:QA",
		"bad remote :QA",
		"finnomena:QA\nother",
	} {
		if got, err := Normalize(Settings{DriveFolder: in}); err == nil {
			t.Errorf("Normalize(%q) = %q, want an error", in, got.DriveFolder)
		}
	}
}

func TestJoinAndRemote(t *testing.T) {
	s := Settings{DriveFolder: "finnomena:QA"}
	if got := s.Join("MOBILITY/2026/TR-1 - r"); got != "finnomena:QA/MOBILITY/2026/TR-1 - r" {
		t.Errorf("Join = %q", got)
	}
	if got := (Settings{DriveFolder: "finnomena:"}).Join("MOBILITY"); got != "finnomena:MOBILITY" {
		t.Errorf("Join at the root = %q", got)
	}
	if s.Remote() != "finnomena" {
		t.Errorf("Remote = %q", s.Remote())
	}
}

func TestNewStoreIgnoresAnInvalidFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(`{"driveFolder":"no-colon"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := NewStore(dir)
	if err != nil || st.Get().DriveFolder != "" {
		t.Fatalf("NewStore = %+v, %v; want upload off", st.Get(), err)
	}
}

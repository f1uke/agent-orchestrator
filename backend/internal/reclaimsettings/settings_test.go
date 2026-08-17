package reclaimsettings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewStore_AbsentFile_ReturnsDefaults(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := st.Get()
	if !got.Enabled || got.GraceMinutes != DefaultGraceMinutes {
		t.Fatalf("defaults = %+v, want enabled with a %d-minute grace", got, DefaultGraceMinutes)
	}
	if !got.ArtifactsEnabled {
		t.Fatalf("artefact clearing should default on, got %+v", got)
	}
}

func TestNewStore_PreExistingFile_KeepsDefaultsForAbsentKeys(t *testing.T) {
	// The settings file a user already has on disk predates the artefact knobs.
	// Decoding it must not read those absent keys as "off" — that would silently
	// disable a feature the user never turned off.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName),
		[]byte(`{"enabled":true,"graceMinutes":60}`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := got.Get()
	if s.GraceMinutes != 60 {
		t.Fatalf("the file's own value must win, got %d", s.GraceMinutes)
	}
	if !s.ArtifactsEnabled {
		t.Fatal("an absent artifactsEnabled key must keep the default (on), not decode as off")
	}
	if len(s.Patterns()) == 0 {
		t.Fatal("an absent artifactPatterns key must fall back to the defaults")
	}
}

func TestNewStore_ExplicitFalseStillWins(t *testing.T) {
	// The flip side of merging onto defaults: a key the file DOES carry must
	// override, including an explicit false, or the feature could not be
	// switched off.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName),
		[]byte(`{"enabled":false,"graceMinutes":60,"artifactsEnabled":false}`), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := st.Get()
	if s.Enabled {
		t.Fatal("explicit enabled:false must switch auto-reclaim off")
	}
	if s.ArtifactsEnabled {
		t.Fatal("explicit artifactsEnabled:false must switch artefact clearing off")
	}
	if len(s.Patterns()) != 0 {
		t.Fatal("with artefact clearing off, no pattern may be reported as regenerable")
	}
}

func TestPatterns_UserOverrideReplacesDefaults(t *testing.T) {
	s := Settings{ArtifactsEnabled: true, ArtifactPatterns: []string{"only-this"}}
	got := s.Patterns()
	if len(got) != 1 || got[0] != "only-this" {
		t.Fatalf("user patterns must replace the defaults, got %v", got)
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
}

func TestSet_NegativeGrace_Rejected(t *testing.T) {
	st, _ := NewStore(t.TempDir())
	if err := st.Set(Settings{Enabled: true, GraceMinutes: -1}); err == nil {
		t.Fatal("want error for negative grace")
	}
}

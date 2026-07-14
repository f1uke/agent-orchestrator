package evidenceretention

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	d := Default()
	if !d.Enabled || d.MaxAgeDays != DefaultRetentionDays {
		t.Fatalf("Default() = %+v, want {true, %d}", d, DefaultRetentionDays)
	}
}

func TestCutoff(t *testing.T) {
	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		s       Settings
		wantOK  bool
		wantAgo time.Duration // expected now-cutoff when ok
	}{
		{"disabled keeps forever", Settings{Enabled: false, MaxAgeDays: 30}, false, 0},
		{"zero days keeps forever", Settings{Enabled: true, MaxAgeDays: 0}, false, 0},
		{"negative keeps forever", Settings{Enabled: true, MaxAgeDays: -5}, false, 0},
		{"30 days", Settings{Enabled: true, MaxAgeDays: 30}, true, 30 * 24 * time.Hour},
		{"tiny TTL clamps up to floor", Settings{Enabled: true, MaxAgeDays: 1}, true, 1 * 24 * time.Hour},
		{"huge TTL clamps to ceiling", Settings{Enabled: true, MaxAgeDays: 999999}, true, MaxRetentionDays * 24 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cutoff, ok := tc.s.Cutoff(now)
			if ok != tc.wantOK {
				t.Fatalf("Cutoff ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if got := now.Sub(cutoff); got != tc.wantAgo {
				t.Fatalf("Cutoff age = %v, want %v", got, tc.wantAgo)
			}
		})
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Get(); got != Default() {
		t.Fatalf("fresh store = %+v, want Default %+v", got, Default())
	}
	next := Settings{Enabled: false, MaxAgeDays: 7}
	if err := st.Set(next); err != nil {
		t.Fatal(err)
	}
	if got := st.Get(); got != next {
		t.Fatalf("after Set Get = %+v, want %+v", got, next)
	}
	// Reload from disk picks up the persisted value.
	reloaded, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Get(); got != next {
		t.Fatalf("reloaded = %+v, want %+v", got, next)
	}
	if _, err := os.Stat(filepath.Join(dir, fileName)); err != nil {
		t.Fatalf("settings file not written: %v", err)
	}
}

func TestSetRejectsInvalid(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Set(Settings{Enabled: true, MaxAgeDays: -1}); err == nil {
		t.Fatal("expected error for negative MaxAgeDays")
	}
	if err := st.Set(Settings{Enabled: true, MaxAgeDays: MaxRetentionDays + 1}); err == nil {
		t.Fatal("expected error for MaxAgeDays over the ceiling")
	}
	// A rejected Set must not mutate the stored value.
	if got := st.Get(); got != Default() {
		t.Fatalf("store mutated after rejected Set: %+v", got)
	}
}

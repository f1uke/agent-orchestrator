package learnsettings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStore_DefaultsPersistAndRefusesBadValues(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Get(); got != Default() || got.CollectModel != "claude-sonnet-5-5" || got.CollectEffort != "low" {
		t.Fatalf("defaults = %+v", got)
	}
	next := Settings{CollectModel: "claude-opus-5-5", CollectEffort: "medium", DailyBudgetUSD: 5}
	if err := s.Set(next); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := NewStore(dir)
	if reloaded.Get() != next {
		t.Errorf("reloaded = %+v", reloaded.Get())
	}
	for _, bad := range []Settings{
		{CollectModel: "", CollectEffort: "low", DailyBudgetUSD: 1},
		{CollectModel: "m", CollectEffort: "turbo", DailyBudgetUSD: 1},
		{CollectModel: "m", CollectEffort: "low", DailyBudgetUSD: -1},
		{CollectModel: "m", CollectEffort: "low", DailyBudgetUSD: 1000},
	} {
		if s.Set(bad) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(`{"collectEffort":"turbo"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if broken, _ := NewStore(dir); broken.Get() != Default() {
		t.Errorf("an invalid file must fall back to defaults, got %+v", broken.Get())
	}
}

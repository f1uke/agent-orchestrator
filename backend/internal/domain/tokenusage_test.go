package domain

import "testing"

func TestTokenUsageRawTotal(t *testing.T) {
	u := TokenUsage{Input: 82010, CacheCreation: 2525549, CacheRead: 152740511, Output: 998731, Turns: 602}
	if got, want := u.RawTotal(), int64(156346801); got != want {
		t.Fatalf("RawTotal() = %d, want %d", got, want)
	}
}

func TestTokenUsageCostWeighted(t *testing.T) {
	// input×1 + cache_creation×1.25 + cache_read×0.1 + output×5, rounded.
	u := TokenUsage{Input: 82010, CacheCreation: 2525549, CacheRead: 152740511, Output: 998731}
	if got, want := u.CostWeighted(), int64(23506652); got != want {
		t.Fatalf("CostWeighted() = %d, want %d", got, want)
	}
}

func TestTokenUsageCostWeightedRounds(t *testing.T) {
	// 0.1 * 5 = 0.5 -> rounds to 1 (half-away-from-zero).
	u := TokenUsage{CacheRead: 5}
	if got, want := u.CostWeighted(), int64(1); got != want {
		t.Fatalf("CostWeighted() = %d, want %d", got, want)
	}
}

func TestTokenUsageIsRunaway(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  int64
		want bool
	}{
		{"below", RunawayRawTokenThreshold - 1, false},
		{"at threshold is not runaway", RunawayRawTokenThreshold, false},
		{"above", RunawayRawTokenThreshold + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := TokenUsage{CacheRead: tc.raw}
			if got := u.IsRunaway(); got != tc.want {
				t.Fatalf("IsRunaway() for raw=%d = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestTokenUsageAdd(t *testing.T) {
	a := TokenUsage{Input: 1, CacheCreation: 2, CacheRead: 3, Output: 4, Turns: 5}
	b := TokenUsage{Input: 10, CacheCreation: 20, CacheRead: 30, Output: 40, Turns: 50}
	got := a.Add(b)
	want := TokenUsage{Input: 11, CacheCreation: 22, CacheRead: 33, Output: 44, Turns: 55}
	if got != want {
		t.Fatalf("Add() = %+v, want %+v", got, want)
	}
}

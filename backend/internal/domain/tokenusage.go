package domain

import "math"

// Token cost multipliers relative to base input = 1, using the standard Anthropic
// cache pricing ratios (cache-write ×1.25, cache-read ×0.1, output ×5). They turn
// the four raw token buckets into a single comparable "cost-weighted" number whose
// ratios are stable regardless of the absolute price tier. Kept here as the single
// source of truth so the wire/DTO layer and tests weigh usage the same way.
const (
	tokenCostInput         = 1.0
	tokenCostCacheCreation = 1.25
	tokenCostCacheRead     = 0.1
	tokenCostOutput        = 5.0
)

// RunawayRawTokenThreshold flags a session whose RAW token total is far into the
// outlier tail. Measured worker distribution (see the token-usage investigation):
// median ~26M raw, mean ~67M, worst 990M. 200M (~8× median, ~3× mean) sits clearly
// in the runaway tail, so it catches genuine blow-ups without false-flagging a
// normal-but-large session. Tunable in one place.
const RunawayRawTokenThreshold int64 = 200_000_000

// TokenUsage is the per-session sum of the four token buckets parsed from the
// harness transcript, plus the number of assistant turns they came from. It stores
// only the durable measured facts; RawTotal, CostWeighted, and IsRunaway are derived
// on demand so the weighting formula and threshold never need a migration to change.
type TokenUsage struct {
	Input         int64
	CacheCreation int64
	CacheRead     int64
	Output        int64
	// Turns is the count of distinct assistant messages the usage was summed over
	// (deduped by message id, since a transcript writes one line per content block).
	Turns int64
}

// Add returns the element-wise sum of two usages. Used to accumulate across a
// session's (rare) multiple transcript files.
func (u TokenUsage) Add(o TokenUsage) TokenUsage {
	return TokenUsage{
		Input:         u.Input + o.Input,
		CacheCreation: u.CacheCreation + o.CacheCreation,
		CacheRead:     u.CacheRead + o.CacheRead,
		Output:        u.Output + o.Output,
		Turns:         u.Turns + o.Turns,
	}
}

// RawTotal is the unweighted sum of the four buckets.
func (u TokenUsage) RawTotal() int64 {
	return u.Input + u.CacheCreation + u.CacheRead + u.Output
}

// CostWeighted is the buckets weighted by the Anthropic cache pricing ratios,
// rounded to the nearest whole token-equivalent.
func (u TokenUsage) CostWeighted() int64 {
	weighted := float64(u.Input)*tokenCostInput +
		float64(u.CacheCreation)*tokenCostCacheCreation +
		float64(u.CacheRead)*tokenCostCacheRead +
		float64(u.Output)*tokenCostOutput
	return int64(math.Round(weighted))
}

// IsRunaway reports whether the raw total has crossed RunawayRawTokenThreshold.
func (u TokenUsage) IsRunaway() bool {
	return u.RawTotal() > RunawayRawTokenThreshold
}

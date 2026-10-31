package chatproto

import (
	"encoding/json"
	"time"
)

// RateLimit is the subscription usage claude reports in a rate_limit_event:
// the account's five-hour and weekly windows. A window claude did not report
// is nil.
type RateLimit struct {
	FiveHour *RateLimitWindow
	SevenDay *RateLimitWindow
}

// RateLimitWindow is one usage window. Utilization is a fraction, 0.36 for 36%.
type RateLimitWindow struct {
	Utilization float64
	ResetsAt    time.Time
	Status      string
}

// ParseRateLimit reads a rate_limit_event line. It reports false for any other
// line, and for an event with no window it can read.
func ParseRateLimit(line []byte) (RateLimit, bool) {
	type window struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    *int64   `json:"resetsAt"`
		Status      string   `json:"status"`
	}
	var w struct {
		Type string `json:"type"`
		Info struct {
			Windows map[string]json.RawMessage `json:"unifiedWindows"`
		} `json:"rate_limit_info"`
	}
	if json.Unmarshal(line, &w) != nil || w.Type != "rate_limit_event" {
		return RateLimit{}, false
	}
	read := func(key string) *RateLimitWindow {
		var win window
		raw, ok := w.Info.Windows[key]
		if !ok || json.Unmarshal(raw, &win) != nil || win.Utilization == nil || win.ResetsAt == nil {
			return nil
		}
		return &RateLimitWindow{Utilization: *win.Utilization, ResetsAt: time.Unix(*win.ResetsAt, 0).UTC(), Status: win.Status}
	}
	rl := RateLimit{FiveHour: read("five_hour"), SevenDay: read("seven_day")}
	return rl, rl.FiveHour != nil || rl.SevenDay != nil
}

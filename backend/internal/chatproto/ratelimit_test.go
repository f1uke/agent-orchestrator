package chatproto

import (
	"reflect"
	"testing"
	"time"
)

func TestParseRateLimit(t *testing.T) {
	real := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1793374200,"rateLimitType":"five_hour","overageStatus":"rejected","isUsingOverage":false,"unifiedWindows":{"five_hour":{"utilization":0.01,"resetsAt":1793374200,"status":"allowed"},"seven_day":{"utilization":0.36,"resetsAt":1793692800,"status":"allowed"}}},"uuid":"u","session_id":"s"}`
	got, ok := ParseRateLimit([]byte(real))
	if !ok {
		t.Fatal("did not parse the shape claude emits")
	}
	want := RateLimit{
		FiveHour: &RateLimitWindow{Utilization: 0.01, ResetsAt: time.Unix(1793374200, 0).UTC(), Status: "allowed"},
		SevenDay: &RateLimitWindow{Utilization: 0.36, ResetsAt: time.Unix(1793692800, 0).UTC(), Status: "allowed"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}

	onlyWeek, ok := ParseRateLimit([]byte(`{"type":"rate_limit_event","rate_limit_info":{"unifiedWindows":{"seven_day":{"utilization":0.5,"resetsAt":1793692800,"status":"allowed_warning"}}}}`))
	if !ok || onlyWeek.FiveHour != nil || onlyWeek.SevenDay == nil || onlyWeek.SevenDay.Status != "allowed_warning" {
		t.Errorf("one window: got %+v, %v", onlyWeek, ok)
	}

	for name, line := range map[string]string{
		"another event":        `{"type":"result","subtype":"success"}`,
		"no windows":           `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}`,
		"utilization a string": `{"type":"rate_limit_event","rate_limit_info":{"unifiedWindows":{"five_hour":{"utilization":"1%","resetsAt":1,"status":"allowed"}}}}`,
		"no reset time":        `{"type":"rate_limit_event","rate_limit_info":{"unifiedWindows":{"five_hour":{"utilization":0.1,"status":"allowed"}}}}`,
		"not JSON":             `nope`,
	} {
		if got, ok := ParseRateLimit([]byte(line)); ok {
			t.Errorf("%s: parsed as %+v, want rejected", name, got)
		}
	}
}

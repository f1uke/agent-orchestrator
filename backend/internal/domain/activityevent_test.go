package domain

import (
	"encoding/json"
	"testing"
	"time"
)

// The decay contract is the whole point of the feed: a finished action must not
// read as currently happening. These tests pin the three-rung ladder
// (detail -> coarse -> unknown) as executable behaviour rather than prose.

func TestActivityEvent_DetailDecaysToCoarse(t *testing.T) {
	at := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	ev := ActivityEvent{
		SessionID:   "ao-7",
		Kind:        ActivityEventToolStart,
		At:          at,
		Tool:        "Bash",
		Text:        "Running the test suite",
		TTLMs:       DurationMs(ToolStartDetailTTL),
		Coarse:      CoarseWorking,
		CoarseTTLMs: DurationMs(CoarseWorkingTTL),
	}

	if !ev.DetailFreshAt(at.Add(ToolStartDetailTTL - time.Millisecond)) {
		t.Error("detail must be presentable up to its TTL")
	}
	if ev.DetailFreshAt(at.Add(ToolStartDetailTTL)) {
		t.Error("detail must NOT be presentable at/after its TTL: that is the stale-bubble lie")
	}
	// Past the detail TTL the coarse rung is still true.
	if !ev.CoarseFreshAt(at.Add(ToolStartDetailTTL + time.Second)) {
		t.Error("coarse must outlive the detail")
	}
	if ev.CoarseFreshAt(at.Add(CoarseWorkingTTL)) {
		t.Error("coarse must expire at its own TTL, leaving the consumer at unknown")
	}
}

func TestActivityEvent_StickyCoarseNeverDecays(t *testing.T) {
	at := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	for _, state := range []ActivityState{ActivityWaitingInput, ActivityExited} {
		coarse, ttl := CoarseFromActivityState(state)
		if ttl != 0 {
			t.Errorf("%s: coarse %q ttl = %v, want 0 (sticky)", state, coarse, ttl)
		}
		ev := ActivityEvent{At: at, Coarse: coarse, CoarseTTLMs: DurationMs(ttl)}
		if !ev.CoarseFreshAt(at.Add(365 * 24 * time.Hour)) {
			t.Errorf("%s: sticky coarse must never expire", state)
		}
	}
}

func TestActivityEvent_DetaillessEventCarriesNoText(t *testing.T) {
	ev := ActivityEvent{At: time.Now().UTC(), Kind: ActivityEventActivity, Coarse: CoarseIdle}
	if ev.DetailFreshAt(ev.At) {
		t.Error("ttlMs == 0 means the event carries no detail, so nothing may be shown from it")
	}
}

func TestCoarseFromActivityState(t *testing.T) {
	cases := []struct {
		state ActivityState
		want  ActivityCoarse
		ttl   time.Duration
	}{
		{ActivityActive, CoarseWorking, CoarseWorkingTTL},
		{ActivityIdle, CoarseIdle, CoarseIdleTTL},
		{ActivityWaitingInput, CoarseWaiting, 0},
		{ActivityExited, CoarseExited, 0},
		{ActivityState("bogus"), "", 0},
	}
	for _, tc := range cases {
		got, ttl := CoarseFromActivityState(tc.state)
		if got != tc.want || ttl != tc.ttl {
			t.Errorf("CoarseFromActivityState(%q) = (%q, %v), want (%q, %v)", tc.state, got, ttl, tc.want, tc.ttl)
		}
	}
}

// The coarse TTLs must track the status deriver's own graces, otherwise the feed
// keeps claiming "working" past the point AO itself stopped believing it.
func TestCoarseTTLsMirrorStatusDeriverGraces(t *testing.T) {
	if CoarseWorkingTTL != 10*time.Minute {
		t.Errorf("CoarseWorkingTTL = %v, want activeStaleGrace (10m)", CoarseWorkingTTL)
	}
	if CoarseIdleTTL != 45*time.Second {
		t.Errorf("CoarseIdleTTL = %v, want waitingInputGrace (45s)", CoarseIdleTTL)
	}
}

func TestDetailTTLForKind(t *testing.T) {
	cases := []struct {
		kind ActivityEventKind
		want time.Duration
	}{
		{ActivityEventToolStart, ToolStartDetailTTL},
		{ActivityEventToolEnd, ToolEndDetailTTL},
		{ActivityEventToolFailed, ToolFailedDetailTTL},
		{ActivityEventMessage, MessageDetailTTL},
		{ActivityEventActivity, 0},
		{ActivityEventKind("bogus"), 0},
	}
	for _, tc := range cases {
		if got := DetailTTL(tc.kind); got != tc.want {
			t.Errorf("DetailTTL(%q) = %v, want %v", tc.kind, got, tc.want)
		}
	}
}

// The wire shape is the contract the overlay builds against: pin the JSON keys.
func TestActivityEvent_WireShape(t *testing.T) {
	ev := ActivityEvent{
		SessionID:   "ao-7",
		Kind:        ActivityEventToolStart,
		At:          time.Date(2026, 7, 22, 9, 14, 3, 421_000_000, time.UTC),
		Tool:        "Read",
		Target:      "hooks.go",
		TTLMs:       DurationMs(ToolStartDetailTTL),
		Coarse:      CoarseWorking,
		CoarseTTLMs: DurationMs(CoarseWorkingTTL),
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"sessionId":"ao-7","kind":"tool_start","at":"2026-07-22T09:14:03.421Z",` +
		`"tool":"Read","target":"hooks.go","ttlMs":20000,"coarse":"working","coarseTtlMs":600000}`
	if string(data) != want {
		t.Errorf("wire shape drifted:\n got %s\nwant %s", data, want)
	}
}

// An event that only carries detail (a message tap) must leave the coarse rung
// alone: it says something flew by, not how busy the agent is.
func TestActivityEvent_AbsentCoarseIsOmitted(t *testing.T) {
	ev := ActivityEvent{
		SessionID: "ao-7",
		Kind:      ActivityEventMessage,
		At:        time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC),
		Text:      "[from @ao-1] continue",
		TTLMs:     DurationMs(MessageDetailTTL),
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["coarse"]; ok {
		t.Errorf("absent coarse must be omitted so the consumer keeps its previous level: %s", data)
	}
	if ev.CoarseFreshAt(ev.At) {
		t.Error("an event with no coarse must not read as a fresh coarse level")
	}
}

package domain

import "testing"

// Awake is the definition ONE-AWAKE-AT-A-TIME is enforced on, so what it counts
// and what it deliberately ignores are both pinned here.
func TestSessionRecord_Awake(t *testing.T) {
	cases := []struct {
		name string
		rec  SessionRecord
		want bool
	}{
		{name: "a running session", rec: SessionRecord{}, want: true},
		{name: "terminated: its runtime was destroyed", rec: SessionRecord{IsTerminated: true}},
		{name: "suspended: the tmux was reaped, the worktree kept", rec: SessionRecord{IsSuspended: true}},
		{name: "a TODO: prepared, never started, no runtime", rec: SessionRecord{IsTodo: true}},
		{
			// The one that matters most. The design's baton parks dev at an empty
			// prompt and lets qa run; parked is a READING from the agent's own hook,
			// and a parked agent still owns a live pane a human or a nudge can put
			// straight back to work. Awake must not believe it.
			name: "parked: the turn is over but the process is still there",
			rec:  SessionRecord{Activity: Activity{State: ActivityParked}},
			want: true,
		},
		{
			name: "waiting on a permission prompt: still a live agent in the tree",
			rec:  SessionRecord{Activity: Activity{State: ActivityWaitingInput}},
			want: true,
		},
		{
			name: "idle: nothing has been reported for a while, but nothing was reaped either",
			rec:  SessionRecord{Activity: Activity{State: ActivityIdle}},
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rec.Awake(); got != tc.want {
				t.Fatalf("Awake() = %v, want %v", got, tc.want)
			}
		})
	}
}

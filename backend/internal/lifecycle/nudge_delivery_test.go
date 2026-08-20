package lifecycle

import (
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// This file guards the rule that these reducers do NOT decide whether a session
// can receive a message. They used to, by comparing against
// domain.ActivityWaitingInput and returning before the messenger was ever
// called, which lost the nudge outright: nothing was sent, nothing was queued,
// nothing logged, and — because the SCM observer stamps an observation
// acknowledged as soon as lifecycle returns nil — nothing ever re-delivered it.
//
// Two states are exercised against every actionable nudge:
//
//   - parked: the agent's turn ended and it is sitting at an ordinary prompt.
//     Nothing is blocking it. This is the state in which it most needs to hear
//     that CI went red, and the nudge must be sent.
//   - waiting_input: a permission prompt is open in the pane. The nudge must
//     still REACH the messenger, which holds it (ports.SendOutcome.Queued) until
//     the agent is listening. What must never happen again is the drop.

// nudgeCase is one actionable nudge, driven through its own reducer entry point.
type nudgeCase struct {
	name string
	// session prepares the record beyond its activity state (auto-nudge opt-in,
	// PR facts, ...).
	session func(id domain.SessionID) domain.SessionRecord
	// fire drives the reducer that owns this nudge.
	fire func(t *testing.T, m *Manager, id domain.SessionID)
	// want is a fragment of the message body that identifies this nudge.
	want string
}

func nudgeCases() []nudgeCase {
	return []nudgeCase{
		{
			name:    "CI failing",
			session: working,
			fire: func(t *testing.T, m *Manager, id domain.SessionID) {
				t.Helper()
				o := ports.PRObservation{Fetched: true, URL: "pr1", CI: domain.CIFailing, Checks: []ports.PRCheckObservation{
					{Name: "build", Status: domain.PRCheckFailed, CommitHash: "sha-1", LogTail: "FAILED: build broke"},
				}}
				if err := m.ApplyPRObservation(ctx, id, o); err != nil {
					t.Fatalf("ApplyPRObservation: %v", err)
				}
			},
			want: "FAILED: build broke",
		},
		{
			name:    "review comment",
			session: autoNudgeSession,
			fire: func(t *testing.T, m *Manager, id domain.SessionID) {
				t.Helper()
				o := ports.PRObservation{Fetched: true, URL: "pr1", Comments: []ports.PRCommentObservation{
					{ID: "c1", Author: "alice", File: "handler.go", Line: 75, Body: "guard this nil"},
				}}
				if err := m.ApplyPRObservation(ctx, id, o); err != nil {
					t.Fatalf("ApplyPRObservation: %v", err)
				}
			},
			want: "guard this nil",
		},
		{
			name:    "merge conflict",
			session: working,
			fire: func(t *testing.T, m *Manager, id domain.SessionID) {
				t.Helper()
				o := ports.PRObservation{Fetched: true, URL: "pr1", Mergeability: domain.MergeConflicting, HeadSHA: "sha-1"}
				if err := m.ApplyPRObservation(ctx, id, o); err != nil {
					t.Fatalf("ApplyPRObservation: %v", err)
				}
			},
			want: "conflict",
		},
		{
			name:    "AO review verdict",
			session: working,
			fire: func(t *testing.T, m *Manager, id domain.SessionID) {
				t.Helper()
				r := ReviewResult{RunID: "run-1", PRURL: "pr1", TargetSHA: "sha-1", Verdict: domain.VerdictChangesRequested, Body: "extract this helper"}
				outcome, err := m.ApplyReviewResult(ctx, id, r)
				if err != nil {
					t.Fatalf("ApplyReviewResult: %v", err)
				}
				if outcome != ReviewDeliverySent {
					t.Fatalf("outcome = %q, want %q", outcome, ReviewDeliverySent)
				}
			},
			want: "extract this helper",
		},
	}
}

// TestNudgesReachTheMessengerForAParkedSession is the regression test for the
// reported bug. A parked agent is not in a conversation with anyone: it finished
// its turn and is waiting at its prompt. Every actionable nudge must reach it.
func TestNudgesReachTheMessengerForAParkedSession(t *testing.T) {
	for _, tc := range nudgeCases() {
		t.Run(tc.name, func(t *testing.T) {
			m, st, msg := newManager()
			rec := tc.session("mer-1")
			rec.Activity = domain.Activity{State: domain.ActivityParked, LastActivityAt: time.Now()}
			st.sessions["mer-1"] = rec

			tc.fire(t, m, "mer-1")

			if len(msg.msgs) != 1 {
				t.Fatalf("parked session received %d nudges, want 1: %v", len(msg.msgs), msg.msgs)
			}
			if !strings.Contains(msg.msgs[0], tc.want) {
				t.Fatalf("nudge body %q does not contain %q", msg.msgs[0], tc.want)
			}
		})
	}
}

// TestNudgesReachTheMessengerForAWaitingSession pins the OTHER half of the fix.
// A session at a permission prompt must not be typed at — but that decision is
// the messenger's, and it HOLDS the message rather than dropping it. Lifecycle's
// job is to hand it over. Before the fix these reducers returned before the
// messenger was reached, so the nudge was gone for good.
func TestNudgesReachTheMessengerForAWaitingSession(t *testing.T) {
	for _, tc := range nudgeCases() {
		t.Run(tc.name, func(t *testing.T) {
			m, st, msg := newManager()
			// A messenger wired to a queue reports the message as HELD, which is
			// exactly what the production messenger does for a session that cannot
			// receive right now (see daemon.runtimeMessenger).
			msg.outcome = ports.SendOutcome{Queued: true, QueuedAt: time.Now(), Pending: 1}
			rec := tc.session("mer-1")
			rec.Activity = domain.Activity{State: domain.ActivityWaitingInput, LastActivityAt: time.Now()}
			st.sessions["mer-1"] = rec

			tc.fire(t, m, "mer-1")

			if len(msg.msgs) != 1 {
				t.Fatalf("nudge for a session at a prompt was DROPPED (%d reached the messenger, want 1)", len(msg.msgs))
			}
			if !strings.Contains(msg.msgs[0], tc.want) {
				t.Fatalf("nudge body %q does not contain %q", msg.msgs[0], tc.want)
			}
		})
	}
}

// A terminated session is the one case that still cancels a nudge outright: the
// work is over, so there is nothing left to act on it and holding the message
// would be a promise AO cannot keep.
func TestNudgesAreCancelledForATerminatedSession(t *testing.T) {
	for _, tc := range nudgeCases() {
		t.Run(tc.name, func(t *testing.T) {
			m, st, msg := newManager()
			rec := tc.session("mer-1")
			rec.IsTerminated = true
			st.sessions["mer-1"] = rec

			// The AO-review case asserts ReviewDeliverySent, which a terminated
			// session must not produce, so drive that reducer directly here.
			if tc.name == "AO review verdict" {
				outcome, err := m.ApplyReviewResult(ctx, "mer-1", ReviewResult{RunID: "run-1", PRURL: "pr1", Verdict: domain.VerdictChangesRequested})
				if err != nil {
					t.Fatalf("ApplyReviewResult: %v", err)
				}
				if outcome != ReviewDeliveryNoop {
					t.Fatalf("outcome = %q, want %q", outcome, ReviewDeliveryNoop)
				}
			} else {
				tc.fire(t, m, "mer-1")
			}

			if len(msg.msgs) != 0 {
				t.Fatalf("terminated session was nudged: %v", msg.msgs)
			}
		})
	}
}

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The account of how a session ended is only worth writing if it survives the
// process that wrote it — the question "why did it disappear?" is always asked
// later, often after a restart.
func TestSessionTerminationRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))

	// A live session carries no account.
	if got, _, _ := s.GetSession(ctx, r.ID); !got.Termination.IsZero() {
		t.Fatalf("fresh session already has a termination: %+v", got.Termination)
	}

	at := time.Now().UTC().Truncate(time.Second)
	r.IsTerminated = true
	r.Termination = domain.Termination{
		Source:         domain.TerminationSourceAgent,
		Reason:         "prompt_input_exit",
		LastState:      domain.ActivityActive,
		TranscriptPath: "/transcripts/mer-1.jsonl",
		At:             at,
	}
	if err := s.UpdateSession(ctx, r); err != nil {
		t.Fatal(err)
	}

	got, _, _ := s.GetSession(ctx, r.ID)
	if got.Termination.Source != domain.TerminationSourceAgent {
		t.Errorf("source = %q, want %q", got.Termination.Source, domain.TerminationSourceAgent)
	}
	if got.Termination.Reason != "prompt_input_exit" {
		t.Errorf("reason = %q, want the stored reason", got.Termination.Reason)
	}
	if got.Termination.LastState != domain.ActivityActive {
		t.Errorf("lastState = %q, want %q", got.Termination.LastState, domain.ActivityActive)
	}
	if got.Termination.TranscriptPath != "/transcripts/mer-1.jsonl" {
		t.Errorf("transcriptPath = %q, want the stored path", got.Termination.TranscriptPath)
	}
	if !got.Termination.At.Equal(at) {
		t.Errorf("at = %v, want %v", got.Termination.At, at)
	}

	// A restore clears it: the account must not outlive the ending it describes.
	got.Termination = domain.Termination{}
	got.IsTerminated = false
	if err := s.UpdateSession(ctx, got); err != nil {
		t.Fatal(err)
	}
	if again, _, _ := s.GetSession(ctx, r.ID); !again.Termination.IsZero() {
		t.Errorf("termination = %+v, want cleared", again.Termination)
	}
}

// ListAllSessions is what the board and every sweep read; a column that only
// round-trips through GetSession would leave the account invisible to them.
func TestListAllSessionsCarriesTheTermination(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))

	r.IsTerminated = true
	r.Termination = domain.Termination{
		Source: domain.TerminationSourceAO, Reason: domain.TerminationCauseAutoReclaim,
		LastState: domain.ActivityIdle, At: time.Now().UTC().Truncate(time.Second),
	}
	if err := s.UpdateSession(ctx, r); err != nil {
		t.Fatal(err)
	}

	all, err := s.ListAllSessions(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("list = %d sessions, err = %v", len(all), err)
	}
	if all[0].Termination.Reason != domain.TerminationCauseAutoReclaim {
		t.Errorf("reason = %q, want %q", all[0].Termination.Reason, domain.TerminationCauseAutoReclaim)
	}
	if all[0].Termination.Source != domain.TerminationSourceAO {
		t.Errorf("source = %q, want %q", all[0].Termination.Source, domain.TerminationSourceAO)
	}
}

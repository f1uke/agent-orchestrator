package controllers

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The termination account has to reach the API, or it is a fact the daemon
// keeps to itself while the human still cannot answer "why did it stop?".
func TestSessionView_CarriesTheTerminationAccount(t *testing.T) {
	at := time.Date(2026, 8, 17, 9, 36, 5, 0, time.UTC)
	view := sessionView(domain.Session{
		SessionRecord: domain.SessionRecord{
			ID: "ao-1", ProjectID: "demo", Kind: domain.KindWorker, IsTerminated: true,
			Activity: domain.Activity{State: domain.ActivityExited},
			Termination: domain.Termination{
				Source: domain.TerminationSourceAgent, Reason: "prompt_input_exit",
				LastState: domain.ActivityActive, TranscriptPath: "/transcripts/ao-1.jsonl", At: at,
			},
		},
	})
	if view.Termination == nil {
		t.Fatal("session view dropped the termination account")
	}
	if view.Termination.Source != "agent" || view.Termination.Reason != "prompt_input_exit" {
		t.Errorf("termination = %+v", view.Termination)
	}
	if view.Termination.LastState != "active" || view.Termination.TranscriptPath != "/transcripts/ao-1.jsonl" {
		t.Errorf("termination = %+v", view.Termination)
	}
	if !view.Termination.At.Equal(at) {
		t.Errorf("at = %v, want %v", view.Termination.At, at)
	}
}

// A live session has no ending, so the field is omitted rather than serialized
// as a hollow object the UI would have to special-case.
func TestSessionView_LiveSessionHasNoTermination(t *testing.T) {
	view := sessionView(domain.Session{
		SessionRecord: domain.SessionRecord{ID: "ao-1", ProjectID: "demo", Activity: domain.Activity{State: domain.ActivityActive}},
	})
	if view.Termination != nil {
		t.Errorf("termination = %+v, want nil for a live session", view.Termination)
	}
}

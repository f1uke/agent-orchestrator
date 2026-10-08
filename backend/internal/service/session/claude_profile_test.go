package session

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/claudeprofile"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

func TestToAPIError_ClaudeProfileErrors(t *testing.T) {
	unknown := fmt.Errorf("spawn: %w", &claudeprofile.UnknownProfileError{Name: "nope", Known: []string{"Subscription", "OmniRoute"}})
	broken := fmt.Errorf("restart mer-1: %w", &claudeprofile.SettingsFileError{Profile: "Work", File: "/w.json", Problem: "is not a JSON object"})
	unsupported := fmt.Errorf("set claude profile mer-1: %w", sessionmanager.ErrClaudeProfileUnsupported)

	cases := []struct {
		err     error
		kind    apierr.Kind
		code    string
		message string
	}{
		{unknown, apierr.KindInvalid, "UNKNOWN_CLAUDE_PROFILE", `unknown Claude profile "nope" (known: Subscription, OmniRoute)`},
		{broken, apierr.KindInvalid, "CLAUDE_PROFILE_SETTINGS_INVALID", `the settings file /w.json of Claude profile "Work" is not a JSON object`},
		{unsupported, apierr.KindConflict, "CLAUDE_PROFILE_UNSUPPORTED", ""},
	}
	for _, c := range cases {
		var got *apierr.Error
		if !errors.As(toAPIError(c.err), &got) {
			t.Fatalf("%v did not map to an API error", c.err)
		}
		if got.Kind != c.kind || got.Code != c.code || (c.message != "" && got.Message != c.message) {
			t.Errorf("%v -> %+v, want kind %v code %s message %q", c.err, got, c.kind, c.code, c.message)
		}
	}

	var got *apierr.Error
	_ = errors.As(toAPIError(unknown), &got)
	if known, _ := got.Details["known"].([]string); len(known) != 2 {
		t.Errorf("unknown profile details = %v, want the known profile names", got.Details)
	}
}

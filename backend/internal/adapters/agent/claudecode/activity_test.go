package claudecode

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestNestedWorktreeDenial(t *testing.T) {
	tests := []struct {
		name    string
		event   string
		payload string
		want    bool
	}{
		{
			name:    "worktree-isolated child",
			event:   "pre-tool-use",
			payload: `{"tool_name":"Agent","tool_input":{"isolation":"worktree"}}`,
			want:    true,
		},
		{
			name:    "EnterWorktree",
			event:   "pre-tool-use",
			payload: `{"tool_name":"EnterWorktree","tool_input":{"name":"nested"}}`,
			want:    true,
		},
		{
			name:    "shared-worktree child",
			event:   "pre-tool-use",
			payload: `{"tool_name":"Agent","tool_input":{"subagent_type":"general-purpose"}}`,
			want:    false,
		},
		{
			name:    "unrelated tool",
			event:   "pre-tool-use",
			payload: `{"tool_name":"Read","tool_input":{"file_path":"README.md"}}`,
			want:    false,
		},
		{
			name:    "malformed payload",
			event:   "pre-tool-use",
			payload: `not json`,
			want:    false,
		},
		{
			// Only pre-tool-use runs BEFORE the tool, so it is the only callback
			// that can still prevent the wrong checkout. A post-tool-use carrying
			// the same tool must not be denied.
			name:    "post-tool-use is never denied",
			event:   "post-tool-use",
			payload: `{"tool_name":"Agent","tool_input":{"isolation":"worktree"}}`,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := NestedWorktreeDenial(tt.event, []byte(tt.payload))
			if got != tt.want {
				t.Fatalf("NestedWorktreeDenial(%q, %q) denied = %v, want %v", tt.event, tt.payload, got, tt.want)
			}
		})
	}
}

func TestDeriveActivityState(t *testing.T) {
	tests := []struct {
		name    string
		event   string
		payload string
		want    domain.ActivityState
		wantOK  bool
	}{
		{"user prompt -> active", "user-prompt-submit", `{}`, domain.ActivityActive, true},
		{"pre tool use -> active", "pre-tool-use", `{"tool_name":"Bash"}`, domain.ActivityActive, true},
		{"post tool use -> active", "post-tool-use", `{"tool_name":"Bash"}`, domain.ActivityActive, true},
		{"post tool use failure -> active", "post-tool-use-failure", `{"tool_name":"Bash"}`, domain.ActivityActive, true},
		{"stop -> idle", "stop", `{}`, domain.ActivityIdle, true},
		{"notification idle_prompt -> parked", "notification", `{"notification_type":"idle_prompt"}`, domain.ActivityParked, true},
		{"notification permission_prompt -> waiting_input", "notification", `{"notification_type":"permission_prompt"}`, domain.ActivityWaitingInput, true},
		{"notification auth_success -> no signal", "notification", `{"notification_type":"auth_success"}`, "", false},
		{"notification empty type -> no signal", "notification", `{}`, "", false},
		{"notification malformed payload -> no signal", "notification", `not json`, "", false},
		{"session-end logout -> exited", "session-end", `{"reason":"logout"}`, domain.ActivityExited, true},
		{"session-end prompt_input_exit -> exited", "session-end", `{"reason":"prompt_input_exit"}`, domain.ActivityExited, true},
		{"session-end other -> exited", "session-end", `{"reason":"other"}`, domain.ActivityExited, true},
		{"session-end absent reason -> exited", "session-end", `{}`, domain.ActivityExited, true},
		{"session-end clear -> no signal", "session-end", `{"reason":"clear"}`, "", false},
		{"session-end resume -> no signal", "session-end", `{"reason":"resume"}`, "", false},
		{"session-start -> no signal", "session-start", `{}`, "", false},
		{"unknown event -> no signal", "frobnicate", `{}`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := DeriveActivityState(tt.event, []byte(tt.payload))
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("DeriveActivityState(%q, %q) = (%q, %v), want (%q, %v)",
					tt.event, tt.payload, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

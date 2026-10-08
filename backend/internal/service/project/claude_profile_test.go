package project_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

func TestManager_ClaudeProfileIsValidatedAndStoredCanonical(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	repo := gitRepo(t)

	_, err := m.Add(ctx, project.AddInput{Path: repo, ProjectID: ptr("ao"), Config: &domain.ProjectConfig{ClaudeProfile: "nope"}})
	wantCode(t, err, "UNKNOWN_CLAUDE_PROFILE")
	if _, err := m.Add(ctx, project.AddInput{Path: repo, ProjectID: ptr("ao")}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	_, err = m.SetConfig(ctx, "ao", project.SetConfigInput{Config: domain.ProjectConfig{ClaudeProfile: "nope"}, MergeFields: []string{"claudeProfile"}})
	wantCode(t, err, "UNKNOWN_CLAUDE_PROFILE")

	proj, err := m.SetConfig(ctx, "ao", project.SetConfigInput{Config: domain.ProjectConfig{ClaudeProfile: "omniroute"}, MergeFields: []string{"claudeProfile"}})
	if err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if proj.Config == nil || proj.Config.ClaudeProfile != "OmniRoute" {
		t.Fatalf("stored claudeProfile = %#v, want the canonical OmniRoute", proj.Config)
	}

	proj, err = m.SetConfig(ctx, "ao", project.SetConfigInput{Config: domain.ProjectConfig{}, MergeFields: []string{"claudeProfile"}})
	if err != nil || (proj.Config != nil && proj.Config.ClaudeProfile != "") {
		t.Fatalf("clearing the default: %#v, %v; want empty", proj.Config, err)
	}
}

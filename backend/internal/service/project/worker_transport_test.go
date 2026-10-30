package project_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

func TestManager_WorkerTransportRoundTripsAndRefusesUnknownValues(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	if _, err := m.Add(ctx, project.AddInput{Path: gitRepo(t), ProjectID: ptr("ao")}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	set := func(t domain.Transport) error {
		_, err := m.SetConfig(ctx, "ao", project.SetConfigInput{Config: domain.ProjectConfig{WorkerTransport: t}, MergeFields: []string{"workerTransport"}})
		return err
	}
	stored := func(t *testing.T) domain.Transport {
		t.Helper()
		got, err := m.Get(ctx, "ao")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Project == nil || got.Project.Config == nil {
			return ""
		}
		return got.Project.Config.WorkerTransport
	}

	if err := set(domain.TransportTerminal); err != nil {
		t.Fatalf("set terminal: %v", err)
	}
	if got := stored(t); got != domain.TransportTerminal {
		t.Fatalf("stored = %q, want terminal", got)
	}

	wantCode(t, set("pigeon"), "INVALID_PROJECT_CONFIG")
	if got := stored(t); got != domain.TransportTerminal {
		t.Fatalf("a refused write changed the stored value to %q", got)
	}

	if err := set(""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := stored(t); got != "" {
		t.Fatalf("after clearing, stored = %q, want it inheriting", got)
	}
}

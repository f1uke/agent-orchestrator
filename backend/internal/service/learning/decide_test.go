package learning_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learndecide"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/learning"
)

type fakeDecider struct{ got []learndecide.RunOpts }

func (f *fakeDecider) RunNow(_ context.Context, o learndecide.RunOpts) error {
	f.got = append(f.got, o)
	return nil
}
func (f *fakeDecider) Progress() learndecide.Progress { return learndecide.Progress{Tasks: 1} }

func TestDecide_StartsListsAndShows(t *testing.T) {
	st, svc, _ := setup(t, true)
	ctx := context.Background()
	d := &fakeDecider{}
	if err := svc.StartDecide(ctx, "", "", 1); !errors.Is(err, learning.ErrDecideUnavailable) {
		t.Errorf("without the stage: %v", err)
	}
	svc = svc.WithDecide(ctx, st, d)
	if err := svc.StartDecide(ctx, "nope", "", 1); !errors.Is(err, learning.ErrUnknownProject) {
		t.Errorf("unknown project: %v", err)
	}
	if err := svc.StartDecide(ctx, "p", "", 100); !errors.Is(err, learning.ErrInvalidBudget) {
		t.Errorf("budget cap: %v", err)
	}
	if err := svc.StartDecide(ctx, "p", "solo:x", 2); err != nil || len(d.got) != 1 || d.got[0].Task != "solo:x" {
		t.Fatalf("start: %v %+v", err, d.got)
	}
	now := time.Now().UTC()
	if err := st.CommitDecide(ctx, domain.LearnDecideResult{TaskKey: "solo:x", ProjectID: "p", Outcome: domain.LearnOutcomeMerged,
		Proposals: []domain.LearnProposal{
			{ProjectID: "p", TaskKey: "solo:x", Action: domain.LearnProposeCreateMemory, TargetPath: "/a", Scope: "project:p", Title: "kept"},
			{ProjectID: "p", TaskKey: "solo:x", Action: domain.LearnProposeCreateMemory, TargetPath: "/b", Scope: "project:p", Title: "dropped",
				Status: domain.LearnProposalDropped, DropReason: "why"},
		}}, now); err != nil {
		t.Fatal(err)
	}
	pending, _ := svc.Proposals(ctx, "p", false)
	all, _ := svc.Proposals(ctx, "p", true)
	if len(pending) != 1 || pending[0].Title != "kept" || len(all) != 2 || all[0].Title != "dropped" {
		t.Errorf("pending %+v / all %+v (newest first)", pending, all)
	}
	if _, _, err := svc.Proposal(ctx, 999); !errors.Is(err, learning.ErrUnknownProposal) {
		t.Errorf("unknown proposal: %v", err)
	}
	if p, _, err := svc.Proposal(ctx, pending[0].ID); err != nil || p.Title != "kept" {
		t.Errorf("show: %+v %v", p, err)
	}
}

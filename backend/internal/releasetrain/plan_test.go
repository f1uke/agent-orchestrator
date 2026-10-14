package releasetrain

import (
	"errors"
	"strings"
	"testing"
)

func TestParseTrainKeyInvertsKey(t *testing.T) {
	for _, id := range []TrainID{{21, 0, 2026}, {20, 1, 2026}} {
		got, ok := ParseTrainKey(id.Key())
		if !ok || got != id {
			t.Fatalf("ParseTrainKey(%q) = %v %v, want %v", id.Key(), got, ok, id)
		}
	}
	for _, bad := range []string{"", "21", "#21", "21-26", "a-2026", "21-2026-"} {
		if _, ok := ParseTrainKey(bad); ok {
			t.Fatalf("ParseTrainKey(%q) accepted", bad)
		}
	}
}

func TestPlanValidate(t *testing.T) {
	train := TrainID{22, 0, 2026}
	ok := LlbPlan{ID: "demo-llb", Name: "Demo", TargetKind: TargetTrain, TargetTrain: &train,
		Branches: []PlanBranch{{App: "di", Branch: "feature/demo"}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid plan: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(p *LlbPlan)
		want   string
	}{
		{"id with a slash", func(p *LlbPlan) { p.ID = "demo/llb" }, "id must be"},
		{"no name", func(p *LlbPlan) { p.Name = " " }, "name is required"},
		{"train target without a train", func(p *LlbPlan) { p.TargetTrain = nil }, "target train is required"},
		{"dropped with a train", func(p *LlbPlan) { p.TargetKind = TargetDropped }, "only for a train target"},
		{"unknown target", func(p *LlbPlan) { p.TargetKind = "later"; p.TargetTrain = nil }, "target must be"},
		{"branch twice", func(p *LlbPlan) { p.Branches = append(p.Branches, p.Branches[0]) }, "listed twice"},
		{"accepted without a reason", func(p *LlbPlan) { p.Accepted = []PlanAccepted{{MRRef: "di!5"}} }, "needs a ref and a reason"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := ok
			p.Branches = append([]PlanBranch{}, ok.Branches...)
			tc.mutate(&p)
			err := p.Validate()
			if !errors.Is(err, ErrInvalidPlan) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate = %v, want ErrInvalidPlan saying %q", err, tc.want)
			}
		})
	}
}

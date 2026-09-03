package lifecycle

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/messagetemplates"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// withTarget is a working session that records where its PR is meant to merge.
func withTarget(id domain.SessionID, target string) domain.SessionRecord {
	rec := working(id)
	rec.PRTarget = target
	return rec
}

// A PR opened against the repository's default branch instead of the session's
// PR target is the mistake that made two boards red in 24 hours (#282, #287):
// `gh pr create` with no --base picks the default silently. AO must SAY so - and
// only say so, since a deliberate base stays the worker's call.
func TestPRObservation_WrongBaseNudgesWithBothBranches(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = withTarget("mer-1", "main-fluke")

	o := ports.PRObservation{Fetched: true, URL: "pr1", Number: 287, SourceBranch: "fix/rollup", TargetBranch: "main"}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("want one wrong-base nudge, got %v", msg.msgs)
	}
	for _, want := range []string{"`main`", "`main-fluke`", "gh pr edit --base main-fluke", "glab mr update --target-branch main-fluke"} {
		if !strings.Contains(msg.msgs[0], want) {
			t.Fatalf("nudge missing %q: %q", want, msg.msgs[0])
		}
	}

	// Re-observing the same divergence must stay silent; the worker was told.
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("wrong-base nudge repeated on an unchanged observation: %v", msg.msgs)
	}

	// Retargeting to a THIRD branch is a new divergence, so it is news again.
	o.TargetBranch = "develop"
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 2 || !strings.Contains(msg.msgs[1], "`develop`") {
		t.Fatalf("a new wrong base must nudge again: %v", msg.msgs)
	}
}

func TestPRObservation_RightBaseIsSilent(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = withTarget("mer-1", "main-fluke")

	o := ports.PRObservation{Fetched: true, URL: "pr1", Number: 288, SourceBranch: "fix/rollup", TargetBranch: "main-fluke"}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("a PR on target must not nudge: %v", msg.msgs)
	}
}

// A session with no recorded target has nothing to compare against - rows made
// before AO recorded one. Guessing would nudge every one of them.
func TestPRObservation_NoRecordedTargetIsSilent(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")

	o := ports.PRObservation{Fetched: true, URL: "pr1", Number: 289, SourceBranch: "fix/rollup", TargetBranch: "main"}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("a session with no recorded PR target must not nudge: %v", msg.msgs)
	}
}

// A stacked PR targets the sibling below it on purpose - the documented shape for
// a second PR from one session. It must not be reported as a wrong base.
func TestPRObservation_StackedPRIsSilent(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = withTarget("mer-1", "main-fluke")
	st.prs["mer-1"] = []domain.PullRequest{
		{URL: "pr-parent", Number: 1, SourceBranch: "ao/mer-1/parent", TargetBranch: "main-fluke"},
		{URL: "pr1", Number: 2, SourceBranch: "ao/mer-1/child", TargetBranch: "ao/mer-1/parent"},
	}

	o := ports.PRObservation{Fetched: true, URL: "pr1", Number: 2, SourceBranch: "ao/mer-1/child", TargetBranch: "ao/mer-1/parent"}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("a stacked PR must not be reported as a wrong base: %v", msg.msgs)
	}
}

// The nudge renders through the injected Renderer like every other one, so an
// operator can reword it.
func TestPRObservation_WrongBaseUsesTemplateOverride(t *testing.T) {
	st := newFakeStore()
	msg := &fakeMessenger{}
	renderer := messagetemplates.NewRenderer(func() map[string]string {
		return map[string]string{string(messagetemplates.NamePRBaseMismatch): "CUSTOM {{.Base}} -> {{.Target}}"}
	})
	m := New(st, msg, WithMessageRenderer(renderer))
	st.sessions["mer-1"] = withTarget("mer-1", "main-fluke")

	o := ports.PRObservation{Fetched: true, URL: "pr1", Number: 287, TargetBranch: "main"}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 || msg.msgs[0] != "CUSTOM main -> main-fluke" {
		t.Fatalf("wrong-base nudge = %v, want template override applied", msg.msgs)
	}
}

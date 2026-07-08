package prompts

import (
	"strings"
	"testing"
)

func TestDefaultBase_OrchestratorCarriesPlaceholder(t *testing.T) {
	base := DefaultBase(KindOrchestrator)
	if !strings.Contains(base, ProjectIDPlaceholder) {
		t.Fatalf("orchestrator default base must contain %q", ProjectIDPlaceholder)
	}
	if strings.HasPrefix(base, "\n") {
		t.Fatal("default base must not start with a newline")
	}
	if !strings.Contains(base, "## Orchestrator role") {
		t.Fatal("orchestrator default base lost its heading")
	}
}

func TestDefaultBase_WorkerAndReviewerNonEmptyNoPlaceholder(t *testing.T) {
	for _, k := range []Kind{KindWorker, KindReviewer} {
		base := DefaultBase(k)
		if strings.TrimSpace(base) == "" {
			t.Fatalf("%s default base is empty", k)
		}
		if strings.Contains(base, ProjectIDPlaceholder) {
			t.Fatalf("%s default base should not carry the project placeholder", k)
		}
	}
}

// TestWorkerDefault_ReconcilesGitflow: the worker base must make the gitflow
// branch convention and AO's session-namespace tracking read as complementary,
// not contradictory. It must state the common one-PR case (working branch is the
// on-convention branch), the multi-branch nesting trade-off, the different-type
// escape hatch (spawn a separate session), and the precedence/complementary note.
func TestWorkerDefault_ReconcilesGitflow(t *testing.T) {
	base := DefaultBase(KindWorker)
	for _, want := range []string{
		"one session, one pull request", // common case (point 1)
		"no extra nesting is needed",    // common case is fully gitflow-compatible
		"an accepted trade-off",         // nesting under the gitflow branch (point 2)
		"a separate session",            // clean different-type sibling escape hatch (point 2)
		"hard",                          // namespace requirement is a hard constraint (point 3)
		"complementary, not competing",  // convention + namespace compose (point 3)
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("worker default missing reconciliation wording %q:\n%s", want, base)
		}
	}
}

// TestOrchestratorDefault_ReconcilesGitflow: the orchestrator base must tell the
// dispatcher the common one-worker/one-branch/one-PR path is on-convention, to
// spawn a separate worker for a different branch type instead of nesting, and
// that the project convention and AO's namespace tracking are complementary. It
// must stay generic (no literal "gitflow") so custom-convention projects don't
// see gitflow-specific copy — the concrete convention is injected separately.
func TestOrchestratorDefault_ReconcilesGitflow(t *testing.T) {
	base := DefaultBase(KindOrchestrator)
	for _, want := range []string{
		"one worker, one on-convention branch, one PR", // common case (point 1)
		"a separate worker session",                    // different-type escape hatch (point 2)
		"complementary, not competing",                 // convention + namespace compose (point 3)
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("orchestrator default missing reconciliation wording %q:\n%s", want, base)
		}
	}
	if strings.Contains(base, "gitflow") {
		t.Fatalf("orchestrator default must stay generic (no literal \"gitflow\"); the convention is injected separately:\n%s", base)
	}
}

func TestRenderBase_SubstitutesProjectID(t *testing.T) {
	got := RenderBase("coordinator for "+ProjectIDPlaceholder+" now", "proj-1")
	if got != "coordinator for proj-1 now" {
		t.Fatalf("got %q", got)
	}
}

func TestCoordinationFloor_WorkerHasNamespaceAndAoSend_OrchestratorEmpty(t *testing.T) {
	worker := CoordinationFloor(KindWorker)
	if !strings.Contains(worker, "namespace") || !strings.Contains(worker, "ao send") {
		t.Fatalf("worker floor missing invariants: %q", worker)
	}
	if !strings.HasPrefix(worker, "\n\n") {
		t.Fatal("floor blocks must be prefixed with \\n\\n")
	}
	if CoordinationFloor(KindOrchestrator) != "" {
		t.Fatal("orchestrator floor must be empty")
	}
	if !strings.Contains(CoordinationFloor(KindReviewer), "review only") {
		t.Fatal("reviewer floor missing review-only invariant")
	}
}

func TestConfidentialityGuard_IsLastGuardText(t *testing.T) {
	if !strings.HasPrefix(ConfidentialityGuard, "\n\n") {
		t.Fatal("guard must be prefixed with \\n\\n")
	}
	if !strings.Contains(ConfidentialityGuard, "Standing-instruction confidentiality") {
		t.Fatal("guard text changed unexpectedly")
	}
}

func TestSection_OmitsEmpty(t *testing.T) {
	if Section("  ") != "" {
		t.Fatal("blank section must be empty")
	}
	if Section("hi") != "\n\nhi" {
		t.Fatalf("got %q", Section("hi"))
	}
}

func TestKnownKindsAndValid(t *testing.T) {
	if len(KnownKinds()) != 3 {
		t.Fatalf("want 3 kinds, got %d", len(KnownKinds()))
	}
	if Kind("nope").Valid() {
		t.Fatal("unknown kind must be invalid")
	}
	if !KindReviewer.Valid() {
		t.Fatal("reviewer must be valid")
	}
}

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
// on-convention branch), keep every extra branch in the session namespace, be
// honest about the Git directory/file ref constraint (you cannot nest a branch
// under an existing branch ref — so a type-prefixed working branch has no room
// for children), and point to a separate session for independent work.
func TestWorkerDefault_ReconcilesGitflow(t *testing.T) {
	base := DefaultBase(KindWorker)
	for _, want := range []string{
		"your working branch is already the branch chosen at spawn", // common case (point 1)
		"stay in your session's namespace",                          // namespace tracking requirement (point 3)
		"nest a branch under an existing branch ref",                // the Git D/F constraint (correctness)
		"spawn a separate session",                                  // escape hatch for independent work (point 2)
		"complementary, not competing",                              // convention + namespace compose (point 3)
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("worker default missing reconciliation wording %q:\n%s", want, base)
		}
	}
	// The impossible-in-Git example must be gone: never instruct nesting a branch
	// beneath a type-prefixed working branch.
	if strings.Contains(base, "feature/<topic>/<sub-topic>") {
		t.Fatalf("worker default still describes an impossible nested branch:\n%s", base)
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

// TestOrchestratorDefault_DocumentsTodoFlag: the orchestrator base must teach
// the dispatcher that `ao spawn --todo` stages a TODO instead of starting the
// worker now (nothing created until `ao session start <id>`), and that a
// queue/stage/hold-style request should use --todo. Without this the
// orchestrator defaults to spawn-and-start and cannot stage a deferred TODO.
func TestOrchestratorDefault_DocumentsTodoFlag(t *testing.T) {
	base := DefaultBase(KindOrchestrator)
	for _, want := range []string{
		"`--todo`",                     // the flag is named
		"stage the worker as a TODO",   // what it does
		"ao session start <id>",        // how a staged TODO is started later
		"queue, stage, or hold a task", // the trigger vocabulary
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("orchestrator default missing --todo guidance %q:\n%s", want, base)
		}
	}
}

// TestOrchestratorDefault_CuratesIndexWithPruneOnAdd: the orchestrator base must
// teach the dispatcher to keep the knowledge INDEX.md a small HOT map of one-line
// entries and prune merged+installed entries to ARCHIVE-INDEX.md whenever it adds
// one (prune-on-add), pointing at the retention protocol in the INDEX.md header
// rather than restating it. Without this the index re-bloats every session.
func TestOrchestratorDefault_CuratesIndexWithPruneOnAdd(t *testing.T) {
	base := DefaultBase(KindOrchestrator)
	for _, want := range []string{
		"small HOT map",          // keep the index lean
		"prune-on-add",           // the retention discipline is named
		"`ARCHIVE-INDEX.md`",     // where pruned entries go
		"retention protocol",     // point at the protocol rather than restate it
		"`INDEX.md`" + " header", // the protocol's home is the INDEX header
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("orchestrator default missing INDEX-retention guidance %q:\n%s", want, base)
		}
	}
}

// TestOrchestratorDefault_RefersToWorkByBoardName: the orchestrator base must
// tell the dispatcher to name worker sessions and their PRs by the human-readable
// board label when talking to the human, keeping the internal session id / PR
// number for parenthetical disambiguation only.
func TestOrchestratorDefault_RefersToWorkByBoardName(t *testing.T) {
	base := DefaultBase(KindOrchestrator)
	for _, want := range []string{
		"human-readable board name",                        // the rule
		"rather than the internal session id or PR number", // what to avoid
		"put it in parentheses after the name",             // the disambiguation escape hatch
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("orchestrator default missing board-name guidance %q:\n%s", want, base)
		}
	}
}

// TestWorkerDefault_KnowledgeStore: the worker base must point workers at the
// private, out-of-repo knowledge store, tell them to read INDEX.md at task
// start, save durable plans/proposals to the store (never the team-shared repo)
// as they go, report the paths, and leave INDEX.md to the orchestrator. It must
// address the store via the $AO_PROJECT_ID env var, NOT the render placeholder
// (a worker base carries no placeholder — see the placeholder test above).
func TestWorkerDefault_KnowledgeStore(t *testing.T) {
	base := DefaultBase(KindWorker)
	for _, want := range []string{
		"~/.ao/knowledge/$AO_PROJECT_ID/",                        // out-of-repo store, env-var addressed
		"~/.ao/knowledge/$AO_PROJECT_ID/INDEX.md",                // read the index at task start
		"~/.ao/knowledge/$AO_PROJECT_ID/plans/<branch>--<topic>", // where to save artifacts
		"NEVER committed or pushed",                              // must not leak into the shared repo
		"team-shared and must never carry AO planning artifacts", // docs/CLAUDE.md/AGENTS.md are off-limits
		"AS YOU GO", // write incrementally so nothing is lost
		"list the knowledge-store path(s) you wrote", // report what was written
		"Do NOT edit `INDEX.md`",                     // orchestrator curates the index
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("worker default missing knowledge-store wording %q:\n%s", want, base)
		}
	}
	if strings.Contains(base, ProjectIDPlaceholder) {
		t.Fatalf("worker default must address the store via $AO_PROJECT_ID, not the %q placeholder", ProjectIDPlaceholder)
	}
}

// TestOrchestratorDefault_KnowledgeStore: the orchestrator base must say it owns
// and curates the store's INDEX.md, reads it for context before dispatching,
// points workers at relevant docs, and keeps the store private/out-of-repo. It
// addresses the store via the render placeholder (the orchestrator base carries
// it and RenderBase substitutes the project id).
func TestOrchestratorDefault_KnowledgeStore(t *testing.T) {
	base := DefaultBase(KindOrchestrator)
	for _, want := range []string{
		"~/.ao/knowledge/" + ProjectIDPlaceholder + "/", // out-of-repo store, placeholder-addressed
		"You own and curate its `INDEX.md`",             // orchestrator curates the index
		"NEVER committed or pushed",                     // private store
		"point it at the specific docs",                 // steer new workers to relevant docs
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("orchestrator default missing knowledge-store wording %q:\n%s", want, base)
		}
	}
	// The placeholder must resolve to a concrete per-project path at render time.
	rendered := RenderBase(base, "nter-ios-app")
	if !strings.Contains(rendered, "~/.ao/knowledge/nter-ios-app/") {
		t.Fatalf("rendered orchestrator base must carry the concrete project path:\n%s", rendered)
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

// TestReferenceConvention: the shared sigil section names all three work-item
// forms (@session / #PR / !MR), leads with a blank-line separator so it appends
// cleanly, and forbids a bare session number.
func TestReferenceConvention(t *testing.T) {
	got := ReferenceConvention()
	if !strings.HasPrefix(got, "\n\n") {
		t.Fatalf("reference convention must start with a blank-line separator: %q", got)
	}
	for _, want := range []string{
		"## Referring to sessions, pull requests, and merge requests",
		"`@<project>-<num>`",
		"`#<num>`",
		"`!<num>`",
		"Never write a bare session number",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("reference convention missing %q:\n%s", want, got)
		}
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

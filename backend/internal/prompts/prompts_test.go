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

// TestDefaultBase_WorkerCarriesPlaceholder: the worker default now addresses the
// private knowledge store through the shared {{.ProjectID}} template action, the
// same as the orchestrator base, so RenderBase substitutes the concrete project
// id consistently across both kinds (replacing the older $AO_PROJECT_ID env var).
func TestDefaultBase_WorkerCarriesPlaceholder(t *testing.T) {
	base := DefaultBase(KindWorker)
	if strings.TrimSpace(base) == "" {
		t.Fatal("worker default base is empty")
	}
	if !strings.Contains(base, ProjectIDPlaceholder) {
		t.Fatalf("worker default base must carry %q so it renders like the orchestrator base", ProjectIDPlaceholder)
	}
	if strings.HasPrefix(base, "\n") {
		t.Fatal("worker default base must not start with a newline")
	}
}

// TestDefaultBase_ReviewerNonEmptyNoPlaceholder: the reviewer default is a short
// review-only role prompt with no knowledge-store need, so it ships without the
// {{.ProjectID}} action. Rendering is still wired for the reviewer kind (see the
// review package's reviewTexts) so an author CAN use the placeholder in a reviewer
// override and get the same substitution.
func TestDefaultBase_ReviewerNonEmptyNoPlaceholder(t *testing.T) {
	base := DefaultBase(KindReviewer)
	if strings.TrimSpace(base) == "" {
		t.Fatal("reviewer default base is empty")
	}
	if strings.Contains(base, ProjectIDPlaceholder) {
		t.Fatalf("reviewer default base ships without the placeholder (no store need):\n%s", base)
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

// TestOrchestratorDefault_DocumentsTargetFlag: the orchestrator base must teach
// the dispatcher that --from and --target are DISTINCT — --from is the ref the
// worktree is cut from, --target the branch the PR merges into — and that
// --target is optional, resolving to --from when omitted. Without this the
// dispatcher conflates the two and can never spawn a worker that branches off
// one line and lands on another (e.g. a hotfix cut from a release branch).
func TestOrchestratorDefault_DocumentsTargetFlag(t *testing.T) {
	base := DefaultBase(KindOrchestrator)
	for _, want := range []string{
		"`--target <branch>`", // the flag is named
		"CUT FROM",            // what --from means
		"MERGES INTO",         // what --target means
		"resolves to --from",  // it is optional, not required
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("orchestrator default missing --target guidance %q:\n%s", want, base)
		}
	}
}

// TestWorkerDefault_TargetsRecordedPRTarget: the worker base must point the worker
// at the session's RECORDED PR target (the `--target` chosen at spawn) rather than
// assuming it equals the branch the worktree was cut from. Without this a worker
// spawned with a distinct --target opens its PR against the wrong branch.
func TestWorkerDefault_TargetsRecordedPRTarget(t *testing.T) {
	base := DefaultBase(KindWorker)
	for _, want := range []string{
		"recorded PR target", // the concept is named
		"`--target`",         // where it comes from
		"may differ from it", // it is not necessarily the base ref
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("worker default missing PR-target guidance %q:\n%s", want, base)
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

// TestOrchestratorDefault_DocumentsTaskSizeFlag: the orchestrator base must teach
// the dispatcher that `ao spawn --task-size mechanical` lets a small change skip
// the process-skill ceremony (edit + verify only), that real features/bugfixes
// keep full rigor, and name the default. Without this the orchestrator never tags
// task size and every worker pays full ceremony.
func TestOrchestratorDefault_DocumentsTaskSizeFlag(t *testing.T) {
	base := DefaultBase(KindOrchestrator)
	for _, want := range []string{
		"`--task-size mechanical`",                                // the flag + the size that skips ceremony
		"skip the up-front requirements→plan→test-first ceremony", // what mechanical buys
		"default `standard`",                                      // the default is documented
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("orchestrator default missing --task-size guidance %q:\n%s", want, base)
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
// address the store via the {{.ProjectID}} render placeholder — the same
// mechanism as the orchestrator base — so RenderBase substitutes the concrete
// project id (replacing the older $AO_PROJECT_ID env var).
func TestWorkerDefault_KnowledgeStore(t *testing.T) {
	base := DefaultBase(KindWorker)
	for _, want := range []string{
		"~/.ao/knowledge/" + ProjectIDPlaceholder + "/",                        // out-of-repo store, placeholder-addressed
		"~/.ao/knowledge/" + ProjectIDPlaceholder + "/INDEX.md",                // read the index at task start
		"~/.ao/knowledge/" + ProjectIDPlaceholder + "/plans/<branch>--<topic>", // where to save artifacts
		"NEVER committed or pushed",                                            // must not leak into the shared repo
		"team-shared and must never carry AO planning artifacts",               // docs/CLAUDE.md/AGENTS.md are off-limits
		"AS YOU GO", // write incrementally so nothing is lost
		"list the knowledge-store path(s) you wrote", // report what was written
		"Do NOT edit `INDEX.md`",                     // orchestrator curates the index
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("worker default missing knowledge-store wording %q:\n%s", want, base)
		}
	}
	// The placeholder must resolve to a concrete per-project path at render time.
	rendered := RenderBase(base, "demo-ios-app")
	if !strings.Contains(rendered, "~/.ao/knowledge/demo-ios-app/") {
		t.Fatalf("rendered worker base must carry the concrete project path:\n%s", rendered)
	}
	if strings.Contains(rendered, "$AO_PROJECT_ID") {
		t.Fatalf("worker default must address the store via %q, not the $AO_PROJECT_ID env var", ProjectIDPlaceholder)
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
	rendered := RenderBase(base, "demo-ios-app")
	if !strings.Contains(rendered, "~/.ao/knowledge/demo-ios-app/") {
		t.Fatalf("rendered orchestrator base must carry the concrete project path:\n%s", rendered)
	}
}

func TestRenderBase_SubstitutesProjectID(t *testing.T) {
	got := RenderBase("coordinator for "+ProjectIDPlaceholder+" now", "proj-1")
	if got != "coordinator for proj-1 now" {
		t.Fatalf("got %q", got)
	}
}

// TestRenderBase_WorkerDefaultExpandsProjectID: the worker default base must now
// carry the {{.ProjectID}} template action and expand it to the concrete project
// id under RenderBase — the same mechanism as the orchestrator base, replacing
// the older $AO_PROJECT_ID env-var addressing so every session kind renders
// consistently.
func TestRenderBase_WorkerDefaultExpandsProjectID(t *testing.T) {
	base := DefaultBase(KindWorker)
	if !strings.Contains(base, ProjectIDPlaceholder) {
		t.Fatalf("worker default base must carry %q so it renders like the orchestrator base:\n%s", ProjectIDPlaceholder, base)
	}
	rendered := RenderBase(base, "demo-ios-app")
	if strings.Contains(rendered, ProjectIDPlaceholder) {
		t.Fatalf("worker base still carries an unexpanded placeholder after render:\n%s", rendered)
	}
	if !strings.Contains(rendered, "~/.ao/knowledge/demo-ios-app/") {
		t.Fatalf("rendered worker base must carry the concrete project path:\n%s", rendered)
	}
	if strings.Contains(rendered, "$AO_PROJECT_ID") {
		t.Fatalf("worker base must no longer address the store via the $AO_PROJECT_ID env var:\n%s", rendered)
	}
}

// TestRenderBase_TemplateSemantics: RenderBase is a Go text/template render, so a
// base with no actions is byte-for-byte unchanged, and a malformed / unknown-field
// base must not crash prompt assembly — it falls back to the RAW base whole (never
// a partial render, never empty). A bad hand-authored override degrades to literal
// text on the critical spawn path instead of a missing system prompt.
func TestRenderBase_TemplateSemantics(t *testing.T) {
	// No actions: byte-for-byte unchanged. An older override that still documents
	// the store via $AO_PROJECT_ID keeps working (the worker resolves that env var
	// at runtime), so backward compatibility holds with no per-user migration.
	plain := "store at ~/.ao/knowledge/$AO_PROJECT_ID/ stays literal"
	if got := RenderBase(plain, "p"); got != plain {
		t.Fatalf("plain text must render unchanged, got %q", got)
	}
	// Malformed or unknown-field templates fall back to the RAW base whole, not a
	// partial substitution: a valid {{.ProjectID}} sitting next to an invalid
	// action is left literal rather than half-rendered, so the failure is total and
	// obvious rather than a silently corrupted prompt.
	for _, bad := range []string{
		"unterminated {{ action",
		"stray close }} brace",
		"valid " + ProjectIDPlaceholder + " but unknown {{.Nope}} field",
	} {
		if got := RenderBase(bad, "p"); got != bad {
			t.Fatalf("malformed base must fall back to the raw base, got %q for input %q", got, bad)
		}
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

// Removing any of these rules re-opens the failure mode where a child agent
// creates .claude/worktrees/agent-* outside the AO worker branch.
func TestCoordinationFloor_WorkerOwnsOneWorktreeForSameTaskChildren(t *testing.T) {
	worker := CoordinationFloor(KindWorker)
	for _, want := range []string{
		"already runs in an AO-managed git worktree",
		"Agent with `isolation: \"worktree\"`",
		"do not call `EnterWorktree`",
		"one file-writing or implementation child at a time",
		"Read-only children may run concurrently",
		"must not commit, stash, reset, switch or create branches",
	} {
		if !strings.Contains(worker, want) {
			t.Fatalf("worker floor missing same-worktree rule %q:\n%s", want, worker)
		}
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

// TestTaskSizeDirective_MechanicalAuthorizesSkip: a mechanical task must render a
// "\n\n"-prefixed block that (a) names itself, (b) explicitly authorizes skipping
// the process skills, (c) grounds the skip as a deliberate override of the "you
// MUST use skills" hook via the user-instructions-win rule, and (d) carries the
// safety valve to escalate to full process if the task turns out bigger.
func TestTaskSizeDirective_MechanicalAuthorizesSkip(t *testing.T) {
	got := TaskSizeDirective("mechanical")
	if !strings.HasPrefix(got, "\n\n") {
		t.Fatalf("mechanical directive must start with a blank-line separator: %q", got)
	}
	for _, want := range []string{
		"## Task size: mechanical (AO)",
		"authorized to SKIP", // the skip is granted
		"do not open any skill that interviews the human for requirements", // which skills, by function - never by plugin/skill name
		"overrides any \"you MUST use skills\"",                            // grounded against the hook
		"user instructions take precedence over skills",
		"stop and apply the full process", // safety valve
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("mechanical directive missing %q:\n%s", want, got)
		}
	}
}

// TestTaskSizeDirective_StandardDeepAndUnknownRenderNothing: only `mechanical`
// alters the prompt. `standard` (the default), `deep`, empty, and any unknown
// value must render the empty string so the majority worker path stays byte-for-
// byte unchanged and spends no extra tokens (user decision 2026-07-13: deep ==
// standard for prompt purposes).
func TestTaskSizeDirective_StandardDeepAndUnknownRenderNothing(t *testing.T) {
	for _, size := range []string{"standard", "deep", "", "STANDARD", "huge"} {
		if got := TaskSizeDirective(size); got != "" {
			t.Fatalf("TaskSizeDirective(%q) = %q, want empty", size, got)
		}
	}
}

// TestWorkerDefault_ContextEconomy: the worker base must carry the token-economy
// guidance (R3a/b/c): read only the entries the brief names (not the whole
// INDEX), prefer ranged/targeted reads of large files, and cap screenshots per
// verify pass, under a scannable heading.
func TestWorkerDefault_ContextEconomy(t *testing.T) {
	base := DefaultBase(KindWorker)
	for _, want := range []string{
		"## Context economy (AO)",
		"Read only the specific knowledge-store entries your brief names", // R3a
		"ranged read with offset/limit",                                   // R3b targeted reads
		"take screenshots sparingly",                                      // R3c screenshot cap
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("worker default missing context-economy wording %q:\n%s", want, base)
		}
	}
	// R3a must also have softened the standing "read INDEX.md" pointer: the base
	// must no longer tell workers to read the whole index up front.
	if strings.Contains(base, "read `~/.ao/knowledge/"+ProjectIDPlaceholder+"/INDEX.md` if it exists, plus any docs it points to") {
		t.Fatalf("worker default still tells workers to slurp the whole INDEX:\n%s", base)
	}
}

// TestWorkerDefault_ConfirmBeforeReviewReply: the worker base must carry the
// durable standing rule that review feedback is answered by MAKING the change but
// HOLDING the reply until the human confirms. The nudge templates say the same
// thing, but they are operator-editable defaults - the system prompt is the
// backstop that survives an operator editing or clearing them.
func TestWorkerDefault_ConfirmBeforeReviewReply(t *testing.T) {
	base := DefaultBase(KindWorker)
	for _, want := range []string{
		"make the requested code change", // the change itself is not deferred
		"do NOT post a reply comment",    // posting waits
		"resolve/close a review thread",  // resolving waits too
		"until the human has confirmed",  // the gate
		"draft your reply",               // what to do instead
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("worker default missing confirm-before-reply wording %q:\n%s", want, base)
		}
	}
	// Reviewer and orchestrator bases are untouched by this rule.
	if strings.Contains(DefaultBase(KindOrchestrator), "do NOT post a reply comment") {
		t.Fatal("the confirm-before-reply rule belongs to the worker base only")
	}
}

func TestKnownKindsAndValid(t *testing.T) {
	want := []Kind{KindOrchestrator, KindWorker, KindQA, KindReviewer}
	got := KnownKinds()
	if len(got) != len(want) {
		t.Fatalf("want %d kinds, got %d: %v", len(want), len(got), got)
	}
	for i, k := range want {
		if got[i] != k {
			t.Fatalf("kind %d = %q, want %q (KnownKinds order is the order the editors render in)", i, got[i], k)
		}
		if !k.Valid() {
			t.Fatalf("%q must be valid", k)
		}
		if DefaultBase(k) == "" {
			t.Fatalf("%q has no built-in default, so Reset-to-default would blank it", k)
		}
	}
	if Kind("nope").Valid() {
		t.Fatal("unknown kind must be invalid")
	}
}

// TestQADefaultIsQAsJobAndNotDevs pins the two halves of the qa base that make it
// a different agent rather than a second dev: what it owns (triage, running,
// reporting, the four human-only shapes) and what it must not do (implement, open
// the PR, report to the orchestrator).
func TestQADefaultIsQAsJobAndNotDevs(t *testing.T) {
	base := DefaultBase(KindQA)
	for _, want := range []string{
		"qa",
		"you RUN them, and you report what happened",
		"If there is nothing here to exercise at all",
		"say so in your handback",
		"paint",
		"focus",
		"timing",
		"feel",
		"do not open or update the pull request",
		"test:",
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("qa default missing %q:\n%s", want, base)
		}
	}
	// It is qa's own base, not a copy of dev's.
	if strings.Contains(base, "Most sessions open one pull request") {
		t.Fatal("the qa base carries dev's pull-request instructions")
	}
}

// THE HANDBACK. The first full crew run stalled because qa finished and simply
// stopped: dev was asleep, the queue was empty, and nothing on the board said
// nobody was working. qa's obligation to report is what closes that, and it
// lives in the FLOOR because a base is editable and this rule is not.
func TestCoordinationFloor_QAMustHandBackWhenItFinishes(t *testing.T) {
	qa := CoordinationFloor(KindQA)
	for _, want := range []string{
		// It reaches dev by ROLE - the only address that cannot go stale, since a
		// crew is formed after dev's runtime is already launched.
		"ao send --crew dev --about",
		// Always - nothing to exercise is a result too.
		"passed, failed, or with nothing to exercise",
		// What dev needs in order to act without re-deriving it.
		"git rev-parse --short HEAD",
		"the COMMIT you tested",
		"what you SAW, check by check, and the evidence each rests on",
		"Pass and fail cite evidence the same way",
		// Undriveable is a finding from an attempt, never a guess.
		"marked UNDRIVEABLE with the reason from an attempt",
		"never a guess made before trying",
		"what is left for a person to check by hand, and why a machine cannot",
		// The stopping rule, reusing the cap AO already has rather than a new one -
		// and it is now MECHANISM, so the prompt says what actually happens.
		"One message per finish",
		"REFUSED by AO",
	} {
		if !strings.Contains(qa, want) {
			t.Fatalf("qa floor missing handback rule %q:\n%s", want, qa)
		}
	}
	// qa is a worker session, so it keeps every worker invariant as well.
	if !strings.Contains(qa, "namespace") || !strings.Contains(qa, "already runs in an AO-managed git worktree") {
		t.Fatalf("qa floor dropped the worker invariants:\n%s", qa)
	}
	if !strings.HasPrefix(qa, "\n\n") {
		t.Fatal("floor blocks must be prefixed with \\n\\n")
	}
}

// A SOLO worker is what almost every session on this machine is, and it has no
// dev to hand back to. The obligation must not reach it.
func TestCoordinationFloor_SoloWorkerHasNoHandbackObligation(t *testing.T) {
	worker := CoordinationFloor(KindWorker)
	if strings.Contains(worker, "Handing back (AO)") {
		t.Fatalf("the worker floor must not carry qa's handback obligation:\n%s", worker)
	}
	if CoordinationFloor(KindQA) != qaCoordinationFloor+workerFloor+qaHandbackFloor {
		t.Fatal("the qa floor must share workerFloor with the worker floor, so worker invariants cannot drift apart")
	}
	if !strings.HasSuffix(worker, workerFloor) {
		t.Fatal("the worker floor must end with the shared workerFloor")
	}
}

// The base says what qa is FOR and is editable; it must still point at the
// handback rather than telling qa to stop, so base and floor do not contradict.
func TestQABase_TellsItToHandBackRatherThanJustStop(t *testing.T) {
	base := DefaultBase(KindQA)
	if !strings.Contains(base, "ao send --crew dev --about") {
		t.Fatalf("qa base does not point at the handback:\n%s", base)
	}
	if !strings.Contains(base, "do not stop SILENTLY") {
		t.Fatalf("qa base still ends the turn without a handback:\n%s", base)
	}
}

// MISSING 1 - the record -> flow -> commit loop was NOBODY's. The tooling shipped
// long ago and no prompt said whose job it was, so what was left for a person
// never shrank.
// This holds the loop to the commands that actually exist (verified against the
// real `ao` binary): a prompt naming a flag the CLI does not have is worse than
// no prompt.
func TestRecordedFlowLoop_IsQAsAndTeachesTheWholeLoop(t *testing.T) {
	loop := RecordedFlowLoop()
	for _, want := range []string{
		"ao sim claim",
		"ao sim flow record start --name",
		"ao sim flow record status",
		"ao sim flow record stop --entry",
		"ao sim flow check",
		"ao sim flow run",
		// The fact that makes one human play usable at all: the recorder hooks
		// the hold, so their tap and qa's command are captured identically.
		"Device tab",
		"captured identically",
		// The loop only pays off if the check comes OFF the human's list.
		"Commit the flow; the next run of this check is the flow, not a person",
	} {
		if !strings.Contains(loop, want) {
			t.Fatalf("the recorded-flow loop is missing %q:\n%s", want, loop)
		}
	}
	if !strings.HasPrefix(loop, "\n\n") {
		t.Fatal("injected blocks must be prefixed with \\n\\n")
	}
}

// qa can read the paint/focus/timing/feel SHAPE two opposite wrong ways ("not my
// business" / "I ran it, so it passed"), and the rule that used to settle it -
// judge nothing in those four categories - was too broad: if qa photographed the
// layout and the content is visibly clipped, forbidding it to say so throws away
// what it saw and makes the human re-derive it (explicit user decision).
//
// So the test is now about the SUFFICIENCY OF THE EVIDENCE, not the category of
// the case, and this pins the three parts that keep that latitude from drifting
// back into "looks fine to me": the same bar for pass and fail (an asymmetric
// one was proposed and rejected), a citation requirement, and reporting what it
// saw without concluding when the capture cannot settle the check.
func TestQADefault_JudgesBySufficiencyOfEvidenceNotByCategory(t *testing.T) {
	base := DefaultBase(KindQA)
	for _, want := range []string{
		// Driving a person's check is ALLOWED - it is how the evidence gets captured.
		"You may drive any check, including one meant for a person",
		// The test that replaced the blanket prohibition.
		"does this evidence answer what the check asks?",
		// Symmetric, deliberately.
		"Pass and fail carry the SAME bar",
		// The guard that has to come with the latitude.
		"a verdict must cite what in the evidence supports it",
		// And what is left when the capture cannot settle it.
		"report what you SAW without concluding",
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("the qa base does not state judge-when-the-evidence-answers-it: missing %q:\n%s", want, base)
		}
	}
	// The blanket category prohibition must be GONE, not merely contradicted
	// somewhere else in the block: two rules that disagree leave qa to pick.
	if strings.Contains(base, "never do is JUDGE") {
		t.Error("the qa base still carries the blanket judge-nothing rule beside its replacement")
	}
}

// TestCrewProtocol_DevIsToldHowToSummonItsQA. dev's system prompt is fixed when
// its runtime launches and the crew does not exist then, so the block has to be
// true BOTH before and after the join. What changed is WHOSE act the join is:
// AO used to create the qa by observing dev drive the app, and dev was told so as
// something it could neither ask for nor avoid. dev asks now, and the prompt is
// the only place it can learn the verb - no brief and no other block names it.
func TestCrewProtocol_DevIsToldHowToSummonItsQA(t *testing.T) {
	dev := CrewProtocol("dev")
	for _, want := range []string{
		"You are working this task ALONE right now",
		// THE VERB, and the TIME - the time is the whole content of the change.
		"`ao crew review`",
		"When you believe the change is DONE",
		// Nothing else creates one, and forgetting is not silent.
		"Nothing else creates one",
		"AO says so in the report you send",
		// And what changes when it happens.
		"both running at once",
	} {
		if !strings.Contains(dev, want) {
			t.Fatalf("crew dev is not told how to get its qa: missing %q:\n%s", want, dev)
		}
	}
	// The retired trigger must be GONE rather than merely contradicted later in
	// the block: a dev that reads "AO creates one when you drive the app" will
	// wait for it, and nothing is coming.
	for _, gone := range []string{
		"AO creates a qa the first time you touch",
		"That is an observation, not a request",
	} {
		if strings.Contains(dev, gone) {
			t.Fatalf("crew dev still carries the retired runtime-touch trigger: %q\n%s", gone, dev)
		}
	}
	// qa's opening is unchanged in substance: by the time a qa exists, dev has
	// been working for a while and is still working.
	qa := CrewProtocol("qa")
	if !strings.Contains(qa, "you are both running right now") {
		t.Fatalf("qa is not told its crewmate is live:\n%s", qa)
	}
	if strings.Contains(qa, "ao crew review") {
		t.Fatalf("qa was handed dev's verb for summoning a qa:\n%s", qa)
	}
}

// A solo worker is in no crew, so it renders no crew block at all; both members
// name a message's subject as a commit or a Testiny id, never a removed case id.
func TestCrewProtocol_SoloRendersNothingAndAboutNamesAnArtifact(t *testing.T) {
	if CrewProtocol("") != "" {
		t.Fatalf("a solo worker must render no crew block:\n%s", CrewProtocol(""))
	}
	for _, role := range []string{"dev", "qa"} {
		block := CrewProtocol(role)
		if !strings.Contains(block, "--about <commit-sha|testiny-id>") {
			t.Fatalf("crew %s is not told what --about names:\n%s", role, block)
		}
		if !strings.Contains(block, "`$AO_CREW_ID`"+" is the TASK (dev's id), which `ao testiny` and `ao session get` take") {
			t.Fatalf("crew %s is not told which commands take the task id:\n%s", role, block)
		}
	}
}

// A project without Testiny renders no Testiny block, whoever the worker is; one
// with it says where drafts go in the AO project's own store, for every worker
// kind. It names no Testiny project of its own: the agent picks one per task
// from the Jira key, and asks the human when the key does not settle it.
func TestTestinyProtocol_RendersOnlyWhereTheProjectUsesTestiny(t *testing.T) {
	for _, role := range []string{"", "dev", "qa"} {
		if got := TestinyProtocol(false, "mer", role, &MobileScripts{Product: "nter", IOS: true, Store: "/store"}); got != "" {
			t.Fatalf("role %q: a project without Testiny rendered a Testiny block:\n%s", role, got)
		}
		got := TestinyProtocol(true, "mer", role, nil)
		for _, want := range []string{
			"\n\n## Testiny test cases (AO)\n",
			"`~/.ao/knowledge/mer/plans/<branch>--testiny.md`",
			"STAR-2413 is in STAR",
			"MOBILITY project, whose key is MOB",
			"ask the human which project",
			"`ao testiny link \"$AO_CREW_ID\" <run-id> --project <KEY>`",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("role %q: the Testiny block is missing %q:\n%s", role, want, got)
			}
		}
		if strings.Contains(got, "Testiny project `") {
			t.Fatalf("role %q: the Testiny block names a fixed Testiny project:\n%s", role, got)
		}
	}
}

// crewProtocolBudget caps the block EVERY crew member reads on every turn. It is
// a deliberate number, not a fence around the current text. Raise it only with a
// reason, the way the sim guidance budget is raised.
const crewProtocolBudget = 4200

func TestCrewProtocol_StaysWithinItsBudget(t *testing.T) {
	for _, role := range []string{"dev", "qa"} {
		if n := len(CrewProtocol(role)); n > crewProtocolBudget {
			t.Errorf("the %s crew protocol is %d bytes, over its %d-byte budget: it is read on every turn by every crew member, so either cut something or raise the budget deliberately", role, n, crewProtocolBudget)
		}
	}
}

// TestCheckInGate_StandardAndDeepStopBeforeImplementing: on a project that opted
// in, a standard/deep worker must be told (a) it is two turns, (b) orientation -
// reading, searching, writing into the knowledge store - is allowed first, (c)
// the implementation itself is what must wait, (d) the pause is taken by ENDING
// THE TURN, which is the only thing that puts the task in the board's Needs you
// lane, (e) sitting quietly mid-turn is forbidden precisely because it is
// indistinguishable from a hang, (f) the hand-back is short and says what was
// understood / what is intended / what needs deciding, and (g) the human's reply
// is the go-ahead.
func TestCheckInGate_StandardAndDeepStopBeforeImplementing(t *testing.T) {
	for _, size := range []string{"standard", "deep", "", "STANDARD", "huge"} {
		got := CheckInGate(size)
		if !strings.HasPrefix(got, "\n\n") {
			t.Fatalf("CheckInGate(%q) must start with a blank-line separator: %q", size, got)
		}
		for _, want := range []string{
			"## Check in before you implement (AO)",
			"TWO turns",
			"knowledge store",                     // orientation is allowed
			"no edit to a file in the repository", // the implementation is what waits
			"END YOUR TURN",                       // the mechanism, not "wait"
			"**Needs you**",                       // where it becomes visible
			"identical to having hung",            // why a quiet mid-turn pause is wrong
			"what you understand the task to be",
			"what you need decided",
			"reply is your go-ahead",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("CheckInGate(%q) missing %q:\n%s", size, want, got)
			}
		}
	}
}

// TestCheckInGate_OutranksTheTaskBrief is the load-bearing half of the fix. The
// gate is a general rule that arrives EARLY in the prompt; the orchestrator's
// task brief is specific, concrete, arrives later, and reads as the actual
// assignment - and every brief AO's orchestrator writes today ends in some form
// of "implement it, watch CI to green, then report". A worker can reasonably
// follow the brief and never pause, which makes a setting the human deliberately
// turned on do nothing, silently.
//
// So the gate must resolve that conflict IN ITS OWN TEXT, without depending on
// the brief's author cooperating: the brief may have been written by an
// orchestrator that predates this, typed by hand, or carried in by a restored
// session. This asserts the RULE rather than one sentence of it - each group
// below is satisfied by any of several phrasings, so the wording stays free to
// change while the guarantee does not.
func TestCheckInGate_OutranksTheTaskBrief(t *testing.T) {
	for _, size := range []string{"standard", "deep", "", "huge"} {
		got := strings.ToLower(CheckInGate(size))
		groups := []struct {
			what string
			any  []string
		}{
			// It says outright that it wins, and names WHAT it wins over.
			{"a precedence claim", []string{"outranks", "takes precedence", "this section wins", "overrides"}},
			{"the thing it outranks: the task brief", []string{"task brief", "your brief", "the brief"}},
			// And it wins specifically over the instructions a brief actually
			// carries - the ones that read as permission to run straight through.
			{"a brief that says to open the PR", []string{"open the pull request", "opening the pull request", "pull request"}},
			{"a brief that says to watch CI", []string{"watch ci", "ci to green"}},
			{"a brief that says not to stop", []string{"do not stop", "straight through", "without stopping", "run through"}},
			// Whatever the brief said, turn one still ends at the hand-back.
			{"turn one ends regardless of the wording", []string{"however the task was worded", "however it was worded", "whatever the brief says", "no matter how"}},
		}
		for _, g := range groups {
			if !containsAny(got, g.any) {
				t.Fatalf("CheckInGate(%q) must assert %s (one of %q); a gate that does not outrank the brief is a setting that silently does nothing:\n%s", size, g.what, g.any, CheckInGate(size))
			}
		}
	}
}

// containsAny reports whether s contains at least one of the alternatives. It
// keeps the precedence assertions about the RULE rather than about one exact
// sentence of prompt prose.
func containsAny(s string, alternatives []string) bool {
	for _, a := range alternatives {
		if strings.Contains(s, a) {
			return true
		}
	}
	return false
}

// TestCheckInGateBriefingNote_TellsTheOrchestrator: the other half. An
// orchestrator that does not know the gate exists writes briefs that fight it,
// so the note has to (a) say the gate is on here, (b) say it is enforced on the
// worker's side whatever the brief says, (c) tell the orchestrator to describe
// the task instead of scripting a run-through to a merged PR, and (d) keep the
// mechanical exemption honest so it is not used as an escape hatch by accident.
func TestCheckInGateBriefingNote_TellsTheOrchestrator(t *testing.T) {
	got := CheckInGateBriefingNote()
	if !strings.HasPrefix(got, "\n\n") {
		t.Fatalf("CheckInGateBriefingNote must start with a blank-line separator: %q", got)
	}
	low := strings.ToLower(got)
	for _, want := range []string{
		"check-in gate on", // the project fact the orchestrator could not see
		"ends its turn",    // what a worker here actually does
		"**needs you**",    // where the human finds it
		"outranks",         // the brief cannot win, so do not try
		"mechanical",       // the one real exemption
	} {
		if !strings.Contains(low, strings.ToLower(want)) {
			t.Fatalf("CheckInGateBriefingNote missing %q:\n%s", want, got)
		}
	}
	if !containsAny(low, []string{"watch ci to green", "ci to green"}) {
		t.Fatalf("the note must name the run-through instruction it is replacing:\n%s", got)
	}
}

// TestCheckInGateBriefingNote_NamesNoSkillOrPlugin holds convention #278 on the
// orchestrator side too, for the same reason the gate does.
func TestCheckInGateBriefingNote_NamesNoSkillOrPlugin(t *testing.T) {
	got := strings.ToLower(CheckInGateBriefingNote())
	for _, banned := range []string{
		"skill", "plugin", "/loop", "mattpocock", "requirement-gathering", "spec-writing", "ticket-breakdown",
	} {
		if strings.Contains(got, banned) {
			t.Fatalf("the orchestrator briefing note must name no skill or plugin, found %q:\n%s", banned, CheckInGateBriefingNote())
		}
	}
}

// TestCheckInGate_MechanicalNeverPauses: `mechanical` is exempt however the
// project is configured (user decision 2026-09-01). It already carries an
// explicit authorization to go straight to edit + verify, so a gate that stopped
// a one-line fix to ask permission would cost more than the change.
func TestCheckInGate_MechanicalNeverPauses(t *testing.T) {
	if got := CheckInGate("mechanical"); got != "" {
		t.Fatalf("CheckInGate(\"mechanical\") = %q, want empty", got)
	}
}

// TestCheckInGate_NamesNoSkillOrPlugin holds the convention #278 established:
// these prompts describe BEHAVIOUR, never a skill or a plugin by name. Which
// skill the human reaches for during the check-in is the human's business, and a
// prompt that names one rots the moment the human switches plugin sets.
func TestCheckInGate_NamesNoSkillOrPlugin(t *testing.T) {
	got := strings.ToLower(CheckInGate("standard"))
	for _, banned := range []string{
		"skill", "plugin", "/loop", "mattpocock", "requirement-gathering", "spec-writing", "ticket-breakdown",
	} {
		if strings.Contains(got, banned) {
			t.Fatalf("the check-in gate must name no skill or plugin, found %q:\n%s", banned, CheckInGate("standard"))
		}
	}
}

// TestQADefault_StaysLanguageNeutral. qaDefault is injected for EVERY qa in every
// language, so response-language wording must not land here - it would change the
// prompt for every English project. Language wording belongs in
// ResponseLanguageDirective, which is already a no-op for English.
func TestQADefault_StaysLanguageNeutral(t *testing.T) {
	base := DefaultBase(KindQA)
	for _, forbidden := range []string{
		"response language",
		"Human-facing response language",
		"in that language",
		"configured language",
		"Thai",
	} {
		if strings.Contains(strings.ToLower(base), strings.ToLower(forbidden)) {
			t.Fatalf("the qa base must stay language-neutral but mentions %q:\n%s", forbidden, base)
		}
	}
}

// Every agent that runs commands carries the kill-by-PID rule in its FLOOR, so
// an edited or cleared base cannot drop it: on 2026-09-22 one worker's
// `pkill -f 'xcodebuild test'` killed every iOS agent on the machine.
func TestWorkerFloorForbidsPatternKills(t *testing.T) {
	for _, k := range []Kind{KindWorker, KindQA} {
		floor := CoordinationFloor(k)
		for _, want := range []string{"## Stopping processes (AO)", "`$!`", "Never kill by pattern", "`pkill -f`", "`killall`"} {
			if !strings.Contains(floor, want) {
				t.Errorf("%s floor is missing %q", k, want)
			}
		}
	}
}

// Every worktree of a repo shares one stash stack, so a worker that parked a
// patch with `git stash` left it where another session could pop or drop it.
// The rule lives in both worker floors, so no base edit or harness choice drops it.
func TestWorkerFloorForbidsGitStash(t *testing.T) {
	for _, k := range []Kind{KindWorker, KindQA} {
		for _, childWorktrees := range []bool{false, true} {
			floor := CoordinationFloorFor(k, childWorktrees)
			for _, want := range []string{"## Setting work aside (AO)", "Never use `git stash`", "commit it on your branch", "`.patch` file", "`~/.ao/knowledge/<project>/`"} {
				if !strings.Contains(floor, want) {
					t.Errorf("%s floor (childWorktrees=%v) is missing %q", k, childWorktrees, want)
				}
			}
		}
	}
}

// AO does not tell the orchestrator when a worker stops, so a worker that
// finished silently looked like one that died. The report is a FLOOR rule so it
// survives a cleared base and no brief has to repeat it.
func TestCoordinationFloor_WorkerReportsAtEachMoment(t *testing.T) {
	worker := CoordinationFloor(KindWorker)
	for _, want := range []string{
		"report to the orchestrator with `ao send`",
		"**your PR/MR is open**",
		"**you need the human**",
		"**you finish** - your last act before you end your turn",
		"the knowledge-store paths you wrote",
		// The finish report is where the manual checks live.
		"anything a person must check by hand (what, where, and why a test cannot)",
		"`ao orchestrator ls`",
		"a check-in before implementing, where the project has one, is the exception",
	} {
		if !strings.Contains(worker, want) {
			t.Errorf("worker floor missing %q", want)
		}
	}
	if strings.Contains(worker, "true blocker") {
		t.Error("worker floor must not limit orchestrator contact to true blockers")
	}
}

// qa hands back to dev and dev reports; qa must not be told to report to the
// orchestrator, which its own base forbids.
func TestCoordinationFloor_QAIsNotToldToReport(t *testing.T) {
	qa := CoordinationFloor(KindQA)
	if strings.Contains(qa, "report to the orchestrator") {
		t.Fatalf("qa floor must not carry the orchestrator report obligation:\n%s", qa)
	}
	if !strings.Contains(qa, "## Required coordination (AO)") || !strings.Contains(qa, "namespace") {
		t.Fatal("qa floor lost the namespace invariant")
	}
}

func TestOrchestratorDefault_BriefShape(t *testing.T) {
	base := DefaultBase(KindOrchestrator)
	for _, want := range []string{
		"## Briefs",
		"as checks a reader can verify",
		"the exact command or skill that verifies it",
		"the knowledge-store docs to read",
		"the full report of any worker this one builds on",
		"a rough timebox",
		"start ONE and stage the rest with `--todo`",
		"spawn a fresh worker with the consolidated brief",
	} {
		if !strings.Contains(base, want) {
			t.Errorf("orchestrator default missing %q", want)
		}
	}
}

// Asking a worker for status wakes it and buys a turn; status is read, not sent.
func TestOrchestratorDefault_StatusChecksAreReadOnly(t *testing.T) {
	base := DefaultBase(KindOrchestrator)
	for _, want := range []string{
		"## Checking on workers",
		"`ao session get <id>`",
		"Never `ao send` a worker just to ask how it is going",
		"re-query the live state",
		"**Orchestrator additional prompt**",
		`"Must ask" list and a "Just do" list`,
	} {
		if !strings.Contains(base, want) {
			t.Errorf("orchestrator default missing %q", want)
		}
	}
}

// A worker whose agent hands isolated subagents' worktrees to AO is told to use
// them; every other worker keeps the shared-worktree rules, word for word. The
// kill-by-PID rule rides along either way, and nothing else changes.
func TestCoordinationFloorForChildWorktrees(t *testing.T) {
	for _, k := range []Kind{KindWorker, KindQA} {
		shared := CoordinationFloorFor(k, false)
		own := CoordinationFloorFor(k, true)
		if shared != CoordinationFloor(k) {
			t.Errorf("%s: CoordinationFloorFor(false) differs from CoordinationFloor", k)
		}
		if !strings.Contains(shared, "Do not launch an Agent with `isolation: \"worktree\"`") || strings.Contains(shared, "Child agents and their worktrees") {
			t.Errorf("%s: shared floor lost the shared-worktree rule", k)
		}
		for _, want := range []string{"launch each one with `isolation: \"worktree\"`", "Commit before you delegate", "Never call `EnterWorktree`", "## Stopping processes (AO)"} {
			if !strings.Contains(own, want) {
				t.Errorf("%s: child-worktree floor lacks %q", k, want)
			}
		}
		if strings.Contains(own, "Child agents share this AO worktree") {
			t.Errorf("%s: child-worktree floor still carries the shared-worktree section", k)
		}
	}
	if CoordinationFloorFor(KindOrchestrator, true) != "" || CoordinationFloorFor(KindReviewer, true) != CoordinationFloor(KindReviewer) {
		t.Error("the flag changed a floor other than worker or qa")
	}
}

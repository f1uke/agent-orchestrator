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
		"`--task-size mechanical`", // the flag + the size that skips ceremony
		"skip the brainstorm",      // what mechanical buys
		"default `standard`",       // the default is documented
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

// Removing any of these rules re-opens the failure mode where Subagent-Driven
// Development creates .claude/worktrees/agent-* outside the AO worker branch.
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

// TestSmokeChecklistProtocol_AuthorsBeforePR: the smoke protocol must trigger the
// checklist once the change is complete and local checks pass, BEFORE the PR/MR is
// opened — NOT gated on CI being green (CI can't have run yet since the PR isn't
// open). It must also keep the conditional scope, the JSON-on-stdin mechanism, the
// full case schema, and the "play in the Tests tab" contract intact.
func TestSmokeChecklistProtocol_AuthorsBeforePR(t *testing.T) {
	got := SmokeChecklistProtocol()
	if !strings.HasPrefix(got, "\n\n") {
		t.Fatalf("smoke protocol must start with a blank-line separator: %q", got)
	}
	// The old timing must be gone: no "after CI is green", no "wrap-up" trigger.
	for _, forbidden := range []string{"after CI is green", "wrap-up"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("smoke protocol still carries stale ordering %q:\n%s", forbidden, got)
		}
	}
	for _, want := range []string{
		"## Smoke-test checklist (AO)",
		"BEFORE you open the PR/MR",                                       // new timing: author before the PR exists
		"local checks (build, tests, lint) pass",                          // gated on local checks, not CI
		"UI flows, live SCM/CI polling, native-app behavior, timing/race", // conditional scope kept
		"Skip this for pure-logic changes already covered by tests",       // skip clause kept
		"leave `prNum` at 0",                                              // prNum note for pre-PR authoring
		"cat <<'JSON' | ao smoke set \"$AO_CREW_ID\" --from-file -",       // JSON-on-stdin mechanism
		"\"name\"", "\"why\"", "\"steps\"", "\"expected\"", "\"prNum\"", "\"fileRef\"", // case schema
		"plays each case live in the Tests tab, attaches evidence, and reports results back to you", // contract
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("smoke protocol missing %q:\n%s", want, got)
		}
	}
}

// TestSmokeChecklistProtocol_StaysLanguageNeutral: the smoke protocol is injected
// for EVERY worker regardless of language, so response-language wording must never
// land here — that would change the prompt for every English project and spend
// tokens on the default path. The language scoping belongs in
// ResponseLanguageDirective, which is already a no-op for English.
func TestSmokeChecklistProtocol_StaysLanguageNeutral(t *testing.T) {
	got := SmokeChecklistProtocol()
	for _, forbidden := range []string{
		"response language",
		"Human-facing response language",
		"in that language",
		"configured language",
		"Thai",
	} {
		if strings.Contains(strings.ToLower(got), strings.ToLower(forbidden)) {
			t.Fatalf("smoke protocol must stay language-neutral but mentions %q:\n%s", forbidden, got)
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
		"authorized to SKIP",                                       // the skip is granted
		"brainstorming, writing-plans, or test-driven-development", // which skills
		"overrides any \"you MUST use skills\"",                    // grounded against the hook
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
// recording, the four human-only shapes) and what it must not do (implement, open
// the PR, report to the orchestrator).
func TestQADefaultIsQAsJobAndNotDevs(t *testing.T) {
	base := DefaultBase(KindQA)
	for _, want := range []string{
		"qa",
		"ao smoke record",
		"ao smoke retire",
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
		// Always - a stand-down is a result too.
		"passed, failed, or stood down",
		// What dev needs in order to act without re-deriving it.
		"git rev-parse --short HEAD",
		"ao smoke record",
		"RETIRED",
		"left for the human to play",
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
	if CoordinationFloor(KindQA) != worker+qaHandbackFloor {
		t.Fatal("the qa floor must be the worker floor plus the handback block, so worker invariants cannot drift apart")
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

// MISSING 1 - the record -> flow -> retire loop was NOBODY's. The tooling shipped
// long ago and no prompt said whose job it was, so the checklist never shrank.
// This holds the loop to the commands that actually exist (verified against the
// real `ao` binary): a prompt naming a flag the CLI does not have is worse than
// no prompt.
func TestRecordedFlowLoop_IsQAsAndTeachesTheWholeLoop(t *testing.T) {
	loop := RecordedFlowLoop()
	for _, want := range []string{
		"ao sim claim",
		"ao sim record start --name",
		"ao sim record status",
		"ao sim record stop --entry",
		"ao sim flow check",
		"ao sim flow run",
		`ao smoke retire "$AO_CREW_ID" --case <id> --reason`,
		// The fact that makes one human play usable at all: the recorder hooks
		// the hold, so their tap and qa's command are captured identically.
		"Device tab",
		"captured identically",
		// The loop only pays off if the case comes OFF the human's list.
		"retire the case",
	} {
		if !strings.Contains(loop, want) {
			t.Fatalf("the recorded-flow loop is missing %q:\n%s", want, loop)
		}
	}
	if !strings.HasPrefix(loop, "\n\n") {
		t.Fatal("injected blocks must be prefixed with \\n\\n")
	}
}

// MISSING 2 - qa can read the paint/focus/timing/feel SHAPE two opposite wrong
// ways ("not my business" / "I ran it, so it passed"). The rule is: drive
// anything, judge nothing.
func TestQADefault_MayDriveAnyCaseAndJudgeNoHumanOne(t *testing.T) {
	base := DefaultBase(KindQA)
	for _, want := range []string{
		// Driving a human's case is ALLOWED - it is how the evidence gets captured.
		"re-drive ANY case",
		// Judging one is not.
		"never do is JUDGE",
		// And here is the shape of the record that says exactly that.
		"--evidence <file>",
		"NO `--verdict`",
		"without concluding",
		// The restriction the code cannot enforce: qa deciding a paint case is fine.
		"on your own authority",
	} {
		if !strings.Contains(base, want) {
			t.Fatalf("the qa base does not state drive-yes/judge-no: missing %q:\n%s", want, base)
		}
	}
}

// TestCrewProtocol_DevIsToldWhatSummonsItsQA. dev's system prompt is fixed when
// its runtime launches, and under lazy creation the crew does not exist then -
// so the block has to be true BOTH before and after the join, and it has to name
// the event, because that event is something dev does.
func TestCrewProtocol_DevIsToldWhatSummonsItsQA(t *testing.T) {
	dev := CrewProtocol("dev")
	for _, want := range []string{
		"You are working this task ALONE right now",
		"AO creates a qa the first time you touch the app's runtime",
		"ao sim",
		"ao preview",
		// It is an observation, not a request: dev must not treat it as a decision
		// about its own work, which is the incentive problem the design rejected.
		"That is an observation, not a request",
		// And what changes when it happens.
		"both running at once",
	} {
		if !strings.Contains(dev, want) {
			t.Fatalf("crew dev is not told what creates its qa: missing %q:\n%s", want, dev)
		}
	}
	// qa's opening is unchanged in substance: by the time a qa exists, dev has
	// been working for a while and is still working.
	qa := CrewProtocol("qa")
	if !strings.Contains(qa, "you are both running right now") {
		t.Fatalf("qa is not told its crewmate is live:\n%s", qa)
	}
	if strings.Contains(qa, "AO creates a qa the first time") {
		t.Fatalf("qa was handed dev's account of how it got here:\n%s", qa)
	}
}

// The checklist is SHARED, and this test carries the reversal.
//
// It used to pin the opposite: dev was told "do not author or edit the
// checklist" and that `ao smoke set` from it was REFUSED, and qa was checked for
// the ABSENCE of that. The human reversed it after watching a real iOS task
// where qa wrote two cases while several places needed checking - dev is the
// member that knows what the change touched, and qa reconstructs it from
// outside. The refusal is gone from the daemon, so a prompt asserting it would
// now be a lie, and "hand the brief to qa" would send dev to refuse work it is
// allowed to do.
//
// What replaces it is not a softer version of the same rule. It is a different
// kind of instruction - a capability plus the ONE mechanical trap in it - so the
// assertions below are about the trap, not about permission.
func TestCrewProtocol_ChecklistIsSharedPerCase(t *testing.T) {
	for _, role := range []string{"dev", "qa"} {
		block := CrewProtocol(role)
		for _, want := range []string{
			// Both members are told the list is shared, because a rule about a
			// shared artifact that only one member can read is the silence that
			// lost the last argument: qa has to know dev writing cases is correct.
			"The smoke checklist is SHARED",
			// The per-case verbs, which are the whole safety mechanism.
			"ao smoke add",
			"edit --case <id>",
			// And the trap: `set` replaces the list, so the second writer deletes
			// the first. This is the sentence that has to survive being skimmed.
			"Never `ao smoke set` once there are two of you",
			"deletes the other's cases",
		} {
			if !strings.Contains(block, want) {
				t.Fatalf("crew %s is not told the checklist is shared: missing %q:\n%s", role, want, block)
			}
		}
		// The old refusal must not survive anywhere: a prompt that asserts an
		// enforcement AO no longer performs is worse than one that says nothing.
		for _, gone := range []string{
			"do not author or edit the checklist",
			"REFUSED by AO",
			"that brief predates the crew",
		} {
			if strings.Contains(block, gone) {
				t.Fatalf("crew %s still carries the reversed dev refusal %q:\n%s", role, gone, block)
			}
		}
	}
}

// Cases are shared; machine RESULTS are not, and only dev needs telling.
//
// This is the half of the old split that survived the reversal, and it survived
// on its own reasoning rather than by inertia: the human opened CASES to both
// members (who says what is worth checking), which is a different act from
// recording that a run happened. The mechanical half is that a case has ONE
// machine lane and it carries no author, so a second writer there produces a
// result nobody can trace - the exact failure per-case attribution exists to
// prevent on the cases themselves.
//
// The qa-side assertion is the descendant of the old "qa was not handed dev's
// negative": a line telling qa to leave `ao smoke record` to qa is nonsense, and
// a prompt that reads as nonsense is one an agent starts discounting.
func TestCrewProtocol_OnlyDevIsToldToLeaveResultsToQA(t *testing.T) {
	dev := CrewProtocol("dev")
	for _, want := range []string{
		"Cases are shared; RESULTS are not",
		"ao smoke record",
		// Says WHY, so it is not read as etiquette.
		"carries no author",
	} {
		if !strings.Contains(dev, want) {
			t.Fatalf("crew dev is not told to leave machine results to qa: missing %q:\n%s", want, dev)
		}
	}
	if strings.Contains(CrewProtocol("qa"), "Cases are shared; RESULTS are not") {
		t.Fatal("qa was handed dev's carve-out, which tells qa to leave the results to qa")
	}
	if CrewProtocol("") != "" {
		t.Fatalf("a solo worker must render no crew block:\n%s", CrewProtocol(""))
	}
}

// crewProtocolBudget caps the block EVERY crew member reads on every turn. It is
// a deliberate number, not a fence around the current text: the shared-checklist
// rewrite had to be shorter than the restriction it replaced, because describing
// a capability takes fewer words than arguing for a prohibition. Raise it only
// with a reason, the way the sim guidance budget is raised.
const crewProtocolBudget = 4200

func TestCrewProtocol_StaysWithinItsBudget(t *testing.T) {
	for _, role := range []string{"dev", "qa"} {
		if n := len(CrewProtocol(role)); n > crewProtocolBudget {
			t.Errorf("the %s crew protocol is %d bytes, over its %d-byte budget: it is read on every turn by every crew member, so either cut something or raise the budget deliberately", role, n, crewProtocolBudget)
		}
	}
}

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
	rendered := RenderBase(base, "nter-ios-app")
	if !strings.Contains(rendered, "~/.ao/knowledge/nter-ios-app/") {
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
	rendered := RenderBase(base, "nter-ios-app")
	if strings.Contains(rendered, ProjectIDPlaceholder) {
		t.Fatalf("worker base still carries an unexpanded placeholder after render:\n%s", rendered)
	}
	if !strings.Contains(rendered, "~/.ao/knowledge/nter-ios-app/") {
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
		"cat <<'JSON' | ao smoke set \"$AO_SESSION_ID\" --from-file -",    // JSON-on-stdin mechanism
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

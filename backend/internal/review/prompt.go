package review

import (
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/prompts"
)

// reviewTexts returns the user-facing prompt and the system prompt to deliver to
// a reviewer, authored in one place — the reviewer analogue of
// session_manager.buildSpawnTexts. The standing reviewer role lives in the
// system prompt; the per-pass task (which PR/commit, and the exact submit
// command carrying the ids) lives in the prompt, so it is also what AO injects
// into an already-running reviewer to review a new commit.
//
// Step 1 (posting the review) is provider-aware: GitHub pull requests use
// `gh api`, GitLab merge requests use `glab mr note`. The provider is derived
// from each task's URL shape (a GitLab MR URL carries "/-/merge_requests/"), so
// no extra field has to be threaded through the review engine.
//
// The texts are self-contained — they carry the ids the reviewer needs to
// submit — so no environment variables are required.
func reviewTexts(spec LaunchSpec) (prompt, systemPrompt string) {
	// Assemble the reviewer system prompt in one place: the effective global base
	// (override resolved by the Engine, else the built-in default), the project's
	// per-project addition, then AO's protected review-only floor and the
	// always-last confidentiality guard.
	base := spec.ReviewerBase
	if strings.TrimSpace(base) == "" {
		base = prompts.DefaultBase(prompts.KindReviewer)
	}
	// Render the reviewer base through the same {{.ProjectID}} template as the
	// orchestrator/worker bases (session_manager.effectiveBase), so an author can
	// address the private knowledge store in a reviewer base and get the concrete
	// project id. A base with no template actions renders unchanged.
	base = prompts.RenderBase(base, string(spec.ProjectID))
	// The response-language directive is injected LAST (just before the
	// confidentiality guard) so it wins over the English base + review task above
	// it. Empty/English renders nothing, so the default reviewer path is unchanged.
	systemPrompt = base +
		prompts.Section(spec.ReviewerAddition) +
		prompts.CoordinationFloor(prompts.KindReviewer) +
		prompts.ResponseLanguageDirective(spec.ResponseLanguage) +
		prompts.ConfidentialityGuard

	var b strings.Builder
	if reviewIsPreMR(spec) {
		fmt.Fprintf(&b, "Review the work on worker session %s's checkout. It has no pull/merge request yet — review the branch's diff against its base branch.\n", spec.WorkerID)
	} else {
		fmt.Fprintf(&b, "Review the requested pull/merge request(s) for worker session %s.\n", spec.WorkerID)
	}
	b.WriteString(reviewQueueText(spec))
	b.WriteString("\n\nComplete every review task in the queue autonomously. Do not ask the user whether to continue to the next task, and do not stop after the first one unless the provider or checkout is genuinely unusable for every queued task.\n\n")
	b.WriteString("Do these steps in order:\n")
	b.WriteString(reviewStep1(spec))
	b.WriteString(reviewStep2(string(spec.WorkerID), reviewIsPreMR(spec)))
	return b.String(), systemPrompt
}

func reviewQueueText(spec LaunchSpec) string {
	if len(spec.ReviewQueue) <= 1 {
		target := reviewTargetName(spec.PRURL, spec.Branch)
		return fmt.Sprintf("\nReview task queue:\n* 1. %s (head commit %s, run %s)\n", target, spec.TargetSHA, spec.RunID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nAO created %d review tasks for this worker session. Review every queued PR/MR, then submit all results together.\n\nReview task queue:\n", len(spec.ReviewQueue))
	for i, task := range spec.ReviewQueue {
		fmt.Fprintf(&b, "* %d. %s (head commit %s, run %s)\n", i+1, reviewTargetName(task.PRURL, task.Branch), task.TargetSHA, task.RunID)
	}
	return b.String()
}

// reviewTargetName names what a task reviews: its PR/MR URL, or — before any PR
// exists — the branch. An unnamed branch degrades to "this branch" rather than
// printing an empty target.
func reviewTargetName(prURL, branch string) string {
	if prURL != "" {
		return prURL
	}
	if branch != "" {
		return "branch " + branch
	}
	return "this branch"
}

// reviewIsPreMR reports that every task in this launch is PR-less. A pre-MR pass
// has nowhere to post, so step 1 changes shape entirely. A mixed queue cannot
// occur — Trigger keys a whole batch one way or the other — but the check is
// written as "every task", not "the first one", so a future mixed batch keeps the
// provider-posting instructions rather than silently losing them.
func reviewIsPreMR(spec LaunchSpec) bool {
	if len(spec.ReviewQueue) == 0 {
		return spec.PRURL == ""
	}
	for _, task := range spec.ReviewQueue {
		if task.PRURL != "" {
			return false
		}
	}
	return true
}

// reviewURLIsGitLab reports whether a review target URL is a GitLab merge
// request, keyed on the "/-/merge_requests/" path marker (host-agnostic for
// self-hosted GitLab). Anything else is treated as a GitHub pull request.
func reviewURLIsGitLab(u string) bool {
	return strings.Contains(u, "/-/merge_requests/")
}

// reviewQueueProviders reports which providers appear in the queue (or the
// single PRURL). It defaults to GitHub when no URL is present so an empty spec
// still yields a usable prompt.
func reviewQueueProviders(spec LaunchSpec) (github, gitlab bool) {
	urls := make([]string, 0, len(spec.ReviewQueue)+1)
	if len(spec.ReviewQueue) == 0 {
		urls = append(urls, spec.PRURL)
	}
	for _, t := range spec.ReviewQueue {
		urls = append(urls, t.PRURL)
	}
	for _, u := range urls {
		if u == "" {
			continue
		}
		if reviewURLIsGitLab(u) {
			gitlab = true
		} else {
			github = true
		}
	}
	if !github && !gitlab {
		github = true
	}
	return github, gitlab
}

// reviewStep1 selects the provider-appropriate "post the review" instructions.
// A queue is single-provider in practice (one worker session = one repo), but a
// mixed queue is handled by emitting both blocks with a routing note.
func reviewStep1(spec LaunchSpec) string {
	if reviewIsPreMR(spec) {
		return preMRReviewStep1
	}
	github, gitlab := reviewQueueProviders(spec)
	switch {
	case gitlab && !github:
		return gitlabReviewStep1
	case github && gitlab:
		return "Each task uses the tool that matches its URL: a github.com pull URL uses the `gh api` flow below; a GitLab merge-request URL (its path contains \"/-/merge_requests/\") uses the `glab mr note` flow below.\n\n" + githubReviewStep1 + "\n" + gitlabReviewStep1
	default:
		return githubReviewStep1
	}
}

// preMRReviewStep1 replaces the posting step when there is no pull/merge request
// yet. There is nothing to post to, so the review body submitted in step 2 is the
// ONLY carrier for the findings: it is what AO persists on the review_run row and
// what every AO surface reads back. Saying that plainly is what stops a reviewer
// from writing a two-line "see the PR" summary that points at nothing.
const preMRReviewStep1 = "1. There is no pull request or merge request for this work yet, so there is nowhere to post a review. Do NOT create one, and do not run `gh`, `glab`, `git push` or anything else that would publish this branch — reviewing is read-only.\n\n" +
	"   - Read the diff yourself: find the branch's base (`git log --oneline` against the target branch, or `git merge-base`) and read `git diff <base>...HEAD`, including any uncommitted work in the checkout.\n" +
	"   - Your review body in step 2 is the only place this review lands. Write the whole review there — the summary AND every finding, each as `<path>:<line> — <finding>` so it is actionable without an inline comment to anchor it.\n" +
	"   - Use an empty githubReviewId in step 2; there is no provider review to reference.\n"

// githubReviewStep1 is the original GitHub review-posting flow, preserved
// verbatim so GitHub behavior is unchanged.
const githubReviewStep1 = "1. For each PR below, post a separate review on that pull request and capture its id in one call. Post with `gh api` rather than `gh pr review`: it is the only way to attach inline comments, and its response carries the created review's id, so AO can tell the worker exactly which review to address. Send the review as a JSON body so the inline comments form a proper array of objects:\n\n" +
	"    printf '%s' '{ \"event\": \"COMMENT\", \"body\": \"<summary>\", \"comments\": [ { \"path\": \"<file>\", \"line\": <n>, \"body\": \"<finding>\" } ] }' | gh api --method POST repos/{owner}/{repo}/pulls/{number}/reviews --input - --jq '.id'\n\n" +
	"   - Substitute the PR's owner/repo/number. Add one object to \"comments\" per inline finding; omit the field for a review with no inline comments.\n" +
	"   - Keep the JSON on one line and shell-escape any single quotes in review text before passing it to printf; do not use a heredoc because reviewer panes run through an interactive PTY.\n" +
	"   - Always use \"event\": \"COMMENT\": reviews are posted from the PR author's own account, and GitHub rejects both APPROVE and REQUEST_CHANGES on your own PR. State in the body whether you are requesting changes or approving; the machine-readable verdict goes to AO in step 2.\n" +
	"   - The printed number is the review id. If the call fails on the provider, leave the id empty.\n"

// gitlabReviewStep1 posts the review to a GitLab merge request with `glab mr
// note` (glab >= 1.94.0 supports diff/line comments natively). The MR URL
// carries everything needed: REPO is the host-qualified project URL (the whole
// MR URL up to "/-/") and IID is the number after "/-/merge_requests/". Passing
// the host-qualified URL to `glab -R` keeps the note on the MR's own GitLab
// instance rather than glab's configured default host — self-hosted GitLab (and
// a multi-host AO_GITLAB_HOST) would otherwise be misrouted to that default.
const gitlabReviewStep1 = "1. For each merge request below, post your review with `glab mr note` (glab supports diff/line comments natively). From the MR URL `https://<host>/<group>/<project>/-/merge_requests/<iid>`, set REPO=`https://<host>/<group>/<project>` (the whole MR URL up to \"/-/\", host included, so glab targets the MR's own GitLab instance instead of glab's default host) and IID=`<iid>` (the number after \"/-/merge_requests/\").\n\n" +
	"   - Post the review summary as a non-blocking note, stating clearly whether you are requesting changes or approving:\n\n" +
	"       glab mr note create <IID> -R <REPO> --resolvable=false -m '<summary markdown>'\n\n" +
	"   - For each inline finding, add a diff comment anchored to the exact line so the worker can resolve it:\n\n" +
	"       glab mr note create <IID> -R <REPO> --file '<path>' --line <n> -m '<finding>'\n\n" +
	"     Use `--old-line <n>` for a removed line, or `--line A:B` for a range. If a diff comment is rejected because the line is not part of the MR diff, fold that finding into the summary note as \"<path>:<line> — <finding>\" instead of failing.\n" +
	"   - Keep each -m message on one line and shell-escape any single quotes; or pipe multi-line text via stdin (printf '%s' '<summary>' | glab mr note create <IID> -R <REPO> --resolvable=false). Do not use a heredoc; reviewer panes run through an interactive PTY.\n" +
	"   - `glab mr note create` does not print a review id, so use an empty githubReviewId for GitLab merge requests in step 2. The machine-readable verdict still goes to AO in step 2.\n"

// reviewStep2 is the provider-neutral bookkeeping step. The githubReviewId field
// name is an opaque id kept for wire/storage compatibility; it is empty for
// GitLab merge requests.
func reviewStep2(workerID string, preMR bool) string {
	lead := "After every task's review is posted in step 1, record AO's bookkeeping for those already-posted reviews using one command."
	if preMR {
		lead = "Record the review with one command. Nothing was posted anywhere in step 1, so this body IS the review."
	}
	tail := "Only if step 1 genuinely fails on the provider for a task, still include that run in step 2 with an empty githubReviewId so the result is recorded."
	if preMR {
		tail = "Submitting is not optional: a pre-MR review that is never submitted leaves no trace at all, because there is no PR carrying it."
	}
	return fmt.Sprintf("2. %s Pass JSON on stdin so nothing is ever written into the worktree (a file there could be committed onto the worker's branch). Include one object per PR/MR run from the queue:\n\n"+
		"    printf '%%s' '{ \"reviews\": [ { \"runId\": \"<run-id>\", \"verdict\": \"<approved|changes_requested>\", \"githubReviewId\": \"<id-from-step-1-or-empty>\", \"body\": \"<your full review markdown>\" } ] }' | ao review submit --session %s --reviews -\n\n"+
		"%s",
		lead, workerID, tail)
}

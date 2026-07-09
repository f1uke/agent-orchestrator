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
	systemPrompt = base +
		prompts.Section(spec.ReviewerAddition) +
		prompts.CoordinationFloor(prompts.KindReviewer) +
		prompts.ConfidentialityGuard

	var b strings.Builder
	fmt.Fprintf(&b, "Review the requested pull/merge request(s) for worker session %s.\n", spec.WorkerID)
	b.WriteString(reviewQueueText(spec))
	b.WriteString("\n\nComplete every review task in the queue autonomously. Do not ask the user whether to continue to the next task, and do not stop after the first one unless the provider or checkout is genuinely unusable for every queued task.\n\n")
	b.WriteString("Do these steps in order:\n")
	b.WriteString(reviewStep1(spec))
	b.WriteString(reviewStep2(string(spec.WorkerID)))
	return b.String(), systemPrompt
}

func reviewQueueText(spec LaunchSpec) string {
	if len(spec.ReviewQueue) <= 1 {
		return fmt.Sprintf("\nReview task queue:\n* 1. %s (head commit %s, run %s)\n", spec.PRURL, spec.TargetSHA, spec.RunID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nAO created %d review tasks for this worker session. Review every queued PR/MR, then submit all results together.\n\nReview task queue:\n", len(spec.ReviewQueue))
	for i, task := range spec.ReviewQueue {
		fmt.Fprintf(&b, "* %d. %s (head commit %s, run %s)\n", i+1, task.PRURL, task.TargetSHA, task.RunID)
	}
	return b.String()
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
func reviewStep2(workerID string) string {
	return fmt.Sprintf("2. After every task's review is posted in step 1, record AO's bookkeeping for those already-posted reviews using one command. Pass JSON on stdin so nothing is ever written into the worktree (a file there could be committed onto the worker's branch). Include one object per PR/MR run from the queue:\n\n"+
		"    printf '%%s' '{ \"reviews\": [ { \"runId\": \"<run-id>\", \"verdict\": \"<approved|changes_requested>\", \"githubReviewId\": \"<id-from-step-1-or-empty>\", \"body\": \"<your full review markdown>\" } ] }' | ao review submit --session %s --reviews -\n\n"+
		"Only if step 1 genuinely fails on the provider for a task, still include that run in step 2 with an empty githubReviewId so the result is recorded.",
		workerID)
}

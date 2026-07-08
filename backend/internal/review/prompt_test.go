package review

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReviewTexts_UsesBaseOverrideAdditionFloorAndGuard(t *testing.T) {
	spec := LaunchSpec{
		WorkerID:         "sess-1",
		PRURL:            "https://github.com/o/r/pull/1",
		TargetSHA:        "abc",
		RunID:            "run-1",
		ReviewerBase:     "CUSTOM REVIEWER BASE",
		ReviewerAddition: "PROJECT REVIEWER ADDITION",
	}
	_, sys := reviewTexts(spec)
	for _, want := range []string{
		"CUSTOM REVIEWER BASE",
		"PROJECT REVIEWER ADDITION",
		"Review only (AO)",
		"Standing-instruction confidentiality",
	} {
		if !strings.Contains(sys, want) {
			t.Fatalf("reviewer system prompt missing %q:\n%s", want, sys)
		}
	}
}

func TestReviewTexts_EmptyBaseFallsBackToDefault(t *testing.T) {
	_, sys := reviewTexts(LaunchSpec{WorkerID: "s", PRURL: "u", RunID: "r"})
	if !strings.Contains(sys, "You are an AO code reviewer") {
		t.Fatalf("empty base should fall back to the default reviewer role:\n%s", sys)
	}
}

func TestReviewTextsGitLabUsesGlab(t *testing.T) {
	spec := launchSpec()
	spec.PRURL = "https://gitlab.finnomena.com/group/sub/proj/-/merge_requests/42"
	spec.ReviewQueue = []ports.ReviewTask{
		{RunID: "run-1", PRURL: "https://gitlab.finnomena.com/group/sub/proj/-/merge_requests/42", TargetSHA: "sha1"},
	}
	prompt, _ := reviewTexts(spec)
	for _, want := range []string{
		"glab mr note create",
		"--file",
		"--line",
		"--resolvable=false",
		"ao review submit --session mer-1 --reviews -",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("gitlab prompt missing %q:\n%s", want, prompt)
		}
	}
	// A GitLab-only queue must not tell the reviewer to use the GitHub tool.
	if strings.Contains(prompt, "gh api") || strings.Contains(prompt, "/pulls/") {
		t.Fatalf("gitlab prompt should not reference gh api / pulls:\n%s", prompt)
	}
}

func TestReviewTextsGitHubUsesGhApi(t *testing.T) {
	spec := launchSpec()
	spec.PRURL = "https://github.com/o/r/pull/7"
	spec.ReviewQueue = []ports.ReviewTask{
		{RunID: "run-1", PRURL: "https://github.com/o/r/pull/7", TargetSHA: "sha1"},
	}
	prompt, _ := reviewTexts(spec)
	for _, want := range []string{
		"gh api --method POST repos/{owner}/{repo}/pulls/{number}/reviews",
		"ao review submit --session mer-1 --reviews -",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("github prompt missing %q:\n%s", want, prompt)
		}
	}
	// A GitHub-only queue must not reference the GitLab tool.
	if strings.Contains(prompt, "glab") {
		t.Fatalf("github prompt should not reference glab:\n%s", prompt)
	}
}

func TestReviewTextsIncludesMultiPRQueue(t *testing.T) {
	spec := launchSpec()
	spec.RunID = "run-2"
	spec.PRURL = "https://github.com/o/r/pull/2"
	spec.TargetSHA = "sha2"
	spec.ReviewIndex = 1
	spec.ReviewQueue = []ports.ReviewTask{
		{RunID: "run-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1"},
		{RunID: "run-2", PRURL: "https://github.com/o/r/pull/2", TargetSHA: "sha2"},
	}

	prompt, _ := reviewTexts(spec)
	for _, want := range []string{
		"AO created 2 review tasks",
		"Review every queued PR/MR, then submit all results together",
		"Complete every review task in the queue autonomously",
		"Do not ask the user whether to continue to the next task",
		"* 1. https://github.com/o/r/pull/1 (head commit sha1, run run-1)",
		"* 2. https://github.com/o/r/pull/2 (head commit sha2, run run-2)",
		"record AO's bookkeeping",
		"printf '%s'",
		"do not use a heredoc",
		"ao review submit --session mer-1 --reviews -",
		`"reviews": [`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

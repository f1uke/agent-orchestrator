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

// TestReviewTexts_ResponseLanguageDirective: a non-English resolved language
// injects the human-facing directive into the reviewer prompt, positioned LAST
// (just before the confidentiality guard so it wins over the English base). The
// reviewer's review comments are human-facing, so this must reach it too.
func TestReviewTexts_ResponseLanguageDirective(t *testing.T) {
	spec := LaunchSpec{WorkerID: "s", PRURL: "u", RunID: "r", ResponseLanguage: "Thai"}
	_, sys := reviewTexts(spec)
	if !strings.Contains(sys, "## Human-facing response language (AO)") {
		t.Fatalf("reviewer prompt missing response-language directive:\n%s", sys)
	}
	if !strings.Contains(sys, "in Thai") {
		t.Fatalf("reviewer directive does not reflect the configured language:\n%s", sys)
	}
	if !strings.Contains(sys, "review comments") {
		t.Fatalf("reviewer directive should scope review comments as human-facing:\n%s", sys)
	}
	langIdx := strings.Index(sys, "## Human-facing response language (AO)")
	guardIdx := strings.Index(sys, "## Standing-instruction confidentiality")
	if guardIdx < 0 || langIdx < 0 || langIdx > guardIdx {
		t.Fatalf("directive must sit just before the confidentiality guard (lang=%d guard=%d):\n%s", langIdx, guardIdx, sys)
	}
}

// TestReviewTexts_EnglishDefaultNoDirective: English/empty injects nothing, so
// the default reviewer path is unchanged.
func TestReviewTexts_EnglishDefaultNoDirective(t *testing.T) {
	for _, lang := range []string{"", "English"} {
		_, sys := reviewTexts(LaunchSpec{WorkerID: "s", PRURL: "u", RunID: "r", ResponseLanguage: lang})
		if strings.Contains(sys, "## Human-facing response language (AO)") {
			t.Fatalf("language %q must not inject a directive:\n%s", lang, sys)
		}
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
		// REPO must stay host-qualified so glab targets the MR's own GitLab
		// instance instead of glab's default host.
		"REPO=`https://<host>/<group>/<project>`",
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

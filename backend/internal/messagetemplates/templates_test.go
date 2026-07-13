package messagetemplates

import (
	"strings"
	"testing"
)

func TestExecuteRendersData(t *testing.T) {
	out, err := Execute("hi {{.PRURL}}", ReviewCommentData{PRURL: "there"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "hi there" {
		t.Fatalf("got %q, want %q", out, "hi there")
	}
}

func TestExecuteReportsParseError(t *testing.T) {
	if _, err := Execute("{{.Broken", ReviewCommentData{}); err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

func TestKnownNamesValidAndHaveDefaults(t *testing.T) {
	names := KnownNames()
	if len(names) != 6 {
		t.Fatalf("want 6 templates, got %d", len(names))
	}
	for _, n := range names {
		if !n.Valid() {
			t.Fatalf("%q not Valid()", n)
		}
		if Default(n) == "" {
			t.Fatalf("%q has empty default", n)
		}
	}
	if Name("bogus").Valid() {
		t.Fatal("bogus should be invalid")
	}
}

func TestDefaultsRenderWithZeroData(t *testing.T) {
	// Every built-in default must parse+execute against its data struct so a
	// bad built-in can never strand a nudge. Zero-value data is the worst case.
	cases := map[Name]any{
		NameReviewCommentDispatch: ReviewCommentData{},
		NameCIFailing:             CIFailingData{},
		NameMergeConflict:         MergeConflictData{},
		NameTrackerBotComment:     TrackerBotData{},
		NameAOReviewerBatch:       AOReviewerBatchData{},
		NameAOReviewerSingle:      AOReviewerSingleData{},
	}
	for n, data := range cases {
		if _, err := Execute(Default(n), data); err != nil {
			t.Fatalf("default %q failed to render: %v", n, err)
		}
	}
}

// The single-comment dispatch must tell the worker the comment's file:line and
// quoted body (the bug this fix closes) plus one clear instruction - mirroring
// the frontend genPrompt phrasing. No numbered list for a lone comment.
func TestReviewCommentDefaultSingleGolden(t *testing.T) {
	out, err := Execute(Default(NameReviewCommentDispatch), ReviewCommentData{
		PRIdentity: "PR #7",
		PRURL:      "https://x/pr/7",
		Count:      1,
		Comments:   []ReviewCommentItem{{Index: 1, File: "a.go", Line: 75, Body: "tidy this"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "A reviewer left an unresolved comment on PR #7. Address it: make the change, keep it minimal and consistent with the surrounding code, then reply on that thread summarizing what you did." +
		"\nPR: https://x/pr/7" +
		"\n\na.go:75\n> tidy this"
	if out != want {
		t.Fatalf("single golden mismatch:\n got %q\nwant %q", out, want)
	}
}

// The multi-comment dispatch states the shared instruction once, then a numbered
// file:line list with each quoted body - mirroring the frontend batchPrompt. PR
// identity falls back to "your PR" when absent.
func TestReviewCommentDefaultMultiGolden(t *testing.T) {
	out, err := Execute(Default(NameReviewCommentDispatch), ReviewCommentData{
		Count: 2,
		Comments: []ReviewCommentItem{
			{Index: 1, File: "a.go", Line: 75, Body: "first"},
			{Index: 2, File: "b.go", Line: 12, Body: "second"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "There are 2 unresolved review comments on your PR to address. For each: make the change, keep it minimal and consistent with the surrounding code, then reply on that thread summarizing what you did." +
		"\n\n1. a.go:75\n   > first" +
		"\n\n2. b.go:12\n   > second"
	if out != want {
		t.Fatalf("multi golden mismatch:\n got %q\nwant %q", out, want)
	}
}

// A bare changes-requested decision carries no inline threads (Count 0): omit the
// list and just point the worker at the review feedback. Also the zero-value
// worst case, so it must render without error.
func TestReviewCommentDefaultNoCommentsGolden(t *testing.T) {
	out, err := Execute(Default(NameReviewCommentDispatch), ReviewCommentData{})
	if err != nil {
		t.Fatal(err)
	}
	want := "A reviewer requested changes on your PR. Address the feedback, then reply on the review threads summarizing what you did."
	if out != want {
		t.Fatalf("no-comment render = %q, want %q", out, want)
	}
}

func TestAOReviewerBatchGolden(t *testing.T) {
	data := AOReviewerBatchData{
		Count: 2,
		Reviews: []AOReviewItem{
			{Index: 1, PRURL: "https://x/pr/1", Verdict: "changes_requested", TargetSHA: "abc", ReviewID: "R1", Body: "fix it"},
			{Index: 2, PRURL: "https://x/pr/2", Verdict: "changes_requested"},
		},
	}
	out, err := Execute(Default(NameAOReviewerBatch), data)
	if err != nil {
		t.Fatal(err)
	}
	// Review 1 carries a GitHub review id (reply-on-review branch); Review 2 has
	// none — the GitLab case — so it takes the {{else}} branch and is still told
	// to resolve the reviewer's resolvable discussion threads.
	want := "[AO reviewer] AO's internal code reviewer submitted 2 review(s) requesting changes.\n" +
		"\nReview 1\nPR: https://x/pr/1\nVerdict: changes_requested" +
		"\nHead commit: abc" +
		"\nReview: R1\nOnce you have addressed it, reply on review R1 with how you addressed it, then resolve the review comment threads you addressed." +
		"\n\nReview body:\nfix it\n" +
		"\nReview 2\nPR: https://x/pr/2\nVerdict: changes_requested" +
		"\nOnce you have addressed it, resolve the review comment threads you addressed."
	if out != want {
		t.Fatalf("batch golden mismatch:\n got %q\nwant %q", out, want)
	}
}

func TestAOReviewerSingleGolden(t *testing.T) {
	out, err := Execute(Default(NameAOReviewerSingle), AOReviewerSingleData{
		PRURL: "https://x/pr/9", Verdict: "changes_requested", ReviewID: "R9", Body: "please fix",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "[AO reviewer] AO's internal code reviewer submitted a review.\n\nPR: https://x/pr/9\nVerdict: changes_requested" +
		"\nReview: R9\n\nOnce you have addressed it, reply on review R9 with how you addressed it, then resolve the review comment threads you addressed." +
		"\n\nReview body:\nplease fix"
	if out != want {
		t.Fatalf("single golden mismatch:\n got %q\nwant %q", out, want)
	}
}

// A GitLab merge request carries no review id, so the single template must take
// the {{else}} branch: still tell the worker to resolve the resolvable
// discussion threads, without referencing a non-existent "review <id>".
func TestAOReviewerSingleGitLabResolvesThreadsWithoutReviewID(t *testing.T) {
	out, err := Execute(Default(NameAOReviewerSingle), AOReviewerSingleData{
		PRURL: "https://gitlab.finnomena.com/g/p/-/merge_requests/9", Verdict: "changes_requested", ReviewID: "", Body: "please fix",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "[AO reviewer] AO's internal code reviewer submitted a review.\n\nPR: https://gitlab.finnomena.com/g/p/-/merge_requests/9\nVerdict: changes_requested" +
		"\n\nOnce you have addressed it, resolve the review comment threads you addressed." +
		"\n\nReview body:\nplease fix"
	if out != want {
		t.Fatalf("gitlab single mismatch:\n got %q\nwant %q", out, want)
	}
	if strings.Contains(out, "reply on review") || strings.Contains(out, "\nReview:") {
		t.Fatalf("gitlab nudge must not reference a review id: %q", out)
	}
}

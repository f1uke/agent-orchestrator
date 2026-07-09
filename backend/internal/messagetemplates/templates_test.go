package messagetemplates

import "testing"

func TestExecuteRendersData(t *testing.T) {
	out, err := Execute("hi {{.Comments}}", ReviewCommentData{Comments: "there"})
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

func TestReviewCommentDefaultOmitsBlankComments(t *testing.T) {
	out, err := Execute(Default(NameReviewCommentDispatch), ReviewCommentData{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "A reviewer left feedback on your PR. Address it and push." {
		t.Fatalf("blank-comment render = %q", out)
	}
	out, err = Execute(Default(NameReviewCommentDispatch), ReviewCommentData{Comments: "remove this"})
	if err != nil {
		t.Fatal(err)
	}
	want := "A reviewer left feedback on your PR. Address it and push.\n\nremove this"
	if out != want {
		t.Fatalf("with-comment render = %q, want %q", out, want)
	}
}

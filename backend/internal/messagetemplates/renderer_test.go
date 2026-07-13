package messagetemplates

import (
	"strings"
	"testing"
)

func TestRendererUsesDefaultWhenNoOverride(t *testing.T) {
	r := NewRenderer(func() map[string]string { return nil })
	out, err := r.Render(NameMergeConflict, MergeConflictData{})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := Execute(Default(NameMergeConflict), MergeConflictData{})
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestRendererUsesOverride(t *testing.T) {
	r := NewRenderer(func() map[string]string {
		return map[string]string{string(NameReviewCommentDispatch): "custom:{{range .Comments}} {{.File}}:{{.Line}} {{.Body}}{{end}}"}
	})
	out, err := r.Render(NameReviewCommentDispatch, ReviewCommentData{Count: 1, Comments: []ReviewCommentItem{{Index: 1, File: "a.go", Line: 5, Body: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if out != "custom: a.go:5 hi" {
		t.Fatalf("got %q", out)
	}
}

func TestRendererFallsBackWhenOverrideFails(t *testing.T) {
	r := NewRenderer(func() map[string]string {
		return map[string]string{string(NameReviewCommentDispatch): "{{.Nonexistent}}"}
	})
	out, err := r.Render(NameReviewCommentDispatch, ReviewCommentData{Count: 1, Comments: []ReviewCommentItem{{Index: 1, File: "a.go", Line: 5, Body: "hi"}}})
	if err == nil {
		t.Fatal("expected an error reporting the override failure")
	}
	// Still returns a usable default render carrying the comment's file:line.
	if !strings.HasPrefix(out, "A reviewer left an unresolved comment") || !strings.Contains(out, "a.go:5") {
		t.Fatalf("fallback render = %q", out)
	}
}

func TestRendererBlankOverrideUsesDefault(t *testing.T) {
	r := NewRenderer(func() map[string]string {
		return map[string]string{string(NameMergeConflict): ""}
	})
	out, err := r.Render(NameMergeConflict, MergeConflictData{})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := Execute(Default(NameMergeConflict), MergeConflictData{})
	if out != want {
		t.Fatalf("blank override should use default, got %q", out)
	}
}

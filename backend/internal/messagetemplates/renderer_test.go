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
		return map[string]string{string(NameReviewCommentDispatch): "custom: {{.Comments}}"}
	})
	out, err := r.Render(NameReviewCommentDispatch, ReviewCommentData{Comments: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "custom: hi" {
		t.Fatalf("got %q", out)
	}
}

func TestRendererFallsBackWhenOverrideFails(t *testing.T) {
	r := NewRenderer(func() map[string]string {
		return map[string]string{string(NameReviewCommentDispatch): "{{.Nonexistent}}"}
	})
	out, err := r.Render(NameReviewCommentDispatch, ReviewCommentData{Comments: "hi"})
	if err == nil {
		t.Fatal("expected an error reporting the override failure")
	}
	// Still returns a usable default render.
	if !strings.HasPrefix(out, "A reviewer left feedback") {
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

package session

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/messagetemplates"
)

// stubRenderer echoes each ReviewCommentData item as "file:line body" alongside a
// fixed prefix so tests can assert both sanitization and that file:line + body
// reach the worker, without depending on the real template text.
type stubRenderer struct {
	out string
	err error
}

func (s stubRenderer) Render(_ messagetemplates.Name, data any) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if d, ok := data.(messagetemplates.ReviewCommentData); ok {
		out := s.out
		for _, c := range d.Comments {
			out += fmt.Sprintf("\n%s:%d %s", c.File, c.Line, c.Body)
		}
		return out, nil
	}
	return s.out, nil
}

func TestDispatchCommentToWorker_RendersSanitizesAndSends(t *testing.T) {
	fake := newFakeStore()
	fake.sessions["s1"] = domain.SessionRecord{ID: "s1", ProjectID: "p", Kind: domain.KindWorker}
	stList := &multiPRFakeStore{fakeStore: fake, prs: []domain.PullRequest{{URL: "pr1"}}}
	stList.comments["pr1"] = []domain.PullRequestComment{
		{ThreadID: "T1", ID: "c1", File: "svc.go", Line: 42, Body: "please\x1b]0;pwned\afix"},
	}
	fc := &fakeCommander{}
	svc := &Service{store: stList, manager: fc, renderer: stubRenderer{out: "PROMPT:"}}

	// The extra prompt carries a control byte too, so the control-byte assertion
	// below pins sanitization of BOTH the comment body and the extra prompt (it
	// fails if either SanitizeControlChars call is dropped).
	err := svc.DispatchCommentToWorker(context.Background(), "s1", "pr1", "T1", "also add a test\a")
	if err != nil {
		t.Fatal(err)
	}
	got := fc.lastMessage
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\a') {
		t.Fatalf("dispatched message carries control bytes: %q", got)
	}
	if !strings.Contains(got, "also add a test") {
		t.Fatalf("extra prompt missing: %q", got)
	}
	if !strings.Contains(got, "please") || !strings.Contains(got, "fix") {
		t.Fatalf("comment body missing: %q", got)
	}
	// The worker must be told WHICH file:line to fix - the bug this closes.
	if !strings.Contains(got, "svc.go:42") {
		t.Fatalf("dispatched message missing file:line: %q", got)
	}
}

// End-to-end through the REAL default template (no stub): the manual "Send to
// worker" path must render each comment's file:line + body and the PR URL, so the
// worker knows exactly where to fix - the bug this closes for the manual path.
func TestDispatchCommentToWorker_RealTemplateCarriesFileLine(t *testing.T) {
	fake := newFakeStore()
	fake.sessions["s1"] = domain.SessionRecord{ID: "s1", ProjectID: "p", Kind: domain.KindWorker}
	stList := &multiPRFakeStore{fakeStore: fake, prs: []domain.PullRequest{{URL: "https://x/pr/9"}}}
	stList.comments["https://x/pr/9"] = []domain.PullRequestComment{
		{ThreadID: "T1", ID: "c1", File: "item.swift", Line: 75, Body: "knock out getTotalCount()"},
	}
	fc := &fakeCommander{}
	svc := &Service{store: stList, manager: fc, renderer: messagetemplates.NewRenderer(nil)}

	if err := svc.DispatchCommentToWorker(context.Background(), "s1", "https://x/pr/9", "T1", ""); err != nil {
		t.Fatal(err)
	}
	got := fc.lastMessage
	if !strings.Contains(got, "item.swift:75") {
		t.Fatalf("dispatched message missing file:line: %q", got)
	}
	if !strings.Contains(got, "knock out getTotalCount()") {
		t.Fatalf("dispatched message missing comment body: %q", got)
	}
	if !strings.Contains(got, "PR: https://x/pr/9") {
		t.Fatalf("dispatched message missing PR url: %q", got)
	}
	if !strings.Contains(got, "reply on that thread") {
		t.Fatalf("dispatched message missing the reply instruction: %q", got)
	}
}

func TestDispatchCommentToWorker_UnknownSession(t *testing.T) {
	fake := newFakeStore()
	stList := &multiPRFakeStore{fakeStore: fake, prs: nil}
	fc := &fakeCommander{}
	svc := &Service{store: stList, manager: fc, renderer: stubRenderer{out: "PROMPT:"}}

	err := svc.DispatchCommentToWorker(context.Background(), "ghost", "pr1", "T1", "")
	if err == nil {
		t.Fatal("want error for unknown session")
	}
}

func TestDispatchCommentToWorker_UnknownPR(t *testing.T) {
	fake := newFakeStore()
	fake.sessions["s1"] = domain.SessionRecord{ID: "s1", ProjectID: "p", Kind: domain.KindWorker}
	stList := &multiPRFakeStore{fakeStore: fake, prs: []domain.PullRequest{{URL: "pr1"}}}
	fc := &fakeCommander{}
	svc := &Service{store: stList, manager: fc, renderer: stubRenderer{out: "PROMPT:"}}

	err := svc.DispatchCommentToWorker(context.Background(), "s1", "pr-does-not-exist", "T1", "")
	if err == nil {
		t.Fatal("want error for unknown PR")
	}
}

func TestDispatchCommentToWorker_NoComments(t *testing.T) {
	fake := newFakeStore()
	fake.sessions["s1"] = domain.SessionRecord{ID: "s1", ProjectID: "p", Kind: domain.KindWorker}
	stList := &multiPRFakeStore{fakeStore: fake, prs: []domain.PullRequest{{URL: "pr1"}}}
	stList.comments["pr1"] = []domain.PullRequestComment{
		{ThreadID: "OTHER", ID: "c1", Body: "unrelated"},
	}
	fc := &fakeCommander{}
	svc := &Service{store: stList, manager: fc, renderer: stubRenderer{out: "PROMPT:"}}

	err := svc.DispatchCommentToWorker(context.Background(), "s1", "pr1", "T1", "")
	if err == nil {
		t.Fatal("want error when thread has no comments")
	}
}

package session

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/messagetemplates"
)

// stubRenderer echoes the ReviewCommentData.Comments alongside a fixed prefix
// so tests can assert both sanitization and content propagation without
// depending on the real template text.
type stubRenderer struct {
	out string
	err error
}

func (s stubRenderer) Render(_ messagetemplates.Name, data any) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if d, ok := data.(messagetemplates.ReviewCommentData); ok {
		return s.out + "\n" + d.Comments, nil
	}
	return s.out, nil
}

func TestDispatchCommentToWorker_RendersSanitizesAndSends(t *testing.T) {
	fake := newFakeStore()
	fake.sessions["s1"] = domain.SessionRecord{ID: "s1", ProjectID: "p", Kind: domain.KindWorker}
	stList := &multiPRFakeStore{fakeStore: fake, prs: []domain.PullRequest{{URL: "pr1"}}}
	stList.comments["pr1"] = []domain.PullRequestComment{
		{ThreadID: "T1", ID: "c1", Body: "please\x1b]0;pwned\afix"},
	}
	fc := &fakeCommander{}
	svc := &Service{store: stList, manager: fc, renderer: stubRenderer{out: "PROMPT:"}}

	err := svc.DispatchCommentToWorker(context.Background(), "s1", "pr1", "T1", "also add a test")
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

package session

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func TestListPRCommentThreads_GroupsCommentsUnderThreads(t *testing.T) {
	st := newFakeStore()
	st.sessions["s1"] = domain.SessionRecord{ID: "s1", ProjectID: "proj", Kind: domain.KindWorker}
	prURL := "https://gh/pr/1"
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{{
		URL:      prURL,
		Number:   1,
		Provider: "github",
		HTMLURL:  prURL,
		HeadSHA:  "abc",
	}}}
	stList.threads[prURL] = []domain.PullRequestReviewThread{
		{ThreadID: "T1", Path: "a.go", Line: 10, Resolved: false, IsBot: false},
	}
	stList.comments[prURL] = []domain.PullRequestComment{
		{ThreadID: "T1", ID: "C1", Author: "alice", Body: "fix this", File: "a.go", Line: 10},
		{ThreadID: "T1", ID: "C2", Author: "bob", Body: "agreed", File: "a.go", Line: 10},
		{ThreadID: "T2", ID: "C3", Author: "carol", Body: "orphan", File: "b.go", Line: 5}, // thread not in list
	}

	svc := &Service{store: stList}
	groups, err := svc.ListPRCommentThreads(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].PRURL != prURL || groups[0].HeadSHA != "abc" {
		t.Fatalf("groups = %+v", groups)
	}
	threads := groups[0].Threads
	if len(threads) != 2 {
		t.Fatalf("want 2 threads (T1 + synthesized T2), got %d: %+v", len(threads), threads)
	}
	// T1 keeps both comments oldest-first.
	if threads[0].ThreadID != "T1" || len(threads[0].Comments) != 2 ||
		threads[0].Comments[0].ID != "C1" || threads[0].Comments[1].ID != "C2" {
		t.Fatalf("T1 = %+v", threads[0])
	}
	// Orphan comment gets a synthesized thread anchored to its file/line.
	if threads[1].ThreadID != "T2" || threads[1].Path != "b.go" || threads[1].Line != 5 ||
		len(threads[1].Comments) != 1 || threads[1].Comments[0].ID != "C3" {
		t.Fatalf("synthesized thread = %+v", threads[1])
	}
}

func TestListPRCommentThreads_UnknownSession(t *testing.T) {
	st := newFakeStore()
	svc := &Service{store: st}
	_, err := svc.ListPRCommentThreads(context.Background(), "nope")
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindNotFound || e.Code != "SESSION_NOT_FOUND" {
		t.Fatalf("err = %v, want apierr NotFound SESSION_NOT_FOUND", err)
	}
}

package session

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// newThreadWriteFixtures builds a store seeded with one worker session ("s1")
// owning one PR ("pr1", a github PR URL so fakeSCM.ParseRepository resolves
// it), plus a fakeSCM configured per test.
func newThreadWriteFixtures(scm fakeSCM) (*multiPRFakeStore, fakeSCM) {
	fake := newFakeStore()
	fake.sessions["s1"] = domain.SessionRecord{ID: "s1", ProjectID: "p", Kind: domain.KindWorker}
	st := &multiPRFakeStore{fakeStore: fake, prs: []domain.PullRequest{{URL: "https://github.com/acme/repo/pull/7", Number: 7}}}
	return st, scm
}

func TestReplyToThread_ReturnsComment(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	st, scm := newThreadWriteFixtures(fakeSCM{replyComment: ports.SCMReviewCommentObservation{ID: "c9", Author: "me", Body: "ok"}})
	svc := NewWithDeps(Deps{Store: st, SCM: scm, Clock: func() time.Time { return now }})

	got, err := svc.ReplyToThread(context.Background(), "s1", "https://github.com/acme/repo/pull/7", "T1", "ok")
	if err != nil {
		t.Fatalf("ReplyToThread: %v", err)
	}
	if got.ID != "c9" || got.Author != "me" || got.Body != "ok" {
		t.Fatalf("comment = %+v", got)
	}
	if got.Resolved {
		t.Fatalf("comment.Resolved = true, want false for a fresh reply")
	}
	if got.CreatedAt.IsZero() {
		t.Fatalf("comment.CreatedAt is zero, want s.clock().UTC()")
	}
}

func TestResolveThread_OK(t *testing.T) {
	st, scm := newThreadWriteFixtures(fakeSCM{resolveErr: nil})
	svc := NewWithDeps(Deps{Store: st, SCM: scm, Clock: func() time.Time { return time.Now() }})

	if err := svc.ResolveThread(context.Background(), "s1", "https://github.com/acme/repo/pull/7", "T1"); err != nil {
		t.Fatalf("ResolveThread: %v", err)
	}
}

func TestReplyToThread_UnknownSession(t *testing.T) {
	st, scm := newThreadWriteFixtures(fakeSCM{})
	svc := NewWithDeps(Deps{Store: st, SCM: scm, Clock: func() time.Time { return time.Now() }})

	_, err := svc.ReplyToThread(context.Background(), "unknown", "https://github.com/acme/repo/pull/7", "T1", "ok")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "SESSION_NOT_FOUND" {
		t.Fatalf("err = %v, want apierr SESSION_NOT_FOUND", err)
	}
}

func TestReplyToThread_UnknownPR(t *testing.T) {
	st, scm := newThreadWriteFixtures(fakeSCM{})
	svc := NewWithDeps(Deps{Store: st, SCM: scm, Clock: func() time.Time { return time.Now() }})

	_, err := svc.ReplyToThread(context.Background(), "s1", "https://github.com/acme/repo/pull/999", "T1", "ok")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "PR_NOT_FOUND" {
		t.Fatalf("err = %v, want apierr PR_NOT_FOUND", err)
	}
}

func TestReplyToThread_NilSCMUnavailable(t *testing.T) {
	fake := newFakeStore()
	fake.sessions["s1"] = domain.SessionRecord{ID: "s1", ProjectID: "p", Kind: domain.KindWorker}
	st := &multiPRFakeStore{fakeStore: fake, prs: []domain.PullRequest{{URL: "https://github.com/acme/repo/pull/7", Number: 7}}}
	svc := NewWithDeps(Deps{Store: st, Clock: func() time.Time { return time.Now() }})

	_, err := svc.ReplyToThread(context.Background(), "s1", "https://github.com/acme/repo/pull/7", "T1", "ok")
	if !errors.Is(err, ErrSCMUnavailable) {
		t.Fatalf("err = %v, want ErrSCMUnavailable", err)
	}
}

func TestReplyToThread_ProviderNotFound(t *testing.T) {
	st, scm := newThreadWriteFixtures(fakeSCM{replyErr: fmt.Errorf("%w", ports.ErrSCMNotFound)})
	svc := NewWithDeps(Deps{Store: st, SCM: scm, Clock: func() time.Time { return time.Now() }})

	_, err := svc.ReplyToThread(context.Background(), "s1", "https://github.com/acme/repo/pull/7", "T1", "ok")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "THREAD_NOT_FOUND" {
		t.Fatalf("err = %v, want apierr THREAD_NOT_FOUND", err)
	}
}

func TestReplyToThread_ProviderForbidden(t *testing.T) {
	st, scm := newThreadWriteFixtures(fakeSCM{replyErr: fmt.Errorf("%w", ports.ErrSCMForbidden)})
	svc := NewWithDeps(Deps{Store: st, SCM: scm, Clock: func() time.Time { return time.Now() }})

	_, err := svc.ReplyToThread(context.Background(), "s1", "https://github.com/acme/repo/pull/7", "T1", "ok")
	if !errors.Is(err, ErrSCMWriteForbidden) {
		t.Fatalf("err = %v, want ErrSCMWriteForbidden", err)
	}
}

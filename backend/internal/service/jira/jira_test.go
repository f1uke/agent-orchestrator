package jira

import (
	"context"
	"errors"
	"testing"

	jiraadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeSessions struct {
	sess domain.Session
	err  error
}

func (f fakeSessions) Get(context.Context, domain.SessionID) (domain.Session, error) {
	return f.sess, f.err
}

type fakeIssues struct {
	iss jiraadapter.Issue
	err error
	got string
}

func (f *fakeIssues) Get(_ context.Context, key string) (jiraadapter.Issue, error) {
	f.got = key
	return f.iss, f.err
}

type fakeMover struct {
	transitions []jiraadapter.Transition
	listErr     error
	moveErr     error
	gotKey      string
	gotID       string
}

func (f *fakeMover) Transitions(_ context.Context, key string) ([]jiraadapter.Transition, error) {
	f.gotKey = key
	return f.transitions, f.listErr
}

func (f *fakeMover) Move(_ context.Context, key, transitionID string) error {
	f.gotKey = key
	f.gotID = transitionID
	return f.moveErr
}

func sessionWith(issueID string) domain.Session {
	return domain.Session{SessionRecord: domain.SessionRecord{ID: "s1", IssueID: domain.IssueID(issueID)}}
}

func TestContext_NotLinked(t *testing.T) {
	for _, id := range []string{"", "Fix the thing", "github:acme/repo#1", "gitlab:grp/proj#2", "jira:"} {
		issues := &fakeIssues{}
		svc := New(fakeSessions{sess: sessionWith(id)}, issues, nil)
		res, err := svc.Context(context.Background(), "s1")
		if err != nil {
			t.Fatalf("id=%q err %v", id, err)
		}
		if res.Linked {
			t.Errorf("id=%q should be unlinked", id)
		}
		if issues.got != "" {
			t.Errorf("id=%q must not call the issue reader", id)
		}
	}
}

func TestContext_LinkedSuccess(t *testing.T) {
	issues := &fakeIssues{iss: jiraadapter.Issue{Key: "DEMO-101", Title: "T"}}
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-101")}, issues, nil)
	res, err := svc.Context(context.Background(), "s1")
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if !res.Linked || res.Issue == nil || res.Issue.Key != "DEMO-101" {
		t.Fatalf("result = %+v", res)
	}
	if res.FetchError != "" {
		t.Errorf("unexpected fetch error %q", res.FetchError)
	}
	if issues.got != "DEMO-101" {
		t.Errorf("reader got key %q", issues.got)
	}
}

func TestContext_LinkedFetchError(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{jiraadapter.ErrNotFound, "not found"},
		{jiraadapter.ErrAuthFailed, "authentication"},
		{jiraadapter.ErrUnavailable, "unavailable"},
	}
	for _, tc := range cases {
		issues := &fakeIssues{err: tc.err}
		svc := New(fakeSessions{sess: sessionWith("jira:DEMO-1")}, issues, nil)
		res, err := svc.Context(context.Background(), "s1")
		if err != nil {
			t.Fatalf("err %v", err)
		}
		if !res.Linked || res.Issue != nil || res.FetchError == "" {
			t.Fatalf("expected linked with fetch error, got %+v", res)
		}
	}
}

func TestContext_NilIssueReader(t *testing.T) {
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-1")}, nil, nil)
	res, err := svc.Context(context.Background(), "s1")
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if !res.Linked || res.Issue != nil || res.FetchError == "" {
		t.Errorf("nil reader should report a fetch error, got %+v", res)
	}
}

func TestContext_SessionErrorPropagates(t *testing.T) {
	sentinel := errors.New("boom")
	svc := New(fakeSessions{err: sentinel}, &fakeIssues{}, nil)
	if _, err := svc.Context(context.Background(), "s1"); !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want propagated session error", err)
	}
}

func TestTransitions_Success(t *testing.T) {
	mover := &fakeMover{transitions: []jiraadapter.Transition{{ID: "11", Name: "Start Testing", To: "In Progress"}}}
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-101")}, &fakeIssues{}, mover)
	ts, err := svc.Transitions(context.Background(), "s1")
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if len(ts) != 1 || ts[0].Name != "Start Testing" {
		t.Fatalf("transitions = %+v", ts)
	}
	if mover.gotKey != "DEMO-101" {
		t.Errorf("mover got key %q", mover.gotKey)
	}
}

func TestTransitions_NotLinked(t *testing.T) {
	mover := &fakeMover{}
	svc := New(fakeSessions{sess: sessionWith("github:acme/repo#1")}, &fakeIssues{}, mover)
	if _, err := svc.Transitions(context.Background(), "s1"); !errors.Is(err, ErrNotLinked) {
		t.Errorf("err = %v, want ErrNotLinked", err)
	}
	if mover.gotKey != "" {
		t.Errorf("must not call the mover for an unlinked session")
	}
}

func TestTransitions_NilMover(t *testing.T) {
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-1")}, &fakeIssues{}, nil)
	if _, err := svc.Transitions(context.Background(), "s1"); !errors.Is(err, jiraadapter.ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable", err)
	}
}

func TestMove_SuccessReReadsStatus(t *testing.T) {
	issues := &fakeIssues{iss: jiraadapter.Issue{Key: "DEMO-101", Status: "In Progress", StatusCategory: "indeterminate"}}
	mover := &fakeMover{}
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-101")}, issues, mover)
	res, err := svc.Move(context.Background(), "s1", "11")
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if mover.gotKey != "DEMO-101" || mover.gotID != "11" {
		t.Errorf("mover got key=%q id=%q", mover.gotKey, mover.gotID)
	}
	if res.Key != "DEMO-101" || res.Status != "In Progress" || res.StatusCategory != "indeterminate" {
		t.Errorf("result = %+v, want the re-read status", res)
	}
}

func TestMove_NotLinked(t *testing.T) {
	mover := &fakeMover{}
	svc := New(fakeSessions{sess: sessionWith("")}, &fakeIssues{}, mover)
	if _, err := svc.Move(context.Background(), "s1", "11"); !errors.Is(err, ErrNotLinked) {
		t.Errorf("err = %v, want ErrNotLinked", err)
	}
	if mover.gotID != "" {
		t.Errorf("must not apply a move for an unlinked session")
	}
}

func TestMove_RejectionPropagates(t *testing.T) {
	mover := &fakeMover{moveErr: jiraadapter.ErrBadTransition}
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-1")}, &fakeIssues{}, mover)
	if _, err := svc.Move(context.Background(), "s1", "99"); !errors.Is(err, jiraadapter.ErrBadTransition) {
		t.Errorf("err = %v, want ErrBadTransition", err)
	}
}

func TestMove_SucceedsEvenIfReReadFails(t *testing.T) {
	// A successful move must not be reported as a failure just because the
	// best-effort status re-read errors.
	issues := &fakeIssues{err: jiraadapter.ErrUnavailable}
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-1")}, issues, &fakeMover{})
	res, err := svc.Move(context.Background(), "s1", "11")
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if res.Key != "DEMO-1" || res.Status != "" {
		t.Errorf("result = %+v, want key set and empty status", res)
	}
}

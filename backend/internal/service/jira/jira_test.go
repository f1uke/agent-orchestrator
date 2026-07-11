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

func sessionWith(issueID string) domain.Session {
	return domain.Session{SessionRecord: domain.SessionRecord{ID: "s1", IssueID: domain.IssueID(issueID)}}
}

func TestContext_NotLinked(t *testing.T) {
	for _, id := range []string{"", "Fix the thing", "github:acme/repo#1", "gitlab:grp/proj#2", "jira:"} {
		issues := &fakeIssues{}
		svc := New(fakeSessions{sess: sessionWith(id)}, issues)
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
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-101")}, issues)
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
		svc := New(fakeSessions{sess: sessionWith("jira:DEMO-1")}, issues)
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
	svc := New(fakeSessions{sess: sessionWith("jira:DEMO-1")}, nil)
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
	svc := New(fakeSessions{err: sentinel}, &fakeIssues{})
	if _, err := svc.Context(context.Background(), "s1"); !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want propagated session error", err)
	}
}

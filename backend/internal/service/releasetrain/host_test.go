package releasetrain

import (
	"errors"
	"testing"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

func completeFetch(rt.ResourceSpec, []byte) (Batch, error) { return Batch{Complete: true}, nil }

func atlassianSources(jiraCreds, wikiCreds bool) (*fakeSource, *fakeSource) {
	jira := &fakeSource{system: "jira", host: "atlassian", creds: jiraCreds, fetch: completeFetch, specs: []rt.ResourceSpec{
		rt.SpecFor(rt.KindJiraVersions, "project", ""), rt.SpecFor(rt.KindCards, "v:1", "di"), rt.SpecFor(rt.KindCards, "v:2", "di"),
	}}
	wiki := &fakeSource{system: "confluence", host: "atlassian", creds: wikiCreds, fetch: completeFetch, specs: []rt.ResourceSpec{
		rt.SpecFor(rt.KindPages, "space:DEMOSPACE", ""),
	}}
	return jira, wiki
}

func TestSourcesOnOneHostShareItsBudgetAndPause(t *testing.T) {
	jira, wiki := atlassianSources(true, true)
	h := newHarness(t, jira, wiki)
	h.limits = map[string]HostLimits{"atlassian": {Requests: 3, Concurrency: 1}}
	p := h.poller()
	h.poll(p)
	if n := len(jira.takeCalls()) + len(wiki.takeCalls()); n != 3 || p.LastPass().Requests["atlassian"] != 3 {
		t.Fatalf("fetched %d, counted %v; want one 3-request budget across Jira and Confluence", n, p.LastPass().Requests)
	}

	wiki.fetch = func(rt.ResourceSpec, []byte) (Batch, error) {
		return Batch{}, &FetchError{Class: rt.ErrRateLimited, RetryAfter: 10 * time.Minute, Err: errors.New("429")}
	}
	h.limits = map[string]HostLimits{"atlassian": {Requests: 20, Concurrency: 1}}
	h.now = t0.Add(time.Hour)
	h.poll(h.poller())
	jira.takeCalls()
	wiki.takeCalls()
	for scope, r := range h.rows() {
		if r.ErrorClass != rt.ErrRateLimited || r.NextAttemptAt == nil || r.NextAttemptAt.Before(h.now.Add(10*time.Minute)) {
			t.Errorf("%s = %s until %v; a Confluence 429 must pause every Atlassian resource", scope, r.ErrorClass, r.NextAttemptAt)
		}
	}
	h.now = h.now.Add(9 * time.Minute)
	h.poll(h.poller())
	if got := append(jira.takeCalls(), wiki.takeCalls()...); len(got) != 0 {
		t.Fatalf("fetched %v inside the 429 window", got)
	}
}

func TestOneSourcesMissingCredentialsLeaveItsHostSiblingRunning(t *testing.T) {
	jira, wiki := atlassianSources(true, false)
	h := newHarness(t, jira, wiki)
	h.limits = map[string]HostLimits{"atlassian": {Requests: 20, Concurrency: 1}}
	h.poll(h.poller())
	rows := h.rows()
	if len(jira.takeCalls()) != 3 || rows["project"].CompleteAt == nil {
		t.Fatalf("jira row = %+v; Confluence's missing credentials stopped Jira", rows["project"])
	}
	if r := rows["space:DEMOSPACE"]; r.ErrorClass != rt.ErrUnconfigured || r.ErrorMessage != "no confluence credentials" || len(wiki.takeCalls()) != 0 {
		t.Fatalf("confluence row = %+v, want unconfigured", r)
	}
}

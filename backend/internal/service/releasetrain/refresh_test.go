package releasetrain

import (
	"errors"
	"reflect"
	"testing"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

func TestRefreshMakesTheNamedSourceDueOnceOnTheNextPass(t *testing.T) {
	gl := &fakeSource{system: "gitlab", creds: true, specs: []rt.ResourceSpec{tagSpec("p:1")}, fetch: constFetch(t)}
	jira := &fakeSource{system: "jira", creds: true, specs: []rt.ResourceSpec{rt.SpecFor(rt.KindJiraVersions, "project", "")}}
	jira.fetch = func(rt.ResourceSpec, []byte) (Batch, error) { return Batch{Complete: true}, nil }
	h := newHarness(t, gl, jira)
	p := h.poller()
	h.poll(p)
	gl.takeCalls()
	jira.takeCalls()

	h.now = t0.Add(time.Minute)
	if err := p.Refresh("gitlab"); err != nil {
		t.Fatal(err)
	}
	h.poll(p)
	if got := gl.takeCalls(); !reflect.DeepEqual(got, []string{"gitlab.tags p:1 "}) {
		t.Fatalf("after refresh gitlab fetched %v, want the fresh p:1 again", got)
	}
	if got := jira.takeCalls(); len(got) != 0 {
		t.Fatalf("a gitlab refresh fetched jira %v", got)
	}
	h.now = t0.Add(2 * time.Minute)
	h.poll(p)
	if got := gl.takeCalls(); len(got) != 0 {
		t.Fatalf("the refresh was served once, yet the next pass fetched %v", got)
	}
}

func TestRefreshWithoutASourceRefreshesEverySource(t *testing.T) {
	gl := &fakeSource{system: "gitlab", creds: true, specs: []rt.ResourceSpec{tagSpec("p:1")}, fetch: constFetch(t)}
	jira := &fakeSource{system: "jira", creds: true, specs: []rt.ResourceSpec{rt.SpecFor(rt.KindJiraVersions, "project", "")}}
	jira.fetch = func(rt.ResourceSpec, []byte) (Batch, error) { return Batch{Complete: true}, nil }
	h := newHarness(t, gl, jira)
	p := h.poller()
	h.poll(p)
	gl.takeCalls()
	jira.takeCalls()

	h.now = t0.Add(time.Minute)
	if err := p.Refresh(""); err != nil {
		t.Fatal(err)
	}
	h.poll(p)
	if len(gl.takeCalls()) != 1 || len(jira.takeCalls()) != 1 {
		t.Fatal("a refresh of every source skipped one")
	}
}

func TestRefreshOfAnUnknownSourceIsRefused(t *testing.T) {
	h := newHarness(t, &fakeSource{system: "gitlab"})
	if err := h.poller().Refresh("confluence"); !errors.Is(err, ErrUnknownSource) {
		t.Fatalf("Refresh(confluence) err = %v, want ErrUnknownSource", err)
	}
}

func TestRefreshDoesNotBreakAHostPause(t *testing.T) {
	gl := &fakeSource{system: "gitlab", creds: true, specs: []rt.ResourceSpec{tagSpec("p:1"), tagSpec("p:2")}}
	gl.fetch = func(rt.ResourceSpec, []byte) (Batch, error) {
		return Batch{}, &FetchError{Class: rt.ErrRateLimited, RetryAfter: 10 * time.Minute, Err: errors.New("429")}
	}
	h := newHarness(t, gl)
	p := h.poller()
	h.poll(p)
	gl.takeCalls()

	gl.fetch = constFetch(t)
	h.now = t0.Add(time.Minute)
	if err := p.Refresh("gitlab"); err != nil {
		t.Fatal(err)
	}
	h.poll(p)
	if got := gl.takeCalls(); len(got) != 0 {
		t.Fatalf("a refresh inside a 429 window fetched %v", got)
	}
}

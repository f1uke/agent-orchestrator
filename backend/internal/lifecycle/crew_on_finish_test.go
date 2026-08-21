package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A crew is one TASK. When the reducer ends a dev because its work is over, the
// session manager's crew fan-out has to run too - the merge path is the one
// route to termination that does not go through Teardown, which is where that
// fan-out lives. These pin the contract of the injected hook: WHEN it fires,
// WHAT it is told, and that a solo session never sees it.

// crewReaperSpy records every call and, crucially, whether the session was still
// UN-terminated at the moment it fired. That is the members-then-dev ordering:
// the members go before dev's row records that the task is over.
type crewReaperSpy struct {
	calls          []domain.SessionID
	causes         []string
	devWasTerminal []bool
	err            error
}

func (s *crewReaperSpy) fn(st *fakeStore) func(context.Context, domain.SessionID, string) error {
	return func(_ context.Context, id domain.SessionID, cause string) error {
		s.calls = append(s.calls, id)
		s.causes = append(s.causes, cause)
		s.devWasTerminal = append(s.devWasTerminal, st.sessions[id].IsTerminated)
		return s.err
	}
}

func TestPRObservation_MergedEndsTheCrewBeforeDev(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", Merged: true}}
	spy := &crewReaperSpy{}
	m.SetCrewReaper(spy.fn(st))

	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Merged: true}); err != nil {
		t.Fatal(err)
	}
	if len(spy.calls) != 1 || spy.calls[0] != "mer-1" {
		t.Fatalf("crew reaper calls = %v, want exactly one for mer-1", spy.calls)
	}
	if spy.causes[0] != domain.TerminationCauseWorkComplete {
		t.Fatalf("crew reaper cause = %q, want %q: the member ends for the same reason its dev does",
			spy.causes[0], domain.TerminationCauseWorkComplete)
	}
	if spy.devWasTerminal[0] {
		t.Fatal("the crew was reaped AFTER dev terminated; the order is members, then dev, then the disk reclaim")
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("dev did not terminate")
	}
}

// Best-effort: a member that will not die must not stop dev from recording that
// its work is done, or the task becomes one nobody can finish.
func TestPRObservation_CrewReaperFailureStillTerminatesDev(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", Merged: true}}
	spy := &crewReaperSpy{err: errors.New("tmux is wedged")}
	m.SetCrewReaper(spy.fn(st))

	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Merged: true}); err != nil {
		t.Fatalf("a failed crew teardown must not fail the observation: %v", err)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("a failed crew teardown left dev un-terminated")
	}
}

// The completion bar is unchanged: a dev with an open sibling PR is not finished,
// so its crew is not ended either.
func TestPRObservation_OpenSiblingDoesNotEndTheCrew(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", Merged: true}, {URL: "pr2"}}
	spy := &crewReaperSpy{}
	m.SetCrewReaper(spy.fn(st))

	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Merged: true}); err != nil {
		t.Fatal(err)
	}
	if len(spy.calls) != 0 {
		t.Fatalf("a dev with an open PR is not finished; its crew must stay: %v", spy.calls)
	}
}

// A keep-warm worker SUSPENDS in place instead of terminating - it is expected to
// open more PRs, so it is not finished and its crew must survive with it.
func TestPRObservation_KeepWarmMergeDoesNotEndTheCrew(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = mergedWorker("mer-1", domain.KindWorker, true)
	st.prs["mer-1"] = []domain.PullRequest{{Number: 7, Merged: true}}
	spy := &crewReaperSpy{}
	m.SetCrewReaper(spy.fn(st))
	m.SetRuntimeSuspender(func(context.Context, domain.SessionID) error { return nil })

	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Merged: true}); err != nil {
		t.Fatal(err)
	}
	if len(spy.calls) != 0 {
		t.Fatalf("a keep-warm worker is not finished; its crew must not be reaped: %v", spy.calls)
	}
}

// A closing tracker issue ends the session the same way a merge does, so it has
// to end the crew the same way too.
func TestTrackerFacts_IssueClosedEndsTheCrewBeforeDev(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	spy := &crewReaperSpy{}
	m.SetCrewReaper(spy.fn(st))

	o := ports.TrackerObservation{
		Fetched: true,
		Issue:   ports.TrackerIssueObservation{URL: "https://github.com/o/r/issues/1", State: domain.IssueDone},
	}
	if err := m.ApplyTrackerFacts(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(spy.calls) != 1 || spy.causes[0] != domain.TerminationCauseIssueClosed {
		t.Fatalf("crew reaper calls = %v causes = %v, want one %q", spy.calls, spy.causes, domain.TerminationCauseIssueClosed)
	}
	if spy.devWasTerminal[0] {
		t.Fatal("the crew was reaped after dev terminated; members go first")
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("a closed issue did not terminate the session")
	}
}

// A Manager with no reaper wired - every pre-existing test, and a daemon built
// before this hook existed - still terminates exactly as it did.
func TestPRObservation_MergedWithNoCrewReaperTerminatesAsBefore(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", Merged: true}}

	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Merged: true}); err != nil {
		t.Fatal(err)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("an unwired Manager stopped terminating on merge")
	}
}

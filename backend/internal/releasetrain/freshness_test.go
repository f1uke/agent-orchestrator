package releasetrain

import (
	"testing"
	"time"
)

func sourceOf(t *testing.T, b Board, system string) SourceFreshness {
	t.Helper()
	for _, s := range b.Sources {
		if s.System == system {
			return s
		}
	}
	t.Fatalf("no %s source", system)
	return SourceFreshness{}
}

func withLookupsHoursOld(f *fixture, hours int) Inputs {
	in := f.inputs()
	at := f.now.Add(-time.Duration(hours) * time.Hour)
	old := func(kind KindName, scope Scope) ResourceStatus {
		return ResourceStatus{Kind: kind, Scope: scope, CompleteAt: &at, LastAttemptAt: &at}
	}
	Put(&in.Snap, KindJiraUsers, old(KindJiraUsers.Name, UserScope("acc-ann")), []JiraUserFact{{AccountID: "acc-ann", DisplayName: "Ann"}})
	Put(&in.Snap, KindGitLabUser, old(KindGitLabUser.Name, MyselfScope), []GitLabUserFact{{Username: "dee", Name: "Dee"}})
	return in
}

func TestASourceIsAsFreshAsTheDataThePageDependsOnNotItsOldestLookup(t *testing.T) {
	f := newFixture(t, today)
	healthy(f)
	f.mr("di", "DEMO-1 a", "feat/DEMO-1", "develop", MROpened, "2026-10-12", "")
	b := Reconcile(withLookupsHoursOld(f, 5), f.env())

	for _, system := range []string{"gitlab", "jira"} {
		s := sourceOf(t, b, system)
		if s.AsOf == nil || f.now.Sub(*s.AsOf) > time.Minute {
			t.Errorf("%s age = %v; MRs, jobs, milestones and versions are fresh, so a 5h-old lookup must not age it", system, s.AsOf)
		}
		if len(s.Issues) != 0 {
			t.Errorf("%s issues = %+v; a lookup inside its 24h budget is no issue", system, s.Issues)
		}
	}
	if users := kindEntry(t, b, KindJiraUsers.Name); users.AsOf == nil || f.now.Sub(*users.AsOf) < 4*time.Hour {
		t.Errorf("jira.users = %+v; the lookup keeps its own age in its kind entry", users)
	}
}

func TestASourceReadsStaleWhenADataKindIsReallyStale(t *testing.T) {
	f := newFixture(t, today)
	healthy(f)
	f.stale(KindMRs.Name)
	b := Reconcile(withLookupsHoursOld(f, 5), f.env())

	s := sourceOf(t, b, "gitlab")
	if s.AsOf == nil || f.now.Sub(*s.AsOf) < 23*time.Hour {
		t.Fatalf("gitlab age = %v; the day-old MR read must age the source", s.AsOf)
	}
	if len(s.Issues) == 0 {
		t.Fatalf("gitlab lists no issue for its stale MR reads")
	}
}

func TestAFailingLookupStaysAnIssueOfItsSourceButNotItsAge(t *testing.T) {
	f := newFixture(t, today)
	healthy(f)
	in := withLookupsHoursOld(f, 5)
	st, _ := in.Snap.Status(KindGitLabUser.Name, MyselfScope)
	st.ErrorClass = ErrAuth
	Put(&in.Snap, KindGitLabUser, st, []GitLabUserFact{{Username: "dee"}})
	b := Reconcile(in, f.env())

	s := sourceOf(t, b, "gitlab")
	if len(s.Issues) != 1 || s.Issues[0].Kind != KindGitLabUser.Name {
		t.Fatalf("gitlab issues = %+v; want the failing lookup", s.Issues)
	}
	if s.AsOf == nil || f.now.Sub(*s.AsOf) > time.Minute {
		t.Fatalf("gitlab age = %v; a failing lookup is an issue, not an age", s.AsOf)
	}
}

package releasetrain

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestIdentityIsKnownOnlyWhenBothAccountsAreRead(t *testing.T) {
	for _, tc := range []struct {
		name         string
		jira, gitlab bool
		want         IdentityState
	}{
		{"both", true, true, IdentityKnown{JiraAccountID: "acc-dee", GitLabUsername: "dee", DisplayName: "Dee"}},
		{"no GitLab user", true, false, IdentityUnknown{Reason: "the GitLab user (gitlab.user) has not been read"}},
		{"no Jira account", false, true, IdentityUnknown{Reason: "the Jira account (jira.myself) has not been read"}},
		{"neither", false, false, IdentityUnknown{Reason: "neither the Jira account (jira.myself) nor the GitLab user (gitlab.user) has been read"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, today)
			f.me()
			if !tc.jira {
				f.myself = nil
			}
			if !tc.gitlab {
				f.gitlabUser = nil
			}
			if got := f.reconcile().Identity; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("identity = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestIdentityJSONCarriesItsKind(t *testing.T) {
	f := newFixture(t, today)
	f.me()
	raw, err := json.Marshal(f.reconcile())
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Identity map[string]string `json:"identity"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"kind": "known", "jiraAccountId": "acc-dee", "gitlabUsername": "dee", "displayName": "Dee"}
	if !reflect.DeepEqual(w.Identity, want) {
		t.Fatalf("identity = %v, want %v", w.Identity, want)
	}
}

func mineSetup(t *testing.T) (*fixture, *fv) {
	t.Helper()
	f := newFixture(t, today)
	f.me()
	v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	v.releaseMR(liveFreeze, MROpened)
	v.issue("DEMO-1", "In Progress", "indeterminate")
	v.issue("DEMO-2", "In Progress", "indeterminate")
	v.issue("DEMO-3", "In Progress", "indeterminate")
	v.issue("DEMO-4", "In Progress", "indeterminate")
	f.issues[v.jira.ID][0].Assignee = dee
	f.subtasks = append(f.subtasks, &IssueFact{Key: "DEMO-20", Parent: "DEMO-2", Subtask: true, Assignee: dee})
	mr := f.mr("di", "DEMO-3 part", "feat/DEMO-3", "develop", MRMerged, "2026-10-01", "2026-10-02")
	mr.AuthorUsername = "dee"
	f.mr("di", "DEMO-4 part", "feat/DEMO-4", "develop", MRMerged, "2026-10-01", "2026-10-02")
	return f, v
}

func TestMineOnCards(t *testing.T) {
	f, _ := mineSetup(t)
	b := f.reconcile()
	got := map[string][]MineWhy{}
	for _, c := range b.Trains[0].version("di").Cards {
		for _, r := range c.Mine {
			got[c.Key] = append(got[c.Key], r.Why)
		}
	}
	want := map[string][]MineWhy{"DEMO-1": {MineAssignee}, "DEMO-2": {MineSubtask}, "DEMO-3": {MineMRAuthor}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("card mine = %v, want %v (DEMO-4's MR is someone else's)", got, want)
	}
	if vm := b.Trains[0].version("di").Mine; len(vm) != 3 || vm[0].Why != MineCard {
		t.Fatalf("version mine = %v, want the three cards of mine", vm)
	}
}

func TestMineOnVersions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fixture, v *fv)
		want  MineWhy
	}{
		{"release MR author", func(f *fixture, v *fv) { v.mr.AuthorUsername = "dee" }, MineReleaseMR},
		{"release ticket assignee", func(f *fixture, v *fv) {
			f.issues[v.jira.ID] = append(f.issues[v.jira.ID], &IssueFact{Key: "DEMO-9", ReleaseTicket: true, Assignee: dee, FixVersions: []string{v.jira.ID}})
		}, MineReleaseTicket},
		{"approver", func(f *fixture, v *fv) { v.approvers("Ann:APPROVED", "dee:PENDING") }, MineApprover},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, today)
			f.me()
			v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
			v.releaseMR(liveFreeze, MROpened)
			tc.setup(f, v)
			got := f.reconcile().Trains[0].version("di").Mine
			if len(got) != 1 || got[0].Why != tc.want {
				t.Fatalf("version mine = %v, want %s", got, tc.want)
			}
		})
	}
}

func TestMineOnHotfixesAndLongLivedGroups(t *testing.T) {
	f := newFixture(t, today)
	f.me()
	ms := f.milestone("di", "Hotfix 3.4.1", "crash", "2026-10-10", "2026-10-13")
	hf := f.mr("di", "fix crash", "hotfix/3.4.1", "main", MRMerged, "2026-10-10", "2026-10-11")
	hf.MilestoneID, hf.AuthorUsername = &ms.ID, "dee"

	f.mr("di", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
	f.llbCards = append(f.llbCards, &IssueFact{Key: "DEMO-1", Assignee: dee})
	f.mr("di", "DEMO-2 bond", "feat/DEMO-2", "feature/demo-bond", MRMerged, "2026-09-01", "2026-09-02")
	land := f.mr("di", "bond", "feature/demo-bond", "develop", MROpened, "2026-10-12", "")
	land.AuthorUsername = "dee"
	f.mr("di", "DEMO-3 chat", "feat/DEMO-3", "feature/demo-chat", MRMerged, "2026-09-01", "2026-09-02")
	b := f.reconcile()

	if len(b.Hotfixes) != 1 || len(b.Hotfixes[0].Mine) != 1 || b.Hotfixes[0].Mine[0].Why != MineMRAuthor {
		t.Fatalf("hotfixes = %+v, want my hotfix MR", b.Hotfixes)
	}
	got := map[PlanID][]MineWhy{}
	for _, g := range b.LLBs {
		for _, r := range g.Mine {
			got[g.ID] = append(got[g.ID], r.Why)
		}
	}
	want := map[PlanID][]MineWhy{"demo-wallet": {MineCard}, "demo-bond": {MineLanding}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("group mine = %v, want %v", got, want)
	}
}

func TestMineOnLongLivedBranchCards(t *testing.T) {
	f := newFixture(t, today)
	f.me()
	f.mr("di", "DEMO-1 wallet", "feat/DEMO-1", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
	f.llbCards = append(f.llbCards, &IssueFact{Key: "DEMO-1", Assignee: dee})
	authored := f.mr("di", "DEMO-2 wallet", "feat/DEMO-2", "feature/demo-wallet", MRMerged, "2026-09-03", "2026-09-04")
	authored.AuthorUsername = "dee"
	f.mr("di", "DEMO-3 wallet", "feat/DEMO-3", "feature/demo-wallet", MRMerged, "2026-09-05", "2026-09-06")
	got := map[string][]MineWhy{}
	for _, c := range f.reconcile().LLBs[0].Branches[0].Cards {
		got[c.Key] = nil
		for _, r := range c.Mine {
			got[c.Key] = append(got[c.Key], r.Why)
		}
	}
	want := map[string][]MineWhy{"DEMO-1": {MineAssignee}, "DEMO-2": {MineMRAuthor}, "DEMO-3": nil}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("long-lived card mine = %v, want %v", got, want)
	}
}

func TestMineOnHitsFollowsTheirSubject(t *testing.T) {
	f, v := mineSetup(t)
	v.mr.AuthorUsername = "dee"
	other := f.version("da", "21", "3.5.0", liveFreeze, liveDue)
	other.releaseMR(liveFreeze, MROpened)
	v.detail().Conflicts = true
	other.detail().Conflicts = true
	f.mr("da", "DEMO-50", "feat/DEMO-50", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-01")
	f.mr("da", "DEMO-5 wallet", "feat/DEMO-5", "feature/demo-wallet", MRMerged, "2026-09-01", "2026-09-02")
	land := f.mr("da", "wallet", "feature/demo-wallet", "develop", MROpened, "2026-10-12", "")
	land.AuthorUsername = "dee"
	b := f.reconcile()

	mine := map[HitID]bool{}
	for _, h := range b.Alerts.Hits {
		mine[h.ID] = len(h.Mine) > 0
	}
	for id, want := range map[HitID]bool{
		"C5~v/21-2026/di": true,
		"C5~v/21-2026/da": false,
		HitID("L1~mr/da/" + strconv.Itoa(land.IID)): true,
	} {
		if got, ok := mine[id]; !ok || got != want {
			t.Fatalf("hit %s mine = %v (listed %v), want %v; hits %v", id, got, ok, want, mine)
		}
	}
}

func TestUnknownIdentityLeavesEveryMineEmpty(t *testing.T) {
	f, v := mineSetup(t)
	f.gitlabUser = nil
	v.mr.AuthorUsername = "dee"
	b := f.reconcile()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"mine"`) {
		t.Fatalf("board with unknown identity carries a mine field")
	}
}

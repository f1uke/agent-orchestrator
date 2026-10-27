package releasetrain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// peopleBoard puts Dee (the tokens' owner) and these people on one train:
//   - Ann: Jira acc-ann and GitLab ann, one display name "Ann Example" (a name match).
//   - Bob: Jira acc-bob "Bob Builder" and GitLab bobby "Bobby B." (joined only by a link).
//   - Cat: Jira acc-cat and two GitLab users, all "Cat Example" (ambiguous, so never joined).
//   - Dave: Jira acc-dave only.
//   - Eve: GitLab eve only.
func peopleBoard(t *testing.T, mutate func(f *fixture), links ...PersonLink) (*fixture, Inputs) {
	t.Helper()
	f := newFixture(t, today)
	f.me()
	v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	v.releaseMR(liveFreeze, MROpened)
	for _, key := range []string{"DEMO-1", "DEMO-2", "DEMO-3", "DEMO-4", "DEMO-5", "DEMO-6", "DEMO-7"} {
		v.issue(key, "In Progress", "indeterminate")
	}
	issues := f.issues[v.jira.ID]
	issues[0].Assignee = &Person{AccountID: "acc-ann", Name: "Ann Example"}
	issues[1].Assignee = &Person{AccountID: "acc-bob", Name: "Bob Builder"}
	issues[2].Assignee = &Person{AccountID: "acc-cat", Name: "Cat Example"}
	issues[3].Assignee = &Person{AccountID: "acc-dave", Name: "Dave Example"}
	byUser := func(key, username, name string) {
		mr := f.mr("di", key+" part", "feat/"+key, "develop", MRMerged, "2026-10-01", "2026-10-02")
		mr.AuthorUsername, mr.Author = username, name
	}
	byUser("DEMO-1", "ann", "Ann Example")
	byUser("DEMO-2", "bobby", "Bobby B.")
	byUser("DEMO-3", "cat1", "Cat Example")
	byUser("DEMO-5", "cat2", "Cat Example")
	byUser("DEMO-6", "eve", "Eve Example")
	byUser("DEMO-7", "", "Rae")
	if mutate != nil {
		mutate(f)
	}
	in := f.inputs()
	in.Links = links
	return f, in
}

func personByID(t *testing.T, b Board, id string) BoardPerson {
	t.Helper()
	for _, p := range b.People {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("no person %q in %+v", id, b.People)
	return BoardPerson{}
}

func TestPeopleAreJoinedByLinkThenSelfThenUnambiguousName(t *testing.T) {
	f, in := peopleBoard(t, nil, PersonLink{JiraAccountID: "acc-bob", GitLabUsername: "bobby"})
	b := Reconcile(in, f.env())

	want := []BoardPerson{
		{ID: "ann", Name: "Ann Example", JiraAccountID: "acc-ann", GitLabUsername: "ann", Match: MatchName},
		{ID: "bobby", Name: "Bob Builder", JiraAccountID: "acc-bob", GitLabUsername: "bobby", Match: MatchLinked},
		{ID: "cat1", Name: "Cat Example", GitLabUsername: "cat1", Match: MatchGitLabOnly},
		{ID: "cat2", Name: "Cat Example", GitLabUsername: "cat2", Match: MatchGitLabOnly},
		{ID: "jira:acc-cat", Name: "Cat Example", JiraAccountID: "acc-cat", Match: MatchJiraOnly},
		{ID: "jira:acc-dave", Name: "Dave Example", JiraAccountID: "acc-dave", Match: MatchJiraOnly},
		{ID: "dee", Name: "Dee", JiraAccountID: "acc-dee", GitLabUsername: "dee", Match: MatchSelf, Self: true},
		{ID: "eve", Name: "Eve Example", GitLabUsername: "eve", Match: MatchGitLabOnly},
	}
	if !reflect.DeepEqual(b.People, want) {
		t.Fatalf("people =\n%+v\nwant\n%+v", b.People, want)
	}
}

func TestWithoutALinkTwoDifferentNamesStaySeparate(t *testing.T) {
	f, in := peopleBoard(t, nil)
	b := Reconcile(in, f.env())
	if p := personByID(t, b, "jira:acc-bob"); p.Match != MatchJiraOnly || p.GitLabUsername != "" {
		t.Fatalf("Bob's Jira account = %+v, want Jira only", p)
	}
	if p := personByID(t, b, "bobby"); p.Match != MatchGitLabOnly || p.JiraAccountID != "" {
		t.Fatalf("Bob's GitLab user = %+v, want GitLab only", p)
	}
}

func TestPeopleNamesAreMatchedIgnoringCaseAndSpacing(t *testing.T) {
	f, in := peopleBoard(t, func(f *fixture) {
		f.issues[f.versions[0].ID][0].Assignee.Name = "  ann   EXAMPLE "
	})
	if p := personByID(t, Reconcile(in, f.env()), "ann"); p.Match != MatchName {
		t.Fatalf("Ann = %+v, want a name match across case and spacing", p)
	}
}

func TestALinkToAPersonNotOnTheBoardAddsNoOne(t *testing.T) {
	f, in := peopleBoard(t, nil, PersonLink{JiraAccountID: "acc-zed", GitLabUsername: "zed", Name: "Zed Example"})
	for _, p := range Reconcile(in, f.env()).People {
		if p.ID == "zed" || p.JiraAccountID == "acc-zed" {
			t.Fatalf("the board lists %+v, who is on no card", p)
		}
	}
}

func TestAnAccountBelongsToTheFirstLinkThatClaimsIt(t *testing.T) {
	f, in := peopleBoard(t, nil,
		PersonLink{JiraAccountID: "acc-bob", GitLabUsername: "bobby", Name: "Bob"},
		PersonLink{JiraAccountID: "acc-bob", GitLabUsername: "eve", Name: "Not Bob"},
	)
	b := Reconcile(in, f.env())
	if p := personByID(t, b, "bobby"); p.Name != "Bob" || p.Match != MatchLinked {
		t.Fatalf("Bob = %+v", p)
	}
	if p := personByID(t, b, "eve"); p.Match != MatchGitLabOnly {
		t.Fatalf("Eve = %+v, want her own GitLab-only line since Bob's Jira account was taken", p)
	}
}

func TestALinkOverridesTheMonitorsOwnPairing(t *testing.T) {
	f, in := peopleBoard(t, nil, PersonLink{JiraAccountID: "acc-dee", GitLabUsername: "dee2", Name: "Dee Two"})
	b := Reconcile(in, f.env())
	p := personByID(t, b, "dee2")
	if p.JiraAccountID != "acc-dee" || p.Match != MatchLinked || !p.Self {
		t.Fatalf("person = %+v, want the link to win and stay the monitor's own person", p)
	}
	if v, ok := b.Viewer.(ViewerPerson); !ok || v.Person.ID != "dee2" {
		t.Fatalf("viewer = %#v, want the linked person", b.Viewer)
	}
}

func TestThePeopleListIsNeverNilAndAnUnknownIdentityHasNoViewer(t *testing.T) {
	f := newFixture(t, today)
	b := f.reconcile()
	if b.People == nil || len(b.People) != 0 {
		t.Fatalf("people = %#v, want an empty list", b.People)
	}
	want := ViewerUnknown{Reason: "neither the Jira account (jira.myself) nor the GitLab user (gitlab.user) has been read"}
	if !reflect.DeepEqual(b.Viewer, want) {
		t.Fatalf("viewer = %#v, want %#v", b.Viewer, want)
	}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"people":[]`) {
		t.Fatalf("board JSON lacks an empty people list: %s", raw)
	}
}

func TestTheViewerIsTheMonitorsOwnPersonUnlessAnIdIsGiven(t *testing.T) {
	f, in := peopleBoard(t, nil, PersonLink{JiraAccountID: "acc-bob", GitLabUsername: "bobby"})
	for _, tc := range []struct {
		name, viewer, wantID string
	}{
		{"nobody named", "", "dee"},
		{"gitlab username", "ann", "ann"},
		{"jira form of a merged person", "jira:acc-ann", "ann"},
		{"jira form of a linked person, after the link added his GitLab side", "jira:acc-bob", "bobby"},
		{"username of a linked person", "bobby", "bobby"},
		{"jira-only person", "jira:acc-dave", "jira:acc-dave"},
		{"gitlab-only person", "eve", "eve"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in.Viewer = tc.viewer
			v, ok := Reconcile(in, f.env()).Viewer.(ViewerPerson)
			if !ok || v.Person.ID != tc.wantID {
				t.Fatalf("viewer = %#v, want person %s", v, tc.wantID)
			}
		})
	}
}

func TestAViewerNotOnTheBoardCarriesNoMineAnywhere(t *testing.T) {
	f, in := peopleBoard(t, func(f *fixture) {
		f.subtasks = append(f.subtasks, &IssueFact{Key: "DEMO-20", Parent: "DEMO-2", Subtask: true, Assignee: dee})
		f.issues[f.versions[0].ID][0].Assignee = dee
	})
	self := Reconcile(in, f.env())
	if !strings.Contains(mustJSON(t, self), `"mine":[`) {
		t.Fatal("the fixture carries nothing of Dee's, so the check below proves nothing")
	}
	in.Viewer = "nobody"
	b := Reconcile(in, f.env())
	want := ViewerUnknown{ID: "nobody", Reason: `no person "nobody" on the Board`}
	if !reflect.DeepEqual(b.Viewer, want) {
		t.Fatalf("viewer = %#v, want %#v", b.Viewer, want)
	}
	if raw := mustJSON(t, b); strings.Contains(raw, `"mine":[`) {
		t.Fatalf("a Mine is set for a viewer nobody is: %s", raw)
	}
	for _, p := range b.People {
		if p.Self != (p.ID == "dee") {
			t.Fatalf("person %+v: the monitor's own flag does not move with the viewer", p)
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func mineOfCards(b Board) map[string][]MineWhy {
	out := map[string][]MineWhy{}
	for _, c := range b.Trains[0].version("di").Cards {
		for _, r := range c.Mine {
			out[c.Key] = append(out[c.Key], r.Why)
		}
	}
	return out
}

func TestAJiraOnlyViewerOwnsHisCardsAndNoMRWithoutAnAuthor(t *testing.T) {
	f, in := peopleBoard(t, func(f *fixture) {
		f.subtasks = append(f.subtasks, &IssueFact{Key: "DEMO-30", Parent: "DEMO-6", Subtask: true, Assignee: &Person{AccountID: "acc-dave", Name: "Dave Example"}})
	})
	in.Viewer = "jira:acc-dave"
	b := Reconcile(in, f.env())
	want := map[string][]MineWhy{"DEMO-4": {MineAssignee}, "DEMO-6": {MineSubtask}}
	if got := mineOfCards(b); !reflect.DeepEqual(got, want) {
		t.Fatalf("Dave's card mine = %v, want %v (DEMO-7's MR has no author username and must match no one)", got, want)
	}
}

func TestAGitLabOnlyViewerOwnsHisMRsAndNoCardWithoutAnAssigneeId(t *testing.T) {
	f, in := peopleBoard(t, func(f *fixture) {
		issues := f.issues[f.versions[0].ID]
		issues[5].Assignee = &Person{Name: "Nobody In Particular"}
	})
	in.Viewer = "eve"
	b := Reconcile(in, f.env())
	want := map[string][]MineWhy{"DEMO-6": {MineMRAuthor}}
	if got := mineOfCards(b); !reflect.DeepEqual(got, want) {
		t.Fatalf("Eve's card mine = %v, want %v (DEMO-6's assignee has no account id and must match no one)", got, want)
	}
	if vm := b.Trains[0].version("di").Mine; len(vm) != 1 || vm[0].Why != MineCard || vm[0].Ref != "DEMO-6" {
		t.Fatalf("version mine = %v, want only DEMO-6", vm)
	}
}

func TestAMergedViewerOwnsWhatEitherAccountDid(t *testing.T) {
	f, in := peopleBoard(t, nil, PersonLink{JiraAccountID: "acc-bob", GitLabUsername: "bobby"})
	in.Viewer = "jira:acc-bob"
	b := Reconcile(in, f.env())
	want := map[string][]MineWhy{"DEMO-2": {MineAssignee, MineMRAuthor}}
	if got := mineOfCards(b); !reflect.DeepEqual(got, want) {
		t.Fatalf("Bob's card mine = %v, want %v", got, want)
	}
}

func TestACardSubtaskIsMineForItsAssigneeAndNoOneElse(t *testing.T) {
	f, in := peopleBoard(t, func(f *fixture) {
		f.subtasks = append(f.subtasks,
			&IssueFact{Key: "DEMO-20", Parent: "DEMO-4", Subtask: true, Assignee: &Person{AccountID: "acc-ann", Name: "Ann Example"}},
			&IssueFact{Key: "DEMO-21", Parent: "DEMO-4", Subtask: true},
		)
	})
	in.Viewer = "ann"
	got := mineOfCards(Reconcile(in, f.env()))["DEMO-4"]
	if want := []MineWhy{MineSubtask}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Ann's mine on DEMO-4 = %v, want %v (her subtask only, not the unassigned one)", got, want)
	}
	in.Viewer = ""
	if got := mineOfCards(Reconcile(in, f.env()))["DEMO-4"]; got != nil {
		t.Fatalf("Dee's mine on DEMO-4 = %v, want none: the subtask is Ann's", got)
	}
}

func TestSubtaskAssigneesAreOnTheBoardAsPeople(t *testing.T) {
	f, in := peopleBoard(t, func(f *fixture) {
		f.subtasks = append(f.subtasks,
			&IssueFact{Key: "DEMO-20", Parent: "DEMO-4", Subtask: true, Assignee: &Person{AccountID: "acc-fay", Name: "Fay Example"}},
			&IssueFact{Key: "DEMO-90", Parent: "DEMO-99", Subtask: true, Assignee: &Person{AccountID: "acc-gil", Name: "Gil Example"}},
		)
	})
	b := Reconcile(in, f.env())
	personByID(t, b, "jira:acc-fay")
	for _, p := range b.People {
		if p.JiraAccountID == "acc-gil" {
			t.Fatalf("%+v works on a card that is not on the Board", p)
		}
	}
}

func TestRuleMinePoliciesFollowTheViewer(t *testing.T) {
	t.Run("pending approver", func(t *testing.T) {
		f, _ := approvalTrain(t, "ann:PENDING", "dee:APPROVED")
		in := f.inputs()
		in.Viewer = "jira:acc-ann"
		expectMine(t, Reconcile(in, f.env()), "D3~v/21-2026/di", []MineReason{{Why: MineApprover}})
		in.Viewer = ""
		expectMine(t, Reconcile(in, f.env()), "D3~v/21-2026/di", nil)
	})
	t.Run("release owner", func(t *testing.T) {
		f := newFixture(t, today)
		f.me()
		v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
		v.jira.Name = "iOS 3.5.1"
		v.approvers("dee:PENDING")
		v.issue("DEMO-1", "In Progress", "indeterminate")
		v.releaseMR(liveFreeze, MROpened)
		v.jobList().Jobs = []JobFact{{Name: "build_test", Status: "failed"}}
		v.mr.AuthorUsername, v.mr.Author = "ann", "Ann Example"
		in := f.inputs()
		in.Viewer = "ann"
		b := Reconcile(in, f.env())
		expectMine(t, b, "C1~v/21-2026/di", []MineReason{{Why: MineReleaseMR, Ref: mrRef(*v.mr)}})
		in.Viewer = ""
		expectMine(t, Reconcile(in, f.env()), "C1~v/21-2026/di", nil)
	})
}

func TestViewerJSONCarriesItsKind(t *testing.T) {
	f, in := peopleBoard(t, nil)
	for _, tc := range []struct {
		viewer string
		want   string
	}{
		{"", `{"kind":"person","person":{"id":"dee","name":"Dee","jiraAccountId":"acc-dee","gitlabUsername":"dee","match":"self","self":true}}`},
		{"jira:acc-dave", `{"kind":"person","person":{"id":"jira:acc-dave","name":"Dave Example","jiraAccountId":"acc-dave","match":"jira_only"}}`},
		{"zed", `{"kind":"unknown","id":"zed","reason":"no person \"zed\" on the Board"}`},
	} {
		in.Viewer = tc.viewer
		raw, err := json.Marshal(Reconcile(in, f.env()).Viewer)
		if err != nil || string(raw) != tc.want {
			t.Fatalf("viewer %q writes %s (%v), want %s", tc.viewer, raw, err, tc.want)
		}
	}
}

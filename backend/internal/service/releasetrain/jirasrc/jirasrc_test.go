package jirasrc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/atlassian"
	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	svc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/rttest"
)

var bangkok = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		panic(err)
	}
	return loc
}()

func testView() rt.PlanView {
	cfg := rt.Config{
		Timezone: "Asia/Bangkok", HistoryFrom: rt.Day{Y: 2026, M: time.April, D: 1}, AgeOutDays: 7, FixWrites: "off", GitLabHost: "gitlab.example.com",
		Jira: rt.JiraConfig{Project: "DEMO", TechRequestProject: "DEMOREQ", ForceUpdateSummary: "force update"},
		Families: []rt.Family{
			{ID: "demo", Label: "Demo", TrainLabel: "Release Train", Apps: []rt.AppID{"di", "da"}},
			{ID: "pilot", Label: "Pilot", TrainLabel: "Pilot Release Train", Apps: []rt.AppID{"pi"}},
		},
		Apps: []rt.App{
			{ID: "di", Platform: rt.PlatformIOS, GitLabProject: 1001, JiraVersionPrefix: "iOS"},
			{ID: "da", Platform: rt.PlatformAndroid, GitLabProject: 1002, JiraVersionPrefix: "Android"},
			{ID: "pi", Platform: rt.PlatformIOS, GitLabProject: 1003, JiraVersionPrefix: "iOS Pilot"},
		},
	}
	return rt.PlanView{
		Config: cfg, Loc: bangkok, HistoryFrom: cfg.HistoryFrom, Apps: cfg.Apps,
		Versions: []rt.PlannedVersion{
			{App: "di", Project: 1001, Live: true, JiraVersionID: "5001"},
			{App: "da", Project: 1002, Live: false, JiraVersionID: "5002"},
			{App: "pi", Project: 1003, Live: true},
		},
		PersonIDs:       []string{"acc-ann", "acc-bob"},
		LLBCardKeys:     []string{"DEMO-30", "DEMO-31"},
		MyAccountID:     "acc-dee",
		WatchedCardKeys: []string{"DEMO-1", "DEMO-30"},
	}
}

func TestPlanLeavesMySubtasksOutUntilMyselfIsRead(t *testing.T) {
	v := testView()
	v.MyAccountID = ""
	for _, s := range newSource(rttest.NewJira(t)).Plan(v) {
		if s.Kind == rt.KindMySubtasks.Name {
			t.Fatalf("planned %s before jira.myself was read", s.Kind)
		}
	}
}

func newSource(jira *rttest.Host) *Source {
	return New(Options{Config: func() (atlassian.Config, error) {
		return atlassian.Config{BaseURL: jira.URL, Email: "dee@example.com", Token: "t"}, nil
	}})
}

func TestPlanListsEveryJiraResourceTheViewNeeds(t *testing.T) {
	var got []string
	for _, s := range newSource(rttest.NewJira(t)).Plan(testView()) {
		got = append(got, fmt.Sprintf("%s %s %s %v", s.Kind, s.Scope, s.App, s.Budget))
	}
	want := []string{
		"jira.versions project:DEMO  5m0s",
		"jira.techreq project:DEMOREQ  30m0s",
		"jira.relatedwork v:5001 di 15m0s",
		"jira.cards v:5001 di 5m0s",
		"jira.relatedwork v:5002 da 6h0m0s",
		"jira.cards v:5002 da 6h0m0s",
		"jira.users u:acc-ann  24h0m0s",
		"jira.users u:acc-bob  24h0m0s",
		"jira.llb_cards llb  10m0s",
		"jira.my_subtasks mine  5m0s",
		"jira.myself me  24h0m0s",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestPlanLeavesTechReqOutWhenNotConfigured(t *testing.T) {
	v := testView()
	v.Config.Jira.TechRequestProject = ""
	for _, s := range newSource(rttest.NewJira(t)).Plan(v) {
		if s.Kind == rt.KindTechReq.Name {
			t.Fatalf("planned %s %s without a tech request project", s.Kind, s.Scope)
		}
	}
}

func fetch(t *testing.T, s *Source, spec rt.ResourceSpec) (svc.Batch, error) {
	t.Helper()
	return s.Fetch(context.Background(), spec, testView(), nil)
}

func decode[T any](t *testing.T, k rt.Kind[T], b svc.Batch) []T {
	t.Helper()
	out := make([]T, 0, len(b.Items))
	for _, it := range b.Items {
		v, err := k.Decode(it.Body)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func TestVersionsKeepThoseInRangeWithApproverIDs(t *testing.T) {
	jira := rttest.NewJira(t)
	jira.Set("/rest/api/3/project/DEMO/version", `{"isLast":true,"values":[
		{"id":"5001","name":"iOS 3.5.0","description":"Release Train #21/2026","released":false,"releaseDate":"2026-10-19","approvers":[{"accountId":"acc-ann","status":"APPROVED"}]},
		{"id":"4001","name":"iOS 2.0.0","description":"Release Train #2/2025","released":true,"releaseDate":"2025-01-10"},
		{"id":"5009","name":"iOS 3.6.0","description":"Release Train #23/2026","released":false}
	]}`)
	b, err := fetch(t, newSource(jira), rt.SpecFor(rt.KindJiraVersions, "project:DEMO", ""))
	if err != nil || !b.Complete {
		t.Fatalf("fetch: %v", err)
	}
	rd := rt.Day{Y: 2026, M: 10, D: 19}
	want := []rt.JiraVersionFact{
		{ID: "5001", Name: "iOS 3.5.0", Description: "Release Train #21/2026", ReleaseDate: &rd,
			Approvers: []rt.Approver{{AccountID: "acc-ann", Status: "APPROVED"}}, URL: jira.URL + "/projects/DEMO/versions/5001"},
		{ID: "5009", Name: "iOS 3.6.0", Description: "Release Train #23/2026", Approvers: []rt.Approver{}, URL: jira.URL + "/projects/DEMO/versions/5009"},
	}
	if got := decode(t, rt.KindJiraVersions, b); !reflect.DeepEqual(got, want) {
		t.Fatalf("versions =\n%+v\nwant\n%+v", got, want)
	}
	if reqs := jira.TakeRequests(); !strings.Contains(reqs[0], "expand=approvers%2Cissuesstatus") {
		t.Fatalf("requests = %v", reqs)
	}
}

func TestRelatedWorkUsersAndMyselfMapIntoFacts(t *testing.T) {
	jira := rttest.NewJira(t)
	jira.Set("/rest/api/3/version/5001/relatedwork", `[{"category":"Development","title":"Scan","url":"https://example.com/scan"}]`)
	jira.Set("/rest/api/3/user?accountId=acc-ann", `{"accountId":"acc-ann","displayName":"Ann Example","avatarUrls":{"48x48":"https://example.atlassian.net/a.png"}}`)
	jira.Set("/rest/api/3/myself", `{"accountId":"acc-dee","displayName":"Dee Example"}`)
	s := newSource(jira)

	b, err := fetch(t, s, rt.SpecFor(rt.KindRelatedWork, rt.VersionScope("5001"), "di"))
	if got := decode(t, rt.KindRelatedWork, b); err != nil || !reflect.DeepEqual(got, []rt.RelatedWorkFact{{VersionID: "5001",
		Links: []rt.RelatedWork{{Category: "Development", Title: "Scan", URL: "https://example.com/scan"}}}}) {
		t.Fatalf("related work = %+v, %v", got, err)
	}
	b, err = fetch(t, s, rt.SpecFor(rt.KindJiraUsers, rt.UserScope("acc-ann"), ""))
	if got := decode(t, rt.KindJiraUsers, b); err != nil || !reflect.DeepEqual(got, []rt.JiraUserFact{{AccountID: "acc-ann", DisplayName: "Ann Example", AvatarURL: "https://example.atlassian.net/a.png"}}) {
		t.Fatalf("user = %+v, %v", got, err)
	}
	b, err = fetch(t, s, rt.SpecFor(rt.KindJiraMyself, rt.MyselfScope, ""))
	if got := decode(t, rt.KindJiraMyself, b); err != nil || !reflect.DeepEqual(got, []rt.JiraMyselfFact{{AccountID: "acc-dee", DisplayName: "Dee Example"}}) {
		t.Fatalf("myself = %+v, %v", got, err)
	}
}

func TestUserReadDownloadsTheAvatarIntoTheStore(t *testing.T) {
	jira := rttest.NewJira(t)
	jira.Set("/rest/api/3/user?accountId=acc-ann", `{"accountId":"acc-ann","displayName":"Ann Example","avatarUrls":{"48x48":"`+jira.URL+`/avatars/ann.png"}}`)
	jira.Set("/avatars/ann.png", "ann-bytes")
	jira.Set("/rest/api/3/user?accountId=acc-bob", `{"accountId":"acc-bob","displayName":"Bob Example","avatarUrls":{"48x48":"https://avatars.elsewhere.example/bob.png"}}`)
	store := svc.NewAvatarStore(t.TempDir())
	s := New(Options{Avatars: store, Config: func() (atlassian.Config, error) {
		return atlassian.Config{BaseURL: jira.URL, Email: "dee@example.com", Token: "t"}, nil
	}})

	for _, id := range []string{"acc-ann", "acc-bob"} {
		b, err := fetch(t, s, rt.SpecFor(rt.KindJiraUsers, rt.UserScope(id), ""))
		if got := decode(t, rt.KindJiraUsers, b); err != nil || len(got) != 1 || got[0].AccountID != id {
			t.Fatalf("user %s = %+v, %v", id, got, err)
		}
	}
	if got, err := store.Get("acc-ann"); err != nil || string(got) != "ann-bytes" {
		t.Fatalf("ann's avatar = %q, %v", got, err)
	}
	if _, err := store.Get("acc-bob"); !errors.Is(err, svc.ErrNoAvatar) {
		t.Fatalf("an avatar on a host that is not allowed was stored: %v", err)
	}
	for _, req := range jira.TakeRequests() {
		if !strings.HasPrefix(req, "GET ") {
			t.Fatalf("the user read sent %s", req)
		}
	}
}

func TestUserReadKeepsTheProfileWhenTheAvatarFails(t *testing.T) {
	jira := rttest.NewJira(t)
	jira.Set("/rest/api/3/user?accountId=acc-ann", `{"accountId":"acc-ann","displayName":"Ann Example","avatarUrls":{"48x48":"`+jira.URL+`/avatars/ann.png"}}`)
	jira.Fail("/avatars/ann.png", http.StatusServiceUnavailable, nil)
	s := New(Options{Avatars: svc.NewAvatarStore(t.TempDir()), Config: func() (atlassian.Config, error) {
		return atlassian.Config{BaseURL: jira.URL, Email: "dee@example.com", Token: "t"}, nil
	}})
	b, err := fetch(t, s, rt.SpecFor(rt.KindJiraUsers, rt.UserScope("acc-ann"), ""))
	if got := decode(t, rt.KindJiraUsers, b); err != nil || len(got) != 1 || got[0].DisplayName != "Ann Example" {
		t.Fatalf("user = %+v, %v", got, err)
	}
}

func issue(key, summary, status, category string, subtask bool, fixVersions []string, extra string) string {
	fv := make([]string, len(fixVersions))
	for i, id := range fixVersions {
		fv[i] = fmt.Sprintf(`{"id":%q}`, id)
	}
	return fmt.Sprintf(`{"key":%q,"fields":{"summary":%q,"status":{"name":%q,"statusCategory":{"key":%q}},"issuetype":{"name":"Task","subtask":%v},"fixVersions":[%s],"created":"2026-10-01T10:00:00.000+0700"%s}}`,
		key, summary, status, category, subtask, strings.Join(fv, ","), extra)
}

func TestCardsMarkAnyFamilysReleaseTicket(t *testing.T) {
	jira := rttest.NewJira(t)
	jira.Set(`/rest/api/3/search/jql?jql=fixVersion = 5001 ORDER BY key`, `{"issues":[`+
		issue("DEMO-1", "Release notes for Wallet top up", "In Progress", "indeterminate", false, []string{"5001"}, `,"assignee":{"accountId":"acc-ann","displayName":"Ann Example"}`)+","+
		issue("DEMO-2", "Release Demo 3.5.0", "Done", "done", false, []string{"5001"}, "")+","+
		issue("DEMO-3", "Release Pilot 1.3.0", "Done", "done", false, []string{"5001"}, "")+","+
		issue("DEMO-4", "iOS task", "Done", "done", true, []string{"5001"}, "")+`]}`)
	b, err := fetch(t, newSource(jira), rt.SpecFor(rt.KindCards, rt.VersionScope("5001"), "di"))
	if err != nil {
		t.Fatal(err)
	}
	url := func(k string) string { return jira.URL + "/browse/" + k }
	want := []rt.IssueFact{
		{Key: "DEMO-1", Summary: "Release notes for Wallet top up", Status: "In Progress", StatusCategory: "indeterminate", FixVersions: []string{"5001"}, Assignee: &rt.Person{AccountID: "acc-ann", Name: "Ann Example"}, IssueType: "Task", URL: url("DEMO-1")},
		{Key: "DEMO-2", Summary: "Release Demo 3.5.0", Status: "Done", StatusCategory: "done", ReleaseTicket: true, FixVersions: []string{"5001"}, IssueType: "Task", URL: url("DEMO-2")},
		{Key: "DEMO-3", Summary: "Release Pilot 1.3.0", Status: "Done", StatusCategory: "done", ReleaseTicket: true, FixVersions: []string{"5001"}, IssueType: "Task", URL: url("DEMO-3")},
		{Key: "DEMO-4", Summary: "iOS task", Status: "Done", StatusCategory: "done", Subtask: true, FixVersions: []string{"5001"}, IssueType: "Task", URL: url("DEMO-4")},
	}
	if got := decode(t, rt.KindCards, b); !reflect.DeepEqual(got, want) {
		t.Fatalf("cards =\n%+v\nwant\n%+v", got, want)
	}
}

func TestTechReqParsesPlatformAndVersionFromTheSummary(t *testing.T) {
	jira := rttest.NewJira(t)
	jira.Set(`/rest/api/3/search/jql?jql=project = DEMOREQ AND summary ~ "force update" AND created >= "2026-04-01" ORDER BY key`, `{"issues":[`+
		issue("REQ-1", "[PROD] | iOS | force update to 3.5.0", "To Do", "new", false, nil, `,"duedate":"2026-10-21"`)+","+
		issue("REQ-2", "[PROD] | ADR | Force update 3.4.1", "Done", "done", false, nil, "")+","+
		issue("REQ-3", "Force update docs cleanup", "Done", "done", false, nil, "")+`]}`)
	b, err := fetch(t, newSource(jira), rt.SpecFor(rt.KindTechReq, "project:DEMOREQ", ""))
	if err != nil {
		t.Fatal(err)
	}
	due, created := rt.Day{Y: 2026, M: 10, D: 21}, rt.Day{Y: 2026, M: 10, D: 1}
	want := []rt.ForceUpdateFact{
		{Key: "REQ-1", Status: "To Do", StatusCategory: "new", Platform: rt.PlatformIOS, Version: "3.5.0", Due: &due, CreatedOn: created, URL: jira.URL + "/browse/REQ-1"},
		{Key: "REQ-2", Status: "Done", StatusCategory: "done", Platform: rt.PlatformAndroid, Version: "3.4.1", CreatedOn: created, URL: jira.URL + "/browse/REQ-2"},
	}
	if got := decode(t, rt.KindTechReq, b); !reflect.DeepEqual(got, want) {
		t.Fatalf("techreq =\n%+v\nwant\n%+v", got, want)
	}
}

func TestErrorsClassifyIntoFetchErrors(t *testing.T) {
	versions := "/rest/api/3/project/DEMO/version"
	morePages := `{"isLast":false,"values":[{"id":"5001","name":"iOS 3.5.0","releaseDate":"2026-10-19"}]}`
	for _, tc := range []struct {
		name    string
		arrange func(*rttest.Host)
		class   rt.ErrorClass
		retry   time.Duration
	}{
		{"429 with Retry-After", func(j *rttest.Host) {
			j.Fail(versions, http.StatusTooManyRequests, map[string]string{"Retry-After": "90"})
		}, rt.ErrRateLimited, 90 * time.Second},
		{"429 on a later page inside ErrIncomplete", func(j *rttest.Host) {
			j.SetEveryPage(versions, morePages)
			j.FailAfter(versions, 1, http.StatusTooManyRequests, map[string]string{"Retry-After": "5"})
		}, rt.ErrRateLimited, 5 * time.Second},
		{"page cap", func(j *rttest.Host) { j.SetEveryPage(versions, morePages) }, rt.ErrTruncated, 0},
		{"401", func(j *rttest.Host) { j.Fail(versions, http.StatusUnauthorized, nil) }, rt.ErrAuth, 0},
		{"404", func(*rttest.Host) {}, rt.ErrNotFound, 0},
		{"503", func(j *rttest.Host) { j.Fail(versions, http.StatusServiceUnavailable, nil) }, rt.ErrUnavailable, 0},
		{"bad JSON", func(j *rttest.Host) { j.Set(versions, `{"values":"nope"}`) }, rt.ErrDecode, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jira := rttest.NewJira(t)
			tc.arrange(jira)
			_, err := fetch(t, newSource(jira), rt.SpecFor(rt.KindJiraVersions, "project:DEMO", ""))
			var fe *svc.FetchError
			if !errors.As(err, &fe) || fe.Class != tc.class || fe.RetryAfter != tc.retry {
				t.Fatalf("err = %v (%#v), want class %s retry %v", err, fe, tc.class, tc.retry)
			}
		})
	}
}

func TestCredentialsFollowTheConfigChainSilently(t *testing.T) {
	missing := New(Options{Config: func() (atlassian.Config, error) {
		return atlassian.Config{}, fmt.Errorf("%w: no Jira API token", atlassian.ErrAuthFailed)
	}})
	if ok, err := missing.Credentials(context.Background(), testView()); ok || err != nil {
		t.Fatalf("missing token: %v, %v", ok, err)
	}
	if ok, err := newSource(rttest.NewJira(t)).Credentials(context.Background(), testView()); !ok || err != nil {
		t.Fatalf("configured: %v, %v", ok, err)
	}
}

func TestLLBCardsCarryTheirParentAndItsTypeAndPageCardsTheirOwnType(t *testing.T) {
	jira := rttest.NewJira(t)
	jira.Set(`/rest/api/3/search/jql?jql=key in (DEMO-30, DEMO-31) ORDER BY key`, `{"issues":[`+
		issue("DEMO-30", "Wallet limits", "In Progress", "indeterminate", false, nil, `,"parent":{"key":"DEMO-5","fields":{"issuetype":{"name":"Epic"}}}`)+","+
		issue("DEMO-31", "iOS wallet limits", "To Do", "new", true, nil, `,"parent":{"key":"DEMO-30","fields":{"issuetype":{"name":"Story"}}}`)+`]}`)
	jira.Set(`/rest/api/3/search/jql?jql=key in (DEMO-5) ORDER BY key`, `{"issues":[{"key":"DEMO-5","fields":{"summary":"Wallet","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},"issuetype":{"name":"Epic"}}}]}`)
	src := newSource(jira)
	b, err := fetch(t, src, rt.SpecFor(rt.KindLLBCards, rt.LLBCardsScope, ""))
	if err != nil {
		t.Fatal(err)
	}
	type parent struct{ Type, Parent, ParentType string }
	var got []parent
	for _, is := range decode(t, rt.KindLLBCards, b) {
		got = append(got, parent{is.IssueType, is.Parent, is.ParentType})
	}
	if want := []parent{{"Task", "DEMO-5", "Epic"}, {"Task", "DEMO-30", "Story"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("llb cards (type, parent, parent type) = %+v, want %+v", got, want)
	}
	v := testView()
	v.Pages = []rt.PlannedPage{{ID: "901", Keys: []string{"DEMO-5"}, Live: true}}
	pb, err := src.Fetch(t.Context(), rt.SpecFor(rt.KindPageCards, rt.PageScope("901"), ""), v, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := decode(t, rt.KindPageCards, pb); len(got) != 1 || got[0].IssueType != "Epic" {
		t.Fatalf("page cards = %+v, want DEMO-5 typed Epic", got)
	}
}

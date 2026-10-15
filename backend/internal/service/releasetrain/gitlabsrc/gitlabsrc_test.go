package gitlabsrc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	scmgitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/gitlab"
	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	svc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/rttest"
)

var historyFrom = rt.Day{Y: 2026, M: time.April, D: 1}

func testConfig() rt.Config {
	return rt.Config{
		Timezone: "Asia/Bangkok", HistoryFrom: historyFrom, AgeOutDays: 7, FixWrites: "off", GitLabHost: "gitlab.example.com",
		Jira:     rt.JiraConfig{Project: "DEMO"},
		Families: []rt.Family{{ID: "demo", Label: "Demo", TrainLabel: "Release Train", Apps: []rt.AppID{"di", "da"}}},
		Apps: []rt.App{
			{ID: "di", Name: "Demo iOS", Platform: rt.PlatformIOS, GitLabProject: 1001, JiraVersionPrefix: "iOS"},
			{ID: "da", Name: "Demo Android", Platform: rt.PlatformAndroid, GitLabProject: 1002, JiraVersionPrefix: "Android"},
		},
	}
}

var bangkok = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		panic(err)
	}
	return loc
}()

func testView() rt.PlanView {
	cfg := testConfig()
	return rt.PlanView{
		Config: cfg, Loc: bangkok, HistoryFrom: historyFrom, Apps: cfg.Apps,
		Versions: []rt.PlannedVersion{
			{App: "di", Project: 1001, Live: true, ReleaseMR: 41, Pipeline: 9001, Tag: "3.5.0"},
			{App: "da", Project: 1002, Live: false, ReleaseMR: 42, Pipeline: 9002, Tag: "3.4.0"},
			{App: "da", Project: 1002, Live: true},
		},
		LLBs: []rt.PlannedLLB{{App: "da", Project: 1002, Branch: "feature/demo-wallet"}},
	}
}

func newSource(t *testing.T, gl *rttest.Host) *Source {
	t.Helper()
	return New(Options{
		APIBase: func(string) string { return gl.URL },
		Tokens:  func(string) scmgitlab.TokenSource { return scmgitlab.StaticTokenSource("t") },
	})
}

func keys(specs []rt.ResourceSpec) []string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = fmt.Sprintf("%s %s %s %v", s.Kind, s.Scope, s.App, s.Budget)
	}
	return out
}

func TestPlanListsEveryGitLabResourceTheViewNeeds(t *testing.T) {
	got := keys(newSource(t, rttest.NewGitLab(t)).Plan(testView()))
	want := []string{
		"gitlab.milestones p:1001 di 10m0s",
		"gitlab.mrs p:1001 di 2m0s",
		"gitlab.tags p:1001 di 10m0s",
		"gitlab.milestones p:1002 da 10m0s",
		"gitlab.mrs p:1002 da 2m0s",
		"gitlab.tags p:1002 da 10m0s",
		"gitlab.mr_detail p:1001!41 di 2m0s",
		"gitlab.jobs p:1001:pipeline:9001 di 2m0s",
		"gitlab.compare p:1001:3.5.0..main di 30m0s",
		"gitlab.compare p:1002:3.4.0..main da 30m0s",
		"gitlab.compare p:1002:develop..feature/demo-wallet da 30m0s",
		"gitlab.user me  24h0m0s",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func fetch(t *testing.T, s *Source, spec rt.ResourceSpec, cursor []byte) (svc.Batch, error) {
	t.Helper()
	return s.Fetch(context.Background(), spec, testView(), cursor)
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

func TestMilestonesKeepOnlyThoseDueOnOrAfterHistoryFrom(t *testing.T) {
	gl := rttest.NewGitLab(t)
	gl.Set("/projects/1001/milestones", `[
		{"id":11,"title":"Release 3.5.0","description":"Release Train #21/2026","state":"active","start_date":"2026-10-12","due_date":"2026-10-19","web_url":"https://gitlab.example.com/m/11"},
		{"id":12,"title":"Release 3.0.0","description":"Release Train #2/2026","state":"closed","start_date":"2026-01-05","due_date":"2026-01-12","web_url":"https://gitlab.example.com/m/12"},
		{"id":13,"title":"Backlog","description":"","state":"active","start_date":null,"due_date":null,"web_url":"https://gitlab.example.com/m/13"}
	]`)
	b, err := fetch(t, newSource(t, gl), rt.SpecFor(rt.KindMilestones, rt.ProjectScope(1001), "di"), nil)
	if err != nil || !b.Complete {
		t.Fatalf("fetch: %v complete=%v", err, b.Complete)
	}
	got := decode(t, rt.KindMilestones, b)
	start, due := rt.Day{Y: 2026, M: 10, D: 12}, rt.Day{Y: 2026, M: 10, D: 19}
	want := []rt.MilestoneFact{
		{Project: 1001, ID: 11, Title: "Release 3.5.0", Description: "Release Train #21/2026", State: "active", Start: &start, Due: &due, URL: "https://gitlab.example.com/m/11"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("milestones =\n%+v\nwant\n%+v", got, want)
	}
	if reqs := gl.TakeRequests(); len(reqs) != 1 || !strings.Contains(reqs[0], "state=all") || !strings.Contains(reqs[0], "updated_after=2026-04-01T00%3A00%3A00Z") {
		t.Fatalf("requests = %v", reqs)
	}
}

func TestTagURLIsItsCommitURL(t *testing.T) {
	gl := rttest.NewGitLab(t)
	gl.Set("/projects/1001/repository/tags", `[{"name":"3.5.0","target":"abc","commit":{"id":"abc","title":"Merge branch 'release/3.5.0' into 'main'","created_at":"2026-10-18T20:30:00+07:00","web_url":"https://gitlab.example.com/c/abc"}}]`)
	b, err := fetch(t, newSource(t, gl), rt.SpecFor(rt.KindTags, rt.ProjectScope(1001), "di"), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []rt.TagFact{{Project: 1001, Name: "3.5.0", CommitOn: rt.Day{Y: 2026, M: 10, D: 18}, URL: "https://gitlab.example.com/c/abc"}}
	if got := decode(t, rt.KindTags, b); !reflect.DeepEqual(got, want) {
		t.Fatalf("tags = %+v", got)
	}
	if reqs := gl.TakeRequests(); !strings.Contains(reqs[0], "order_by=updated") {
		t.Fatalf("requests = %v", reqs)
	}
}

func TestDetailJobsCompareAndUserMapIntoFacts(t *testing.T) {
	gl := rttest.NewGitLab(t)
	gl.Set("/projects/1001/merge_requests/41", `{"has_conflicts":true,"detailed_merge_status":"conflict","head_pipeline":{"id":9001,"status":"failed","web_url":"https://gitlab.example.com/p/9001","duration":4260}}`)
	gl.Set("/projects/1001/pipelines/9001/jobs", `[{"name":"build_test","status":"failed","allow_failure":false,"stage":"test"},{"name":"lint","status":"failed","allow_failure":true,"stage":"test"}]`)
	gl.Set("/projects/1001/repository/compare", `{"web_url":"https://gitlab.example.com/compare/3.5.0...main","commits":[{"id":"d1","title":"Merge branch 'fix/crash' into 'main'","created_at":"2026-10-20T09:00:00+07:00","web_url":"https://gitlab.example.com/c/d1"}]}`)
	gl.Set("/user", `{"username":"dee","name":"Dee Example"}`)
	s := newSource(t, gl)

	b, err := fetch(t, s, rt.SpecFor(rt.KindMRDetail, rt.MRScope(1001, 41), "di"), nil)
	dur := 4260
	wantDetail := []rt.MRDetailFact{{Project: 1001, IID: 41, Conflicts: true, Pipeline: &rt.PipelineFact{ID: 9001, Status: "failed", URL: "https://gitlab.example.com/p/9001", Duration: &dur}}}
	if got := decode(t, rt.KindMRDetail, b); err != nil || !reflect.DeepEqual(got, wantDetail) {
		t.Fatalf("detail = %+v, %v", got, err)
	}

	b, err = fetch(t, s, rt.SpecFor(rt.KindJobs, rt.PipelineScope(1001, 9001), "di"), nil)
	wantJobs := []rt.JobsFact{{Project: 1001, Pipeline: 9001, Jobs: []rt.JobFact{{Name: "build_test", Status: "failed"}, {Name: "lint", Status: "failed", AllowFailure: true}}}}
	if got := decode(t, rt.KindJobs, b); err != nil || !reflect.DeepEqual(got, wantJobs) {
		t.Fatalf("jobs = %+v, %v", got, err)
	}

	b, err = fetch(t, s, rt.SpecFor(rt.KindCompare, rt.CompareScope(1001, "3.5.0", "main"), "di"), nil)
	wantCompare := []rt.CompareFact{{Project: 1001, From: "3.5.0", To: "main", URL: "https://gitlab.example.com/compare/3.5.0...main",
		Commits: []rt.CommitFact{{Title: "Merge branch 'fix/crash' into 'main'", CreatedOn: rt.Day{Y: 2026, M: 10, D: 20}, URL: "https://gitlab.example.com/c/d1"}}}}
	if got := decode(t, rt.KindCompare, b); err != nil || !reflect.DeepEqual(got, wantCompare) {
		t.Fatalf("compare = %+v, %v", got, err)
	}

	b, err = fetch(t, s, rt.SpecFor(rt.KindGitLabUser, rt.MyselfScope, ""), nil)
	if got := decode(t, rt.KindGitLabUser, b); err != nil || !reflect.DeepEqual(got, []rt.GitLabUserFact{{Username: "dee", Name: "Dee Example"}}) {
		t.Fatalf("user = %+v, %v", got, err)
	}
	reqs := gl.TakeRequests()
	if !strings.Contains(reqs[1], "include_retried=false") || !strings.Contains(reqs[2], "from=3.5.0") || !strings.Contains(reqs[2], "to=main") || !strings.Contains(reqs[2], "straight=true") {
		t.Fatalf("requests = %v", reqs)
	}
}

func at(s string) time.Time {
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return v
}

func mrs(n int, start time.Time, step time.Duration) []rttest.MR {
	out := make([]rttest.MR, n)
	for i := range out {
		out[i] = rttest.MR{IID: i + 1, Title: fmt.Sprintf("DEMO-%d change", i+1), Source: "feature/x", Target: "develop", State: "merged",
			Author: "Rae Example", Username: "rae", CreatedAt: start, UpdatedAt: start.Add(time.Duration(i) * step), WebURL: fmt.Sprintf("https://gitlab.example.com/mr/%d", i+1)}
	}
	return out
}

func walkMRs(t *testing.T, s *Source, cursor []byte, maxPages int) (seen map[int]bool, pages int, last []byte, complete bool) {
	t.Helper()
	seen = map[int]bool{}
	spec := rt.SpecFor(rt.KindMRs, rt.ProjectScope(1001), "di")
	for pages < maxPages {
		b, err := fetch(t, s, spec, cursor)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, mr := range decode(t, rt.KindMRs, b) {
			seen[mr.IID] = true
		}
		cursor = b.Cursor
		if b.Complete {
			return seen, pages, cursor, true
		}
	}
	return seen, pages, cursor, false
}

func TestMRCursorWalksEveryMRAndCompletesOnAShortPage(t *testing.T) {
	gl := rttest.NewGitLab(t)
	gl.SetMRs(1001, mrs(250, at("2026-05-01T00:00:00Z"), time.Minute))
	seen, pages, cursor, complete := walkMRs(t, newSource(t, gl), nil, 10)
	if !complete || len(seen) != 250 || pages != 3 {
		t.Fatalf("complete=%v seen=%d pages=%d", complete, len(seen), pages)
	}
	reqs := gl.TakeRequests()
	if !strings.Contains(reqs[0], "updated_after=2026-04-01T00%3A00%3A00Z") || !strings.Contains(reqs[0], "order_by=updated_at") || !strings.Contains(reqs[0], "sort=asc") {
		t.Fatalf("first request = %s", reqs[0])
	}

	gl.SetMRs(1001, append(mrs(250, at("2026-05-01T00:00:00Z"), time.Minute), rttest.MR{IID: 251, Title: "DEMO-251", State: "opened", Target: "develop", UpdatedAt: at("2026-10-14T09:00:00Z"), CreatedAt: at("2026-10-14T09:00:00Z")}))
	seen, pages, _, complete = walkMRs(t, newSource(t, gl), cursor, 10)
	if !complete || pages != 1 || !seen[251] || len(seen) > 3 {
		t.Fatalf("steady pass complete=%v pages=%d seen=%v", complete, pages, seen)
	}
}

func TestMRCursorPagesThroughAFullPageThatShareOneSecond(t *testing.T) {
	gl := rttest.NewGitLab(t)
	base := at("2026-05-01T00:00:00Z")
	same := mrs(230, base, time.Millisecond)
	gl.SetMRs(1001, same)
	seen, pages, _, complete := walkMRs(t, newSource(t, gl), nil, 10)
	if !complete || len(seen) != 230 || pages != 4 {
		t.Fatalf("complete=%v seen=%d pages=%d; requests %v", complete, len(seen), pages, gl.TakeRequests())
	}
}

func TestMRFactsCarryAuthorUsernameAndMilestone(t *testing.T) {
	gl := rttest.NewGitLab(t)
	merged := at("2026-10-12T03:00:00Z")
	gl.SetMRs(1001, []rttest.MR{{IID: 41, Title: "Release 3.5.0", Source: "release/3.5.0", Target: "main", State: "merged", Author: "Rae Example", Username: "rae",
		MilestoneID: 11, CreatedAt: at("2026-10-11T20:00:00Z"), UpdatedAt: merged, MergedAt: &merged, WebURL: "https://gitlab.example.com/mr/41"}})
	b, err := fetch(t, newSource(t, gl), rt.SpecFor(rt.KindMRs, rt.ProjectScope(1001), "di"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ms, created, mergedOn := 11, rt.Day{Y: 2026, M: 10, D: 12}, rt.Day{Y: 2026, M: 10, D: 12}
	want := []rt.MRFact{{Project: 1001, IID: 41, Title: "Release 3.5.0", SourceBranch: "release/3.5.0", TargetBranch: "main", State: "merged",
		Author: "Rae Example", AuthorUsername: "rae", CreatedOn: created, MergedOn: &mergedOn, MilestoneID: &ms, URL: "https://gitlab.example.com/mr/41"}}
	if got := decode(t, rt.KindMRs, b); !reflect.DeepEqual(got, want) {
		t.Fatalf("mrs =\n%+v\nwant\n%+v", got, want)
	}
	if b.Items[0].SourceUpdatedAt == nil || !b.Items[0].SourceUpdatedAt.Equal(merged) {
		t.Fatalf("source updated at = %v", b.Items[0].SourceUpdatedAt)
	}
}

func TestErrorsClassifyIntoFetchErrors(t *testing.T) {
	spec := rt.SpecFor(rt.KindTags, rt.ProjectScope(1001), "di")
	for _, tc := range []struct {
		name    string
		arrange func(*rttest.Host)
		class   rt.ErrorClass
		retry   time.Duration
	}{
		{"429 with Retry-After", func(gl *rttest.Host) {
			gl.Fail("/projects/1001/repository/tags", http.StatusTooManyRequests, map[string]string{"Retry-After": "120"})
		}, rt.ErrRateLimited, 2 * time.Minute},
		{"401", func(gl *rttest.Host) { gl.Fail("/projects/1001/repository/tags", http.StatusUnauthorized, nil) }, rt.ErrAuth, 0},
		{"404", func(*rttest.Host) {}, rt.ErrNotFound, 0},
		{"502", func(gl *rttest.Host) { gl.Fail("/projects/1001/repository/tags", http.StatusBadGateway, nil) }, rt.ErrUnavailable, 0},
		{"page cap", func(gl *rttest.Host) {
			gl.SetEveryPage("/projects/1001/repository/tags", fullTagPage())
		}, rt.ErrTruncated, 0},
		{"429 on a later page", func(gl *rttest.Host) {
			gl.SetEveryPage("/projects/1001/repository/tags", fullTagPage())
			gl.FailAfter("/projects/1001/repository/tags", 1, http.StatusTooManyRequests, map[string]string{"Retry-After": "7"})
		}, rt.ErrRateLimited, 7 * time.Second},
		{"bad JSON", func(gl *rttest.Host) { gl.Set("/projects/1001/repository/tags", `{"not":"a list"}`) }, rt.ErrDecode, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gl := rttest.NewGitLab(t)
			tc.arrange(gl)
			_, err := fetch(t, newSource(t, gl), spec, nil)
			var fe *svc.FetchError
			if !errors.As(err, &fe) || fe.Class != tc.class || fe.RetryAfter != tc.retry {
				t.Fatalf("err = %#v, want class %s retry %v", err, tc.class, tc.retry)
			}
		})
	}
}

func fullTagPage() string {
	rows := make([]map[string]any, 100)
	for i := range rows {
		rows[i] = map[string]any{"name": fmt.Sprintf("1.0.%d", i), "commit": map[string]any{"created_at": "2026-10-01T00:00:00Z"}}
	}
	b, _ := json.Marshal(rows)
	return string(b)
}

func TestCredentialsProbeTheConfiguredHost(t *testing.T) {
	var hosts []string
	token := ""
	s := New(Options{Tokens: func(host string) scmgitlab.TokenSource {
		hosts = append(hosts, host)
		return scmgitlab.TokenSource(tokenFunc(func() string { return token }))
	}})
	if ok, err := s.Credentials(context.Background(), testView()); ok || err != nil {
		t.Fatalf("no token: Credentials = %v, %v", ok, err)
	}
	token = "t"
	if ok, err := s.Credentials(context.Background(), testView()); !ok || err != nil {
		t.Fatalf("token: Credentials = %v, %v", ok, err)
	}
	if !reflect.DeepEqual(hosts, []string{"gitlab.example.com"}) {
		t.Fatalf("token sources built for %v, want one for the configured host", hosts)
	}
}

type tokenFunc func() string

func (f tokenFunc) Token(ctx context.Context) (string, error) {
	return scmgitlab.StaticTokenSource(f()).Token(ctx)
}

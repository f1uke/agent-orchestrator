package releasetrain_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/atlassian"
	scmgitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/gitlab"
	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	svc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/gitlabsrc"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/jirasrc"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/rttest"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

const demoConfig = `{
  "timezone": "Asia/Bangkok", "historyFrom": "2026-04-01", "ageOutDays": 7, "fixWrites": "off",
  "gitlabHost": "gitlab.example.com",
  "jira": { "project": "DEMO", "techRequestProject": "DEMOREQ", "forceUpdateSummary": "force update" },
  "families": [ { "id": "demo", "label": "Demo", "trainLabel": "Release Train", "apps": ["di"] } ],
  "apps": [ { "id": "di", "name": "Demo iOS", "platform": "ios", "gitlabProject": 1001, "jiraVersionPrefix": "iOS" } ]
}`

type world struct {
	t          *testing.T
	gl         *rttest.Host
	jira       *rttest.Host
	store      *sqlite.Store
	poller     *svc.Poller
	configPath string
	now        time.Time
}

func at(s string) time.Time {
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return v
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, gl: rttest.NewGitLab(t), jira: rttest.NewJira(t), now: at("2026-10-14T03:00:00Z")}
	w.gl.Set("/projects/1001/milestones", `[{"id":11,"title":"Release 3.5.0","description":"Release Train #21/2026","state":"active","start_date":"2026-10-12","due_date":"2026-10-19","web_url":"https://gitlab.example.com/m/11"}]`)
	w.gl.Set("/projects/1001/repository/tags", `[{"name":"3.5.0","commit":{"id":"c1","created_at":"2026-10-13T10:00:00Z","web_url":"https://gitlab.example.com/c/c1"}}]`)
	w.gl.Set("/projects/1001/merge_requests/301", `{"has_conflicts":false,"head_pipeline":{"id":9001,"status":"success","web_url":"https://gitlab.example.com/p/9001","duration":600}}`)
	w.gl.Set("/projects/1001/pipelines/9001/jobs", `[{"name":"build_test","status":"success"}]`)
	w.gl.Set("/projects/1001/repository/compare", `{"web_url":"https://gitlab.example.com/compare","commits":[]}`)
	w.gl.Set("/user", `{"username":"dee","name":"Dee Example"}`)
	var mrs []rttest.MR
	base := at("2026-05-01T00:00:00Z")
	for i := 1; i <= 300; i++ {
		mrs = append(mrs, rttest.MR{IID: i, Title: fmt.Sprintf("DEMO-%d change", i), Source: "feature/x", Target: "develop", State: "merged",
			Author: "Rae Example", Username: "rae", CreatedAt: base, UpdatedAt: base.Add(time.Duration(i) * time.Hour)})
	}
	mrs = append(mrs, rttest.MR{IID: 301, Title: "Release 3.5.0", Source: "release/3.5.0", Target: "main", State: "opened",
		Author: "Rae Example", Username: "rae", MilestoneID: 11, CreatedAt: at("2026-10-12T02:00:00Z"), UpdatedAt: at("2026-10-12T02:00:00Z")})
	w.gl.SetMRs(1001, mrs)

	w.jira.Set("/rest/api/3/project/DEMO/version", `{"isLast":true,"values":[{"id":"5001","name":"iOS 3.5.0","description":"Release Train #21/2026","releaseDate":"2026-10-19","approvers":[{"accountId":"acc-ann","status":"PENDING"}]}]}`)
	w.jira.Set("/rest/api/3/version/5001/relatedwork", `[]`)
	w.jira.Set(`/rest/api/3/search/jql?jql=fixVersion = 5001 ORDER BY key`, `{"issues":[{"key":"DEMO-1","fields":{"summary":"Release Demo 3.5.0","status":{"name":"To Do","statusCategory":{"key":"new"}},"issuetype":{"subtask":false},"created":"2026-10-01T10:00:00.000+0700"}}]}`)
	w.jira.Set(`/rest/api/3/search/jql?jql=project = DEMOREQ AND summary ~ "force update" AND created >= "2026-04-01" ORDER BY key`, `{"issues":[]}`)
	w.jira.Set("/rest/api/3/user?accountId=acc-ann", `{"accountId":"acc-ann","displayName":"Ann Example"}`)
	w.jira.Set("/rest/api/3/myself", `{"accountId":"acc-dee","displayName":"Dee Example"}`)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, svc.ConfigFileName), []byte(demoConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	w.store = st
	w.configPath = filepath.Join(dir, svc.ConfigFileName)
	glURL := w.gl.URL
	jiraCfg := atlassian.Config{BaseURL: w.jira.URL, Email: "dee@example.com", Token: "t"}
	w.poller = svc.NewPoller(svc.PollerDeps{
		Config: svc.NewConfigLoader(w.configPath),
		Store:  st,
		Sources: []svc.Source{
			gitlabsrc.New(gitlabsrc.Options{
				APIBase: func(string) string { return glURL },
				Tokens:  func(string) scmgitlab.TokenSource { return scmgitlab.StaticTokenSource("t") },
			}),
			jirasrc.New(jirasrc.Options{Config: func() (atlassian.Config, error) { return jiraCfg, nil }}),
		},
		Clock:  func() time.Time { return w.now },
		Rand:   func() float64 { return 0.5 },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return w
}

func (w *world) pass() (gitlab, jira []string) {
	w.t.Helper()
	if err := w.poller.Poll(context.Background()); err != nil {
		w.t.Fatal(err)
	}
	gitlab, jira = paths(w.gl.TakeRequests()), paths(w.jira.TakeRequests())
	if counted := w.poller.LastPass().Requests; counted["gitlab"] != len(gitlab) || counted["jira"] != len(jira) {
		w.t.Fatalf("budget counted %v, servers saw gitlab %d jira %d", counted, len(gitlab), len(jira))
	}
	return gitlab, jira
}

func paths(reqs []string) []string {
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		method, rest, _ := strings.Cut(r, " ")
		u, _ := url.Parse(rest)
		if method != "GET" {
			out = append(out, "NOT A GET: "+r)
			continue
		}
		out = append(out, u.Path)
	}
	sort.Strings(out)
	return out
}

// settle polls every Tick until a pass plans no new resource and every one is complete, and returns the passes it took.
func (w *world) settle() int {
	w.t.Helper()
	for passes := 1; passes <= 10; passes++ {
		before := total(w.kinds())
		w.pass()
		w.now = w.now.Add(svc.Tick)
		if total(w.kinds()) == before && len(w.incomplete()) == 0 {
			return passes
		}
	}
	w.t.Fatalf("not settled after 10 passes: %v", w.incomplete())
	return 0
}

func total(byKind map[rt.KindName]int) int {
	n := 0
	for _, c := range byKind {
		n += c
	}
	return n
}

func (w *world) incomplete() []string {
	w.t.Helper()
	_, rows, err := w.store.LoadReleaseSnapshot(context.Background())
	if err != nil {
		w.t.Fatal(err)
	}
	var out []string
	for _, r := range rows {
		if r.CompleteAt == nil || r.ErrorClass != "" {
			out = append(out, fmt.Sprintf("%s %s %s", r.Kind, r.Scope, r.ErrorClass))
		}
	}
	return out
}

func (w *world) kinds() map[rt.KindName]int {
	_, rows, _ := w.store.LoadReleaseSnapshot(context.Background())
	out := map[rt.KindName]int{}
	for _, r := range rows {
		out[r.Kind]++
	}
	return out
}

func TestBothSourcesBackfillToCompleteThenFetchOnlyWhatIsDue(t *testing.T) {
	w := newWorld(t)
	passes := w.settle()
	if left := w.incomplete(); len(left) > 0 || passes < 2 {
		t.Fatalf("after %d passes still incomplete: %v", passes, left)
	}
	if !reachesEveryReadKind(w.kinds()) {
		t.Fatalf("resources by kind %v miss a kind a rule reads", w.kinds())
	}

	w.now = w.now.Add(61 * time.Second)
	gitlab, jira := w.pass()
	wantGitLab := []string{"/projects/1001/merge_requests", "/projects/1001/merge_requests/301", "/projects/1001/pipelines/9001/jobs"}
	if strings.Join(gitlab, " ") != strings.Join(wantGitLab, " ") || len(jira) != 0 {
		t.Fatalf("steady pass after 61s fetched gitlab %v jira %v; want %v and nothing from jira", gitlab, jira, wantGitLab)
	}
	w.now = w.now.Add(5 * time.Minute)
	_, jira = w.pass()
	wantJira := []string{"/rest/api/3/project/DEMO/version", "/rest/api/3/search/jql"}
	if strings.Join(jira, " ") != strings.Join(wantJira, " ") {
		t.Fatalf("steady pass after 5m fetched jira %v; want %v", jira, wantJira)
	}
}

func reachesEveryReadKind(got map[rt.KindName]int) bool {
	for _, k := range rt.ReadKinds() {
		if got[k] == 0 {
			return false
		}
	}
	return true
}

func TestEveryKindHasExactlyOneProducingSource(t *testing.T) {
	w := newWorld(t)
	w.settle()
	snap, _, err := w.store.LoadReleaseSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg := svc.NewConfigLoader(w.configPath).Load()
	if cfg.Config == nil {
		t.Fatalf("config: %s", cfg.Message)
	}
	view := rt.NewPlanView(*cfg.Config, snap, rt.Env{Now: w.now, Loc: cfg.Loc, AgeOut: cfg.Config.AgeOutDays})
	sources := []svc.Source{gitlabsrc.New(gitlabsrc.Options{Tokens: func(string) scmgitlab.TokenSource { return scmgitlab.StaticTokenSource("t") }}), jirasrc.New(jirasrc.Options{})}
	producers := map[rt.KindName][]string{}
	for _, src := range sources {
		seen := map[rt.KindName]bool{}
		for _, spec := range src.Plan(view) {
			if _, ok := rt.LookupKind(spec.Kind); !ok {
				t.Errorf("%s plans %s, which no Kind handle registers", src.System(), spec.Kind)
			}
			if !seen[spec.Kind] {
				seen[spec.Kind] = true
				producers[spec.Kind] = append(producers[spec.Kind], src.System())
			}
		}
	}
	for _, k := range rt.ReadKinds() {
		if len(producers[k]) != 1 {
			t.Errorf("kind %s is read by a rule or stage and produced by %v; want exactly one source", k, producers[k])
		}
	}
	for _, k := range []rt.KindName{rt.KindJiraUsers.Name, rt.KindJiraMyself.Name, rt.KindGitLabUser.Name} {
		if len(producers[k]) != 1 {
			t.Errorf("kind %s produced by %v; want exactly one source", k, producers[k])
		}
	}
}

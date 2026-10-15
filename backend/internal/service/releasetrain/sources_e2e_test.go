package releasetrain_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/atlassian"
	scmgitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/gitlab"
	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	svc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/confluencesrc"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/gitlabsrc"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/jirasrc"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/rttest"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

const demoConfig = `{
  "timezone": "Asia/Bangkok", "historyFrom": "2026-04-01", "ageOutDays": 7, "fixWrites": "off",
  "gitlabHost": "gitlab.example.com",
  "jira": { "project": "DEMO", "techRequestProject": "DEMOREQ", "forceUpdateSummary": "force update" },
  "confluence": { "space": "DEMOSPACE", "trainPageTitle": "Release train", "freezeCell": "Code freeze", "publishCell": "Store release" },
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

func baseMRs() []rttest.MR {
	var mrs []rttest.MR
	base := at("2026-05-01T00:00:00Z")
	for i := 1; i <= 300; i++ {
		mrs = append(mrs, rttest.MR{IID: i, Title: fmt.Sprintf("DEMO-%d change", i), Source: "feature/x", Target: "develop", State: "merged",
			Author: "Rae Example", Username: "rae", CreatedAt: base, UpdatedAt: base.Add(time.Duration(i) * time.Hour)})
	}
	mrs = append(mrs, rttest.MR{IID: 301, Title: "Release 3.5.0", Source: "release/3.5.0", Target: "main", State: "opened",
		Author: "Rae Example", Username: "rae", MilestoneID: 11, CreatedAt: at("2026-10-12T02:00:00Z"), UpdatedAt: at("2026-10-12T02:00:00Z")})
	return append(mrs, rttest.MR{IID: 302, Title: "DEMO-30 wallet", Source: "feature/DEMO-30", Target: "feature/demo-wallet", State: "merged",
		Author: "Dee Example", Username: "dee", CreatedAt: at("2026-10-01T02:00:00Z"), UpdatedAt: at("2026-10-12T03:00:00Z")})
}

func newWorld(t *testing.T) *world { return newWorldWith(t, demoConfig) }

func newWorldWith(t *testing.T, config string) *world {
	t.Helper()
	w := &world{t: t, gl: rttest.NewGitLab(t), jira: rttest.NewJira(t), now: at("2026-10-14T03:00:00Z")}
	w.gl.Set("/projects/1001/milestones", `[{"id":11,"title":"Release 3.5.0","description":"Release Train #21/2026","state":"active","start_date":"2026-10-12","due_date":"2026-10-19","web_url":"https://gitlab.example.com/m/11"}]`)
	w.gl.Set("/projects/1001/repository/tags", `[{"name":"3.5.0","commit":{"id":"c1","created_at":"2026-10-13T10:00:00Z","web_url":"https://gitlab.example.com/c/c1"}}]`)
	w.gl.Set("/projects/1001/merge_requests/301", `{"has_conflicts":false,"head_pipeline":{"id":9001,"status":"success","web_url":"https://gitlab.example.com/p/9001","duration":600}}`)
	w.gl.Set("/projects/1001/pipelines/9001/jobs", `[{"name":"build_test","status":"success"}]`)
	w.gl.Set("/projects/1001/repository/compare", `{"web_url":"https://gitlab.example.com/compare","commits":[]}`)
	w.gl.Set("/user", `{"username":"dee","name":"Dee Example"}`)
	w.gl.SetMRs(1001, baseMRs())
	w.jira.Set(`/rest/api/3/search/jql?jql=parent in (DEMO-1, DEMO-30, DEMO-40) AND assignee = "acc-dee" ORDER BY key`, `{"issues":[{"key":"DEMO-41","fields":{"summary":"iOS top up","status":{"name":"To Do","statusCategory":{"key":"new"}},"issuetype":{"subtask":true},"parent":{"key":"DEMO-40"},"assignee":{"accountId":"acc-dee","displayName":"Dee Example"},"created":"2026-10-01T10:00:00.000+0700"}}]}`)
	w.jira.Set(`/rest/api/3/search/jql?jql=key in (DEMO-30) ORDER BY key`, `{"issues":[{"key":"DEMO-30","fields":{"summary":"Wallet","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},"issuetype":{"subtask":false},"assignee":{"accountId":"acc-dee","displayName":"Dee Example"},"created":"2026-10-01T10:00:00.000+0700"}}]}`)

	w.jira.Set("/rest/api/3/project/DEMO/version", `{"isLast":true,"values":[{"id":"5001","name":"iOS 3.5.0","description":"Release Train #21/2026","releaseDate":"2026-10-19","approvers":[{"accountId":"acc-ann","status":"PENDING"}]}]}`)
	w.jira.Set("/rest/api/3/version/5001/relatedwork", `[]`)
	w.jira.Set(`/rest/api/3/search/jql?jql=fixVersion = 5001 ORDER BY key`, `{"issues":[{"key":"DEMO-1","fields":{"summary":"Release Demo 3.5.0","status":{"name":"To Do","statusCategory":{"key":"new"}},"issuetype":{"subtask":false},"created":"2026-10-01T10:00:00.000+0700"}}]}`)
	w.jira.Set(`/rest/api/3/search/jql?jql=project = DEMOREQ AND summary ~ "force update" AND created >= "2026-04-01" ORDER BY key`, `{"issues":[]}`)
	w.jira.Set("/rest/api/3/user?accountId=acc-ann", `{"accountId":"acc-ann","displayName":"Ann Example"}`)
	w.jira.Set("/rest/api/3/myself", `{"accountId":"acc-dee","displayName":"Dee Example"}`)
	w.jira.Set(`/wiki/rest/api/content/search?cql=space = "DEMOSPACE" AND title ~ "Release train" AND created >= "2026-01-31"`,
		`{"results":[{"id":"801","title":"2026.10.12 - Release train #21","history":{"createdDate":"2026-09-14T06:30:00.000Z"},"_links":{"webui":"/spaces/DEMOSPACE/pages/801"},`+
			`"body":{"storage":{"value":"<table><tr><th>Code freeze</th><td><time datetime=\"2026-10-12\" /></td></tr><tr><th>Store release</th><td><time datetime=\"2026-10-20\" /></td></tr></table><p>DEMO-1 DEMO-40</p>"}}}],"_links":{}}`)
	w.jira.Set(`/rest/api/3/search/jql?jql=key in (DEMO-1, DEMO-40) ORDER BY key`, `{"issues":[{"key":"DEMO-40","fields":{"summary":"Wallet top up","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},"issuetype":{"subtask":false},"created":"2026-10-01T10:00:00.000+0700"}}]}`)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, svc.ConfigFileName), []byte(config), 0o600); err != nil {
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
			confluencesrc.New(confluencesrc.Options{Config: func() (atlassian.Config, error) { return jiraCfg, nil }}),
		},
		Clock:  func() time.Time { return w.now },
		Rand:   func() float64 { return 0.5 },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return w
}

// pass runs one poll pass and returns the paths each host served; jira and
// confluence share the one Atlassian site and its budget.
func (w *world) pass() (gitlab, atlassian []string) {
	w.t.Helper()
	if err := w.poller.Poll(context.Background()); err != nil {
		w.t.Fatal(err)
	}
	gitlab, atlassian = paths(w.gl.TakeRequests()), paths(w.jira.TakeRequests())
	if counted := w.poller.LastPass().Requests; counted["gitlab"] != len(gitlab) || counted["atlassian"] != len(atlassian) {
		w.t.Fatalf("budget counted %v, servers saw gitlab %d atlassian %d", counted, len(gitlab), len(atlassian))
	}
	return gitlab, atlassian
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
			out = append(out, fmt.Sprintf("%s %s %s %s", r.Kind, r.Scope, r.ErrorClass, r.ErrorMessage))
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
	w := newSprintWorld(t)
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
	wantJira := append([]string{"/rest/api/3/project/DEMO/version"}, slices.Repeat([]string{"/rest/api/3/search/jql"}, 7)...)
	if strings.Join(jira, " ") != strings.Join(wantJira, " ") {
		t.Fatalf("steady pass after 6m fetched atlassian %v; want versions and seven searches: the version's cards, the page's cards, the long-lived cards, my subtasks, the sprint's cards, its This Sprint keys (10m, first due here) and the sprint-branch cards", jira)
	}
	w.now = w.now.Add(10 * time.Minute)
	_, atl := w.pass()
	if !slices.Contains(atl, "/wiki/rest/api/content/search") {
		t.Fatalf("steady pass after 15m fetched atlassian %v; want the Confluence pages refetched", atl)
	}
}

func TestAPageOnlyCardRidesItsTrainEndToEnd(t *testing.T) {
	w := newWorld(t)
	w.settle()
	cfg := svc.NewConfigLoader(w.configPath)
	board := svc.NewBoardService(svc.BoardDeps{Config: cfg, Store: w.store, Clock: func() time.Time { return w.now }})
	body, _, err := board.Board(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Board struct {
			Trains []struct {
				Pages []struct {
					ID     string `json:"id"`
					Freeze string `json:"freeze"`
				} `json:"pages"`
				Versions []struct {
					Cards []struct {
						Key    string   `json:"key"`
						Status string   `json:"status"`
						Why    []string `json:"why"`
						Mine   []struct {
							Why string `json:"why"`
							Ref string `json:"ref"`
						} `json:"mine"`
					} `json:"cards"`
				} `json:"versions"`
			} `json:"trains"`
			Identity struct {
				Kind string `json:"kind"`
			} `json:"identity"`
			LLBs []struct {
				ID       string `json:"id"`
				Branches []struct {
					Cards []struct {
						Key    string `json:"key"`
						Status string `json:"status"`
					} `json:"cards"`
				} `json:"branches"`
			} `json:"llbs"`
			Alerts struct {
				Hits []struct {
					ID string `json:"id"`
				} `json:"hits"`
			} `json:"alerts"`
		} `json:"board"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	tr := resp.Board.Trains[0]
	if len(tr.Pages) != 1 || tr.Pages[0].ID != "801" || tr.Pages[0].Freeze != "2026-10-12" {
		t.Fatalf("pages = %+v, want page 801 attached", tr.Pages)
	}
	cards := tr.Versions[0].Cards
	if len(cards) != 1 || cards[0].Key != "DEMO-40" || cards[0].Status != "In Progress" || strings.Join(cards[0].Why, ",") != "release train page" {
		t.Fatalf("cards = %+v; want the page-only DEMO-40 with its status and the page as why (DEMO-1 is the release ticket)", cards)
	}
	var a6 []string
	for _, h := range resp.Board.Alerts.Hits {
		if strings.HasPrefix(h.ID, "A6~") {
			a6 = append(a6, h.ID)
		}
	}
	if strings.Join(a6, " ") != "A6~vd/21-2026/di/publish" {
		t.Fatalf("A6 hits = %v; the page says publish 20 Oct, the milestone is due 19 Oct", a6)
	}
	if resp.Board.Identity.Kind != "known" || len(cards[0].Mine) != 1 || cards[0].Mine[0].Why != "subtask" || cards[0].Mine[0].Ref != "DEMO-41" {
		t.Fatalf("identity %s, DEMO-40 mine %+v; want known and my subtask DEMO-41", resp.Board.Identity.Kind, cards[0].Mine)
	}
	llbs := resp.Board.LLBs
	if len(llbs) != 1 || llbs[0].ID != "demo-wallet" || llbs[0].Branches[0].Cards[0].Status != "In Progress" {
		t.Fatalf("llbs = %+v; want demo-wallet with DEMO-30's Jira status", llbs)
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
	w := newSprintWorld(t)
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
	sources := []svc.Source{
		gitlabsrc.New(gitlabsrc.Options{Tokens: func(string) scmgitlab.TokenSource { return scmgitlab.StaticTokenSource("t") }}),
		jirasrc.New(jirasrc.Options{}), confluencesrc.New(confluencesrc.Options{}),
	}
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
	for _, k := range []rt.KindName{rt.KindJiraUsers.Name, rt.KindJiraMyself.Name, rt.KindGitLabUser.Name, rt.KindPageCards.Name, rt.KindLLBCards.Name, rt.KindMySubtasks.Name,
		rt.KindBoardSprints.Name, rt.KindSprintCards.Name, rt.KindThisSprint.Name, rt.KindBranchCards.Name} {
		if len(producers[k]) != 1 {
			t.Errorf("kind %s produced by %v; want exactly one source", k, producers[k])
		}
	}
}

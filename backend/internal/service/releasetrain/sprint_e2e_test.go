package releasetrain_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	svc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/rttest"
)

var sprintConfig = strings.Replace(demoConfig, "\n  \"apps\": [",
	"\n  \"boards\": [ { \"id\": 42, \"key\": \"DEMO\", \"ownsSprintBranch\": true } ],"+
		"\n  \"mobile\": { \"projects\": [\"DEMO\"], \"subtaskPattern\": \"^(iOS|Android)\\\\b\" },"+
		"\n  \"apps\": [", 1)

func newSprintWorld(t *testing.T) *world {
	w := newWorldWith(t, sprintConfig)
	w.jira.Set("/rest/agile/1.0/board/42/sprint", `{"isLast":true,"values":[{"id":7021,"name":"DEMO 2026-21","state":"active","startDate":"2026-10-06T02:00:00.000Z","endDate":"2026-10-20T10:00:00.000Z"}]}`)
	w.jira.Set(`/rest/api/3/search/jql?jql=sprint = 7021 ORDER BY key`, `{"issues":[`+
		`{"key":"DEMO-360","fields":{"summary":"Wallet history","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},"issuetype":{"subtask":false},"assignee":{"accountId":"acc-dee","displayName":"Dee Example"},"created":"2026-10-01T10:00:00.000+0700"}},`+
		`{"key":"DEMO-363","fields":{"summary":"iOS history list","status":{"name":"To Do","statusCategory":{"key":"new"}},"issuetype":{"subtask":true},"parent":{"key":"DEMO-360"},"created":"2026-10-01T10:00:00.000+0700"}}]}`)
	w.jira.Set(`/rest/api/3/search/jql?jql=sprint = 7021 AND sprint not in closedSprints() ORDER BY key`, `{"issues":[{"key":"DEMO-360","fields":{}}]}`)
	w.jira.Set(`/rest/api/3/search/jql?jql=key in (DEMO-361, DEMO-362) ORDER BY key`, `{"issues":[`+
		`{"key":"DEMO-361","fields":{"summary":"Coupon","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},"issuetype":{"subtask":false},"created":"2026-10-01T10:00:00.000+0700"}},`+
		`{"key":"DEMO-362","fields":{"summary":"Bond","status":{"name":"Done","statusCategory":{"key":"done"}},"issuetype":{"subtask":false},"created":"2026-10-01T10:00:00.000+0700"}}]}`)
	w.jira.Set(`/rest/api/3/search/jql?jql=parent in (DEMO-1, DEMO-30, DEMO-361, DEMO-362, DEMO-40) AND assignee = "acc-dee" ORDER BY key`, `{"issues":[]}`)
	w.gl.SetMRs(1001, sprintMRs())
	return w
}

func sprintMRs() []rttest.MR {
	mrs := baseMRs()
	merged := at("2026-10-08T03:00:00Z")
	return append(mrs,
		rttest.MR{IID: 401, Title: "DEMO-360 history", Source: "feature/DEMO-360", Target: "feature/sprint-2026-21", State: "merged", Author: "Dee Example", Username: "dee",
			CreatedAt: at("2026-10-07T03:00:00Z"), UpdatedAt: merged, MergedAt: &merged, WebURL: "https://gitlab.example.com/mr/401"},
		rttest.MR{IID: 402, Title: "DEMO-361 coupon", Source: "feature/DEMO-361", Target: "feature/sprint-2026-21", State: "opened", Author: "Rae Example", Username: "rae",
			CreatedAt: at("2026-10-09T03:00:00Z"), UpdatedAt: at("2026-10-09T03:00:00Z"), WebURL: "https://gitlab.example.com/mr/402"},
		rttest.MR{IID: 403, Title: "DEMO-362 bond", Source: "feature/DEMO-362", Target: "feature/sprint-20-2026", State: "merged", Author: "Rae Example", Username: "rae",
			CreatedAt: at("2026-10-01T03:00:00Z"), UpdatedAt: at("2026-10-02T03:00:00Z"), MergedAt: ptrTime(at("2026-10-02T03:00:00Z")), WebURL: "https://gitlab.example.com/mr/403"},
	)
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestSprintRowsAndSprintBranchMembershipEndToEnd(t *testing.T) {
	w := newSprintWorld(t)
	w.settle()
	board := svc.NewBoardService(svc.BoardDeps{Config: svc.NewConfigLoader(w.configPath), Store: w.store, Clock: func() time.Time { return w.now }})
	body, _, err := board.Board(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Board struct {
			Trains []struct {
				Versions []struct {
					Cards []struct {
						Key string   `json:"key"`
						URL string   `json:"url"`
						Why []string `json:"why"`
					} `json:"cards"`
				} `json:"versions"`
			} `json:"trains"`
			Sprints []struct {
				Number int `json:"number"`
				Sprint struct {
					Start string `json:"start"`
					End   string `json:"end"`
				} `json:"sprint"`
				Cards []struct {
					Key       string   `json:"key"`
					OnBoard   bool     `json:"onBoard"`
					ActiveWhy []string `json:"activeWhy"`
					Mine      []struct {
						Why string `json:"why"`
					} `json:"mine"`
					MRs []struct {
						IID  int    `json:"iid"`
						Dest string `json:"dest"`
					} `json:"mrs"`
				} `json:"cards"`
			} `json:"sprints"`
		} `json:"board"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Board.Sprints) != 1 {
		t.Fatalf("sprints = %+v; want board 42's row", resp.Board.Sprints)
	}
	row := resp.Board.Sprints[0]
	if row.Number != 21 || row.Sprint.Start != "2026-10-06" || row.Sprint.End != "2026-10-20" {
		t.Fatalf("row = %+v; want sprint 21, 6 to 20 Oct in Bangkok", row)
	}
	got := map[string]string{}
	for _, c := range row.Cards {
		dests := make([]string, 0, len(c.MRs))
		for _, m := range c.MRs {
			dests = append(dests, m.Dest)
		}
		got[c.Key] = strings.Join(c.ActiveWhy, "+") + " | " + strings.Join(dests, ",") + " | " + map[bool]string{true: "on", false: "off"}[c.OnBoard]
		if c.Key == "DEMO-360" && (len(c.Mine) != 2 || c.Mine[0].Why != "assignee" || c.Mine[1].Why != "mr_author") {
			t.Errorf("DEMO-360 mine = %+v; want assignee and MR author", c.Mine)
		}
	}
	want := map[string]string{
		"DEMO-360": "new this sprint+MR this sprint+mobile subtask open | sprint | on",
		"DEMO-361": "MR this sprint | sprint_open | off",
	}
	if len(got) != len(want) || got["DEMO-360"] != want["DEMO-360"] || got["DEMO-361"] != want["DEMO-361"] {
		t.Fatalf("sprint cards = %v; want %v", got, want)
	}
	var units []string
	for _, c := range resp.Board.Trains[0].Versions[0].Cards {
		units = append(units, c.Key+":"+strings.Join(c.Why, "+"))
	}
	if !slices.Contains(units, "DEMO-362:sprint branch") {
		t.Fatalf("train 21 cards = %v; want DEMO-362 by its MR into the sprint 20 branch", units)
	}
}

func TestSprintReadsAreGETsOnTheirBudgets(t *testing.T) {
	w := newSprintWorld(t)
	w.settle()
	w.now = w.now.Add(6 * time.Minute)
	_, atl := w.pass()
	for _, p := range []string{"/rest/api/3/search/jql"} {
		if !slices.Contains(atl, p) {
			t.Fatalf("steady pass after 6m fetched %v; want the sprint cards read again", atl)
		}
	}
	if slices.Contains(atl, "/rest/agile/1.0/board/42/sprint") {
		t.Fatalf("steady pass after 6m fetched %v; the active sprint is read every 30 min", atl)
	}
	w.now = w.now.Add(30 * time.Minute)
	_, atl = w.pass()
	if !slices.Contains(atl, "/rest/agile/1.0/board/42/sprint") {
		t.Fatalf("pass after 36m fetched %v; want the board's active sprint read again", atl)
	}
}

func TestBoardSprintsStoredByTheActiveOnlyDaemonAreRefetchedInTheFirstPassAfterTheUpgrade(t *testing.T) {
	w := newSprintWorld(t)
	w.jira.Set("/rest/agile/1.0/board/42/sprint", `{"isLast":true,"values":[`+
		`{"id":7021,"name":"DEMO 2026-21","state":"active","startDate":"2026-10-06T02:00:00.000Z","endDate":"2026-10-20T10:00:00.000Z"},`+
		`{"id":7022,"name":"DEMO 2026-22","state":"future","startDate":"2026-10-20T02:00:00.000Z","endDate":"2026-11-03T10:00:00.000Z"}]}`)
	w.jira.Set(`/rest/api/3/search/jql?jql=sprint = 7022 ORDER BY key`, `{"issues":[]}`)
	w.jira.Set(`/rest/api/3/search/jql?jql=sprint = 7022 AND sprint not in closedSprints() ORDER BY key`, `{"issues":[]}`)
	start, end := rt.DayOf(at("2026-10-06T02:00:00Z"), time.UTC), rt.DayOf(at("2026-10-20T10:00:00Z"), time.UTC)
	old, err := rt.EncodeItem(rt.KindBoardSprints, rt.SprintFact{Board: 42, ID: 7021, Name: "DEMO 2026-21", Start: &start, End: &end}, nil)
	if err != nil {
		t.Fatal(err)
	}
	spec := rt.SpecFor(rt.KindBoardSprints, rt.BoardScope(42), "")
	spec.Version = 1
	ctx := context.Background()
	if err := w.store.PlanReleaseResources(ctx, []rt.ResourceSpec{spec}, w.now); err != nil {
		t.Fatal(err)
	}
	if err := w.store.WriteReleaseSnapshot(ctx, spec, []rt.Item{old}, w.now); err != nil {
		t.Fatal(err)
	}

	_, atl := w.pass()

	if !slices.Contains(atl, "/rest/agile/1.0/board/42/sprint") {
		t.Fatalf("first pass after the upgrade fetched %v; want the board's sprints read again, not kept for half their budget", atl)
	}
	for passes := 0; passes < 6; passes++ {
		w.now = w.now.Add(svc.Tick)
		w.pass()
	}
	board := svc.NewBoardService(svc.BoardDeps{Config: svc.NewConfigLoader(w.configPath), Store: w.store, Clock: func() time.Time { return w.now }})
	body, _, err := board.Board(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Board struct {
			Sprints []struct {
				Next   bool `json:"next"`
				Number int  `json:"number"`
			} `json:"sprints"`
		} `json:"board"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Board.Sprints) != 2 || resp.Board.Sprints[0].Next || !resp.Board.Sprints[1].Next || resp.Board.Sprints[1].Number != 22 {
		t.Fatalf("sprints = %+v; want sprint 21 active and sprint 22 next", resp.Board.Sprints)
	}
}

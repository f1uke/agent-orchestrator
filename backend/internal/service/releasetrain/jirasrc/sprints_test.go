package jirasrc

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/rttest"
)

func TestBoardReadAsksForActiveAndFutureSprintsAndCarriesTheirState(t *testing.T) {
	jira := rttest.NewJira(t)
	jira.Set("/rest/agile/1.0/board/42/sprint", `{"isLast":true,"values":[
		{"id":7021,"name":"DEMO 2026-21","state":"active","startDate":"2026-10-06T02:00:00.000Z","endDate":"2026-10-20T10:00:00.000Z"},
		{"id":7022,"name":"DEMO 2026-22","state":"future","startDate":"2026-10-20T10:00:00.000Z","endDate":"2026-11-03T10:00:00.000Z"},
		{"id":7023,"name":"DEMO 2026-23","state":"future"}
	]}`)
	view := testView()
	view.Config.Boards = []rt.BoardConfig{{ID: 42, Key: "DEMO", OwnsSprintBranch: true}}
	b, err := newSource(jira).Fetch(context.Background(), rt.SpecFor(rt.KindBoardSprints, rt.BoardScope(42), ""), view, nil)
	if err != nil || !b.Complete {
		t.Fatalf("fetch: %v", err)
	}
	if reqs := jira.TakeRequests(); len(reqs) != 1 || !strings.Contains(reqs[0], "state=active%2Cfuture") {
		t.Fatalf("requests = %v; want one board read for active and future sprints", reqs)
	}
	states := map[int]rt.SprintState{}
	dated := map[int]bool{}
	for _, s := range decode(t, rt.KindBoardSprints, b) {
		states[s.ID], dated[s.ID] = s.State, s.Start != nil && s.End != nil
	}
	if want := map[int]rt.SprintState{7021: rt.SprintActive, 7022: rt.SprintFuture, 7023: rt.SprintFuture}; !reflect.DeepEqual(states, want) {
		t.Fatalf("states = %v; want %v", states, want)
	}
	if want := map[int]bool{7021: true, 7022: true, 7023: false}; !reflect.DeepEqual(dated, want) {
		t.Fatalf("dated = %v; want %v", dated, want)
	}
}

func TestSprintEndDayClosesTheDayBeforeWhenJiraEndsItAtLocalMidnight(t *testing.T) {
	bangkok, err := time.LoadLocation("Asia/Bangkok")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		end  string
		want string
	}{
		{"evening of a Friday keeps its own day", "2026-10-30T11:00:00Z", "2026-10-30"},
		{"local midnight closes the Monday before", "2026-11-02T17:00:00Z", "2026-11-02"},
		{"local midnight closes the Thursday before a Friday", "2026-11-12T17:00:00Z", "2026-11-12"},
		{"one second past local midnight keeps its day", "2026-11-02T17:00:01Z", "2026-11-03"},
		{"a minute before local midnight keeps its day", "2026-11-02T16:59:00Z", "2026-11-02"},
		{"UTC midnight is seven in the morning there and keeps its day", "2026-11-03T00:00:00Z", "2026-11-03"},
	}
	for _, c := range cases {
		end, err := time.Parse(time.RFC3339, c.end)
		if err != nil {
			t.Fatal(err)
		}
		if got := sprintEndDay(end, bangkok).String(); got != c.want {
			t.Errorf("%s: end %s = %s; want %s", c.name, c.end, got, c.want)
		}
	}
}

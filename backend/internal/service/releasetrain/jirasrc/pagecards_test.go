package jirasrc

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/rttest"
)

func TestPlanReadsTheCardsOfEveryAttachedPage(t *testing.T) {
	v := testView()
	v.Pages = []rt.PlannedPage{{ID: "901", Keys: []string{"DEMO-1"}, Live: true}, {ID: "902", Keys: []string{"DEMO-2"}}}
	var got []string
	for _, s := range newSource(rttest.NewJira(t)).Plan(v) {
		if s.Kind == rt.KindPageCards.Name {
			got = append(got, fmt.Sprintf("%s %v", s.Scope, s.Budget))
		}
	}
	if want := []string{"page:901 5m0s", "page:902 6h0m0s"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("page card specs = %v, want %v", got, want)
	}
}

func TestPageCardsReadTheListedKeysLikeVersionCards(t *testing.T) {
	jira := rttest.NewJira(t)
	jira.Set(`/rest/api/3/search/jql?jql=key in (DEMO-1, DEMO-2, OTHER-9) ORDER BY key`, `{"issues":[`+
		issue("DEMO-1", "Wallet top up", "In Progress", "indeterminate", false, nil, "")+","+
		issue("DEMO-2", "Release Demo 3.5.0", "Done", "done", false, []string{"5001"}, "")+`]}`)
	v := testView()
	v.Pages = []rt.PlannedPage{{ID: "901", Keys: []string{"DEMO-1", "DEMO-2", "OTHER-9"}, Live: true}, {ID: "902", Keys: []string{}}}
	src := newSource(jira)
	b, err := src.Fetch(t.Context(), rt.SpecFor(rt.KindPageCards, rt.PageScope("901"), ""), v, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := decode(t, rt.KindPageCards, b)
	if len(got) != 2 || got[0].Key != "DEMO-1" || got[0].Status != "In Progress" || !got[1].ReleaseTicket {
		t.Fatalf("page cards = %+v, want both known keys, the release ticket marked", got)
	}
	empty, err := src.Fetch(t.Context(), rt.SpecFor(rt.KindPageCards, rt.PageScope("902"), ""), v, nil)
	if err != nil || !empty.Complete || len(empty.Items) != 0 {
		t.Fatalf("a page with no keys = %+v, %v; want a complete empty read", empty, err)
	}
	reqs := jira.TakeRequests()
	if len(reqs) != 1 || !strings.HasPrefix(reqs[0], "GET ") {
		t.Fatalf("requests = %v, want one GET (an empty page sends nothing)", reqs)
	}
}

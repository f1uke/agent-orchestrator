package confluencesrc

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/atlassian"
	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	svc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/rttest"
)

const demoCQL = `space = "DEMOSPACE" AND title ~ "Release train" AND created >= "2026-01-31"`

func testView(t *testing.T) rt.PlanView {
	t.Helper()
	cfg := rt.Config{
		Timezone: "Asia/Bangkok", HistoryFrom: rt.Day{Y: 2026, M: time.April, D: 1}, AgeOutDays: 7, FixWrites: "off", GitLabHost: "gitlab.example.com",
		Jira:       rt.JiraConfig{Project: "DEMO"},
		Confluence: &rt.ConfluenceConfig{Space: "DEMOSPACE", TrainPageTitle: "Release train", FreezeCell: "Code freeze", PublishCell: "Store release"},
		Families: []rt.Family{
			{ID: "demo", TrainLabel: "Release Train", Apps: []rt.AppID{"di"}},
			{ID: "pilot", TrainLabel: "Pilot Release Train", Apps: []rt.AppID{"pi"}},
		},
		Apps: []rt.App{
			{ID: "di", Platform: rt.PlatformIOS, GitLabProject: 1001, JiraVersionPrefix: "iOS"},
			{ID: "pi", Platform: rt.PlatformIOS, GitLabProject: 1003, JiraVersionPrefix: "iOS Pilot"},
		},
	}
	loc, err := cfg.Validate()
	if err != nil {
		t.Fatal(err)
	}
	return rt.PlanView{Config: cfg, Loc: loc, HistoryFrom: cfg.HistoryFrom, Apps: cfg.Apps}
}

func newSource(host *rttest.Host) *Source {
	return New(Options{Config: func() (atlassian.Config, error) {
		return atlassian.Config{BaseURL: host.URL, Email: "dee@example.com", Token: "t"}, nil
	}})
}

func page(id, title, body string) string {
	return fmt.Sprintf(`{"id":%q,"title":%q,"body":{"storage":{"value":%q}},"history":{"createdDate":"2026-09-16T06:30:40.949Z"},"_links":{"webui":"/spaces/DEMOSPACE/pages/%s"}}`, id, title, body, id)
}

const table = `<table><tr><th>Code freeze</th><td><time datetime="2026-10-13" /></td></tr><tr><th>Store release</th><td><time datetime="2026-10-20" /></td></tr></table><p>DEMO-7</p>`

func TestPlanIsOneResourcePerConfiguredSpaceAndNoneWithout(t *testing.T) {
	v := testView(t)
	specs := newSource(rttest.NewJira(t)).Plan(v)
	if len(specs) != 1 || specs[0].Kind != rt.KindPages.Name || specs[0].Scope != "space:DEMOSPACE" || specs[0].Budget != 15*time.Minute {
		t.Fatalf("plan = %+v, want one 15m confluence.pages resource for DEMOSPACE", specs)
	}
	if got := CQL(v); got != demoCQL {
		t.Fatalf("CQL = %s, want %s (created since historyFrom less 60 days)", got, demoCQL)
	}
	v.Config.Confluence = nil
	if specs := newSource(rttest.NewJira(t)).Plan(v); len(specs) != 0 {
		t.Fatalf("plan without a confluence section = %+v, want none", specs)
	}
}

func TestFetchMapsTrainPagesAcrossNextLinksAndDropsOthers(t *testing.T) {
	host := rttest.NewJira(t)
	host.Set("/wiki/rest/api/content/search?cql="+demoCQL, `{"results":[`+
		page("11", "2026.10.13 - Release train #21", table)+","+
		page("12", "Release train retro", "")+
		`],"_links":{"next":"/rest/api/content/search?next=true&cursor=c2&limit=25"}}`)
	host.Set("/wiki/rest/api/content/search?cursor=c2", `{"results":[`+
		page("13", "2026.10.13 - Pilot Release train #21", "<p>no table</p>")+`],"_links":{}}`)
	v := testView(t)
	spec := rt.SpecFor(rt.KindPages, rt.SpaceScope("DEMOSPACE"), "")
	b, err := newSource(host).Fetch(t.Context(), spec, v, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []rt.PageFact
	for _, it := range b.Items {
		f, err := rt.KindPages.Decode(it.Body)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, f)
	}
	if !b.Complete || len(got) != 2 {
		t.Fatalf("pages = %+v, want the two train pages and not the retro", got)
	}
	demo, pilot := got[0], got[1]
	if demo.Family != "demo" || demo.Number != 21 || demo.Freeze == nil || demo.Freeze.String() != "2026-10-13" || demo.Publish.String() != "2026-10-20" ||
		demo.Created.String() != "2026-09-16" || demo.URL != host.URL+"/wiki/spaces/DEMOSPACE/pages/11" || strings.Join(demo.Keys, ",") != "DEMO-7" {
		t.Fatalf("demo page = %+v", demo)
	}
	if pilot.Family != "pilot" || pilot.ParseError == "" {
		t.Fatalf("pilot page = %+v, want a parse error, not a dropped page", pilot)
	}
	for _, r := range host.TakeRequests() {
		if !strings.HasPrefix(r, "GET /wiki/rest/api/content/search?") {
			t.Fatalf("request %s, want GETs of the Confluence search only", r)
		}
	}
}

func TestErrorsClassifyIntoFetchErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		header map[string]string
		after  int
		want   rt.ErrorClass
		wait   time.Duration
	}{
		{"429", http.StatusTooManyRequests, map[string]string{"Retry-After": "30"}, 0, rt.ErrRateLimited, 30 * time.Second},
		{"401", http.StatusUnauthorized, nil, 0, rt.ErrAuth, 0},
		{"404", http.StatusNotFound, nil, 0, rt.ErrNotFound, 0},
		{"400 bad CQL", http.StatusBadRequest, nil, 0, rt.ErrNotFound, 0},
		{"5xx", http.StatusBadGateway, nil, 0, rt.ErrUnavailable, 0},
		{"later page 5xx", http.StatusBadGateway, nil, 1, rt.ErrTruncated, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := rttest.NewJira(t)
			host.Set("/wiki/rest/api/content/search?cql="+demoCQL, `{"results":[],"_links":{"next":"/rest/api/content/search?cursor=c2"}}`)
			host.Set("/wiki/rest/api/content/search?cursor=c2", `{"results":[],"_links":{}}`)
			key := "/wiki/rest/api/content/search?cql=" + demoCQL
			if tc.after > 0 {
				key = "/wiki/rest/api/content/search?cursor=c2"
			}
			host.Fail(key, tc.status, tc.header)
			_, err := newSource(host).Fetch(t.Context(), rt.SpecFor(rt.KindPages, rt.SpaceScope("DEMOSPACE"), ""), testView(t), nil)
			var fe *svc.FetchError
			if !errors.As(err, &fe) || fe.Class != tc.want || fe.RetryAfter != tc.wait {
				t.Fatalf("err = %v, want class %s waiting %v", err, tc.want, tc.wait)
			}
		})
	}
}

func TestSystemAndHost(t *testing.T) {
	s := newSource(rttest.NewJira(t))
	if s.System() != "confluence" || s.Host() != "atlassian" {
		t.Fatalf("system %s host %s, want confluence on the atlassian host", s.System(), s.Host())
	}
}

package releasetrain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func confluenceConfig() Config {
	cfg := fixtureConfig()
	cfg.Confluence = &ConfluenceConfig{Space: "DEMOSPACE", TrainPageTitle: "Release Train", FreezeCell: "Code freeze", PublishCell: "Store release"}
	return cfg
}

const storageBody = `<p>Plan for DEMO-12 and <a href="https://jira.example.com/browse/DEMO-3">DEMO-3</a>, again DEMO-12.</p>
<table><tbody>
<tr><th><p><strong>Code freeze</strong></p></th>
  <td><p><time datetime="2026-10-12" /></p></td></tr>
<tr><td><p>Store release</p></td>
  <td><p>2026-10-19 (Mon)</p></td></tr>
</tbody></table>
<ac:task-list>
<ac:task><ac:task-id>1</ac:task-id><ac:task-status>complete</ac:task-status><ac:task-body>QA sign-off OTHER-7</ac:task-body></ac:task>
<ac:task><ac:task-id>2</ac:task-id><ac:task-status>incomplete</ac:task-status><ac:task-body>Store notes</ac:task-body></ac:task>
<ac:task><ac:task-id>3</ac:task-id><ac:task-status>complete</ac:task-status><ac:task-body>Smoke test</ac:task-body></ac:task>
</ac:task-list>`

func rawPage(title, body string) RawPage {
	return RawPage{ID: "77", Title: title, URL: "https://wiki.example.com/pages/77", Created: time.Date(2026, 9, 20, 23, 30, 0, 0, time.UTC), Body: body}
}

func TestParseTrainPageReadsTitleCellsKeysAndTasks(t *testing.T) {
	cfg := confluenceConfig()
	loc, _ := cfg.Validate()
	got, ok := ParseTrainPage(cfg, loc, rawPage("2026.10.12 - Release train #21", storageBody))
	if !ok {
		t.Fatal("title not recognised")
	}
	want := PageFact{
		ID: "77", Title: "2026.10.12 - Release train #21", URL: "https://wiki.example.com/pages/77",
		Created: Day{2026, time.September, 21}, Family: "demo", Number: 21,
		Freeze: &Day{2026, time.October, 12}, Publish: &Day{2026, time.October, 19},
		Keys: []string{"DEMO-12", "DEMO-3", "OTHER-7"}, Tasks: 3, Ticked: 2,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("page =\n%+v\nwant\n%+v", got, want)
	}
}

func TestParseTrainPageFamilyFromTheLongestTrainLabel(t *testing.T) {
	cfg := confluenceConfig()
	loc, _ := cfg.Validate()
	for title, want := range map[string]struct {
		family        FamilyID
		number, extra int
		ok            bool
	}{
		"2026.10.12 - Release train #21":         {"demo", 21, 0, true},
		"2026.10.12 - Pilot Release train #21":   {"pilot", 21, 0, true},
		"2026.10.06 - Release train #20-1":       {"demo", 20, 1, true},
		"2026.10.12 - Release train #21 (Moved)": {"", 0, 0, false},
		"Release train retro":                    {"", 0, 0, false},
		"2026.10.12 - PreRelease train #21":      {"", 0, 0, false},
	} {
		got, ok := ParseTrainPage(cfg, loc, rawPage(title, storageBody))
		if ok != want.ok || got.Family != want.family || got.Number != want.number || got.Extra != want.extra {
			t.Errorf("%q: family %q #%d-%d ok %v; want %+v", title, got.Family, got.Number, got.Extra, ok, want)
		}
	}
}

func TestTableDate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		want  string
		found bool
	}{
		{"time element", `<tr><th>Code freeze</th><td><time datetime="2026-10-12" /></td></tr>`, "2026-10-12", true},
		{"plain date in text", `<tr><td>Code freeze</td><td><p>due <b>2026-10-13</b></p></td></tr>`, "2026-10-13", true},
		{"time wins over text", `<tr><td>Code freeze</td><td>2026-01-01 <time datetime="2026-10-14"/></td></tr>`, "2026-10-14", true},
		{"cell with text and no date", `<tr><td>Code freeze</td><td><p>to be decided</p></td></tr>`, "", true},
		{"missing cell", `<tr><td>Kick-off</td><td>2026-10-01</td></tr>`, "", false},
		{"label without a following cell", `<p>Code freeze</p>`, "", false},
		{"non-breaking space between cells", "<tr><td>Code freeze</td> <td>2026-10-15</td></tr>", "2026-10-15", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, found := tableDate(tc.body, "Code freeze")
			got := ""
			if d != nil {
				got = d.String()
			}
			if got != tc.want || found != tc.found {
				t.Fatalf("tableDate = %q, %v; want %q, %v", got, found, tc.want, tc.found)
			}
		})
	}
}

func TestParseTrainPageWithoutTheDateCellsIsAParseError(t *testing.T) {
	cfg := confluenceConfig()
	loc, _ := cfg.Validate()
	got, ok := ParseTrainPage(cfg, loc, rawPage("Release train #21", `<p>DEMO-1</p><ac:task>x</ac:task>`))
	if !ok || got.ParseError == "" || got.Freeze != nil || got.Publish != nil || !strings.Contains(got.ParseError, "Code freeze") {
		t.Fatalf("page = %+v, want a parse error naming the missing cells", got)
	}
	if !reflect.DeepEqual(got.Keys, []string{"DEMO-1"}) || got.Tasks != 1 {
		t.Fatalf("keys %v tasks %d: the rest of the page still reads", got.Keys, got.Tasks)
	}
	got, _ = ParseTrainPage(cfg, loc, rawPage("Release train #21", `<table><tr><td>Code freeze</td><td>soon</td></tr><tr><td>Store release</td><td>later</td></tr></table>`))
	if got.ParseError != "" || got.Freeze != nil {
		t.Fatalf("page = %+v, want labelled cells with no date to be silent", got)
	}
}

func TestConfigValidateNeedsEveryConfluenceField(t *testing.T) {
	for _, edit := range []func(*ConfluenceConfig){
		func(c *ConfluenceConfig) { c.FreezeCell = "" },
		func(c *ConfluenceConfig) { c.PublishCell = "" },
		func(c *ConfluenceConfig) { c.Space = `DEMO" OR space = X` },
	} {
		cfg := confluenceConfig()
		edit(cfg.Confluence)
		if _, err := cfg.Validate(); err == nil {
			t.Errorf("Validate(%+v) passed", *cfg.Confluence)
		}
	}
}

func trainPages(b Board, train string) map[FamilyID]string {
	out := map[FamilyID]string{}
	for _, tr := range b.Trains {
		if tr.ID.Key() == train {
			for _, p := range tr.Pages {
				out[p.Family] = p.ID
			}
		}
	}
	return out
}

func TestPagesAttachWithinSixtyDaysBeforeFreeze(t *testing.T) {
	for _, tc := range []struct {
		name     string
		train    string
		created  string
		attached bool
	}{
		{"created on freeze day", "21", liveFreeze, true},
		{"60 days before freeze", "21", "2026-08-10", true},
		{"61 days before freeze", "21", "2026-08-09", false},
		{"after freeze", "21", "2026-10-10", false},
		{"another train's page", "22", "2026-09-20", false},
		{"an extra train's page", "21-1", "2026-09-20", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, today)
			healthy(f)
			p := f.page("demo", tc.train, tc.created, liveFreeze, liveDue)
			got := trainPages(f.reconcile(), "21-2026")
			if (got["demo"] == p.ID) != tc.attached {
				t.Fatalf("pages on #21 = %v; want attached %v", got, tc.attached)
			}
		})
	}
}

func TestTheNewestPageOfAFamilyWins(t *testing.T) {
	f := newFixture(t, today)
	healthy(f)
	f.page("demo", "21", "2026-09-01", liveFreeze, liveDue)
	newer := f.page("demo", "21", "2026-09-10", liveFreeze, liveDue)
	if got := trainPages(f.reconcile(), "21-2026"); len(got) != 1 || got["demo"] != newer.ID {
		t.Fatalf("pages = %v, want only the newer %s", got, newer.ID)
	}
}

func cardOn(b Board, app AppID, key string) *CardUnit {
	for _, tr := range b.Trains {
		if v := tr.version(app); v != nil {
			for i := range v.Cards {
				if v.Cards[i].Key == key {
					return &v.Cards[i]
				}
			}
		}
	}
	return nil
}

func TestPageMembershipPinsByFixVersionThenMRThenFamily(t *testing.T) {
	f := newFixture(t, today)
	di := healthy(f)
	da := f.version("da", "21", "3.5.0", liveFreeze, liveDue)
	pi := f.version("pi", "21", "1.2.0", liveFreeze, liveDue)
	p := f.page("demo", "21", "2026-09-20", liveFreeze, liveDue)
	f.card(p, "DEMO-20", da.jira.ID)
	f.card(p, "DEMO-21")
	di.cardMR("DEMO-21 tidy", "feat/tidy", "develop", MROpened, "2026-10-01", "")
	f.card(p, "DEMO-22")
	f.card(p, "DEMO-1", di.jira.ID)
	f.card(p, "DEMO-23").Subtask = true
	f.card(p, "DEMO-24").ReleaseTicket = true
	_ = pi

	b := f.reconcile()
	for _, tc := range []struct {
		key  string
		apps []AppID
	}{
		{"DEMO-20", []AppID{"da"}},
		{"DEMO-21", []AppID{"di"}},
		{"DEMO-22", []AppID{"di", "da"}},
		{"DEMO-23", nil},
		{"DEMO-24", nil},
	} {
		var on []AppID
		for _, app := range []AppID{"di", "da", "pi", "pa"} {
			if c := cardOn(b, app, tc.key); c != nil {
				on = append(on, app)
				if !reflect.DeepEqual(c.Why, []CardWhy{WhyPage}) {
					t.Errorf("%s on %s why = %v, want the page", tc.key, app, c.Why)
				}
			}
		}
		if !reflect.DeepEqual(on, tc.apps) {
			t.Errorf("%s is on %v, want %v", tc.key, on, tc.apps)
		}
	}
	if c := cardOn(b, "di", "DEMO-1"); c == nil || !reflect.DeepEqual(c.Why, []CardWhy{WhyFixVersion, WhyPage}) {
		t.Fatalf("DEMO-1 = %+v, want one unit with both reasons", c)
	}
	if c := cardOn(b, "di", "DEMO-21"); c == nil || c.Status != "In Progress" {
		t.Fatalf("DEMO-21 = %+v, want its Jira status from the page's card read", c)
	}
}

func a6Fixture(t *testing.T) (*fixture, *fv) {
	f := newFixture(t, today)
	v := healthy(f)
	return f, v
}

func TestA6(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fixture)
		want  []string
		text  []string
	}{
		{"no page", func(*fixture) {}, nil, nil},
		{"page agrees", func(f *fixture) { f.page("demo", "21", "2026-09-20", liveFreeze, liveDue) }, nil, nil},
		{"freeze differs", func(f *fixture) { f.page("demo", "21", "2026-09-20", "2026-10-08", liveDue) },
			[]string{"A6~vd/21-2026/di/freeze"}, []string{"Train 21 freeze dates differ: page Thu 8 Oct, milestone Fri 9 Oct"}},
		{"publish differs", func(f *fixture) { f.page("demo", "21", "2026-09-20", liveFreeze, "2026-10-19") },
			[]string{"A6~vd/21-2026/di/publish"}, []string{"Train 21 publish dates differ: page Mon 19 Oct, milestone Fri 16 Oct"}},
		{"both differ", func(f *fixture) { f.page("demo", "21", "2026-09-20", "2026-10-08", "2026-10-19") },
			[]string{"A6~vd/21-2026/di/freeze", "A6~vd/21-2026/di/publish"}, nil},
		{"page date missing is quiet", func(f *fixture) { f.page("demo", "21", "2026-09-20", "", "") }, nil, nil},
		{"unreadable page", func(f *fixture) {
			f.page("demo", "21", "2026-09-20", "", "").ParseError = `no "Code freeze" cell in the page table`
		}, []string{"A6~vd/21-2026/di/page"}, []string{"The train 21 page cannot be read"}},
		{"another family's page", func(f *fixture) { f.page("pilot", "21", "2026-09-20", "2026-10-08", "2026-10-19") }, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := a6Fixture(t)
			tc.setup(f)
			b := f.reconcile()
			if got := firedIDs(b, "A6"); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("A6 = %v, want %v", got, tc.want)
			}
			for i, text := range tc.text {
				if h := firedHits(b, "A6")[i]; h.Copy.What != text || h.Primary == nil || h.Primary.Label != "Open page" || h.Primary.URL == "" {
					t.Fatalf("hit %d = %q (%+v), want %q and the page as the primary action", i, h.Copy.What, h.Primary, text)
				}
			}
			if _, clean := outcomeOf(b, "A6", "di").(Clean); len(tc.want) == 0 && !clean {
				t.Fatalf("A6 di = %#v, want Clean", outcomeOf(b, "A6", "di"))
			}
		})
	}
}

func TestA6WithoutConfluenceIsUnknownNotClean(t *testing.T) {
	f, _ := a6Fixture(t)
	snap := f.snapshot()
	in := f.inputs()
	in.Snap = Snapshot{res: map[resKey]resource{}}
	for k, r := range snap.res {
		if k.kind != KindPages.Name {
			in.Snap.res[k] = r
		}
	}
	if _, ok := outcomeOf(Reconcile(in, f.env()), "A6", "di").(Unknown); !ok {
		t.Fatalf("A6 with no confluence.pages resource = %#v, want Unknown", outcomeOf(Reconcile(in, f.env()), "A6", "di"))
	}
}

func TestBoardJSONCarriesEachTrainsPagesForThePage(t *testing.T) {
	f := newFixture(t, today)
	healthy(f)
	p := f.page("demo", "21", "2026-09-20", liveFreeze, liveDue)
	p.Tasks, p.Ticked, p.ParseError = 7, 3, "no freeze cell"
	raw, err := json.Marshal(f.reconcile())
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Trains []struct {
			Pages []map[string]any `json:"pages"`
		} `json:"trains"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	got := w.Trains[0].Pages
	want := map[string]any{"title": p.Title, "url": p.URL, "freeze": liveFreeze, "publish": liveDue, "tasks": 7.0, "ticked": 3.0, "parseError": "no freeze cell", "family": "demo"}
	if len(got) != 1 {
		t.Fatalf("pages = %v", got)
	}
	for k, v := range want {
		if got[0][k] != v {
			t.Errorf("page %s = %v, want %v", k, got[0][k], v)
		}
	}
}

func TestSettledPageCardsAreJudgedByTheSettledBudget(t *testing.T) {
	f := newFixture(t, today)
	healthy(f)
	f.published("da", "18", "3.2.0", "2026-08-01", "2026-08-08")
	settled := f.page("demo", "18", "2026-07-15", "2026-08-01", "2026-08-08")
	f.statuses[KindPageCards.Name] = func(st *ResourceStatus) {
		at := f.now.Add(-time.Hour)
		st.CompleteAt = &at
	}
	for _, s := range f.reconcile().Sources {
		for _, is := range s.Issues {
			if is.Scope == PageScope(settled.ID) {
				t.Fatalf("settled page's cards listed not fresh after 1h: %+v", is)
			}
		}
	}
}

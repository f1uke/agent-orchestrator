package releasetrain

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func (f *fixture) sprint(board int, n int, start, end string) *SprintFact {
	key := ""
	for _, b := range f.cfg.Boards {
		if b.ID == board {
			key = b.Key
		}
	}
	id := 7000 + f.id()
	s := &SprintFact{Board: board, ID: id, Name: key + " 2026-" + strconv.Itoa(n), Start: dayPtr(f.t, start), End: dayPtr(f.t, end),
		URL: "https://jira.example.com/sprint/" + strconv.Itoa(id)}
	f.sprints = append(f.sprints, s)
	return s
}

func (f *fixture) sprintCard(s *SprintFact, key, status string, isNew bool) *IssueFact {
	is := &IssueFact{Key: key, Summary: "Card " + key, Status: status, StatusCategory: categoryOf(status), URL: "https://jira.example.com/browse/" + key}
	f.sprintIss[s.ID] = append(f.sprintIss[s.ID], is)
	if isNew {
		f.thisSprint[s.ID] = append(f.thisSprint[s.ID], key)
	}
	return is
}

func (f *fixture) titledCard(s *SprintFact, key, summary, status string, isNew bool) *IssueFact {
	is := f.sprintCard(s, key, status, isNew)
	is.Summary = summary
	return is
}

func (f *fixture) subtask(s *SprintFact, parent, key, summary, category string) *IssueFact {
	is := &IssueFact{Key: key, Summary: summary, Status: "Task", StatusCategory: category, Subtask: true, Parent: parent, URL: "https://jira.example.com/browse/" + key}
	f.sprintIss[s.ID] = append(f.sprintIss[s.ID], is)
	return is
}

func categoryOf(status string) string {
	switch status {
	case "Done", "Ready for UAT":
		return StatusCategoryDone
	case "To Do", "Ready to Dev":
		return "new"
	}
	return "indeterminate"
}

func demoSprint(f *fixture) *SprintFact { return f.sprint(42, 7, "2026-10-06", "2026-10-20") }

func rowOf(t *testing.T, b Board, key string) SprintRow {
	t.Helper()
	for _, r := range b.Sprints {
		if r.Board.Key == key {
			return r
		}
	}
	t.Fatalf("no sprint row for %s in %+v", key, b.Sprints)
	return SprintRow{}
}

func cardKeys(r SprintRow) []string {
	out := []string{}
	for _, c := range r.Cards {
		if c.Mobile {
			out = append(out, c.Key)
		}
	}
	return out
}

func allCardKeys(r SprintRow) []string {
	out := []string{}
	for _, c := range r.Cards {
		out = append(out, c.Key)
	}
	return out
}

func TestSprintRowShowsEveryCardAndMarksTheOnesThatTouchMobile(t *testing.T) {
	f := newFixture(t, today)
	s := f.sprint(43, 7, "2026-10-05", "2026-10-16")
	f.sprintCard(s, "OTHER-1", "In Progress", true)
	f.sprintCard(s, "OTHER-2", "In Progress", true)
	f.subtask(s, "OTHER-2", "OTHER-3", "iOS login screen", "indeterminate")
	f.sprintCard(s, "OTHER-4", "In Progress", true)
	f.mr("da", "OTHER-4 wallet", "feat/OTHER-4", "develop", MROpened, "2026-10-06", "")
	f.sprintCard(s, "OTHER-5", "In Progress", true)
	f.subtask(s, "OTHER-5", "OTHER-6", "Backend endpoint", "indeterminate")
	f.sprint(42, 7, "2026-10-06", "2026-10-20")
	f.sprintCard(f.sprints[1], "DEMO-1", "To Do", false)

	b := f.reconcile()

	if got := cardKeys(rowOf(t, b, "OTHER")); !reflect.DeepEqual(got, []string{"OTHER-2", "OTHER-4"}) {
		t.Fatalf("OTHER mobile cards = %v; want the one with an iOS subtask and the one with an MR", got)
	}
	if got := allCardKeys(rowOf(t, b, "OTHER")); !reflect.DeepEqual(got, []string{"OTHER-1", "OTHER-2", "OTHER-4", "OTHER-5"}) {
		t.Fatalf("OTHER cards = %v; want every top-level card of the sprint", got)
	}
	if got := cardKeys(rowOf(t, b, "DEMO")); !reflect.DeepEqual(got, []string{"DEMO-1"}) {
		t.Fatalf("DEMO cards = %v; want DEMO-1, in a mobile project", got)
	}
	if r := rowOf(t, b, "OTHER"); r.Total != 4 || r.Number != 7 {
		t.Fatalf("OTHER total %d number %d; want 4 cards on the board and sprint 7", r.Total, r.Number)
	}
}

func TestSprintCardIsActiveForItsReasonsElseCarried(t *testing.T) {
	f := newFixture(t, today)
	s := f.sprint(42, 7, "2026-10-06", "2026-10-20")
	f.sprintCard(s, "DEMO-1", "In Progress", true)
	f.sprintCard(s, "DEMO-2", "In Progress", false)
	f.mr("di", "DEMO-2 old", "feat/DEMO-2", "develop", MRMerged, "2026-09-01", "2026-09-02")
	f.mr("di", "DEMO-2 new", "feat/DEMO-2b", "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")
	f.sprintCard(s, "DEMO-3", "In Progress", false)
	f.subtask(s, "DEMO-3", "DEMO-4", "Android crash", "indeterminate")
	f.sprintCard(s, "DEMO-5", "Done", false)
	f.mr("di", "DEMO-5 old", "feat/DEMO-5", "develop", MRMerged, "2026-09-01", "2026-09-02")
	f.subtask(s, "DEMO-5", "DEMO-8", "iOS done", StatusCategoryDone)
	f.sprintCard(s, "DEMO-6", "In Progress", false)
	f.subtask(s, "DEMO-6", "DEMO-7", "iOS polish", "new")
	f.thisSprint[s.ID] = append(f.thisSprint[s.ID], "DEMO-7")

	b := f.reconcile()

	want := map[string][]ActiveWhy{
		"DEMO-1": {ActiveNew},
		"DEMO-2": {ActiveMR},
		"DEMO-3": {ActiveSubtaskOpen},
		"DEMO-5": {},
		"DEMO-6": {ActiveNew, ActiveSubtaskOpen},
	}
	row := rowOf(t, b, "DEMO")
	for _, c := range row.Cards {
		if !reflect.DeepEqual(c.ActiveWhy, want[c.Key]) || c.Active != (len(want[c.Key]) > 0) {
			t.Errorf("%s active %v %v; want %v", c.Key, c.Active, c.ActiveWhy, want[c.Key])
		}
	}
	if row.Counts != (SprintCounts{New: 2, CarriedWithMRs: 2, CarriedDone: 1}) {
		t.Fatalf("counts = %+v; want 2 new, 2 carried with MRs or open mobile work, 1 carried done", row.Counts)
	}
}

func TestSprintCardShowsOnOneBoardAndTheOwnerPullsInOffBoardCards(t *testing.T) {
	f := newFixture(t, today)
	own := f.sprint(42, 7, "2026-10-06", "2026-10-20")
	other := f.sprint(43, 7, "2026-10-05", "2026-10-16")
	f.sprintCard(other, "OTHER-1", "In Progress", true)
	f.mr("di", "OTHER-1 coupon", "feat/OTHER-1", "feature/sprint-2026-7", MROpened, "2026-10-07", "")
	f.mr("di", "OTHER-9 coupon", "feat/OTHER-9", "feature/sprint-2026-7", MROpened, "2026-10-07", "")
	f.branchIss = append(f.branchIss,
		&IssueFact{Key: "OTHER-1", Summary: "Coupon", Status: "In Progress", StatusCategory: "indeterminate"},
		&IssueFact{Key: "OTHER-9", Summary: "Coupon list", Status: "In Progress", StatusCategory: "indeterminate", URL: "https://jira.example.com/browse/OTHER-9"})
	f.sprintCard(own, "DEMO-1", "In Progress", true)

	b := f.reconcile()

	if got := cardKeys(rowOf(t, b, "DEMO")); !reflect.DeepEqual(got, []string{"DEMO-1", "OTHER-9"}) {
		t.Fatalf("DEMO cards = %v; want its own card and the off-board OTHER-9, but not OTHER-1, which is on OTHER's sprint", got)
	}
	if got := cardKeys(rowOf(t, b, "OTHER")); !reflect.DeepEqual(got, []string{"OTHER-1"}) {
		t.Fatalf("OTHER cards = %v; want OTHER-1", got)
	}
	c := b.sprintCard("OTHER-9")
	if c.OnBoard || c.URL != "https://jira.example.com/browse/OTHER-9" {
		t.Fatalf("OTHER-9 = %+v; want off the board, with its browse URL", c)
	}
}

func TestSprintCardMRsCarryTheirAppRouteKind(t *testing.T) {
	f := newFixture(t, today)
	s := f.sprint(42, 7, "2026-10-06", "2026-10-20")
	f.sprintCard(s, "DEMO-1", "In Progress", true)
	f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MRMerged, "2026-10-07", "2026-10-08")
	f.mr("di", "DEMO-1 b", "feat/DEMO-1b", "feature/sprint-7-2026", MROpened, "2026-10-09", "")
	f.mr("da", "DEMO-1 c", "feat/DEMO-1", "develop", MROpened, "2026-10-09", "")
	f.mr("da", "DEMO-1 closed", "feat/DEMO-1", "develop", MRClosed, "2026-10-09", "")

	b := f.reconcile()

	c := b.sprintCard("DEMO-1")
	if len(c.MRs) != 3 {
		t.Fatalf("mrs = %+v; want three, closed left out", c.MRs)
	}
	for _, m := range c.MRs {
		var route *Route
		for i := range c.Routes {
			if c.Routes[i].App == m.App {
				route = &c.Routes[i]
			}
		}
		if route == nil || m.Dest != route.Kind {
			t.Fatalf("%s !%d dest %s, route %+v; every MR carries its app's route kind", m.App, m.IID, m.Dest, route)
		}
	}
	if c.MRs[0].Dest != DestSprint || c.MRs[2].Dest != DestDevelopOpen {
		t.Fatalf("dests = %s, %s; want sprint for di, develop_open for da", c.MRs[0].Dest, c.MRs[2].Dest)
	}
	row := rowOf(t, b, "DEMO")
	if len(row.Dests) != 2 || row.Dests[0].Count != 1 || row.Dests[1].To != (OffFlow{}) {
		t.Fatalf("dests = %+v; want train #8 then off flow, one card each", row.Dests)
	}
}

func TestCardRidesTheTrainOfItsMRIntoTheSprintBeforeIt(t *testing.T) {
	f := newFixture(t, today)
	v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	v.releaseMR("2026-10-09", MROpened)
	f.mr("di", "DEMO-50 wallet", "feat/DEMO-50", "feature/sprint-2026-20", MRMerged, "2026-10-01", "2026-10-02")
	f.mr("di", "DEMO-51 chat", "feat/DEMO-51", "feature/sprint-19-2026", MRMerged, "2026-09-20", "2026-09-21")
	f.mr("da", "DEMO-52 card", "feat/DEMO-52", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-02")
	f.branchIss = append(f.branchIss,
		&IssueFact{Key: "DEMO-50", Status: "Done", StatusCategory: "done", URL: "https://jira.example.com/browse/DEMO-50"},
		&IssueFact{Key: "DEMO-51", Status: "Done", StatusCategory: "done"},
		&IssueFact{Key: "DEMO-52", Status: "Done", StatusCategory: "done"})

	b := f.reconcile()

	cards := b.Trains[0].version("di").Cards
	if len(cards) != 1 || cards[0].Key != "DEMO-50" || !reflect.DeepEqual(cards[0].Why, []CardWhy{WhySprintBranch}) {
		t.Fatalf("cards = %+v; want DEMO-50 by its MR into the sprint 20 branch, not DEMO-51 (sprint 19) or DEMO-52 (other app)", cards)
	}
	if _, ok := cards[0].Code.(CodeMerged); !ok || cards[0].URL != "https://jira.example.com/browse/DEMO-50" {
		t.Fatalf("DEMO-50 = %+v; want merged, with its URL", cards[0])
	}
}

func TestEveryBoardRoutesByTheSprintBranchOwnersNumber(t *testing.T) {
	f := newFixture(t, today)
	own := f.sprint(42, 7, "2026-10-06", "2026-10-20")
	other := f.sprint(43, 8, "2026-10-05", "2026-10-16")
	f.sprintCard(own, "DEMO-1", "In Progress", true)
	f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MROpened, "2026-10-07", "")
	f.sprintCard(other, "OTHER-1", "In Progress", true)
	f.mr("di", "OTHER-1 a", "feat/OTHER-1", "feature/sprint-7-2026", MROpened, "2026-10-07", "")

	b := f.reconcile()

	for _, key := range []string{"DEMO-1", "OTHER-1"} {
		c := b.sprintCard(key)
		if len(c.Routes) != 1 || c.Routes[0].Kind != DestSprintOpen || c.Routes[0].To != (ToTrain{Train: TrainID{Number: 8, Year: 2026}}) {
			t.Errorf("%s routes = %+v; want sprint 7's branch, owned by DEMO, to ride train 8 on every board", key, c.Routes)
		}
	}
	if rowOf(t, b, "OTHER").Number != 8 {
		t.Fatalf("OTHER's label keeps its own sprint number")
	}
}

func TestACardTitledAsMobileIsMobileWithNoSubtaskAndNoMR(t *testing.T) {
	f := newFixture(t, today)
	s := f.sprint(43, 7, "2026-10-05", "2026-10-16")
	f.titledCard(s, "OTHER-1", "App - fictional thing", "To Do", false)
	f.titledCard(s, "OTHER-2", "Web - fictional thing", "To Do", false)
	f.titledCard(s, "OTHER-3", "[iOS] fictional thing", "To Do", false)
	f.titledCard(s, "OTHER-4", "Application form", "To Do", false)

	r := rowOf(t, f.reconcile(), "OTHER")

	if got := cardKeys(r); !reflect.DeepEqual(got, []string{"OTHER-1", "OTHER-3"}) {
		t.Fatalf("OTHER mobile cards = %v; want the App and the [iOS] titled cards, not Web or Application", got)
	}
	if got := allCardKeys(r); len(got) != 4 {
		t.Fatalf("OTHER cards = %v; want all four, a title only marks a card mobile", got)
	}
	if r.Total != 4 {
		t.Fatalf("total = %d; want all 4 cards of the sprint", r.Total)
	}
	for _, c := range r.Cards {
		if c.Active {
			t.Errorf("%s is active; a title only decides whether a card is mobile", c.Key)
		}
	}
}

func TestACardPatternInTheConfigReplacesTheDefault(t *testing.T) {
	f := newFixture(t, today)
	f.cfg.Mobile.CardPattern = `^Wallet\b`
	s := f.sprint(43, 7, "2026-10-05", "2026-10-16")
	f.titledCard(s, "OTHER-1", "App - fictional thing", "To Do", false)
	f.titledCard(s, "OTHER-2", "Wallet - fictional thing", "To Do", false)

	if got := cardKeys(rowOf(t, f.reconcile(), "OTHER")); !reflect.DeepEqual(got, []string{"OTHER-2"}) {
		t.Fatalf("OTHER mobile cards = %v; want only the card the configured pattern names", got)
	}
}

func TestAWebCardIsShownInItsRowAndRaisesNothingNorCounts(t *testing.T) {
	f := newFixture(t, today)
	s := f.sprint(43, 7, "2026-10-05", "2026-10-16")
	f.titledCard(s, "OTHER-1", "Web - fictional thing", "Done", true)
	f.titledCard(s, "OTHER-2", "App - fictional thing", "In Progress", true)
	f.sprintCard(f.sprint(42, 7, "2026-10-06", "2026-10-20"), "DEMO-1", "To Do", true)
	f.me()
	f.sprintIss[s.ID][0].Assignee = dee
	f.sprintIss[s.ID][1].Assignee = dee
	b := f.reconcile()

	r := rowOf(t, b, "OTHER")
	var web *SprintCard
	for i := range r.Cards {
		if r.Cards[i].Key == "OTHER-1" {
			web = &r.Cards[i]
		}
	}
	if web == nil || web.Mobile || len(web.Routes) != 0 || web.Active || len(web.Mine) != 0 {
		t.Fatalf("OTHER-1 = %+v; want it in the row, not mobile, with no route, not active", web)
	}
	if c := b.sprintCard("OTHER-2"); c == nil || len(c.Mine) != 1 {
		t.Fatalf("OTHER-2 = %+v; want the mobile card mine as its assignee", c)
	}
	if r.Total != 2 || len(r.Cards) != 2 {
		t.Fatalf("total %d, cards %d; want both cards shown", r.Total, len(r.Cards))
	}
	if r.Counts != (SprintCounts{New: 1}) || len(r.Dests) != 1 || r.Dests[0].Keys[0] != "OTHER-2" {
		t.Fatalf("counts %+v dests %+v; want the web card in neither", r.Counts, r.Dests)
	}
	if got := firedIDs(b, "G2"); !reflect.DeepEqual(got, []string{"G2~c/OTHER-2"}) {
		t.Errorf("G2 hits = %v; want the mobile App card with no MR, and not the web one", got)
	}
	for _, h := range b.Alerts.Hits {
		if strings.Contains(string(h.ID), "OTHER-1") {
			t.Errorf("hit %s names the web card", h.ID)
		}
	}
}

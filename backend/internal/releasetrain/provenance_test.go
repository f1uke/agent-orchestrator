package releasetrain

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCatalogueCarriesEachRulesDeclaredReads(t *testing.T) {
	b := newFixture(t, today).reconcile()

	byID := map[RuleID]RuleInfo{}
	for _, r := range b.Catalogue {
		byID[r.ID] = r
	}
	for _, r := range ruleTable() {
		got := byID[r.ID].Reads
		if len(got) == 0 || !reflect.DeepEqual(got, r.Reads) {
			t.Errorf("rule %s catalogue reads = %v, want its declared reads %v", r.ID, got, r.Reads)
		}
	}
}

func TestBoardKindsListEveryRegisteredKindWithItsSystemLabelAndBudget(t *testing.T) {
	b := newFixture(t, today).reconcile()

	if len(b.Kinds) != len(kinds) {
		t.Fatalf("board lists %d kinds, the registry has %d", len(b.Kinds), len(kinds))
	}
	labels := map[string]KindName{}
	for i, k := range b.Kinds {
		meta, ok := kinds[k.Kind]
		if !ok || k.Label == "" || k.BudgetSeconds != int(meta.Budget.Seconds()) {
			t.Errorf("kind %+v; want a registered kind with a label and its budget %v", k, meta.Budget)
		}
		if want, _, _ := strings.Cut(string(k.Kind), "."); k.System != want {
			t.Errorf("kind %s system = %q, want %q", k.Kind, k.System, want)
		}
		if other, dup := labels[k.Label]; dup {
			t.Errorf("kinds %s and %s share the label %q", other, k.Kind, k.Label)
		}
		labels[k.Label] = k.Kind
		if i > 0 && b.Kinds[i-1].Kind >= k.Kind {
			t.Errorf("kinds not sorted: %s before %s", b.Kinds[i-1].Kind, k.Kind)
		}
	}
}

func TestPageCardsBadgeAsJira(t *testing.T) {
	k := kindEntry(t, newFixture(t, today).reconcile(), KindPageCards.Name)
	if k.System != "jira" || k.Label != "page cards (Jira)" {
		t.Fatalf("jira.page_cards = %+v; it reads Jira, with keys a Confluence page lists", k)
	}
}

func TestKindFreshnessJudgesEachKindOnItsOwnReads(t *testing.T) {
	f := newFixture(t, today)
	healthy(f)
	f.stale(KindJiraVersions.Name)
	f.statuses[KindTags.Name] = func(st *ResourceStatus) { st.ErrorClass = ErrRateLimited }
	f.neverComplete(KindTechReq.Name)
	b := f.reconcile()

	issueKinds := func(k KindFreshness) []string {
		var out []string
		for _, st := range k.Issues {
			out = append(out, string(st.Kind)+" "+string(st.ErrorClass))
		}
		return out
	}

	milestones := kindEntry(t, b, KindMilestones.Name)
	if milestones.Resources != 4 || len(milestones.Issues) != 0 || milestones.AsOf == nil || f.now.Sub(*milestones.AsOf) > time.Minute {
		t.Errorf("gitlab.milestones = %+v; want 4 fresh resources, no issues, a recent age", milestones)
	}
	versions := kindEntry(t, b, KindJiraVersions.Name)
	if got := issueKinds(versions); len(got) != 1 || got[0] != "jira.versions unavailable" || versions.AsOf == nil || f.now.Sub(*versions.AsOf) < 23*time.Hour {
		t.Errorf("jira.versions = %+v; want stale, aged by its own day-old read", versions)
	}
	if got := issueKinds(kindEntry(t, b, KindTags.Name)); len(got) != 4 || got[0] != "gitlab.tags rate_limited" {
		t.Errorf("gitlab.tags issues = %v; want each app's failing read", got)
	}
	techreq := kindEntry(t, b, KindTechReq.Name)
	if got := issueKinds(techreq); len(got) != 1 || got[0] != "jira.techreq auth" || techreq.AsOf != nil {
		t.Errorf("jira.techreq = %+v; want never read: an issue and no age", techreq)
	}
	if mrs := kindEntry(t, b, KindMRs.Name); len(mrs.Issues) != 0 {
		t.Errorf("gitlab.mrs issues = %v; another kind's trouble must not reach it", mrs.Issues)
	}
	if users := kindEntry(t, b, KindJiraUsers.Name); users.Resources != 0 || users.AsOf != nil || len(users.Issues) != 0 {
		t.Errorf("jira.users = %+v; a kind with no planned resource has nothing to report", users)
	}
}

func TestKindFreshnessJudgesASettledVersionsReadByTheSettledBudget(t *testing.T) {
	f := newFixture(t, today)
	live := healthy(f)
	settled := f.published("da", "18", "3.2.0", "2026-08-01", "2026-08-08")
	f.statuses[KindCards.Name] = func(st *ResourceStatus) {
		at := f.now.Add(-time.Hour)
		if st.Scope == VersionScope(settled.jira.ID) {
			at = f.now.Add(-5 * time.Hour)
		}
		st.CompleteAt = &at
	}
	cards := kindEntry(t, f.reconcile(), KindCards.Name)

	if len(cards.Issues) != 1 || cards.Issues[0].Scope != VersionScope(live.jira.ID) {
		t.Fatalf("jira.cards issues = %+v; want only the live version's hour-old read, the settled one is within 6h", cards.Issues)
	}
	if cards.Resources != 2 || cards.AsOf == nil || f.now.Sub(*cards.AsOf) != time.Hour {
		t.Fatalf("jira.cards = %+v; want 2 resources aged by the live read only", cards)
	}
}

func TestSourcesAreTheRollupOfTheirKinds(t *testing.T) {
	f := everyRuleFires(t)
	f.stale(KindJiraVersions.Name)
	b := f.reconcile()

	for _, s := range b.Sources {
		var resources, issues int
		var asOf *time.Time
		for _, k := range b.Kinds {
			if k.System != s.System {
				continue
			}
			resources += k.Resources
			issues += len(k.Issues)
			if k.AsOf != nil && (asOf == nil || k.AsOf.Before(*asOf)) {
				asOf = k.AsOf
			}
		}
		if resources != s.Resources || issues != len(s.Issues) || !reflect.DeepEqual(asOf, s.AsOf) {
			t.Errorf("%s = %d resources, %d issues, age %v; its kinds add up to %d, %d, %v", s.System, s.Resources, len(s.Issues), s.AsOf, resources, issues, asOf)
		}
	}
}

func TestScanAndApproveDatesAreAOConventions(t *testing.T) {
	f := newFixture(t, today)
	healthy(f)
	f.version("da", "21", "3.5.1", liveFreeze, liveDue)
	b := f.reconcile()

	want := map[StageKey]bool{StageScan: true, StageApprove: true, StageTag: true}
	for _, v := range b.Trains[0].Versions {
		for _, s := range v.Stages {
			if s.Conventional != want[s.Key] {
				t.Errorf("%s %s conventional = %v, want %v: only freeze + 1, freeze + 2 and publish - 1 are AO's own dates", v.App, s.Key, s.Conventional, want[s.Key])
			}
		}
	}
}

func TestCardUnitEvidenceNamesTheRecordBehindEachReason(t *testing.T) {
	f := newFixture(t, today)
	v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	v.releaseMR("2026-10-09", MROpened)
	p := f.page("demo", "21", "2026-09-20", liveFreeze, liveDue)
	v.issue("DEMO-1", "In Progress", "indeterminate")
	f.card(p, "DEMO-1", v.jira.ID)
	first := v.cardMR("DEMO-1 login", "feat/DEMO-1", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-02")
	second := v.cardMR("DEMO-1 login fix", "feat/DEMO-1-fix", "feature/sprint-20-2026", MRMerged, "2026-10-03", "2026-10-03")
	v.cardMR("DEMO-1 elsewhere", "feat/DEMO-1-x", "develop", MROpened, "2026-10-04", "")
	f.branchIss = append(f.branchIss, &IssueFact{Key: "DEMO-1", Status: "Done", StatusCategory: StatusCategoryDone})
	f.card(p, "DEMO-30")
	v.cardMR("DEMO-31 chat", "feat/DEMO-31", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-02")
	f.branchIss = append(f.branchIss, &IssueFact{Key: "DEMO-31", Status: "Done", StatusCategory: StatusCategoryDone})

	b := f.reconcile()

	all := cardOn(b, "di", "DEMO-1")
	if all == nil || !reflect.DeepEqual(all.Why, []CardWhy{WhyFixVersion, WhyPage, WhySprintBranch}) {
		t.Fatalf("DEMO-1 = %+v; the fixture needs a card in by all three reasons", all)
	}
	want := CardEvidence{
		FixVersion: &FixVersionEvidence{ID: v.jira.ID, Name: v.jira.Name, URL: v.jira.URL},
		Page:       &PageEvidence{ID: p.ID, Title: p.Title, URL: p.URL},
		SprintMR:   &SprintMREvidence{Branch: "feature/sprint-20-2026", MRs: []int{first, second}},
	}
	if !reflect.DeepEqual(all.Evidence, want) || all.FactsFrom != KindCards.Name {
		t.Fatalf("DEMO-1 evidence = %+v from %s\nwant %+v from jira.cards", all.Evidence, all.FactsFrom, want)
	}
	if c := cardOn(b, "di", "DEMO-30"); c == nil || !reflect.DeepEqual(c.Evidence, CardEvidence{Page: want.Page}) || c.FactsFrom != KindPageCards.Name {
		t.Fatalf("DEMO-30 = %+v; want only the page as evidence, facts from jira.page_cards", c)
	}
	if c := cardOn(b, "di", "DEMO-31"); c == nil || c.Evidence.FixVersion != nil || c.Evidence.Page != nil || c.Evidence.SprintMR == nil || c.FactsFrom != KindBranchCards.Name {
		t.Fatalf("DEMO-31 = %+v; want only the sprint MR as evidence, facts from jira.branch_cards", c)
	}
}

func TestEveryCardUnitHasEvidenceForExactlyItsReasons(t *testing.T) {
	b := everyRuleFires(t).reconcile()
	units := 0
	for _, tr := range b.Trains {
		for _, v := range tr.Versions {
			for _, c := range v.Cards {
				units++
				var got []CardWhy
				if c.Evidence.FixVersion != nil {
					got = append(got, WhyFixVersion)
				}
				if c.Evidence.Page != nil {
					got = append(got, WhyPage)
				}
				if c.Evidence.SprintMR != nil {
					got = append(got, WhySprintBranch)
				}
				if !reflect.DeepEqual(got, c.Why) || c.FactsFrom != whyKind[c.Why[0]] {
					t.Errorf("%s on %s why %v, evidence for %v, facts from %s", c.Key, c.App, c.Why, got, c.FactsFrom)
				}
			}
		}
	}
	if units < 5 {
		t.Fatalf("only %d card units; the check needs a populated board", units)
	}
}

func TestRoutePlanNamesTheSourcesThatPutTheCardOnThePlanTrain(t *testing.T) {
	f := newFixture(t, today)
	v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	v.releaseMR("2026-10-09", MROpened)
	p := f.page("demo", "21", "2026-09-20", liveFreeze, liveDue)
	sp := f.sprint(42, 21, "2026-10-06", "2026-10-20")
	for _, key := range []string{"DEMO-1", "DEMO-2", "DEMO-3", "DEMO-4"} {
		f.sprintCard(sp, key, "In Progress", true)
		f.mr("di", key+" work", "feat/"+key, "feature/sprint-21-2026", MROpened, "2026-10-07", "")
	}
	v.issue("DEMO-1", "In Progress", "indeterminate")
	f.card(p, "DEMO-2")
	f.mr("di", "DEMO-3 earlier", "feat/DEMO-3-a", "feature/sprint-20-2026", MRMerged, "2026-10-01", "2026-10-02")
	f.branchIss = append(f.branchIss, &IssueFact{Key: "DEMO-3", Status: "In Progress", StatusCategory: "indeterminate"})
	b := f.reconcile()

	for key, want := range map[string][]CardWhy{
		"DEMO-1": {WhyFixVersion},
		"DEMO-2": {WhyPage},
		"DEMO-3": {WhySprintBranch},
		"DEMO-4": nil,
	} {
		routes := b.sprintCard(key).Routes
		if len(routes) != 1 {
			t.Fatalf("%s routes = %+v, want one for di", key, routes)
		}
		r := routes[0]
		if !reflect.DeepEqual(r.PlanFrom, want) || (want == nil) != (r.Plan == nil) {
			t.Errorf("%s plan %v from %v, want from %v", key, r.Plan, r.PlanFrom, want)
		}
	}
}

func kindEntry(t *testing.T, b Board, kind KindName) KindFreshness {
	t.Helper()
	for _, k := range b.Kinds {
		if k.Kind == kind {
			return k
		}
	}
	t.Fatalf("board kinds lack %s", kind)
	return KindFreshness{}
}

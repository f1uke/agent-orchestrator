package releasetrain

import (
	"testing"
)

func alertHit(b Board, id HitID) (AlertHit, bool) {
	for _, h := range b.Alerts.Hits {
		if h.ID == id {
			return h, true
		}
	}
	return AlertHit{}, false
}

func TestNothingOverAStaleReadIsUnknownNotClean(t *testing.T) {
	f := newFixture(t, today)
	healthy(f)
	f.stale(KindJiraVersions.Name)

	b := f.reconcile()

	u, ok := outcomeOf(b, "D1", "di").(Unknown)
	if !ok {
		t.Fatalf("D1 over a stale jira.versions = %T, want Unknown", outcomeOf(b, "D1", "di"))
	}
	if len(u.Missing) == 0 || u.Missing[0].Kind != KindJiraVersions.Name {
		t.Fatalf("Unknown.Missing = %+v, want jira.versions", u.Missing)
	}
	if _, ok := outcomeOf(b, "C5", "di").(Clean); !ok {
		t.Fatalf("C5 reads no Jira, outcome = %T, want Clean", outcomeOf(b, "C5", "di"))
	}
	if b.Alerts.Badge.Count != 0 || b.Alerts.Badge.Unknown == 0 {
		t.Fatalf("badge = %+v, want count 0 and some unknown", b.Alerts.Badge)
	}
}

func TestAHitOverAStaleReadStaysFiredAndDegraded(t *testing.T) {
	f := newFixture(t, today)
	healthy(f).approvers()
	f.stale(KindJiraVersions.Name)

	b := f.reconcile()

	fired, ok := outcomeOf(b, "D1", "di").(Fired)
	if !ok {
		t.Fatalf("D1 = %T, want Fired", outcomeOf(b, "D1", "di"))
	}
	if len(fired.Degraded) != 1 || fired.Degraded[0].Kind != KindJiraVersions.Name {
		t.Fatalf("Degraded = %+v, want jira.versions", fired.Degraded)
	}
	if want := f.now.Add(-24 * 60 * 60 * 1e9); !fired.Hits[0].AsOf.Equal(want) {
		t.Fatalf("hit AsOf = %v, want the stale read's %v", fired.Hits[0].AsOf, want)
	}
	if _, ok := alertHit(b, "D1~v/21-2026/di"); !ok {
		t.Fatalf("D1 hit missing from the alert list")
	}
}

func TestANeverCompletedReadIsUnknownEvenWithHits(t *testing.T) {
	f := newFixture(t, today)
	healthy(f)
	f.versions = nil
	f.neverComplete(KindJiraVersions.Name)

	b := f.reconcile()

	if _, ok := outcomeOf(b, "A3", "di").(Unknown); !ok {
		t.Fatalf("A3 with jira.versions never complete = %T, want Unknown, not a hit for every milestone", outcomeOf(b, "A3", "di"))
	}
	for _, h := range b.Alerts.Hits {
		if h.Rule == "A3" {
			t.Fatalf("A3 hit %s listed from a read that never completed", h.ID)
		}
	}
}

func TestAMissingPerAppReadIsUnknown(t *testing.T) {
	f := newFixture(t, today)
	healthy(f)
	inputs := f.inputs()
	var snap Snapshot
	for k, r := range inputs.Snap.res {
		if k.kind != KindTags.Name || r.status.App != "da" {
			if snap.res == nil {
				snap.res = map[resKey]resource{}
			}
			snap.res[k] = r
		}
	}
	inputs.Snap = snap

	b := Reconcile(inputs, f.env())

	if _, ok := outcomeOf(b, "C7", "da").(Unknown); !ok {
		t.Fatalf("C7 for an app with no gitlab.tags resource = %T, want Unknown", outcomeOf(b, "C7", "da"))
	}
	if _, ok := outcomeOf(b, "C7", "di").(Clean); !ok {
		t.Fatalf("C7 for di = %T, want Clean", outcomeOf(b, "C7", "di"))
	}
}

func TestStageOverAStaleReadIsUnknown(t *testing.T) {
	f := newFixture(t, today)
	v := healthy(f)
	f.stale(KindJobs.Name)

	b := f.reconcile()

	stages := b.Trains[0].version(v.app.ID).Stages
	byKey := map[StageKey]Stage{}
	for _, s := range stages {
		byKey[s.Key] = s
	}
	if s := byKey[StageScan]; s.Status != StatusUnknown || len(s.Missing) == 0 {
		t.Fatalf("scan stage = %+v, want unknown with missing", s)
	}
	if s := byKey[StageFreeze]; s.Status != StatusDone {
		t.Fatalf("freeze stage = %+v, want done", s)
	}
}

func TestForceStageWithoutATicketIsNotObservable(t *testing.T) {
	f := newFixture(t, today)
	v := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	b := f.reconcile()
	for _, s := range b.Trains[0].version(v.app.ID).Stages {
		if s.Key == StageForce && (s.Status != StatusNotObservable || s.Date != nil) {
			t.Fatalf("force stage = %+v, want not_observable with no date", s)
		}
	}
}

func TestHitIDIgnoresEvidenceText(t *testing.T) {
	run := func(approvers ...string) AlertHit {
		f := newFixture(t, "2026-10-15")
		healthy(f).approvers(approvers...)
		h, ok := alertHit(f.reconcile(), "D3~v/21-2026/di")
		if !ok {
			t.Fatalf("no D3 hit")
		}
		return h
	}
	a := run("Ann:APPROVED", "Bob:PENDING")
	b := run("Ann:PENDING", "Bob:PENDING", "Cid:PENDING")
	if a.ID != b.ID {
		t.Fatalf("id changed with the text: %q vs %q", a.ID, b.ID)
	}
	if a.Copy == b.Copy || a.Fingerprint() == b.Fingerprint() {
		t.Fatalf("copy and fingerprint should differ: %q / %q", a.Copy.What, b.Copy.What)
	}
}

func TestHideSurvivesEvidenceChangeAndReturnsOnEscalation(t *testing.T) {
	f := newFixture(t, "2026-10-15")
	v := healthy(f)
	v.approvers("Ann:APPROVED", "Bob:PENDING")
	first, _ := alertHit(f.reconcile(), "D3~v/21-2026/di")
	f.human.Hides[first.ID] = Hide{Fingerprint: first.Fingerprint(), Severity: first.Severity}

	same, _ := alertHit(f.reconcile(), first.ID)
	if same.Bucket != BucketHidden || same.ChangedSinceHidden {
		t.Fatalf("unchanged hidden hit = %s changed=%v, want hidden unchanged", same.Bucket, same.ChangedSinceHidden)
	}

	v.approvers("Ann:PENDING", "Bob:PENDING")
	changed, _ := alertHit(f.reconcile(), first.ID)
	if changed.Bucket != BucketHidden || !changed.ChangedSinceHidden {
		t.Fatalf("changed hidden hit = %s changed=%v, want hidden and changed", changed.Bucket, changed.ChangedSinceHidden)
	}
	if b := f.reconcile(); b.Alerts.Badge.Count != 0 {
		t.Fatalf("badge = %+v, want the hidden hit out of the count", b.Alerts.Badge)
	}

	f.human.Hides[first.ID] = Hide{Fingerprint: first.Fingerprint(), Severity: SevHygiene}
	escalated, _ := alertHit(f.reconcile(), first.ID)
	if escalated.Bucket != BucketBadge {
		t.Fatalf("hit hidden at hygiene now at risk = %s, want back on the badge", escalated.Bucket)
	}

	f.human.Hides[first.ID] = Hide{Fingerprint: first.Fingerprint(), Severity: SevBlock}
	lower, _ := alertHit(f.reconcile(), first.ID)
	if lower.Bucket != BucketHidden {
		t.Fatalf("hit hidden at block now at risk = %s, want still hidden", lower.Bucket)
	}
}

func staleIDs(b Board) []HitID {
	out := []HitID{}
	for _, s := range b.Alerts.StaleHides {
		out = append(out, s.ID)
	}
	return out
}

func TestAHideWhoseHitIsGoneIsListedStale(t *testing.T) {
	f := newFixture(t, "2026-10-15")
	v := healthy(f)
	v.approvers("Ann:APPROVED", "Bob:PENDING")
	hit, _ := alertHit(f.reconcile(), "D3~v/21-2026/di")
	f.human.Hides[hit.ID] = Hide{Fingerprint: hit.Fingerprint(), Severity: hit.Severity, Reason: "waiting on Bob"}

	if got := staleIDs(f.reconcile()); len(got) != 0 {
		t.Fatalf("stale hides while the hit matches = %v, want none", got)
	}

	v.approvers("Ann:APPROVED", "Bob:APPROVED")
	b := f.reconcile()
	if len(b.Alerts.StaleHides) != 1 || b.Alerts.StaleHides[0].ID != hit.ID || b.Alerts.StaleHides[0].Reason != "waiting on Bob" {
		t.Fatalf("stale hides after Bob approved = %+v, want the D3 hide with its reason", b.Alerts.StaleHides)
	}
}

func TestAHideIsNotStaleWhileItsRuleCannotSee(t *testing.T) {
	for _, tc := range []struct {
		name  string
		blind func(f *fixture)
	}{
		{"stale read", func(f *fixture) { f.stale(KindJiraVersions.Name) }},
		{"never completed", func(f *fixture) { f.neverComplete(KindJiraVersions.Name) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "2026-10-15")
			healthy(f)
			f.human.Hides["D3~v/21-2026/di"] = Hide{Severity: SevRisk}
			tc.blind(f)
			if got := staleIDs(f.reconcile()); len(got) != 0 {
				t.Fatalf("stale hides over a %s = %v, want none: the rule could not see", tc.name, got)
			}
		})
	}
}

func TestAHideOfAnOffRulesHitIsNotStale(t *testing.T) {
	f := newFixture(t, today)
	d := 71 * 60
	healthy(f).detail().Pipeline.Duration = &d
	f.human.Hides["C3~v/21-2026/di"] = Hide{Severity: SevInfo}
	if got := staleIDs(f.reconcile()); len(got) != 0 {
		t.Fatalf("stale hides for an off rule that still fires = %v, want none", got)
	}
	f.human.Hides["Z9~v/21-2026/di"] = Hide{Severity: SevInfo}
	if got := staleIDs(f.reconcile()); len(got) != 1 || got[0] != "Z9~v/21-2026/di" {
		t.Fatalf("stale hides with a hide of a rule that no longer exists = %v, want it listed", got)
	}
}

func TestCatalogueListsEveryRuleWithItsEffectiveSetting(t *testing.T) {
	f := newFixture(t, today)
	healthy(f)
	f.human.RuleSettings["C3"] = true
	f.human.RuleSettings["A7"] = false
	b := f.reconcile()

	if len(b.Catalogue) != len(ruleTable()) {
		t.Fatalf("catalogue has %d rules, the table %d", len(b.Catalogue), len(ruleTable()))
	}
	byID := map[RuleID]RuleInfo{}
	for _, r := range b.Catalogue {
		byID[r.ID] = r
	}
	for _, tc := range []struct {
		id            RuleID
		defaultOn, on bool
	}{
		{"C3", false, true},
		{"A7", true, false},
		{"C2", false, false},
		{"C1", true, true},
	} {
		got := byID[tc.id]
		if got.DefaultOn != tc.defaultOn || got.On != tc.on {
			t.Fatalf("%s defaultOn=%v on=%v, want %v %v", tc.id, got.DefaultOn, got.On, tc.defaultOn, tc.on)
		}
	}
	if a7 := byID["A7"]; a7.Group != "A" || a7.Severity != SevData || a7.Title == "" {
		t.Fatalf("A7 catalogue row = %+v", a7)
	}
	if d3 := byID["D3"]; d3.Hint == "" {
		t.Fatalf("D3 catalogue row lost its hint: %+v", d3)
	}
}

func TestHitsAgeToHistoryAfterAgeOutDays(t *testing.T) {
	for _, tc := range []struct {
		today string
		want  Bucket
	}{
		{"2026-10-15", BucketBadge},
		{"2026-10-16", BucketBadge},
		{"2026-10-17", BucketHistory},
	} {
		t.Run(tc.today, func(t *testing.T) {
			f := newFixture(t, tc.today)
			v := f.published("di", "20", "3.4.0", "2026-10-01", "2026-10-09")
			v.jira.Released = false
			h, ok := alertHit(f.reconcile(), "E1~v/20-2026/di")
			if !ok || h.Bucket != tc.want {
				t.Fatalf("E1 on %s = %v (found %v), want %s", tc.today, h.Bucket, ok, tc.want)
			}
		})
	}
}

func TestOffRulesRunButNeverReachTheBadge(t *testing.T) {
	f := newFixture(t, today)
	d := 71 * 60
	healthy(f).detail().Pipeline.Duration = &d

	b := f.reconcile()

	o := outcomeOf(b, "C3", "di")
	if _, ok := o.(Fired); !ok {
		t.Fatalf("off rule C3 outcome = %T, want Fired so the Rules sheet can show it", o)
	}
	if _, ok := alertHit(b, "C3~v/21-2026/di"); ok {
		t.Fatalf("off rule C3 reached the alert list")
	}

	f.human.RuleSettings["C3"] = true
	if h, ok := alertHit(f.reconcile(), "C3~v/21-2026/di"); !ok || h.Bucket != BucketBadge {
		t.Fatalf("C3 turned on = %+v (found %v), want on the badge", h.Bucket, ok)
	}

}

func TestUnknownCellsOfOffRulesStayOutOfTheBadge(t *testing.T) {
	f := newFixture(t, today)
	healthy(f)
	f.stale(KindMRDetail.Name)
	if _, ok := outcomeOf(f.reconcile(), "C3", "di").(Unknown); !ok {
		t.Fatalf("C3 over a stale mr_detail is not Unknown")
	}
	off := f.reconcile().Alerts.Badge.Unknown
	f.human.RuleSettings["C3"] = true
	if on := f.reconcile().Alerts.Badge.Unknown; on != off+1 {
		t.Fatalf("badge unknown with C3 on = %d, off = %d; want exactly one more", on, off)
	}
}

func TestBadgeCountsActiveHitsAndWorstSeverity(t *testing.T) {
	f := newFixture(t, today)
	v := healthy(f)
	v.detail().Conflicts = true
	v.issue("DEMO-9", "In Progress", "indeterminate")

	b := f.reconcile()

	if b.Alerts.Badge.Count != 2 || b.Alerts.Badge.Worst == nil || *b.Alerts.Badge.Worst != SevBlock {
		t.Fatalf("badge = %+v, want 2 hits, worst block", b.Alerts.Badge)
	}
	if b.Alerts.Hits[0].Rule != "C5" {
		t.Fatalf("first hit = %s, want the block C5 first", b.Alerts.Hits[0].Rule)
	}
}

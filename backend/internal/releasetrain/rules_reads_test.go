package releasetrain

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
)

func everyRuleFires(t *testing.T) *fixture {
	f := newFixture(t, "2026-10-15")

	live := f.version("di", "21", "3.5.0", liveFreeze, liveDue)
	live.approvers("Ann:APPROVED", "Bob:PENDING")
	live.jira.ReleaseDate = dayPtr(t, "2026-10-17")
	live.relatedWork(CategoryScanPage)
	live.releaseMR(liveFreeze, MROpened)
	live.detail().Conflicts = true
	slow := 71 * 60
	live.detail().Pipeline.Duration = &slow
	live.detail().Pipeline.Status = "failed"
	jobs := live.jobList()
	jobs.Jobs = []JobFact{
		{Name: "build_test", Status: "failed"},
		{Name: "lint_report", Status: "failed", AllowFailure: true},
		{Name: "semgrep-sast", Status: "skipped"},
	}
	live.issue("DEMO-1", "In Progress", "indeterminate")
	live.forceUpdate("REQ-1", "To Do", "new", "")
	f.page("demo", "21", "2026-09-20", "2026-10-08", liveDue)

	noApprovers := f.version("da", "21", "3.5.1", liveFreeze, liveDue)
	noApprovers.jira.Name = "Android 3.5.2"

	f.version("pa", "21", "1.2.1", "2026-10-10", liveDue)

	next := f.version("pi", "22", "1.3.0", "2026-10-15", "2026-10-23")
	next.approvers()
	next.jira.ID = "7100"

	shipped := f.published("pa", "20", "1.2.0", "2026-10-01", "2026-10-09")
	shipped.jira.Released = false
	shipped.issue("DEMO-2", "Ready for UAT", "indeterminate")
	shipped.forceUpdate("REQ-2", "In Progress", "indeterminate", "2026-10-12")
	shipped.compare().Commits = []CommitFact{{Title: "Merge branch 'fix/crash' into 'main'", CreatedOn: day(t, "2026-10-10")}}

	f.milestone("pi", "Release 1.2.9", f.label("pi", "21"), liveFreeze, liveDue)
	f.jiraVersion("da", "3.6.0", f.label("da", "23"), "2026-11-06")
	f.jiraVersion("di", "3.4.9", "", "2026-10-03")
	f.milestone("di", "Hotfix 3.4.1", "crash", "2026-10-10", "2026-10-13")
	f.jiraVersion("di", "3.4.1", "", "2026-10-13")
	return f
}

type hitSig struct {
	id          HitID
	fingerprint string
	severity    Severity
}

func ruleSigs(b Board) map[RuleID][]hitSig {
	out := map[RuleID][]hitSig{}
	for _, o := range b.Alerts.Rules {
		if fired, ok := o.Outcome.(Fired); ok {
			for _, h := range fired.Hits {
				out[o.Rule] = append(out[o.Rule], hitSig{h.ID, h.Fingerprint(), h.Severity})
			}
		}
	}
	for _, sigs := range out {
		sort.Slice(sigs, func(i, j int) bool { return sigs[i].id < sigs[j].id })
	}
	return out
}

type stageSig struct {
	status StageStatus
	date   string
	detail string
}

func stageSigs(b Board) map[string]stageSig {
	out := map[string]stageSig{}
	for _, t := range b.Trains {
		for _, v := range t.Versions {
			for _, s := range v.Stages {
				date := ""
				if s.Date != nil {
					date = s.Date.String()
				}
				out[fmt.Sprintf("%s/%s/%s", t.ID.Key(), v.App, s.Key)] = stageSig{s.Status, date, s.Detail}
			}
		}
	}
	return out
}

func allKinds() []KindName {
	out := make([]KindName, 0, len(kinds))
	for k := range kinds {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func reads(list []KindName, k KindName) bool {
	for _, r := range list {
		if r == k {
			return true
		}
	}
	return false
}

func TestRuleReadsAreHonest(t *testing.T) {
	f := everyRuleFires(t)
	in := f.inputs()
	for _, r := range ruleTable() {
		in.Human.RuleSettings[r.ID] = true
	}
	base := Reconcile(in, f.env())
	baseRules, baseStages := ruleSigs(base), stageSigs(base)
	for _, r := range ruleTable() {
		if len(baseRules[r.ID]) == 0 {
			t.Fatalf("fixture does not fire %s; every rule must fire at least once", r.ID)
		}
	}

	for _, k := range allKinds() {
		perturbed := in
		perturbed.Snap = in.Snap.withoutItems(k)
		b := Reconcile(perturbed, f.env())
		rules, stages := ruleSigs(b), stageSigs(b)
		for _, r := range ruleTable() {
			if !reads(r.Reads, k) && !reflect.DeepEqual(rules[r.ID], baseRules[r.ID]) {
				t.Errorf("rule %s reads %s: add it to Rule.Reads", r.ID, k)
			}
		}
		for _, row := range stageTable {
			if reads(row.reads, k) {
				continue
			}
			for key, sig := range baseStages {
				if stageKeyOf(key) == row.key && stages[key] != sig {
					t.Errorf("stage %s reads %s: add it to the stage row's reads", row.key, k)
					break
				}
			}
		}
	}
}

func stageKeyOf(key string) StageKey {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '/' {
			return StageKey(key[i+1:])
		}
	}
	return ""
}

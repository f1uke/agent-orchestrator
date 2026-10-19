package releasetrain

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

func TestEveryRuleDeclaresTheSubjectKindsItsHitsCarry(t *testing.T) {
	f := everyRuleFires(t)
	in := f.inputs()
	for _, r := range ruleTable() {
		in.Human.RuleSettings[r.ID] = true
	}
	b := Reconcile(in, f.env())
	emitted := map[RuleID]map[SubjectKind]bool{}
	for _, o := range b.Alerts.Rules {
		fired, ok := o.Outcome.(Fired)
		if !ok {
			continue
		}
		for _, h := range fired.Hits {
			if emitted[o.Rule] == nil {
				emitted[o.Rule] = map[SubjectKind]bool{}
			}
			emitted[o.Rule][KindOf(h.Subject)] = true
		}
	}
	for _, r := range ruleTable() {
		if len(r.Subjects) == 0 {
			t.Errorf("rule %s declares no subject kind: add it to rule.Subjects", r.ID)
			continue
		}
		declared := map[SubjectKind]bool{}
		for _, k := range r.Subjects {
			declared[k] = true
			if !emitted[r.ID][k] {
				t.Errorf("rule %s declares subject %s, but no hit on the fixture carries it", r.ID, k)
			}
		}
		for k := range emitted[r.ID] {
			if !declared[k] {
				t.Errorf("rule %s emits a %s subject it does not declare: add it to rule.Subjects", r.ID, k)
			}
		}
	}
}

func TestCatalogueServesEachRulesSubjectKinds(t *testing.T) {
	b := newFixture(t, today).reconcile()
	for i, r := range ruleTable() {
		if got := b.Catalogue[i].Subjects; !reflect.DeepEqual(got, r.Subjects) {
			t.Errorf("rule %s catalogue subjects = %v, want %v", r.ID, got, r.Subjects)
		}
	}
}

func TestEverySubjectKindIsListed(t *testing.T) {
	f := everyRuleFires(t)
	in := f.inputs()
	for _, r := range ruleTable() {
		in.Human.RuleSettings[r.ID] = true
	}
	seen := map[SubjectKind]bool{}
	for _, h := range Reconcile(in, f.env()).Alerts.Hits {
		seen[KindOf(h.Subject)] = true
	}
	var got []string
	for k := range seen {
		got = append(got, string(k))
	}
	var want []string
	for _, k := range SubjectKinds {
		want = append(want, string(k))
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hits on the fixture carry subject kinds %v; SubjectKinds lists %v", got, want)
	}
}

func TestAHitWritesItsSubjectWithItsKind(t *testing.T) {
	train := TrainID{Number: 21, Year: 2026}
	for _, tc := range []struct {
		subject Subject
		want    string
	}{
		{SubjVersion{Train: train, App: "di"}, `{"kind":"version","train":"21-2026","app":"di"}`},
		{SubjVersionDate{Train: train, App: "di", Field: DateFreeze}, `{"kind":"versionDate","train":"21-2026","app":"di","field":"freeze"}`},
		{SubjJiraVersion{ID: "7001", App: "da", Name: "Android 3.6.0"}, `{"kind":"jiraVersion","id":"7001","app":"da","name":"Android 3.6.0"}`},
		{SubjMilestone{App: "pi", ID: 9, Title: "Release 1.2.0"}, `{"kind":"milestone","app":"pi","id":9,"title":"Release 1.2.0"}`},
		{SubjHotfix{App: "di", Version: "3.4.1"}, `{"kind":"hotfix","app":"di","version":"3.4.1"}`},
		{SubjTrainPair{A: train, B: TrainID{Number: 22, Year: 2026}}, `{"kind":"trainPair","a":"21-2026","b":"22-2026"}`},
		{SubjHotfixTrain{App: "di", Version: "3.4.1", Train: train}, `{"kind":"hotfixTrain","app":"di","version":"3.4.1","train":"21-2026"}`},
		{SubjCardOnTrain{Train: train, App: "di", Key: "DEMO-3"}, `{"kind":"cardOnTrain","train":"21-2026","app":"di","key":"DEMO-3"}`},
		{SubjMR{App: "di", IID: 7, Source: "feat/DEMO-1", Target: "develop"}, `{"kind":"mr","app":"di","iid":7,"source":"feat/DEMO-1","target":"develop"}`},
		{SubjCard{Key: "DEMO-1", Board: "DEMO"}, `{"kind":"card","key":"DEMO-1","board":"DEMO"}`},
		{SubjPageFamily{Train: train, Family: "demo", Bucket: "merged", Keys: []string{"DEMO-1"}}, `{"kind":"pageFamily","train":"21-2026","family":"demo","bucket":"merged","keys":["DEMO-1"]}`},
	} {
		body, err := json.Marshal(Hit{ID: "X~y", Subject: tc.subject, Evidence: Evidence{}, Links: []Link{}})
		if err != nil {
			t.Fatal(err)
		}
		var probe struct {
			Subject json.RawMessage `json:"subject"`
		}
		if err := json.Unmarshal(body, &probe); err != nil {
			t.Fatal(err)
		}
		if string(probe.Subject) != tc.want {
			t.Errorf("%T writes subject %s, want %s", tc.subject, probe.Subject, tc.want)
		}
	}
}

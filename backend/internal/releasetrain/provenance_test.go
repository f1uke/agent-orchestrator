package releasetrain

import (
	"reflect"
	"strings"
	"testing"
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

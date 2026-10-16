package releasetrain

import (
	"reflect"
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

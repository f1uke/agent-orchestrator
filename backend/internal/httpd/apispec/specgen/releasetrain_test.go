package specgen

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

func TestReleaseTrainUnionTwinsMatchWhatTheCoreWrites(t *testing.T) {
	at := time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC)
	degraded := []rt.ResourceStatus{{Kind: "jira.versions", Scope: "project", CompleteAt: &at, ErrorClass: rt.ErrUnavailable}}
	for _, tc := range []struct {
		variant rt.Variant
		twin    any
	}{
		{rt.Clean{CheckedAt: at}, rtOutcomeClean{}},
		{rt.Fired{Hits: []rt.Hit{{ID: "C1~v/21-2026/di"}}, AsOf: at, Degraded: degraded}, rtOutcomeFired{}},
		{rt.Unknown{Missing: degraded}, rtOutcomeUnknown{}},
		{rt.CodeMerged{MRs: []int{7}}, rtCodeMerged{}},
		{rt.CodeOpen{MRs: []int{8}}, rtCodeOpen{}},
		{rt.CodeElsewhere{Branch: "feature/demo", On: rt.Day{Y: 2026, M: 10, D: 1}}, rtCodeElsewhere{}},
		{rt.CodeNone{}, rtCodeNone{}},
	} {
		body, err := json.Marshal(tc.variant)
		if err != nil {
			t.Fatal(err)
		}
		twin := reflect.New(reflect.TypeOf(tc.twin))
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(twin.Interface()); err != nil {
			t.Fatalf("%T writes %s, which its spec twin %T cannot read: %v", tc.variant, body, tc.twin, err)
		}
		again, err := json.Marshal(twin.Interface())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(again, body) {
			t.Fatalf("%T writes %s; its spec twin %T writes %s", tc.variant, body, tc.twin, again)
		}
		field, _ := reflect.TypeOf(tc.twin).FieldByName("Kind")
		if got := field.Tag.Get("const"); got != tc.variant.Variant() {
			t.Fatalf("%T declares kind %q, the core writes %q", tc.twin, got, tc.variant.Variant())
		}
	}
}

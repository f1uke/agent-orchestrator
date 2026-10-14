package releasetrain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// Variant names a sealed-union member on the wire: its "kind".
type Variant interface{ Variant() string }

// Variant is "clean".
func (Clean) Variant() string { return "clean" }

// Variant is "fired".
func (Fired) Variant() string { return "fired" }

// Variant is "unknown".
func (Unknown) Variant() string { return "unknown" }

// Variant is "merged".
func (CodeMerged) Variant() string { return "merged" }

// Variant is "open".
func (CodeOpen) Variant() string { return "open" }

// Variant is "elsewhere".
func (CodeElsewhere) Variant() string { return "elsewhere" }

// Variant is "none".
func (CodeNone) Variant() string { return "none" }

// MarshalJSON writes the outcome with "kind": "clean".
func (c Clean) MarshalJSON() ([]byte, error) { type plain Clean; return tagged(c, plain(c)) }

// MarshalJSON writes the outcome with "kind": "fired" and its hits as ids;
// the alert list carries each enabled rule's hits in full.
func (f Fired) MarshalJSON() ([]byte, error) {
	ids := make([]HitID, len(f.Hits))
	for i, h := range f.Hits {
		ids[i] = h.ID
	}
	return tagged(f, struct {
		Hits     []HitID          `json:"hits"`
		AsOf     time.Time        `json:"asOf"`
		Degraded []ResourceStatus `json:"degraded,omitempty"`
	}{ids, f.AsOf, f.Degraded})
}

// MarshalJSON writes the outcome with "kind": "unknown".
func (u Unknown) MarshalJSON() ([]byte, error) { type plain Unknown; return tagged(u, plain(u)) }

// MarshalJSON writes the code state with "kind": "merged".
func (c CodeMerged) MarshalJSON() ([]byte, error) { type plain CodeMerged; return tagged(c, plain(c)) }

// MarshalJSON writes the code state with "kind": "open".
func (c CodeOpen) MarshalJSON() ([]byte, error) { type plain CodeOpen; return tagged(c, plain(c)) }

// MarshalJSON writes the code state with "kind": "elsewhere".
func (c CodeElsewhere) MarshalJSON() ([]byte, error) {
	type plain CodeElsewhere
	return tagged(c, plain(c))
}

// MarshalJSON writes the code state with "kind": "none".
func (c CodeNone) MarshalJSON() ([]byte, error) { type plain CodeNone; return tagged(c, plain(c)) }

func tagged(v Variant, fields any) ([]byte, error) {
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	kind := fmt.Sprintf(`{"kind":%q`, v.Variant())
	if bytes.Equal(body, []byte("{}")) {
		return []byte(kind + "}"), nil
	}
	return append([]byte(kind+","), body[1:]...), nil
}

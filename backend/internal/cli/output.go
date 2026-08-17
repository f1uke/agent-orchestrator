package cli

import (
	"encoding/json"
	"io"
)

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// writeJSONLine is one compact object on its own line, for output that is read
// while it is still being produced (`ao sim log --follow --json`).
func writeJSONLine(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}

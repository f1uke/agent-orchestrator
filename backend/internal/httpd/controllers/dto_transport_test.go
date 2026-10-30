package controllers

import (
	"encoding/json"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSessionViewCarriesTheTransport(t *testing.T) {
	for _, tc := range []struct {
		stored domain.Transport
		want   string
	}{
		{domain.TransportChat, "chat"},
		{domain.TransportTerminal, "terminal"},
		{"", "terminal"},
	} {
		s := domain.Session{}
		s.Transport = tc.stored
		b, err := json.Marshal(sessionView(s))
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]any
		if err := json.Unmarshal(b, &wire); err != nil {
			t.Fatal(err)
		}
		if got := wire["transport"]; got != tc.want {
			t.Errorf("stored transport %q is on the wire as %v, want %q", tc.stored, got, tc.want)
		}
	}
}

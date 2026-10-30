package domain

import "fmt"

// Transport is how a session's agent is driven. It is chosen at spawn and never
// changes.
type Transport string

const (
	// TransportTerminal runs the agent's own TUI in the session's pane, and
	// messages are typed into it. It is the default.
	TransportTerminal Transport = "terminal"
	// TransportChat runs `ao session-host` in the pane, which owns a headless
	// claude, and messages go into the host's socket. claude-code only.
	TransportChat Transport = "chat"
	// TransportUnresolved means no level named a transport: a spawn or setting
	// that left it open, or a queued TODO that resolves it when it starts.
	TransportUnresolved Transport = ""
)

// DefaultWorkerTransport is the transport of a new claude-code worker that nothing configured.
const DefaultWorkerTransport = TransportChat

// ParseTransport validates a transport named at a boundary; empty stays empty.
func ParseTransport(s string) (Transport, error) {
	switch t := Transport(s); t {
	case TransportUnresolved, TransportTerminal, TransportChat:
		return t, nil
	}
	return TransportUnresolved, fmt.Errorf("transport must be %s or %s, not %q", TransportTerminal, TransportChat, s)
}

// WithDefault returns t, or terminal when t is unresolved (a row older than transports).
func (t Transport) WithDefault() Transport {
	if t == TransportUnresolved {
		return TransportTerminal
	}
	return t
}

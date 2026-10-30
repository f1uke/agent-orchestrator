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
)

// ParseTransport reads a transport named at a boundary. Empty means the
// default, terminal.
func ParseTransport(s string) (Transport, error) {
	switch t := Transport(s); t {
	case "":
		return TransportTerminal, nil
	case TransportTerminal, TransportChat:
		return t, nil
	}
	return "", fmt.Errorf("transport must be %s or %s, not %q", TransportTerminal, TransportChat, s)
}

// WithDefault returns t, or terminal when t is unset.
func (t Transport) WithDefault() Transport {
	if t == "" {
		return TransportTerminal
	}
	return t
}

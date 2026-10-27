package releaseweb

import (
	"fmt"
	"net"
	"strings"
)

// tailnetSuffix ends every name Tailscale Funnel serves.
const tailnetSuffix = ".ts.net"

// hostAllowlist is the Host headers the release web answers. A page on any
// other name that resolves to 127.0.0.1 (DNS rebinding) sends its own name, so
// the server refuses it before any auth logic runs.
type hostAllowlist struct {
	// port is the listening port, which a loopback Host must name.
	port  string
	extra map[string]bool
}

func newHostAllowlist(addr string, extra []string) (hostAllowlist, error) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return hostAllowlist{}, fmt.Errorf("release web: address %q: %w", addr, err)
	}
	l := hostAllowlist{port: port, extra: map[string]bool{}}
	for _, h := range extra {
		name, err := NormalizeHost(h)
		if err != nil {
			return hostAllowlist{}, err
		}
		l.extra[name] = true
	}
	return l, nil
}

// NormalizeHost checks a host name an operator allows, such as a custom domain
// in front of the web, and returns it lower-cased without a trailing dot.
func NormalizeHost(h string) (string, error) {
	name := canonicalName(h)
	if !validName(name) {
		return "", fmt.Errorf("release web: %q is not a host name; give the name alone, like board.example.com", h)
	}
	return name, nil
}

func (l hostAllowlist) allows(host string) bool {
	name, port, err := net.SplitHostPort(host)
	if err != nil {
		name, port = host, ""
	}
	name = canonicalName(name)
	switch name {
	case "127.0.0.1", "localhost", "::1":
		return port == l.port
	}
	if strings.HasSuffix(name, tailnetSuffix) && validName(name) {
		return true
	}
	return l.extra[name]
}

func canonicalName(h string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
}

// validName reports whether name is dot-separated non-empty labels of letters,
// digits and hyphens.
func validName(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || strings.ContainsFunc(label, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-'
		}) {
			return false
		}
	}
	return true
}

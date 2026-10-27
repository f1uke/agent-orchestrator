package releaseweb

import (
	"fmt"
	"net"
	"strings"
)

const tailnetSuffix = ".ts.net"

// hostAllowlist refuses DNS-rebinding names: a page on another name that resolves to
// 127.0.0.1 sends its own Host.
type hostAllowlist struct {
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

// NormalizeHost validates an allowed host name and canonicalizes it.
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

package releaseweb

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/fsatomic"
)

// hostAllowlist refuses DNS-rebinding names: a page on another name that resolves to
// 127.0.0.1 sends its own Host.
type hostAllowlist struct {
	port  string
	names []string
}

func newHostAllowlist(addr string, extra []string) (hostAllowlist, error) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return hostAllowlist{}, fmt.Errorf("release web: address %q: %w", addr, err)
	}
	names, err := normalizeHosts(extra)
	if err != nil {
		return hostAllowlist{}, err
	}
	return hostAllowlist{port: port, names: names}, nil
}

func normalizeHosts(hosts []string) ([]string, error) {
	var names []string
	for _, h := range hosts {
		name, err := NormalizeHost(h)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names, nil
}

// ReadHosts returns the names in the hosts file, or none when there is no file.
func ReadHosts(p Paths) ([]string, error) {
	b, err := os.ReadFile(p.HostsFile())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("release web: read %s: %w", p.HostsFile(), err)
	}
	var lines []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	names, err := normalizeHosts(lines)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.HostsFile(), err)
	}
	return names, nil
}

// WriteHosts replaces the names in the hosts file.
func WriteHosts(p Paths, hosts []string) error {
	names, err := normalizeHosts(hosts)
	if err != nil {
		return err
	}
	if err := p.Ensure(); err != nil {
		return err
	}
	if err := fsatomic.WriteFile(p.HostsFile(), []byte(strings.Join(names, "\n")+"\n"), 0o600); err != nil {
		return fmt.Errorf("release web: write %s: %w", p.HostsFile(), err)
	}
	return nil
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
	return slices.Contains(l.names, name)
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

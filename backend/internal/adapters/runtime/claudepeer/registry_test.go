package claudepeer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// registryFixture builds a session-registry directory plus a live socket, so a
// test can vary one field at a time and see which check rejects it.
type registryFixture struct {
	dir        string
	socketPath string
}

func newRegistryFixture(t *testing.T) registryFixture {
	t.Helper()
	dir := t.TempDir()
	sockDir, err := os.MkdirTemp("", "ao-cp")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })

	socketPath := filepath.Join(sockDir, "s.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return registryFixture{dir: dir, socketPath: socketPath}
}

func (f registryFixture) writeDescriptor(t *testing.T, pid int, mutate func(m map[string]any)) {
	t.Helper()
	desc := map[string]any{
		"pid":                 pid,
		"sessionId":           "conv-" + strconv.Itoa(pid),
		"name":                "worktree-name-" + strconv.Itoa(pid),
		"peerProtocol":        supportedPeerProtocol,
		"kind":                "interactive",
		"tmux":                "ao-worker-7:@12.%12",
		"messagingSocketPath": f.socketPath,
	}
	if mutate != nil {
		mutate(desc)
	}
	raw, err := json.Marshal(desc)
	if err != nil {
		t.Fatalf("marshal descriptor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, strconv.Itoa(pid)+".json"), raw, 0o600); err != nil {
		t.Fatalf("write descriptor: %v", err)
	}
}

func (f registryFixture) writeKey(t *testing.T, pid int, token string) {
	t.Helper()
	sum := sha256.Sum256([]byte(filepath.Clean(f.socketPath)))
	name := strconv.Itoa(pid) + "." + hex.EncodeToString(sum[:]) + ".key"
	raw, err := json.Marshal(map[string]string{"peerToken": token})
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, name), raw, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
}

func (f registryFixture) registry(argv []string, alive bool) *FileRegistry {
	return NewFileRegistry(FileRegistryOptions{
		Dir:      f.dir,
		PIDAlive: func(int) bool { return alive },
		ProcArgv: func(context.Context, int) ([]string, error) {
			return argv, nil
		},
	})
}

var claudeArgv = []string{"/Users/x/.local/bin/claude", "--session-id", "abc"}

func TestLookupAcceptsALiveClaudeSession(t *testing.T) {
	f := newRegistryFixture(t)
	f.writeDescriptor(t, 4242, nil)
	f.writeKey(t, 4242, "0123456789abcdef0123456789abcdef")

	session, err := f.registry(claudeArgv, true).Lookup(context.Background(), "ao-worker-7")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if session.PID != 4242 || session.SocketPath != f.socketPath {
		t.Fatalf("unexpected session %+v", session)
	}
	if session.PeerToken != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("peer token not read from the key file")
	}
}

func TestLookupRejections(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(m map[string]any)
		argv    []string
		alive   bool
		target  string
		reason  string
		noWrite bool
	}{
		{
			// The other 22 harnesses: nothing registers a descriptor for their
			// pane, so their sends keep going through send-keys unchanged.
			name:    "harness that is not claude-code",
			target:  "ao-codex-worker",
			alive:   true,
			argv:    claudeArgv,
			reason:  "no-descriptor",
			noWrite: true,
		},
		{
			name:   "future wire version",
			mutate: func(m map[string]any) { m["peerProtocol"] = 2 },
			argv:   claudeArgv, alive: true, target: "ao-worker-7",
			reason: "unsupported-peer-protocol",
		},
		{
			name:   "wire version absent",
			mutate: func(m map[string]any) { delete(m, "peerProtocol") },
			argv:   claudeArgv, alive: true, target: "ao-worker-7",
			reason: "unsupported-peer-protocol",
		},
		{
			name:   "background session, not the pane in front of the human",
			mutate: func(m map[string]any) { m["kind"] = "bg" },
			argv:   claudeArgv, alive: true, target: "ao-worker-7",
			reason: "non-interactive-session",
		},
		{
			name:   "no messaging socket published",
			mutate: func(m map[string]any) { m["messagingSocketPath"] = "" },
			argv:   claudeArgv, alive: true, target: "ao-worker-7",
			reason: "incomplete-descriptor",
		},
		{
			name: "process is gone", argv: claudeArgv, alive: false, target: "ao-worker-7",
			reason: "dead-pid",
		},
		{
			name: "pid was recycled by something else",
			argv: []string{"/usr/bin/vim", "notes.md"}, alive: true, target: "ao-worker-7",
			reason: "recycled-pid",
		},
		{
			name:  "session launched with --dangerously-skip-permissions",
			argv:  append([]string{"/Users/x/.local/bin/claude", "--dangerously-skip-permissions"}, "-p"),
			alive: true, target: "ao-worker-7",
			reason: "bypass-permissions-session",
		},
		{
			name:  "session launched with --permission-mode bypassPermissions",
			argv:  []string{"/Users/x/.local/bin/claude", "--permission-mode", "bypassPermissions"},
			alive: true, target: "ao-worker-7",
			reason: "bypass-permissions-session",
		},
		{
			name:   "socket file is gone",
			mutate: func(m map[string]any) { m["messagingSocketPath"] = filepath.Join(t.TempDir(), "missing.sock") },
			argv:   claudeArgv, alive: true, target: "ao-worker-7",
			reason: "socket-missing",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newRegistryFixture(t)
			if !tc.noWrite {
				f.writeDescriptor(t, 4242, tc.mutate)
			}
			_, err := f.registry(tc.argv, tc.alive).Lookup(context.Background(), tc.target)
			if err == nil {
				t.Fatalf("want a rejection with reason %q, got a session", tc.reason)
			}
			if got := lookupReason(err); got != tc.reason {
				t.Fatalf("rejection reason %q, want %q", got, tc.reason)
			}
		})
	}
}

func TestLookupRefusesTwoDescriptorsForOnePane(t *testing.T) {
	f := newRegistryFixture(t)
	f.writeDescriptor(t, 4242, nil)
	f.writeDescriptor(t, 4243, nil)

	_, err := f.registry(claudeArgv, true).Lookup(context.Background(), "ao-worker-7")
	if got := lookupReason(err); got != "ambiguous-descriptor" {
		t.Fatalf("rejection reason %q, want ambiguous-descriptor", got)
	}
}

func TestLookupSucceedsWithoutAKeyFile(t *testing.T) {
	f := newRegistryFixture(t)
	f.writeDescriptor(t, 4242, nil)

	session, err := f.registry(claudeArgv, true).Lookup(context.Background(), "ao-worker-7")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if session.PeerToken != "" {
		t.Fatal("want an empty token when no key file is readable")
	}
}

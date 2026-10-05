package tmux

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// tmuxServers plays several tmux servers at once - the shared pre-upgrade one
// and each session's own - with the target resolution tmux 3.6a really does,
// which is what the simpler fakes cannot show:
//
//   - `=<name>` / `=<name>:` match that session exactly, else "can't find
//     session: <name>";
//   - a plain name matches exactly, else by unique PREFIX, else "can't find
//     window: <name>" - text the runtime used to read as a probe error;
//   - a server whose last session is killed exits.
type tmuxServers struct {
	mu      sync.Mutex
	dataDir string
	nextPID int
	servers map[string]map[string]int // socket -> session name -> pane pid
	// failTarget makes every pane-targeting command naming that session on the
	// shared server fail with output the runtime cannot classify.
	failTarget string
	calls      []runnerCall
}

func newTmuxServers(dataDir string) *tmuxServers {
	return &tmuxServers{dataDir: dataDir, nextPID: 100, servers: map[string]map[string]int{}}
}

// add starts session name on the server at sock, as if this instance had
// launched it there, and returns its pane pid.
func (s *tmuxServers) add(sock, name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addLocked(sock, name)
}

func (s *tmuxServers) addLocked(sock, name string) int {
	if s.servers[sock] == nil {
		s.servers[sock] = map[string]int{}
	}
	s.nextPID++
	s.servers[sock][name] = s.nextPID
	return s.nextPID
}

// remove ends session name on sock behind AO's back (the agent exited and its
// shell with it).
func (s *tmuxServers) remove(sock, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeLocked(sock, name)
}

func (s *tmuxServers) removeLocked(sock, name string) {
	delete(s.servers[sock], name)
	if len(s.servers[sock]) == 0 {
		delete(s.servers, sock)
	}
}

func (s *tmuxServers) has(sock, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.servers[sock][name]
	return ok
}

func exitErr(out string) ([]byte, error) { return []byte(out), &exec.ExitError{} }

// resolve finds the session a -t target names on one server.
func (s *tmuxServers) resolve(sessions map[string]int, target string) (string, string) {
	if strings.HasPrefix(target, "=") {
		name := strings.TrimSuffix(strings.TrimPrefix(target, "="), ":")
		if _, ok := sessions[name]; ok {
			return name, ""
		}
		return "", "can't find session: " + name
	}
	name := strings.TrimSuffix(target, ":")
	if _, ok := sessions[name]; ok {
		return name, ""
	}
	var matches []string
	for n := range sessions {
		if strings.HasPrefix(n, name) {
			matches = append(matches, n)
		}
	}
	if len(matches) == 1 {
		return matches[0], ""
	}
	return "", "can't find window: " + name
}

func (s *tmuxServers) Run(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
	sock, cmd := splitSocket(args)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, runnerCall{env: env, name: name, socket: sock, args: cmd})

	if cmd[0] == "new-session" {
		var session string
		for i := 0; i+1 < len(cmd); i++ {
			if cmd[i] == "-s" {
				session = cmd[i+1]
			}
		}
		if _, ok := s.servers[sock][session]; ok {
			return exitErr("duplicate session: " + session)
		}
		s.addLocked(sock, session)
		return nil, nil
	}
	sessions, up := s.servers[sock]
	if !up {
		return exitErr("no server running on " + sock)
	}
	if cmd[0] == "list-panes" && len(cmd) > 1 && cmd[1] == "-a" {
		names := make([]string, 0, len(sessions))
		for n := range sessions {
			names = append(names, n)
		}
		sort.Strings(names)
		var b strings.Builder
		for _, n := range names {
			b.WriteString(paneLine(n, s.dataDir))
		}
		return []byte(b.String()), nil
	}
	target := ""
	for i := 0; i+1 < len(cmd); i++ {
		if cmd[i] == "-t" {
			target = cmd[i+1]
		}
	}
	if sock == legacySock && s.failTarget != "" && strings.Trim(target, "=:") == s.failTarget {
		return exitErr("server exited unexpectedly")
	}
	session, miss := s.resolve(sessions, target)
	if miss != "" {
		return exitErr(miss)
	}
	switch cmd[0] {
	case "kill-session":
		s.removeLocked(sock, session)
	case "list-panes":
		return []byte(fmt.Sprintf("%d\n", sessions[session])), nil
	}
	return nil, nil
}

// newServersRuntime is a runtime with the shared-server fallback on, driven by
// servers, whose agents are alive exactly when their pane pid is in live.
func newServersRuntime(t *testing.T, servers *tmuxServers, live map[int]bool) *Runtime {
	t.Helper()
	r := New(Options{Binary: "tmux-test", Timeout: time.Second, Shell: "/bin/sh", DataDir: legacyDataDir(), SocketDir: t.TempDir(), LegacySocket: legacySock})
	r.runner = servers
	r.sleep = func(time.Duration) {}
	r.hasLiveChild = func(_ context.Context, pid int) (bool, error) { return live[pid], nil }
	return r
}

// The restart that wedged an orchestrator: its session was born on the shared
// server before every session got its own, and restarting it - destroy, then
// create under the same name - must bring it back on its own server. It used to
// fail with "can't find window", because Create went looking on the shared
// server for the session Destroy had just killed, and failed the same way for
// every later spawn, restore or restart under that name until the daemon
// restarted.
func TestRestartingAPreUpgradeSessionBringsItBackOnItsOwnServer(t *testing.T) {
	servers := newTmuxServers(legacyDataDir())
	pid := servers.add(legacySock, "proj-feature-x")
	servers.add(legacySock, "proj-86") // the shared server keeps running for its other sessions
	r := newServersRuntime(t, servers, map[int]bool{pid: true})
	h := ports.RuntimeHandle{ID: "proj-feature-x"}
	cfg := ports.RuntimeConfig{SessionID: "x", ProjectID: "proj", Branch: "feature/x", WorkspacePath: "/tmp/ws", Argv: []string{"agent"}}

	if alive, err := r.IsAlive(context.Background(), h); err != nil || !alive {
		t.Fatalf("IsAlive before restart = %v, %v; want the pre-upgrade session found", alive, err)
	}
	if err := r.Destroy(context.Background(), h); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if servers.has(legacySock, "proj-feature-x") {
		t.Fatal("Destroy left the pre-upgrade session on the shared server")
	}
	for attempt := 1; attempt <= 2; attempt++ {
		got, err := r.Create(context.Background(), cfg)
		if err != nil {
			t.Fatalf("Create after restart (attempt %d): %v", attempt, err)
		}
		if !servers.has(r.ownSocket(got.ID), "proj-feature-x") {
			t.Fatalf("relaunched session is not on its own server %s", r.ownSocket(got.ID))
		}
		if err := r.Destroy(context.Background(), got); err != nil {
			t.Fatalf("Destroy relaunched session: %v", err)
		}
	}
	if !servers.has(legacySock, "proj-86") {
		t.Fatal("the restart touched another session on the shared server")
	}
}

// A liveness probe for a session that is gone must never answer with a
// neighbour's agent. On the shared server a plain `-t proj-feature-x` resolves
// by prefix to `proj-feature-x-2`, so a dead session read as alive and the
// reaper could never settle it.
func TestAProbeForAGoneSessionNeverAnswersWithANeighbour(t *testing.T) {
	servers := newTmuxServers(legacyDataDir())
	servers.add(legacySock, "proj-feature-x")
	neighbour := servers.add(legacySock, "proj-feature-x-2")
	r := newServersRuntime(t, servers, map[int]bool{neighbour: true})
	h := ports.RuntimeHandle{ID: "proj-feature-x"}

	if alive, err := r.IsAlive(context.Background(), h); err != nil || !alive {
		t.Fatalf("IsAlive = %v, %v; want the pre-upgrade session found", alive, err)
	}
	servers.remove(legacySock, "proj-feature-x") // its agent exited, outside AO

	alive, err := r.AgentAlive(context.Background(), h)
	if err != nil || alive {
		t.Fatalf("AgentAlive = %v, %v; want (false, nil) for a session that is gone", alive, err)
	}
}

// A Create that fails while retiring a pre-upgrade session must not leave the
// name unlaunchable: the cached "it lives on the shared server" is forgotten,
// so the next attempt asks again and, finding nothing there, launches on the
// session's own server.
func TestAFailedRetireDoesNotWedgeTheName(t *testing.T) {
	servers := newTmuxServers(legacyDataDir())
	pid := servers.add(legacySock, "proj-feature-x")
	servers.add(legacySock, "proj-86")
	r := newServersRuntime(t, servers, map[int]bool{pid: true})
	h := ports.RuntimeHandle{ID: "proj-feature-x"}
	cfg := ports.RuntimeConfig{SessionID: "x", ProjectID: "proj", Branch: "feature/x", WorkspacePath: "/tmp/ws", Argv: []string{"agent"}}

	if alive, err := r.IsAlive(context.Background(), h); err != nil || !alive {
		t.Fatalf("IsAlive = %v, %v; want the pre-upgrade session found", alive, err)
	}
	// The session ends behind AO's back, and the shared server answers the
	// first probe of it with something nobody can classify.
	servers.remove(legacySock, "proj-feature-x")
	servers.failTarget = "proj-feature-x"
	if _, err := r.Create(context.Background(), cfg); err == nil {
		t.Fatal("Create = nil error, want the unclassifiable probe to fail this attempt")
	}
	got, err := r.Create(context.Background(), cfg)
	if err != nil {
		t.Fatalf("second Create: %v; want the stale cache forgotten and the session launched", err)
	}
	if !servers.has(r.ownSocket(got.ID), "proj-feature-x") {
		t.Fatal("second Create did not launch on the session's own server")
	}
}

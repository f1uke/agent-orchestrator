package tmux

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// -- socket naming --

func TestSocketDirLivesUnderTheDataDir(t *testing.T) {
	if got, want := SocketDir("/Users/me/.ao/data"), "/Users/me/.ao/data/tmux"; got != want {
		t.Fatalf("SocketDir = %q, want %q", got, want)
	}
	if got, want := SocketPath("/Users/me/.ao/data", "proj-feature-x"), "/Users/me/.ao/data/tmux/proj-feature-x"; got != want {
		t.Fatalf("SocketPath = %q, want %q", got, want)
	}
}

func TestSocketDirFallsBackWhenTheDataDirIsTooDeep(t *testing.T) {
	deep := "/" + strings.Repeat("d", 120)
	dir := SocketDir(deep)
	if strings.HasPrefix(dir, deep) {
		t.Fatalf("SocketDir(%d-byte dir) = %q, want a short fallback", len(deep), dir)
	}
	if len(dir)+1+hashedSocketNameLen > maxSocketPathLen {
		t.Fatalf("fallback dir %q leaves no room for a socket name", dir)
	}
	if other := SocketDir(deep + "x"); other == dir {
		t.Fatalf("two data dirs share fallback socket dir %q", dir)
	}
	if SocketDir(deep) != dir {
		t.Fatal("the fallback is not deterministic")
	}
}

func TestSocketPathInHashesANameThatDoesNotFit(t *testing.T) {
	dir := "/" + strings.Repeat("d", 60)
	long := strings.Repeat("n", 64) // branchNameMaxLen
	p := SocketPathIn(dir, long)
	if len(p) > maxSocketPathLen {
		t.Fatalf("socket path %q is %d bytes, over the %d limit", p, len(p), maxSocketPathLen)
	}
	if filepath.Dir(p) != dir {
		t.Fatalf("socket %q left its dir %q", p, dir)
	}
	if SocketPathIn(dir, long+"x") == p {
		t.Fatal("two long names hash to the same socket")
	}
	if got, want := SocketPathIn(dir, "short"), filepath.Join(dir, "short"); got != want {
		t.Fatalf("a name that fits is hashed anyway: %q, want %q", got, want)
	}
}

func TestLegacyDefaultSocketFollowsTmuxTmpdir(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", "/x/y")
	if got := LegacyDefaultSocket(); got != filepath.Join("/x/y", "tmux-"+strconv.Itoa(os.Getuid()), "default") {
		t.Fatalf("LegacyDefaultSocket = %q", got)
	}
}

// -- every command names its server --

func TestEveryCommandAddressesTheSessionsOwnServer(t *testing.T) {
	r, fr := newTestRuntime(0)
	ctx := context.Background()
	if _, err := r.Create(ctx, ports.RuntimeConfig{SessionID: "sess-1", WorkspacePath: "/tmp/ws", Argv: []string{"agent"}}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	h := ports.RuntimeHandle{ID: "sess-1"}
	_, _ = r.IsAlive(ctx, h)
	_ = r.SendMessage(ctx, h, "hi")
	_, _ = r.GetOutput(ctx, h, 5)
	_ = r.Destroy(ctx, h)

	want := filepath.Join(testDataDir, "tmux", "sess-1")
	if len(fr.calls) == 0 {
		t.Fatal("no tmux calls recorded")
	}
	for _, c := range fr.calls {
		if c.socket != want {
			t.Fatalf("%v ran against socket %q, want the session's own %q", c.args, c.socket, want)
		}
	}
	if info, err := os.Stat(filepath.Dir(want)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("socket dir not created private: %v %v", info, err)
	}
}

func TestTwoSessionsNeverShareAServer(t *testing.T) {
	r, _ := newTestRuntime(0)
	if r.ownSocket("a") == r.ownSocket("b") {
		t.Fatal("two sessions resolve to one tmux server")
	}
}

func TestClientEnvDropsTmuxPaneVariables(t *testing.T) {
	got := stripEnvKeys([]string{"PATH=/bin", "TMUX=/tmp/tmux-501/default,1,0", "TMUX_PANE=%3"}, tmuxClientEnvKeys)
	if len(got) != 1 || got[0] != "PATH=/bin" {
		t.Fatalf("env = %v, want only PATH", got)
	}
	for _, kv := range attachEnv([]string{"TMUX=/tmp/tmux-501/default,1,0", "TMUX_PANE=%3"}) {
		if strings.HasPrefix(kv, "TMUX") {
			t.Fatalf("attach env kept %q: tmux refuses to attach from inside a pane", kv)
		}
	}
}

// -- the pre-upgrade shared server --

const legacySock = "/tmp/tmux-test/default"

// legacyDataDir is the data dir of the instance under test: a real directory,
// since Create writes its launch script there (TestMain removes it). A
// "/sandbox"-prefixed copy of it in a test is another instance whose launch dir
// merely ends the same way.
func legacyDataDir() string { return filepath.Join(testDataDir, "legacy") }

// legacyRunner plays the shared pre-upgrade server: panes lists what
// `list-panes -a` prints there, and every other command against it succeeds
// unless listErr says the server cannot be asked. Commands against any other
// (per-session) socket answer "no server running".
type legacyRunner struct {
	mu      sync.Mutex
	panes   string
	listErr error
	calls   []runnerCall
}

func (l *legacyRunner) Run(_ context.Context, _ []string, name string, args ...string) ([]byte, error) {
	sock, cmd := splitSocket(args)
	l.mu.Lock()
	l.calls = append(l.calls, runnerCall{name: name, socket: sock, args: cmd})
	l.mu.Unlock()
	if sock != legacySock {
		return []byte("no server running on " + sock), &exec.ExitError{}
	}
	if cmd[0] == "list-panes" && len(cmd) > 1 && cmd[1] == "-a" {
		if l.listErr != nil {
			return []byte("lost server"), l.listErr
		}
		return []byte(l.panes), nil
	}
	if cmd[0] == "list-panes" {
		return []byte("4242\n"), nil
	}
	return nil, nil
}

func (l *legacyRunner) socketsFor(verb string) []string {
	var out []string
	for _, c := range l.calls {
		if c.args[0] == verb {
			out = append(out, c.socket)
		}
	}
	return out
}

// newLegacyRuntime is a runtime with the shared-server fallback on.
func newLegacyRuntime(t *testing.T, lr *legacyRunner) *Runtime {
	t.Helper()
	r := New(Options{Binary: "tmux-test", Timeout: time.Second, Shell: "/bin/sh", DataDir: legacyDataDir(), SocketDir: t.TempDir(), LegacySocket: legacySock})
	r.runner = lr
	r.sleep = func(time.Duration) {}
	return r
}

// paneLine is one `list-panes -a` row as the runtime launched it.
func paneLine(session, dataDir string) string {
	return session + "\t/bin/zsh " + dataDir + "/runtime/launch/launch-" + session + "-1234.sh\n"
}

func TestALiveSessionFromBeforeTheUpgradeIsStillReached(t *testing.T) {
	lr := &legacyRunner{panes: paneLine("other", legacyDataDir()) + paneLine("proj-feature-x", legacyDataDir())}
	r := newLegacyRuntime(t, lr)
	h := ports.RuntimeHandle{ID: "proj-feature-x"}

	alive, err := r.IsAlive(context.Background(), h)
	if err != nil || !alive {
		t.Fatalf("IsAlive = %v, %v; want the pre-upgrade session found alive", alive, err)
	}
	if err := r.SendMessage(context.Background(), h, "hi"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	for _, s := range lr.socketsFor("send-keys") {
		if s != legacySock {
			t.Fatalf("send-keys went to %q, want the shared server it lives on", s)
		}
	}
	// One look at the shared server, then the answer is remembered.
	if n := len(lr.socketsFor("list-panes")); n != 1 {
		t.Fatalf("listed the shared server %d times, want once", n)
	}
}

func TestASameNamedSessionFromAnotherInstanceIsNeverAdopted(t *testing.T) {
	// A sandbox AO (its own data dir) put a session with the same name on the
	// shared server. This instance must not see it, let alone kill it.
	lr := &legacyRunner{panes: paneLine("proj-feature-x", "/sandbox"+legacyDataDir())}
	r := newLegacyRuntime(t, lr)
	h := ports.RuntimeHandle{ID: "proj-feature-x"}

	alive, err := r.IsAlive(context.Background(), h)
	if err != nil || alive {
		t.Fatalf("IsAlive = %v, %v; want the foreign session invisible (false, nil)", alive, err)
	}
	if err := r.Destroy(context.Background(), h); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	for _, s := range lr.socketsFor("kill-session") {
		if s == legacySock {
			t.Fatal("Destroy killed another instance's session on the shared server")
		}
	}
}

func TestAnUnanswerableSharedServerIsAProbeErrorNotADeath(t *testing.T) {
	// Reading "could not ask" as "gone" would let the reaper end a live
	// pre-upgrade session and boot relaunch its agent on top of the old one.
	lr := &legacyRunner{listErr: errors.New("timeout")}
	r := newLegacyRuntime(t, lr)
	if _, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: "proj-feature-x"}); err == nil {
		t.Fatal("IsAlive = nil error, want a probe error while the shared server cannot be asked")
	}
}

func TestNoSharedServerMeansTheSessionsOwn(t *testing.T) {
	r := newLegacyRuntime(t, &legacyRunner{})
	// Nothing listens on this shared socket: "no server running" is definitive.
	r.legacySocket = "/tmp/tmux-test/absent"
	alive, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: "proj-feature-x"})
	if err != nil || alive {
		t.Fatalf("IsAlive = %v, %v; want (false, nil) with no shared server at all", alive, err)
	}
}

func TestCreateRetiresAStalePreUpgradeSessionOfTheSameName(t *testing.T) {
	lr := &legacyRunner{panes: paneLine("proj-feature-x", legacyDataDir())}
	r := newLegacyRuntime(t, lr)
	r.hasLiveChild = func(context.Context, int) (bool, error) { return false, nil } // agent gone

	// Create fails at the end (the fake has no per-session server), which is fine:
	// what matters is what happened to the shared server first.
	_, _ = r.Create(context.Background(), ports.RuntimeConfig{SessionID: domain.SessionID("x"), ProjectID: "proj", Branch: "feature/x", WorkspacePath: "/tmp/ws", Argv: []string{"agent"}})

	kills := lr.socketsFor("kill-session")
	if len(kills) == 0 || kills[0] != legacySock {
		t.Fatalf("kill-session sockets = %v, want the stale pre-upgrade session reaped first", kills)
	}
	news := lr.socketsFor("new-session")
	if len(news) == 0 || news[0] != r.ownSocket("proj-feature-x") {
		t.Fatalf("new-session sockets = %v, want the session's own server", news)
	}
}

func TestCreateRefusesWhileAPreUpgradeAgentStillRunsUnderTheName(t *testing.T) {
	lr := &legacyRunner{panes: paneLine("proj-feature-x", legacyDataDir())}
	r := newLegacyRuntime(t, lr)
	r.hasLiveChild = func(context.Context, int) (bool, error) { return true, nil }

	_, err := r.Create(context.Background(), ports.RuntimeConfig{SessionID: "x", ProjectID: "proj", Branch: "feature/x", WorkspacePath: "/tmp/ws", Argv: []string{"agent"}})
	if err == nil || !strings.Contains(err.Error(), "live agent") {
		t.Fatalf("Create err = %v, want a refusal naming the live agent", err)
	}
	if len(lr.socketsFor("kill-session")) != 0 || len(lr.socketsFor("new-session")) != 0 {
		t.Fatal("Create touched a session a live agent still runs in")
	}
}

func TestSweepStaleSocketsRemovesOnlyDeadServers(t *testing.T) {
	dir, err := os.MkdirTemp("", "aosw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	live, err := net.Listen("unix", filepath.Join(dir, "live"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })
	// A socket file nothing listens on: what tmux leaves behind when its server exits.
	dead, err := net.Listen("unix", filepath.Join(dir, "dead"))
	if err != nil {
		t.Fatal(err)
	}
	dead.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = dead.Close()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	SweepStaleSockets(dir)

	for name, want := range map[string]bool{"live": true, "dead": false, "note.txt": true} {
		_, err := os.Stat(filepath.Join(dir, name))
		if got := err == nil; got != want {
			t.Errorf("%s present = %v, want %v", name, got, want)
		}
	}
}

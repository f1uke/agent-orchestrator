package cli

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A lease taken through ANOTHER AO daemon on this machine - a sandbox daemon a
// worker runs with its own AO_DATA_DIR - reaches the CLI with otherDaemon set.
// Its session id is numbered in that daemon's database, so it can equal this
// session's id and still be somebody else: the 2026-10-03 incident had two
// sessions on two daemons both believing the device was theirs to take.

var sandboxDaemon = &domain.SimDaemon{DataDir: "/tmp/ao-sandbox", PID: 4242, Port: 3399}

const sandboxConflictDetails = `"otherDaemon":{"dataDir":"/tmp/ao-sandbox","pid":4242,"port":3399}`

func TestSimList_ShowsAHolderOnAnotherDaemon(t *testing.T) {
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.leases[simUDIDProMax] = simLeaseClient{
		UDID: simUDIDProMax, SessionID: "agent-orchestrator-360",
		AcquiredAt: simFixedNow, ExpiresAt: simFixedNow.Add(7 * time.Minute), OtherDaemon: sandboxDaemon,
	}

	out, errOut, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "list")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "@agent-orchestrator-360 via other AO daemon, port 3399") {
		t.Fatalf("listing must name the holder and the daemon it holds through:\n%s", out)
	}

	out, _, err = executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"otherDaemon"`) || !strings.Contains(out, `"dataDir": "/tmp/ao-sandbox"`) {
		t.Fatalf("--json must carry the other daemon:\n%s", out)
	}
}

func TestSimShot_SameIDOnAnotherDaemonIsNotYou(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "agent-orchestrator-360")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.leases[simUDIDProMax] = simLeaseClient{
		UDID: simUDIDProMax, SessionID: "agent-orchestrator-360",
		AcquiredAt: simFixedNow, ExpiresAt: simFixedNow.Add(7 * time.Minute), OtherDaemon: sandboxDaemon,
	}

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "shot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "You hold") {
		t.Fatalf("a same-id session on another daemon was reported as this one:\n%s", out)
	}
	if !strings.Contains(out, "port 3399") || !strings.Contains(out, "do NOT drive it") {
		t.Fatalf("capture must say another daemon's session holds it:\n%s", out)
	}
}

func TestSimRelease_NeverReleasesAnotherDaemonsLease(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "agent-orchestrator-360")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.leases[simUDIDProMax] = simLeaseClient{
		UDID: simUDIDProMax, SessionID: "agent-orchestrator-360",
		AcquiredAt: simFixedNow, ExpiresAt: simFixedNow.Add(7 * time.Minute), OtherDaemon: sandboxDaemon,
	}

	if _, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "release"); err == nil {
		t.Fatal("a same-id lease on another daemon was taken for this session's own")
	}
	for _, call := range daemon.calls {
		if strings.HasPrefix(call, "DELETE") {
			t.Fatalf("nothing may be released: %v", daemon.calls)
		}
	}
}

func TestSimClaim_RefusalNamesTheOtherDaemon(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "agent-orchestrator-361")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.acquireStatus = http.StatusConflict
	daemon.acquireBody = `{"error":"conflict","code":"SIM_DEVICE_LEASED","message":"leased elsewhere",` +
		`"details":{"udid":"` + simUDIDProMax + `","holder":"agent-orchestrator-360","expiresAt":"2026-08-13T07:48:14Z",` +
		sandboxConflictDetails + `}}`

	_, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "claim")
	if ExitCode(err) != 1 {
		t.Fatalf("exit code = %d, want 1 (err=%v)", ExitCode(err), err)
	}
	msg := err.Error()
	for _, want := range []string{"@agent-orchestrator-360", "port 3399", "/tmp/ao-sandbox", "pid 4242", "exits", "nothing was claimed"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal missing %q:\n%s", want, msg)
		}
	}
}

func TestSimTouch_RefusalNamesTheOtherDaemon(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, daemon := touchDeps(t, driver)
	daemon.holdStatus = http.StatusConflict
	daemon.holdBody = `{"error":"conflict","code":"SIM_DEVICE_BUSY","message":"leased elsewhere",` +
		`"details":{"reason":"leased_by_other","holder":"agent-orchestrator-360","expiresAt":"2026-08-13T07:48:02Z",` +
		sandboxConflictDetails + `}}`

	_, errOut, err := executeCLI(t, deps, "sim", "tap", "0.5", "0.5")
	if err == nil {
		t.Fatal("touching a device another daemon's session holds must fail")
	}
	if len(driver.calls()) != 0 {
		t.Fatalf("nothing may reach the device: %+v", driver.calls())
	}
	msg := err.Error() + errOut
	for _, want := range []string{"@agent-orchestrator-360", "port 3399", "nothing was sent"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal %q must mention %q", msg, want)
		}
	}
}

func TestSimLeaseView_HeldByIgnoresAnotherDaemonsSameID(t *testing.T) {
	expires := simFixedNow.Add(time.Minute)
	mine := simLeaseView{State: domain.SimLeaseHeld, Holder: "mer-9", ExpiresAt: &expires}
	theirs := simLeaseView{State: domain.SimLeaseHeld, Holder: "mer-9", ExpiresAt: &expires, OtherDaemon: sandboxDaemon}
	if !mine.heldBy("mer-9") {
		t.Fatal("this daemon's own lease is not the session's")
	}
	if theirs.heldBy("mer-9") {
		t.Fatal("another daemon's lease counted as this session's because the ids match")
	}
}

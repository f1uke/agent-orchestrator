package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// simDaemon fakes the daemon's lease surface. It records every request so a
// test can assert what the CLI sent, and serves the lease list from a map the
// test controls.
type simDaemon struct {
	leases map[string]simLeaseClient // udid -> lease

	acquireStatus int
	acquireBody   string

	// holdStatus/holdBody override the gesture-hold response, so a test can
	// make the daemon refuse a touch the way contention does.
	holdStatus int
	holdBody   string

	mu          sync.Mutex
	calls       []string // "METHOD path"
	body        string   // last request body
	holdRequest string   // body of the last gesture-hold request
}

// requestedHoldSeconds is the TTL the CLI asked the gesture hold for.
func (d *simDaemon) requestedHoldSeconds(t *testing.T) int {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	var in acquireSimHoldRequest
	if err := json.Unmarshal([]byte(d.holdRequest), &in); err != nil {
		t.Fatalf("hold body %q: %v", d.holdRequest, err)
	}
	return in.HoldSeconds
}

// callLog is every request the CLI made, in order. It is read while a gesture
// is in flight, so it takes the lock.
func (d *simDaemon) callLog() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.Join(d.calls, "\n")
}

func newSimDaemon(t *testing.T, cfg testConfig) *simDaemon {
	t.Helper()
	d := &simDaemon{leases: map[string]simLeaseClient{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.calls = append(d.calls, r.Method+" "+r.URL.Path)
		d.body = string(body)
		d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/hold"):
			d.mu.Lock()
			d.holdRequest = string(body)
			d.mu.Unlock()
			if d.holdStatus != 0 && d.holdStatus != http.StatusOK {
				w.WriteHeader(d.holdStatus)
				_, _ = io.WriteString(w, d.holdBody)
				return
			}
			_, _ = io.WriteString(w, `{"hold":{"udid":"x","sessionId":"mer-9","token":"hold-token-1","expiresAt":"2026-08-13T07:41:32Z"}}`)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/hold/"):
			_, _ = io.WriteString(w, `{"released":true}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sim/leases":
			leases := []simLeaseClient{}
			for _, l := range d.leases {
				leases = append(leases, l)
			}
			_ = json.NewEncoder(w).Encode(listSimLeasesResponse{Leases: leases})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/sim-leases"):
			if d.acquireStatus != 0 && d.acquireStatus != http.StatusOK {
				w.WriteHeader(d.acquireStatus)
				_, _ = io.WriteString(w, d.acquireBody)
				return
			}
			var in acquireSimLeaseRequest
			_ = json.Unmarshal(body, &in)
			ttl := time.Duration(in.TTLSeconds) * time.Second
			if ttl == 0 {
				ttl = 10 * time.Minute
			}
			at := simFixedNow
			lease := simLeaseClient{UDID: in.UDID, SessionID: "mer-9", AcquiredAt: at, ExpiresAt: at.Add(ttl)}
			d.leases[in.UDID] = lease
			_ = json.NewEncoder(w).Encode(simLeaseResponse{Lease: lease})
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/sim-leases/"):
			udid := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			delete(d.leases, udid)
			_ = json.NewEncoder(w).Encode(map[string]bool{"released": true})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	return d
}

var simFixedNow = time.Date(2026, 8, 13, 7, 41, 2, 417_000_000, time.UTC)

// simLeaseDeps is simDeps plus a live daemon (ProcessAlive true).
func simLeaseDeps(t *testing.T, listJSON string, screenshot []byte) Deps {
	t.Helper()
	deps, _ := simDeps(t, listJSON, screenshot)
	deps.ProcessAlive = func(int) bool { return true }
	return deps
}

func bootedProMaxOnly(t *testing.T) string {
	t.Helper()
	return simDevicesJSON(t,
		simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"),
		simDeviceFixture(simUDIDPro, "iPhone 17 Pro", "Shutdown"),
	)
}

// --- ao sim claim ----------------------------------------------------------

func TestSimClaim_ClaimsTheBootedDeviceForThisSession(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)

	out, errOut, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "claim")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if want := "POST /api/v1/sessions/mer-9/sim-leases"; !simCalled(daemon, want) {
		t.Fatalf("calls = %v, want %s", daemon.calls, want)
	}
	var req acquireSimLeaseRequest
	if err := json.Unmarshal([]byte(daemon.body), &req); err != nil {
		t.Fatalf("decode body: %v (%s)", err, daemon.body)
	}
	if req.UDID != simUDIDProMax {
		t.Fatalf("claimed udid = %q, want %q", req.UDID, simUDIDProMax)
	}
	if req.TTLSeconds != 0 {
		t.Fatalf("ttlSeconds = %d, want 0 so the daemon owns the default", req.TTLSeconds)
	}
	for _, want := range []string{"iPhone 17 Pro Max", simUDIDProMax, "ao sim release"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	// The claim must not overstate what it buys: it excludes other AO sessions,
	// not a human driving the same simulator from Xcode.
	if !strings.Contains(out, "Xcode") {
		t.Fatalf("claim output must say what the lease cannot cover:\n%s", out)
	}
}

func TestSimClaim_TTLIsSentInSeconds(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)

	// A gesture-length hold has to be expressible, not just a ten-minute one.
	if _, errOut, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "claim", "--ttl", "5s"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var req acquireSimLeaseRequest
	if err := json.Unmarshal([]byte(daemon.body), &req); err != nil {
		t.Fatalf("decode body: %v (%s)", err, daemon.body)
	}
	if req.TTLSeconds != 5 {
		t.Fatalf("ttlSeconds = %d, want 5", req.TTLSeconds)
	}
}

func TestSimClaim_BadTTLIsAUsageError(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	newSimDaemon(t, cfg)
	_, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "claim", "--ttl", "soon")
	if ExitCode(err) != 2 {
		t.Fatalf("exit code = %d, want 2 for a malformed --ttl (err=%v)", ExitCode(err), err)
	}
}

func TestSimClaim_OutsideASessionIsAUsageError(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	newSimDaemon(t, cfg)
	_, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "claim")
	if ExitCode(err) != 2 {
		t.Fatalf("exit code = %d, want 2 without AO_SESSION_ID (err=%v)", ExitCode(err), err)
	}
}

// Contention must never look like success. The refusal names the device, the
// holder and the time left, and points at the two things that can unblock it.
func TestSimClaim_HeldDeviceIsRefusedAndNamesTheHolder(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.acquireStatus = http.StatusConflict
	daemon.acquireBody = `{"error":"conflict","code":"SIM_DEVICE_LEASED",` +
		`"message":"simulator ` + simUDIDProMax + ` is leased by @mer-3 for another 7m12s",` +
		`"details":{"udid":"` + simUDIDProMax + `","holder":"mer-3","expiresAt":"2026-08-13T07:48:14Z"}}`

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "claim")
	if err == nil {
		t.Fatalf("a held device must fail the command, got success:\n%s", out)
	}
	if ExitCode(err) != 1 {
		t.Fatalf("exit code = %d, want 1", ExitCode(err))
	}
	msg := err.Error()
	for _, want := range []string{"iPhone 17 Pro Max", "mer-3", "7m", "ao sim release"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal missing %q:\n%s", want, msg)
		}
	}
}

// --- ao sim release --------------------------------------------------------

func TestSimRelease_ReleasesTheDeviceThisSessionHolds(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.leases[simUDIDProMax] = simLeaseClient{
		UDID: simUDIDProMax, SessionID: "mer-9",
		AcquiredAt: simFixedNow, ExpiresAt: simFixedNow.Add(10 * time.Minute),
	}

	out, errOut, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "release")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if want := "DELETE /api/v1/sessions/mer-9/sim-leases/" + simUDIDProMax; !simCalled(daemon, want) {
		t.Fatalf("calls = %v, want %s", daemon.calls, want)
	}
	if !strings.Contains(out, simUDIDProMax) {
		t.Fatalf("output missing the udid:\n%s", out)
	}
}

func TestSimRelease_WithNoLeaseHeldIsAnError(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.leases[simUDIDProMax] = simLeaseClient{
		UDID: simUDIDProMax, SessionID: "mer-3", // someone else's
		AcquiredAt: simFixedNow, ExpiresAt: simFixedNow.Add(10 * time.Minute),
	}

	_, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "release")
	if err == nil {
		t.Fatal("releasing with no lease of our own must fail")
	}
	if ExitCode(err) != 1 {
		t.Fatalf("exit code = %d, want 1", ExitCode(err))
	}
	for _, call := range daemon.calls {
		if strings.HasPrefix(call, "DELETE") {
			t.Fatalf("nothing may be released when this session holds nothing: %v", daemon.calls)
		}
	}
}

func TestSimRelease_ExplicitUDIDSkipsTheLookup(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)

	if _, errOut, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "release", "--udid", strings.ToLower(simUDIDProMax)); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	// The udid is normalized before it becomes a path segment: the lease key is
	// upper-case, and a lower-cased path must not miss it.
	if want := "DELETE /api/v1/sessions/mer-9/sim-leases/" + simUDIDProMax; !simCalled(daemon, want) {
		t.Fatalf("calls = %v, want %s", daemon.calls, want)
	}
}

// --- ao sim list -----------------------------------------------------------

func TestSimList_ShowsWhoHoldsEachDevice(t *testing.T) {
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.leases[simUDIDProMax] = simLeaseClient{
		UDID: simUDIDProMax, SessionID: "mer-3",
		AcquiredAt: simFixedNow, ExpiresAt: simFixedNow.Add(7 * time.Minute),
	}

	out, errOut, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "list")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "LEASE") || !strings.Contains(out, "@mer-3") {
		t.Fatalf("listing must show the holder:\n%s", out)
	}
	// A device with no AO lease is never called free: AO cannot see a human
	// driving a simulator from Xcode.
	if !strings.Contains(out, "unknown") {
		t.Fatalf("an unleased device must read as unknown, not free:\n%s", out)
	}
	if strings.Contains(out, "free") {
		t.Fatalf("no device may be reported as free:\n%s", out)
	}
}

func TestSimList_JSONCarriesLeaseState(t *testing.T) {
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.leases[simUDIDProMax] = simLeaseClient{
		UDID: simUDIDProMax, SessionID: "mer-3",
		AcquiredAt: simFixedNow, ExpiresAt: simFixedNow.Add(7 * time.Minute),
	}

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "list", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var res struct {
		Devices []struct {
			UDID  string `json:"udid"`
			Lease struct {
				State  string `json:"state"`
				Holder string `json:"holder"`
				Reason string `json:"reason"`
			} `json:"lease"`
		} `json:"devices"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	for _, d := range res.Devices {
		switch d.UDID {
		case simUDIDProMax:
			if d.Lease.State != "held" || d.Lease.Holder != "mer-3" {
				t.Fatalf("held device lease = %+v", d.Lease)
			}
		default:
			if d.Lease.State != "unknown" || d.Lease.Reason == "" {
				t.Fatalf("unleased device lease = %+v, want unknown with a reason", d.Lease)
			}
		}
	}
}

// Slice 1 shipped `ao sim list` and `ao sim shot` as a daemon-free CLI. Reading
// lease state must never take that away.
func TestSimList_StillWorksWithNoDaemon(t *testing.T) {
	setConfigEnv(t)
	deps, _ := simDeps(t, bootedProMaxOnly(t), fakePNG) // ProcessAlive false: no daemon
	out, errOut, err := executeCLI(t, deps, "sim", "list")
	if err != nil {
		t.Fatalf("listing must survive a stopped daemon: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "iPhone 17 Pro Max") {
		t.Fatalf("device listing lost:\n%s", out)
	}
	if !strings.Contains(out, "unknown") {
		t.Fatalf("lease column must degrade to unknown:\n%s", out)
	}
	// "unknown" here means "nobody could be asked", not "nobody holds it". The
	// footer has to say which, or it states something AO did not check.
	if !strings.Contains(out, "daemon is not reachable") {
		t.Fatalf("the footer must give the real reason state is unknown:\n%s", out)
	}
	if strings.Contains(out, "no AO session holds this device") {
		t.Fatalf("the footer claims something AO never checked:\n%s", out)
	}
}

// --- ao sim shot -----------------------------------------------------------

// A screenshot cannot corrupt someone else's gesture, so it must not require a
// lease - but it must say who holds the device.
func TestSimShot_WorksOnALeasedDeviceAndReportsTheHolder(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.leases[simUDIDProMax] = simLeaseClient{
		UDID: simUDIDProMax, SessionID: "mer-3",
		AcquiredAt: simFixedNow, ExpiresAt: simFixedNow.Add(7 * time.Minute),
	}

	out, errOut, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "shot")
	if err != nil {
		t.Fatalf("a leased device must still be capturable: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "@mer-3") {
		t.Fatalf("capture must report the holder:\n%s", out)
	}
	for _, call := range daemon.calls {
		if strings.Contains(call, "/sim-leases") {
			t.Fatalf("`ao sim shot` must not take a lease: %v", daemon.calls)
		}
	}
}

func TestSimShot_SaysWhenThisSessionHoldsTheDevice(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.leases[simUDIDProMax] = simLeaseClient{
		UDID: simUDIDProMax, SessionID: "mer-9",
		AcquiredAt: simFixedNow, ExpiresAt: simFixedNow.Add(7 * time.Minute),
	}

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "shot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "You hold") {
		t.Fatalf("the capture must tell the holder it is theirs:\n%s", out)
	}
}

func TestSimShot_JSONCarriesLeaseState(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	daemon := newSimDaemon(t, cfg)
	daemon.leases[simUDIDProMax] = simLeaseClient{
		UDID: simUDIDProMax, SessionID: "mer-3",
		AcquiredAt: simFixedNow, ExpiresAt: simFixedNow.Add(7 * time.Minute),
	}

	out, _, err := executeCLI(t, simLeaseDeps(t, bootedProMaxOnly(t), fakePNG), "sim", "shot", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var res struct {
		Path  string `json:"path"`
		Note  string `json:"note"`
		Lease struct {
			State  string `json:"state"`
			Holder string `json:"holder"`
		} `json:"lease"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	// Slice 1's keys are a shipped contract: adding lease state must not move them.
	if res.Path == "" || res.Note == "" {
		t.Fatalf("slice-1 fields lost: %s", out)
	}
	if res.Lease.State != "held" || res.Lease.Holder != "mer-3" {
		t.Fatalf("lease = %+v", res.Lease)
	}
}

func simCalled(d *simDaemon, want string) bool {
	for _, call := range d.calls {
		if call == want {
			return true
		}
	}
	return false
}

package simbridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBridge stands in for the resident bridge process: it answers requests the
// way the real one does, records them, and counts how many times it was
// started - which is what "the process is kept, not respawned" is made of.
type fakeBridge struct {
	t        *testing.T
	payload  string
	replyErr error
	starts   int
	requests []bridgeRequest
	closed   int
}

func (f *fakeBridge) start(context.Context, string, []string) (BridgeSession, error) {
	f.starts++
	return f, nil
}

func (f *fakeBridge) Request(_ context.Context, line []byte) ([]byte, error) {
	var req bridgeRequest
	if err := json.Unmarshal(line, &req); err != nil {
		f.t.Fatalf("the bridge was sent unparsable input %s: %v", line, err)
	}
	f.requests = append(f.requests, req)
	if f.replyErr != nil {
		return nil, f.replyErr
	}
	return []byte(f.payload + "\n"), nil
}

// Diagnostics is what the real addon prints on stdout unbidden.
func (f *fakeBridge) Diagnostics() string { return "com.apple.springboard: 97690" }

func (f *fakeBridge) Close() error {
	f.closed++
	return nil
}

func (f *fakeBridge) last() bridgeRequest {
	f.t.Helper()
	if len(f.requests) == 0 {
		f.t.Fatal("the bridge was never asked for anything")
	}
	return f.requests[len(f.requests)-1]
}

// newTestDriver builds a driver over a fake bridge process, so nothing here
// needs Node, an addon or a mac.
func newTestDriver(t *testing.T, bridge *fakeBridge) *NodeDriver {
	t.Helper()
	tc, err := Install(t.TempDir())
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	bridge.t = t
	d := &NodeDriver{Toolchain: tc, NodePath: "node", Start: bridge.start}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func replyWith(payload string) *fakeBridge { return &fakeBridge{payload: payload} }

func TestDriver_AXRequestCarriesTheAddonPath(t *testing.T) {
	bridge := replyWith(`{"ok":true,"tree":` + rawTree + `,"frontmost":{"bundleId":"com.example.app","pid":7}}`)
	d := newTestDriver(t, bridge)

	snap, err := d.AX(context.Background(), "UDID-1")
	if err != nil {
		t.Fatalf("ax: %v", err)
	}
	got := bridge.last()
	if got.Op != "ax" || got.UDID != "UDID-1" {
		t.Fatalf("request = %+v", got)
	}
	if got.AddonPath != d.Toolchain.Addon || !filepath.IsAbs(got.AddonPath) {
		t.Fatalf("addonPath = %q, want the installed addon's absolute path", got.AddonPath)
	}
	if snap.Frontmost.BundleID != "com.example.app" || snap.NodeCount != 4 {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestDriver_PerformSendsTheEventsVerbatim(t *testing.T) {
	bridge := replyWith(`{"ok":true,"lifted":false}`)
	d := newTestDriver(t, bridge)
	events, err := Tap(Point{X: 0.5, Y: 0.5})
	if err != nil {
		t.Fatalf("tap: %v", err)
	}

	if _, err := d.Perform(context.Background(), "UDID-1", events); err != nil {
		t.Fatalf("perform: %v", err)
	}
	got := bridge.last()
	if len(got.Events) != len(events) || got.Events[0].Type != "begin" {
		t.Fatalf("events = %+v", got.Events)
	}
}

func TestDriver_ReportsARescuedFinger(t *testing.T) {
	// A gesture that ended without its own lift is not a success to swallow:
	// the caller has to be able to say the device was rescued.
	d := newTestDriver(t, replyWith(`{"ok":true,"lifted":true,"liftReason":"gesture ended without a lift"}`))

	res, err := d.Perform(context.Background(), "UDID-1", nil)
	if err != nil {
		t.Fatalf("perform: %v", err)
	}
	if !res.Lifted || res.LiftReason == "" {
		t.Fatalf("result = %+v, want the rescue reported", res)
	}
}

func TestDriver_AddonLoadFailureIsExplained(t *testing.T) {
	// The single most likely real-world failure: an Xcode upgrade moved the
	// private frameworks the addon dlopens.
	d := newTestDriver(t, replyWith(`{"ok":false,"error":{"code":"addon_load_failed","message":"symbol not found"}}`))

	_, err := d.AX(context.Background(), "UDID-1")
	var bridgeErr *Error
	if !errors.As(err, &bridgeErr) || bridgeErr.Code != errCodeAddon {
		t.Fatalf("err = %v, want an addon load failure", err)
	}
	for _, want := range []string{"private Apple frameworks", "ao sim shot", Version} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q must mention %q so a human knows what to do", err, want)
		}
	}
}

func TestDriver_AXUnavailableIsExplained(t *testing.T) {
	d := newTestDriver(t, replyWith(`{"ok":false,"error":{"code":"ax_unavailable","message":"no response"}}`))

	_, err := d.AX(context.Background(), "UDID-1")
	var bridgeErr *Error
	if !errors.As(err, &bridgeErr) || bridgeErr.Code != errCodeAX {
		t.Fatalf("err = %v, want an accessibility failure", err)
	}
}

func TestDriver_AddonChatterOnStdoutDoesNotBreakAGesture(t *testing.T) {
	// The addon prints to stdout itself: `button home` relaunches SpringBoard
	// and logs its pid. A gesture that completed must not be reported as a
	// failure because of it.
	d := newTestDriver(t, replyWith(`{"ok":true,"lifted":false}`))

	if _, err := d.Perform(context.Background(), "UDID-1", nil); err != nil {
		t.Fatalf("perform: %v", err)
	}
}

func TestDriver_UnparsableOutputIsNeverSuccess(t *testing.T) {
	cases := map[string]string{
		"empty":     "",
		"not json":  "Segmentation fault: 11",
		"ok absent": `{"tree":[]}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			d := newTestDriver(t, replyWith(payload))
			if _, err := d.Perform(context.Background(), "UDID-1", nil); err == nil {
				t.Fatal("a bridge that said nothing usable must not read as a completed gesture")
			}
		})
	}
}

func TestDriver_ProcessFailureCarriesItsDiagnostics(t *testing.T) {
	bridge := replyWith("")
	bridge.replyErr = errors.New("EOF")
	d := newTestDriver(t, bridge)

	_, err := d.AX(context.Background(), "UDID-1")
	if err == nil || !strings.Contains(err.Error(), "com.apple.springboard") {
		t.Fatalf("err = %v, want the child's own diagnostics", err)
	}
}

// The reason the bridge is resident at all: the first gesture pays for loading
// the addon and attaching the injector, and every one after it must not.
func TestDriver_KeepsOneBridgeProcessAcrossGestures(t *testing.T) {
	bridge := replyWith(`{"ok":true,"lifted":false}`)
	d := newTestDriver(t, bridge)

	for range 5 {
		if _, err := d.Perform(context.Background(), "UDID-1", nil); err != nil {
			t.Fatalf("perform: %v", err)
		}
	}
	if bridge.starts != 1 {
		t.Fatalf("the bridge was started %d times for 5 gestures, want 1", bridge.starts)
	}
	if len(bridge.requests) != 5 {
		t.Fatalf("the bridge saw %d requests, want 5", len(bridge.requests))
	}
}

// A bridge that stopped answering may have a finger down on the device, and
// nothing above this layer can know. Reusing it would send the next gesture
// into an unknown state, so it is dropped and the next call starts a fresh one
// - which starts with no finger down.
func TestDriver_ABridgeThatStoppedAnsweringIsNotReused(t *testing.T) {
	bridge := replyWith(`{"ok":true,"lifted":false}`)
	bridge.replyErr = errors.New("EOF")
	d := newTestDriver(t, bridge)

	if _, err := d.Perform(context.Background(), "UDID-1", nil); err == nil {
		t.Fatal("a bridge that stopped answering must not read as a completed gesture")
	}
	if bridge.closed != 1 {
		t.Fatalf("the failed bridge was closed %d times, want 1", bridge.closed)
	}
	bridge.replyErr = nil
	if _, err := d.Perform(context.Background(), "UDID-1", nil); err != nil {
		t.Fatalf("the next gesture must start a fresh bridge: %v", err)
	}
	if bridge.starts != 2 {
		t.Fatalf("the bridge was started %d times, want a second one after the failure", bridge.starts)
	}
}

// Closing is the daemon going away. A bridge left running would hold an
// injector attached to a device with nobody able to lift its finger.
func TestDriver_CloseStopsTheBridge(t *testing.T) {
	bridge := replyWith(`{"ok":true,"lifted":false}`)
	d := newTestDriver(t, bridge)
	if _, err := d.Perform(context.Background(), "UDID-1", nil); err != nil {
		t.Fatalf("perform: %v", err)
	}

	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if bridge.closed != 1 {
		t.Fatalf("the bridge was closed %d times, want 1", bridge.closed)
	}
	if _, err := d.Perform(context.Background(), "UDID-1", nil); err == nil {
		t.Fatal("a closed driver must refuse rather than start another bridge")
	}
	if bridge.starts != 1 {
		t.Fatalf("a closed driver started %d bridges, want none after Close", bridge.starts)
	}
}

func TestInstall_IsIdempotentAndRepairsATamperedAddon(t *testing.T) {
	dir := t.TempDir()
	first, err := Install(dir)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, path := range []string{first.Script, first.Addon,
		filepath.Join(first.Dir, "LICENSE-serve-sim.txt"), filepath.Join(first.Dir, "NOTICE-serve-sim.txt")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing %s: %v", filepath.Base(path), err)
		}
	}
	if !strings.Contains(first.Dir, Version) {
		t.Fatalf("install dir %q must be versioned so two builds cannot share one copy", first.Dir)
	}

	if err := os.WriteFile(first.Addon, []byte("corrupted"), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	second, err := Install(dir)
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if second != first {
		t.Fatalf("second install landed elsewhere: %+v vs %+v", second, first)
	}
	got, err := os.ReadFile(second.Addon)
	if err != nil {
		t.Fatalf("read addon: %v", err)
	}
	if len(got) < 1000 {
		t.Fatal("a corrupted addon must be replaced, not loaded")
	}
}

func TestInstall_ConcurrentCallersAllSucceed(t *testing.T) {
	// Every touch command installs on demand, so several can land at once.
	dir := t.TempDir()
	errs := make([]error, 8)
	done := make(chan struct{})
	for i := range errs {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			_, errs[i] = Install(dir)
		}(i)
	}
	for range errs {
		<-done
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("installer %d: %v", i, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "sim", "native", Version))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("a staging file was left behind: %s", e.Name())
		}
	}
	if len(entries) != 5 {
		t.Fatalf("dir holds %d files, want exactly the 5 installed assets", len(entries))
	}
}

func TestAddonDigest_MatchesTheNotice(t *testing.T) {
	digest, err := AddonDigest()
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	notice, err := assets.ReadFile("assets/NOTICE-serve-sim.txt")
	if err != nil {
		t.Fatalf("notice: %v", err)
	}
	if !strings.Contains(string(notice), digest) {
		t.Fatalf("NOTICE does not record the vendored addon's digest %s", digest)
	}
}

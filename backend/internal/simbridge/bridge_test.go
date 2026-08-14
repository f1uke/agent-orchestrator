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

// newTestDriver builds a driver over a fake bridge process, so nothing here
// needs Node, an addon or a mac.
func newTestDriver(t *testing.T, run Runner) *NodeDriver {
	t.Helper()
	tc, err := Install(t.TempDir())
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	return &NodeDriver{Toolchain: tc, NodePath: "node", Run: run}
}

// replyWith fakes the bridge process the way it really behaves: it writes its
// answer to the reply file it was given, and may print anything at all on
// stdout - which the real native addon does.
func replyWith(t *testing.T, payload string, capture *bridgeRequest) Runner {
	t.Helper()
	return func(_ context.Context, _ string, args []string, stdin []byte) ([]byte, []byte, error) {
		if capture != nil {
			if err := json.Unmarshal(stdin, capture); err != nil {
				t.Fatalf("the bridge was sent unparsable input %s: %v", stdin, err)
			}
		}
		if len(args) < 2 {
			t.Fatalf("the bridge was not told where to answer: %v", args)
		}
		if err := os.WriteFile(args[1], []byte(payload), 0o600); err != nil {
			t.Fatalf("write reply: %v", err)
		}
		return []byte("com.apple.springboard: 97690\n"), nil, nil
	}
}

func TestDriver_AXRequestCarriesTheAddonPath(t *testing.T) {
	var got bridgeRequest
	d := newTestDriver(t, replyWith(t, `{"ok":true,"tree":`+rawTree+`,"frontmost":{"bundleId":"com.example.app","pid":7}}`, &got))

	snap, err := d.AX(context.Background(), "UDID-1")
	if err != nil {
		t.Fatalf("ax: %v", err)
	}
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
	var got bridgeRequest
	d := newTestDriver(t, replyWith(t, `{"ok":true,"lifted":false}`, &got))
	events, err := Tap(Point{X: 0.5, Y: 0.5})
	if err != nil {
		t.Fatalf("tap: %v", err)
	}

	if _, err := d.Perform(context.Background(), "UDID-1", events); err != nil {
		t.Fatalf("perform: %v", err)
	}
	if len(got.Events) != len(events) || got.Events[0].Type != "begin" {
		t.Fatalf("events = %+v", got.Events)
	}
}

func TestDriver_ReportsARescuedFinger(t *testing.T) {
	// A gesture that ended without its own lift is not a success to swallow:
	// the caller has to be able to say the device was rescued.
	d := newTestDriver(t, replyWith(t, `{"ok":true,"lifted":true,"liftReason":"gesture ended without a lift"}`, nil))

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
	d := newTestDriver(t, replyWith(t, `{"ok":false,"error":{"code":"addon_load_failed","message":"symbol not found"}}`, nil))

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
	d := newTestDriver(t, replyWith(t, `{"ok":false,"error":{"code":"ax_unavailable","message":"no response"}}`, nil))

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
	d := newTestDriver(t, replyWith(t, `{"ok":true,"lifted":false}`, nil))

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
			d := newTestDriver(t, replyWith(t, payload, nil))
			if _, err := d.Perform(context.Background(), "UDID-1", nil); err == nil {
				t.Fatal("a bridge that said nothing usable must not read as a completed gesture")
			}
		})
	}
}

func TestDriver_ProcessFailureCarriesStderr(t *testing.T) {
	d := newTestDriver(t, func(context.Context, string, []string, []byte) ([]byte, []byte, error) {
		return nil, []byte("node: command failed"), errors.New("exit status 1")
	})

	_, err := d.AX(context.Background(), "UDID-1")
	if err == nil || !strings.Contains(err.Error(), "node: command failed") {
		t.Fatalf("err = %v, want the child's own diagnostics", err)
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

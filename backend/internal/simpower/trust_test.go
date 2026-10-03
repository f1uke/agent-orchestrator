package simpower

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/simslim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simtrust"
)

func writeTestCA(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "proxy-ca.pem")
	if err := os.WriteFile(path, []byte("-----BEGIN CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func isTrustCall(call []string) bool {
	return len(call) > 3 && call[1] == "simctl" && call[2] == "keychain"
}

func newTrustingPower(t *testing.T, rec *recorder) (*Power, *simtrust.Truster) {
	t.Helper()
	p := newTestPower(t, rec)
	tr := simtrust.NewTruster(rec.run)
	p.UseTruster(tr)
	return p, tr
}

// Trust is installed inside the boot, after the profile: `simslim on` reboots
// the device and `simctl keychain` needs it up, and a boot that settled before
// trusting would hand the first claim a device that still cannot reach the
// network through the proxy.
func TestBoot_TrustsTheCAsAfterSlimmingAndBeforeSettling(t *testing.T) {
	rec := &recorder{}
	p, tr := newTrustingPower(t, rec)
	ca := writeTestCA(t)
	setup := &Setup{
		Profile: &simslim.Request{Profile: &simslim.Profile{Keep: []string{"com.apple.apsd"}}},
		Trust:   &simtrust.Request{Files: []string{ca}},
	}

	if err := p.Start(context.Background(), testUDID, Boot, setup, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.wait()

	calls := rec.calls()
	last := calls[len(calls)-1]
	if !isTrustCall(last) || last[len(last)-1] != ca {
		t.Fatalf("last call = %v, want the CA installed after everything else: %v", last, calls)
	}
	if calls[1][0] != simslim.Binary {
		t.Fatalf("second call was not simslim, so trust did not wait for the profile: %v", calls)
	}
	got, ok := tr.Last(testUDID)
	if !ok || len(got.Trusted) != 1 || got.Trusted[0] != ca {
		t.Fatalf("truster remembers %+v, %v; want %s trusted", got, ok, ca)
	}
}

// A CA that will not install is reported on the device listing, never as a
// failed boot - the boot is what breaks the deadlock in which qa can never be
// created.
func TestBoot_AFailedTrustNeverFailsTheBoot(t *testing.T) {
	rec := &recorder{reply: func(_ context.Context, args []string) ([]byte, error) {
		if len(args) > 1 && args[1] == "keychain" {
			return []byte("Unable to add root certificate"), errors.New("exit status 1")
		}
		return nil, nil
	}}
	p, tr := newTrustingPower(t, rec)

	if err := p.Start(context.Background(), testUDID, Boot, &Setup{Trust: &simtrust.Request{Files: []string{writeTestCA(t)}}}, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.wait()

	if st, ok := p.Status(testUDID); ok {
		t.Fatalf("a boot whose trust failed left status %+v; the boot itself worked", st)
	}
	if got, _ := tr.Last(testUDID); len(got.Failed) != 1 {
		t.Fatalf("truster remembers %+v, want the failure", got)
	}
}

func TestBoot_ThatFailedTrustsNothing(t *testing.T) {
	rec := &recorder{reply: func(_ context.Context, args []string) ([]byte, error) {
		if len(args) > 1 && args[1] == "bootstatus" {
			return []byte("Unable to boot device"), errors.New("exit status 1")
		}
		return nil, nil
	}}
	p, tr := newTrustingPower(t, rec)

	if err := p.Start(context.Background(), testUDID, Boot, &Setup{Trust: &simtrust.Request{Files: []string{writeTestCA(t)}}}, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.wait()

	for _, c := range rec.calls() {
		if isTrustCall(c) {
			t.Fatalf("trusted a CA on a device that never came up: %v", c)
		}
	}
	if _, ok := tr.Last(testUDID); ok {
		t.Fatal("a failed boot recorded a trust pass")
	}
}

func TestShutdown_TrustsNothing(t *testing.T) {
	rec := &recorder{}
	p, _ := newTrustingPower(t, rec)

	if err := p.Start(context.Background(), testUDID, Shutdown, &Setup{Trust: &simtrust.Request{Files: []string{writeTestCA(t)}}}, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.wait()

	for _, c := range rec.calls() {
		if isTrustCall(c) {
			t.Fatalf("a shutdown trusted a CA: %v", c)
		}
	}
}

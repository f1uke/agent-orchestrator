package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

const testProxyCA = "/Users/you/Library/Application Support/com.proxyman.NSProxy/app-data/proxyman-ca.pem"

// A boot this command started reports what it trusted, read off the device
// listing the wait already polls.
func TestSimBoot_SaysWhichRootCAsTheBootTrusted(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	listing := bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown")
	listing.Trust = &simTrustClient{Trusted: []string{testProxyCA}, At: time.Now()}
	newSimPowerDaemon(t, cfg, listing)
	deps := simBootDeps(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Shutdown"))

	out, errOut, err := executeCLI(t, deps, "sim", "boot", "--udid", simUDIDProMax)
	if err != nil {
		t.Fatalf("sim boot failed: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, "Trusted root CA: "+testProxyCA) {
		t.Fatalf("output does not say what was trusted:\n%s", out)
	}
}

// The listing remembers a device's LAST pass. On a device that was already up
// that pass may be another session's claim, from a project that trusts
// something else - so a no-op boot must not present it as its own.
func TestSimBoot_AlreadyBootedDoesNotClaimSomebodyElsesTrustPass(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	cfg := setConfigEnv(t)
	listing := bootListing(simUDIDProMax, "iPhone 17 Pro Max", "Booted")
	listing.Trust = &simTrustClient{Trusted: []string{testProxyCA}, At: time.Now()}
	newSimPowerDaemon(t, cfg, listing)
	deps := simBootDeps(t, simDeviceFixture(simUDIDProMax, "iPhone 17 Pro Max", "Booted"))

	out, errOut, err := executeCLI(t, deps, "sim", "boot", "--udid", simUDIDProMax)
	if err != nil {
		t.Fatalf("sim boot failed: %v\nstderr=%s", err, errOut)
	}
	if strings.Contains(out, "Trusted root CA") {
		t.Fatalf("a no-op boot reported a trust pass it did not run:\n%s", out)
	}
}

func TestWriteSimClaim_SaysWhichRootCAsTheClaimTrusted(t *testing.T) {
	var out bytes.Buffer
	err := writeSimClaim(&out, simClaimResult{
		UDID: simUDIDProMax, Name: "iPhone 17 Pro Max", Runtime: "iOS 26.3", Holder: "mer-9",
		ExpiresAt: time.Now(), Note: simLeaseScopeNote,
		Trust: &simTrustClient{Trusted: []string{testProxyCA}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Trusted root CA: "+testProxyCA+"\n") {
		t.Fatalf("claim output does not say what was trusted:\n%s", out.String())
	}
}

// A CA that would not install is a warning that names the symptom - otherwise
// the splash-screen hang it causes reads like an app bug.
func TestWriteSimTrust_AFailureWarnsAndNamesTheSymptom(t *testing.T) {
	var out bytes.Buffer
	err := writeSimTrust(&out, &simTrustClient{Failed: []simTrustFailureClient{
		{File: testProxyCA, Reason: "Unable to add root certificate"},
		{Reason: "could not work out which root CAs to trust: project 7 is degraded"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"Warning: could not make this simulator trust " + testProxyCA + " - Unable to add root certificate.",
		"Warning: could not make this simulator trust its root CAs - could not work out which root CAs to trust: project 7 is degraded.",
		"HTTPS through this Mac's debugging proxy will fail on this device",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

// Nothing configured, or every configured file missing: not a word.
func TestWriteSimTrust_NothingToSayPrintsNothing(t *testing.T) {
	var out bytes.Buffer
	if err := writeSimTrust(&out, nil); err != nil || out.Len() != 0 {
		t.Fatalf("printed %q (err %v), want nothing", out.String(), err)
	}
}

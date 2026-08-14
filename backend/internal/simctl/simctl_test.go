package simctl_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
)

const twoRuntimes = `{"devices":{
  "com.apple.CoreSimulator.SimRuntime.iOS-26-3":[
    {"udid":"AAA","name":"iPhone 17 Pro Max","state":"Booted","isAvailable":true},
    {"udid":"BBB","name":"iPhone 17 Pro","state":"Shutdown","isAvailable":true}],
  "com.apple.CoreSimulator.SimRuntime.iOS-18-0":[
    {"udid":"CCC","name":"iPhone 15","state":"Shutdown","isAvailable":false}]}}`

func lister(out string, err error) simctl.Runner {
	return func(_ context.Context, _ string, _ ...string) ([]byte, error) { return []byte(out), err }
}

func found(string) (string, error) { return "/usr/bin/xcrun", nil }

func TestList_SortsByRuntimeAndDerivesLabel(t *testing.T) {
	devices, err := simctl.List(context.Background(), found, lister(twoRuntimes, nil))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// Runtimes sort by identifier; simctl's own order is kept within one.
	got := []string{}
	for _, d := range devices {
		got = append(got, fmt.Sprintf("%s/%s/%s", d.UDID, d.Runtime, d.State))
	}
	want := []string{"CCC/iOS 18.0/Shutdown", "AAA/iOS 26.3/Booted", "BBB/iOS 26.3/Shutdown"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("device %d: got %q, want %q", i, got[i], want[i])
		}
	}
	if devices[0].Available {
		t.Fatal("isAvailable=false must survive into Available")
	}
	if devices[0].RuntimeIdentifier != "com.apple.CoreSimulator.SimRuntime.iOS-18-0" {
		t.Fatalf("raw runtime identifier lost: %q", devices[0].RuntimeIdentifier)
	}
}

func TestList_MissingXcrunSaysSo(t *testing.T) {
	missing := func(string) (string, error) { return "", errors.New("nope") }
	_, err := simctl.List(context.Background(), missing, lister(twoRuntimes, nil))
	if err == nil || !errors.Is(err, simctl.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

// A non-zero exit with parsable, empty JSON is the one shape only an exit-code
// check can catch: slice 1 shipped this hole and a mutation check found it.
func TestList_ExitCodeIsNotSwallowed(t *testing.T) {
	_, err := simctl.List(context.Background(), found, lister(`{"devices":{}}`, errors.New("exit status 1")))
	if err == nil {
		t.Fatal("a failing simctl must be an error even when its output parses")
	}
}

func TestList_MalformedJSONIsAnError(t *testing.T) {
	if _, err := simctl.List(context.Background(), found, lister("not json", nil)); err == nil {
		t.Fatal("want an error for unparsable simctl output")
	}
}

func devices(states ...string) []simctl.Device {
	out := []simctl.Device{}
	for i, s := range states {
		out = append(out, simctl.Device{
			UDID: fmt.Sprintf("UDID-%d", i), Name: fmt.Sprintf("iPhone %d", i),
			Runtime: "iOS 26.3", State: s, Available: true,
		})
	}
	return out
}

func TestResolve_ExactlyOneBooted(t *testing.T) {
	d, err := simctl.Resolve(devices("Shutdown", "Booted"), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if d.UDID != "UDID-1" {
		t.Fatalf("picked %q", d.UDID)
	}
}

// The whole point of the rule: several booted devices must never resolve to
// one. The UI has to be at least as honest as the CLI here.
func TestResolve_SeveralBootedIsAmbiguousAndNamesThem(t *testing.T) {
	_, err := simctl.Resolve(devices("Booted", "Booted"), "")
	var amb *simctl.AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("want *AmbiguousError, got %v", err)
	}
	if len(amb.Booted) != 2 {
		t.Fatalf("want both booted devices carried, got %d", len(amb.Booted))
	}
}

func TestResolve_NoBootedAndNoDevicesAreDifferentErrors(t *testing.T) {
	if _, err := simctl.Resolve(devices("Shutdown"), ""); !errors.Is(err, simctl.ErrNoBooted) {
		t.Fatalf("want ErrNoBooted, got %v", err)
	}
	if _, err := simctl.Resolve(nil, ""); !errors.Is(err, simctl.ErrNoDevices) {
		t.Fatalf("want ErrNoDevices, got %v", err)
	}
}

func TestResolve_ExplicitUDIDIsCaseInsensitiveAndMustBeBooted(t *testing.T) {
	list := devices("Booted", "Shutdown")
	d, err := simctl.Resolve(list, "udid-0")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if d.UDID != "UDID-0" {
		t.Fatalf("case-insensitive match failed: %q", d.UDID)
	}
	var notBooted *simctl.NotBootedError
	if _, err := simctl.Resolve(list, "UDID-1"); !errors.As(err, &notBooted) {
		t.Fatalf("want *NotBootedError, got %v", err)
	}
	if notBooted.Device.State != "Shutdown" {
		t.Fatalf("the refusal must carry the real state, got %q", notBooted.Device.State)
	}
	if _, err := simctl.Resolve(list, "nope"); !errors.Is(err, simctl.ErrUnknownUDID) {
		t.Fatalf("want ErrUnknownUDID, got %v", err)
	}
}

func TestSummarize_MarksTheDefaultAndSaysWhyThereIsNone(t *testing.T) {
	one := simctl.Summarize(devices("Shutdown", "Booted"))
	if one.DefaultUDID == nil || *one.DefaultUDID != "UDID-1" {
		t.Fatalf("default not marked: %+v", one.DefaultUDID)
	}
	if !one.Devices[1].Default {
		t.Fatal("the chosen device must be flagged in the listing")
	}
	if one.DefaultReason != "the only booted simulator" {
		t.Fatalf("reason: %q", one.DefaultReason)
	}

	none := simctl.Summarize(devices("Booted", "Booted"))
	if none.DefaultUDID != nil {
		t.Fatal("two booted simulators must not produce a default")
	}
	if none.DefaultReason == "" {
		t.Fatal("a listing with no default must say why")
	}
}

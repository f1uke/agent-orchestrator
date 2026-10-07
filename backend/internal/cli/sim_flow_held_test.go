package cli

import (
	"context"
	"strings"
	"testing"
)

// heldFlowDeps is flowRunDeps on a device whose data directory is known, so
// the apps running on it can be told apart from another device's.
func heldFlowDeps(t *testing.T, rec *[]recordedCommand, rows ...string) Deps {
	t.Helper()
	deps, daemon := flowRunDeps(t, true, []byte("Flow passed\n"), nil, rec)
	grantSimLease(daemon, simUDIDProMax, "mer-9")
	listing := simDevicesJSON(t, simDeviceWithData(simUDIDProMax, "iPhone 17 Pro Max", "Booted", "/sim/promax/data"))
	inner := deps.CommandOutput
	deps.CommandOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if len(args) >= 3 && args[1] == "list" && args[2] == "devices" {
			return []byte(listing), nil
		}
		return inner(ctx, name, args...)
	}
	deps, _ = withProcesses(deps, rows...)
	return deps
}

const promaxApp = "/sim/promax/data/Containers/Bundle/Application/U/Nimbus.app/Nimbus"

// A stopped app answers no accessibility query, so a flow against it fails for
// a reason it cannot name; a breakpoint would freeze it mid-flow. Either way
// Maestro must not start.
func TestSimFlowRun_RefusesWhileAnAppOnTheDeviceIsHeld(t *testing.T) {
	cases := map[string][]string{
		"debugger": {
			"900 880 S+ /usr/bin/lldb",
			"950 900 S /Applications/Xcode.app/Contents/SharedFrameworks/LLDB.framework/Versions/A/Resources/debugserver",
			"601 950 SXs " + promaxApp,
		},
		"sigstop": {"601 1 Ts " + promaxApp},
	}
	wants := map[string]string{"debugger": "debugserver 950 (lldb 900)", "sigstop": "`kill -CONT 601`"}
	for name, rows := range cases {
		t.Run(name, func(t *testing.T) {
			var rec []recordedCommand
			deps := heldFlowDeps(t, &rec, rows...)
			_, _, err := executeCLI(t, deps, "sim", "flow", "run", writeFlowFile(t), "--udid", simUDIDProMax)
			if err == nil || !strings.Contains(err.Error(), wants[name]) {
				t.Fatalf("want a refusal naming %q, got %v", wants[name], err)
			}
			if len(rec) != 0 {
				t.Fatalf("maestro was started: %+v", rec)
			}
		})
	}
}

func TestSimFlowRun_AnotherDevicesHeldAppDoesNotStopIt(t *testing.T) {
	var rec []recordedCommand
	deps := heldFlowDeps(t, &rec,
		"601 1 Ss "+promaxApp,
		"701 950 SXs /sim/other/data/Containers/Bundle/Application/U/Nimbus.app/Nimbus",
	)
	if _, _, err := executeCLI(t, deps, "sim", "flow", "run", writeFlowFile(t), "--udid", simUDIDProMax); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(rec) != 1 {
		t.Fatalf("maestro runs = %d, want 1", len(rec))
	}
}

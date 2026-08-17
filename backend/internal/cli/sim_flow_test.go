package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type recordedCommand struct {
	env  []string
	name string
	args []string
}

// flowTestDeps wires LookPath and CommandOutputWithEnv to fakes and records
// every external invocation, which is the only way to assert the two things
// that matter most here: that --device is always passed, and that analytics are
// always disabled.
func flowTestDeps(t *testing.T, found bool, out []byte, runErr error, rec *[]recordedCommand) Deps {
	t.Helper()
	d := DefaultDeps()
	d.LookPath = func(file string) (string, error) {
		if file != "maestro" {
			return "", errors.New("unexpected lookup: " + file)
		}
		if !found {
			return "", errors.New("not found")
		}
		return "/usr/local/bin/maestro", nil
	}
	d.CommandOutputWithEnv = func(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
		*rec = append(*rec, recordedCommand{env: env, name: name, args: args})
		return out, runErr
	}
	return d
}

func writeFlowFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flow.yaml")
	if err := os.WriteFile(path, []byte("appId: ${APP_ID}\n---\n- launchApp\n"), 0o600); err != nil {
		t.Fatalf("write flow: %v", err)
	}
	return path
}

func TestSimFlowCheck_ShellsOutToCheckSyntax(t *testing.T) {
	var rec []recordedCommand
	deps := flowTestDeps(t, true, []byte("OK\n"), nil, &rec)
	path := writeFlowFile(t)

	out, _, err := executeCLI(t, deps, "sim", "flow", "check", path)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(rec) != 1 {
		t.Fatalf("ran %d commands, want 1", len(rec))
	}
	if rec[0].name != "/usr/local/bin/maestro" {
		t.Errorf("ran %q, want the looked-up maestro", rec[0].name)
	}
	if got, want := strings.Join(rec[0].args, " "), "check-syntax "+path; got != want {
		t.Errorf("args = %q, want %q", got, want)
	}
	if !strings.Contains(out, "OK") {
		t.Errorf("maestro's own output must be shown, got %q", out)
	}
}

// Maestro's analytics are on by default and its argv sanitiser transmits
// non-path-like flag values, which includes a udid. AO never lets that happen.
func TestSimFlowCheck_DisablesMaestroAnalytics(t *testing.T) {
	var rec []recordedCommand
	deps := flowTestDeps(t, true, []byte("OK\n"), nil, &rec)

	if _, _, err := executeCLI(t, deps, "sim", "flow", "check", writeFlowFile(t)); err != nil {
		t.Fatalf("check: %v", err)
	}
	var found bool
	for _, e := range rec[0].env {
		if e == "MAESTRO_CLI_NO_ANALYTICS=1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("MAESTRO_CLI_NO_ANALYTICS=1 not in env %v", rec[0].env)
	}
}

func TestSimFlowCheck_MissingMaestroSaysSoAndDoesNotInstall(t *testing.T) {
	var rec []recordedCommand
	deps := flowTestDeps(t, false, nil, nil, &rec)

	_, _, err := executeCLI(t, deps, "sim", "flow", "check", writeFlowFile(t))
	if err == nil {
		t.Fatal("want an error when maestro is absent")
	}
	if !strings.Contains(err.Error(), "maestro") {
		t.Errorf("error must name the missing tool, got %q", err)
	}
	// The point of the message: the rest of `ao sim` is unaffected.
	if !strings.Contains(err.Error(), "ao sim ax --format maestro") {
		t.Errorf("error must point at what still works, got %q", err)
	}
	if len(rec) != 0 {
		t.Errorf("must not run anything, ran %v", rec)
	}
}

func TestSimFlowCheck_SyntaxErrorIsReportedWithMaestrosOwnWords(t *testing.T) {
	var rec []recordedCommand
	deps := flowTestDeps(t, true, []byte("Invalid Command: notACommand at /flow.yaml:3:1\n"), errors.New("exit status 1"), &rec)

	_, _, err := executeCLI(t, deps, "sim", "flow", "check", writeFlowFile(t))
	if err == nil {
		t.Fatal("want an error when the flow does not parse")
	}
	if !strings.Contains(err.Error(), "Invalid Command: notACommand") {
		t.Errorf("must surface maestro's own diagnostic, got %q", err)
	}
}

// Deps.withDefaults must fill in every function member: a partial Deps is the
// normal way callers and tests construct one, and a nil member is a panic at
// the moment the command runs rather than an error anyone can read.
// CommandOutputWithEnv was missed once already.
func TestDepsWithDefaults_FillsCommandOutputWithEnv(t *testing.T) {
	got := Deps{}.withDefaults()
	if got.CommandOutputWithEnv == nil {
		t.Fatal("withDefaults left CommandOutputWithEnv nil; `ao sim flow` would panic on a partial Deps")
	}
}

func TestSimFlowCheck_MissingFileIsRefusedBeforeRunningAnything(t *testing.T) {
	var rec []recordedCommand
	deps := flowTestDeps(t, true, []byte("OK\n"), nil, &rec)

	_, _, err := executeCLI(t, deps, "sim", "flow", "check", filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("want an error for a missing file")
	}
	if len(rec) != 0 {
		t.Errorf("must not run maestro for a file that is not there, ran %v", rec)
	}
}

// --- ao sim flow run --------------------------------------------------------

// flowRunDeps is touchDeps (a booted-only device listing, a fake driver and a
// live fake daemon) plus maestro faked the same way flowTestDeps fakes it for
// `check`: LookPath composes onto touchDeps' own (xcrun must still resolve),
// and CommandOutputWithEnv records every invocation.
func flowRunDeps(t *testing.T, found bool, out []byte, runErr error, rec *[]recordedCommand) (Deps, *simDaemon) {
	t.Helper()
	deps, daemon := touchDeps(t, &fakeSimDriver{})
	lookXcrun := deps.LookPath
	deps.LookPath = func(file string) (string, error) {
		if file != "maestro" {
			return lookXcrun(file)
		}
		if !found {
			return "", errors.New("not found")
		}
		return "/usr/local/bin/maestro", nil
	}
	deps.CommandOutputWithEnv = func(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
		*rec = append(*rec, recordedCommand{env: env, name: name, args: args})
		return out, runErr
	}
	return deps, daemon
}

// grantSimLease makes daemon report udid as held by holder, the same shape
// sim_lease_test.go's own tests use.
func grantSimLease(daemon *simDaemon, udid, holder string) {
	daemon.leases[udid] = simLeaseClient{
		UDID: udid, SessionID: holder,
		AcquiredAt: simFixedNow, ExpiresAt: simFixedNow.Add(10 * time.Minute),
	}
}

// The whole point of the command: Maestro must never be left to pick a device.
// Without --device it takes the single connected simulator - the one a human is
// working on - or boots one and reboots it after rewriting its locale.
func TestSimFlowRun_AlwaysPassesDeviceExplicitly(t *testing.T) {
	var rec []recordedCommand
	deps, daemon := flowRunDeps(t, true, []byte("Flow passed\n"), nil, &rec)
	grantSimLease(daemon, simUDIDProMax, "mer-9") // touchDeps sets AO_SESSION_ID=mer-9
	path := writeFlowFile(t)

	if _, _, err := executeCLI(t, deps, "sim", "flow", "run", path, "--udid", simUDIDProMax); err != nil {
		t.Fatalf("run: %v", err)
	}
	args := strings.Join(rec[0].args, " ")
	if !strings.Contains(args, "--device "+simUDIDProMax) {
		t.Fatalf("args = %q, must pin --device %s", args, simUDIDProMax)
	}
	if !strings.HasPrefix(args, "test ") {
		t.Errorf("args = %q, want the `test` subcommand", args)
	}
	if !strings.HasSuffix(args, path) {
		t.Errorf("args = %q, want the flow file last", args)
	}
}

func TestSimFlowRun_RefusesWhenThisSessionHasNoLease(t *testing.T) {
	var rec []recordedCommand
	deps, _ := flowRunDeps(t, true, []byte("Flow passed\n"), nil, &rec) // nobody holds it

	_, _, err := executeCLI(t, deps, "sim", "flow", "run", writeFlowFile(t), "--udid", simUDIDProMax)
	if err == nil {
		t.Fatal("want a refusal without a lease")
	}
	if !strings.Contains(err.Error(), "ao sim claim") {
		t.Errorf("must say how to fix it, got %q", err)
	}
	if len(rec) != 0 {
		t.Errorf("must not run maestro, ran %v", rec)
	}
}

func TestSimFlowRun_RefusesWhenAnotherSessionHoldsTheDevice(t *testing.T) {
	var rec []recordedCommand
	deps, daemon := flowRunDeps(t, true, []byte("Flow passed\n"), nil, &rec)
	grantSimLease(daemon, simUDIDProMax, "other-7") // touchDeps' session is mer-9

	_, _, err := executeCLI(t, deps, "sim", "flow", "run", writeFlowFile(t), "--udid", simUDIDProMax)
	if err == nil {
		t.Fatal("want a refusal when someone else holds it")
	}
	if !strings.Contains(err.Error(), "other-7") {
		t.Errorf("must name the holder, got %q", err)
	}
	if len(rec) != 0 {
		t.Errorf("must not run maestro, ran %v", rec)
	}
}

func TestSimFlowRun_FailingFlowSurfacesMaestrosOutput(t *testing.T) {
	var rec []recordedCommand
	deps, daemon := flowRunDeps(t, true,
		[]byte("Assert that id: NOPE is visible... FAILED\nAssertion is false: id: NOPE is visible\n"),
		errors.New("exit status 1"), &rec)
	grantSimLease(daemon, simUDIDProMax, "mer-9")

	_, _, err := executeCLI(t, deps, "sim", "flow", "run", writeFlowFile(t), "--udid", simUDIDProMax)
	if err == nil {
		t.Fatal("a failing flow must fail the command")
	}
	if !strings.Contains(err.Error(), "Assertion is false") {
		t.Errorf("must surface maestro's own failure, got %q", err)
	}
}

func TestSimFlowRun_DisablesMaestroAnalytics(t *testing.T) {
	var rec []recordedCommand
	deps, daemon := flowRunDeps(t, true, []byte("Flow passed\n"), nil, &rec)
	grantSimLease(daemon, simUDIDProMax, "mer-9")

	if _, _, err := executeCLI(t, deps, "sim", "flow", "run", writeFlowFile(t), "--udid", simUDIDProMax); err != nil {
		t.Fatalf("run: %v", err)
	}
	var found bool
	for _, e := range rec[0].env {
		if e == "MAESTRO_CLI_NO_ANALYTICS=1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("MAESTRO_CLI_NO_ANALYTICS=1 not in env %v", rec[0].env)
	}
}

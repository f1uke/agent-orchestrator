package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

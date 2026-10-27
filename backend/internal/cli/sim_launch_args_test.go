package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func sameStrings(got, want []string) bool {
	return strings.Join(got, "\x00") == strings.Join(want, "\x00")
}

func TestSimLaunch_PassesTheArgumentsAfterTheDashVerbatim(t *testing.T) {
	deps, _, dataPath, _ := appDeps(t)
	installFixture(t, dataPath, "MyApp", "com.example.MyApp", "1.0", "1", "build A")
	deps, launches := withConsoleLaunch(deps)

	_, errOut, err := executeCLI(t, deps, "sim", "launch", "com.example.MyApp", "--", "-SomeFlag", "YES", "-Two words")
	if err != nil {
		t.Fatalf("launch failed: %v\nstderr=%s", err, errOut)
	}
	want := []string{"xcrun", "simctl", "launch", simUDIDProMax, "com.example.MyApp", "-SomeFlag", "YES", "-Two words"}
	if len(launches.calls) != 1 || !sameStrings(launches.calls[0], want) {
		t.Fatalf("launched %q, want %q", launches.calls, want)
	}
}

func TestSimLaunch_ArgumentsWithoutABundleIDStillLaunchTheDefaultApp(t *testing.T) {
	deps, _, dataPath, _ := appDeps(t)
	installFixture(t, dataPath, "MyApp", "com.example.MyApp", "1.0", "1", "build A")
	deps, launches := withConsoleLaunch(deps)

	if _, errOut, err := executeCLI(t, deps, "sim", "launch", "--", "-Flag"); err != nil {
		t.Fatalf("launch failed: %v\nstderr=%s", err, errOut)
	}
	want := []string{"xcrun", "simctl", "launch", simUDIDProMax, "com.example.MyApp", "-Flag"}
	if len(launches.calls) != 1 || !sameStrings(launches.calls[0], want) {
		t.Fatalf("launched %q, want %q", launches.calls, want)
	}
}

func TestSimLaunch_RefusesTwoBundleIDsBeforeTheDash(t *testing.T) {
	deps, _, dataPath, _ := appDeps(t)
	installFixture(t, dataPath, "MyApp", "com.example.MyApp", "1.0", "1", "build A")
	deps, launches := withConsoleLaunch(deps)

	_, _, err := executeCLI(t, deps, "sim", "launch", "com.example.MyApp", "-Flag-without-dash", "--", "-X")
	if err == nil || len(launches.calls) != 0 {
		t.Fatalf("err = %v, launches = %q; a second word before -- must be refused", err, launches.calls)
	}
}

func TestSimLaunch_EachEnvBecomesASimctlChildVariable(t *testing.T) {
	deps, _, dataPath, _ := appDeps(t)
	installFixture(t, dataPath, "MyApp", "com.example.MyApp", "1.0", "1", "build A")
	deps, launches := withConsoleLaunch(deps)

	if _, errOut, err := executeCLI(t, deps, "sim", "launch",
		"--env", "API_HOST=https://int.example.com/a,b", "--env", "FEATURE=x=y"); err != nil {
		t.Fatalf("launch failed: %v\nstderr=%s", err, errOut)
	}
	want := []string{"SIMCTL_CHILD_API_HOST=https://int.example.com/a,b", "SIMCTL_CHILD_FEATURE=x=y"}
	if len(launches.envs) != 1 || !sameStrings(launches.envs[0], want) {
		t.Fatalf("env = %q, want %q", launches.envs, want)
	}
}

func TestSimLaunch_RefusesAMalformedEnvBeforeTakingTheDevice(t *testing.T) {
	for _, tc := range []struct{ value, says string }{
		{"s3cr3t", "KEY=VALUE"},
		{"=s3cr3t", "empty key"},
		{"A=s3cr3t", "more than once"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			deps, daemon, dataPath, calls := appDeps(t)
			installFixture(t, dataPath, "MyApp", "com.example.MyApp", "1.0", "1", "build A")
			deps, launches := withConsoleLaunch(deps)

			_, _, err := executeCLI(t, deps, "sim", "launch", "--env", "A=1", "--env", tc.value)
			if err == nil || !strings.Contains(err.Error(), tc.says) || !strings.Contains(err.Error(), "--env") {
				t.Fatalf("err = %v, want it to name --env and say %q", err, tc.says)
			}
			if strings.Contains(err.Error(), "s3cr3t") {
				t.Fatalf("the error echoed what was passed, which may be a secret: %v", err)
			}
			if simCalled(daemon, "POST /api/v1/sessions/mer-9/sim-leases") || len(launches.calls) != 0 || ranSimctl(*calls, "launch") != nil {
				t.Fatalf("a refused --env still took the device or launched:\n%s", daemon.callLog())
			}
		})
	}
}

func TestSimLaunch_ConsoleCarriesTheArgumentsAndEnv(t *testing.T) {
	deps, _, dataPath, _ := appDeps(t)
	installFixture(t, dataPath, "Nimbus", "com.example.Nimbus", "1.0", "1", "nimbus")
	deps, launches := withConsoleLaunch(deps)
	logPath, _ := consolePaths(t, simUDIDProMax, "com.example.Nimbus")

	if _, errOut, err := executeCLI(t, deps, "sim", "launch", "com.example.Nimbus", "--console",
		"--env", "TOKEN=abc", "--", "-Flag", "YES"); err != nil {
		t.Fatalf("launch --console failed: %v\n%s", err, errOut)
	}
	want := []string{"xcrun", "simctl", "launch", "--terminate-running-process",
		"--stdout=" + logPath, "--stderr=" + logPath, simUDIDProMax, "com.example.Nimbus", "-Flag", "YES"}
	if len(launches.calls) != 1 || !sameStrings(launches.calls[0], want) {
		t.Fatalf("launched %q, want %q", launches.calls, want)
	}
	if !sameStrings(launches.envs[0], []string{"SIMCTL_CHILD_NSUnbufferedIO=YES", "SIMCTL_CHILD_TOKEN=abc"}) {
		t.Fatalf("env = %q", launches.envs[0])
	}
}

func TestSimLaunch_TerminateFirstCarriesTheArgumentsAndEnv(t *testing.T) {
	deps, _, dataPath, calls := appDeps(t)
	installFixture(t, dataPath, "MyApp", "com.example.MyApp", "1.0", "1", "build A")
	deps, launches := withConsoleLaunch(deps)

	if _, errOut, err := executeCLI(t, deps, "sim", "launch", "--terminate-first",
		"--env", "K=v", "--", "-Flag"); err != nil {
		t.Fatalf("launch failed: %v\nstderr=%s", err, errOut)
	}
	if ranSimctl(*calls, "terminate") == nil {
		t.Fatal("--terminate-first did not terminate")
	}
	want := []string{"xcrun", "simctl", "launch", simUDIDProMax, "com.example.MyApp", "-Flag"}
	if len(launches.calls) != 1 || !sameStrings(launches.calls[0], want) || !sameStrings(launches.envs[0], []string{"SIMCTL_CHILD_K=v"}) {
		t.Fatalf("launched %q with env %q", launches.calls, launches.envs)
	}
}

func TestSimLaunch_ReportsTheArgumentsAndEnvKeysButNeverTheValues(t *testing.T) {
	const secret = "s3cr3t-token-value"
	run := func(t *testing.T, extra ...string) string {
		t.Helper()
		deps, _, dataPath, _ := appDeps(t)
		installFixture(t, dataPath, "MyApp", "com.example.MyApp", "1.0", "1", "build A")
		deps, _ = withConsoleLaunch(deps)
		args := append([]string{"sim", "launch", "--env", "API_TOKEN=" + secret, "--env", "MODE=debug"}, extra...)
		args = append(args, "--", "-Flag", "YES")
		out, errOut, err := executeCLI(t, deps, args...)
		if err != nil {
			t.Fatalf("launch failed: %v\nstderr=%s", err, errOut)
		}
		if strings.Contains(out+errOut, secret) || strings.Contains(out+errOut, "debug") {
			t.Fatalf("an env value was printed:\n%s%s", out, errOut)
		}
		return out
	}

	var result struct {
		Args    []string `json:"args"`
		EnvKeys []string `json:"envKeys"`
	}
	if err := json.Unmarshal([]byte(run(t, "--json")), &result); err != nil {
		t.Fatal(err)
	}
	if !sameStrings(result.Args, []string{"-Flag", "YES"}) || !sameStrings(result.EnvKeys, []string{"API_TOKEN", "MODE"}) {
		t.Fatalf("json = %+v", result)
	}

	text := run(t)
	for _, want := range []string{"-Flag YES", "API_TOKEN", "MODE"} {
		if !strings.Contains(text, want) {
			t.Errorf("launch output does not say %q:\n%s", want, text)
		}
	}
}

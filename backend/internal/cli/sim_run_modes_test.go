package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/iosrun"
)

func TestSimRun_BuildOnlyTouchesNoSimulator(t *testing.T) {
	deps, daemon, calls, builds := configuredRunDeps(t, `"Nter"`, `"Dev"`)
	dir := t.TempDir()
	t.Setenv(iosrun.EnvRunDir, dir)

	out, errOut, err := executeCLI(t, deps, "sim", "run", "--build-only", "--attach")
	if err != nil {
		t.Fatalf("sim run --build-only failed: %v\nstderr=%s", err, errOut)
	}
	if len(*builds) != 1 || !strings.Contains(out, "Built Nter (Dev).") {
		t.Fatalf("builds=%d out=%q", len(*builds), out)
	}
	for _, verb := range []string{"boot", "install", "launch", "terminate"} {
		if ranSimctl(*calls, verb) != nil {
			t.Fatalf("a build-only run ran simctl %s", verb)
		}
	}
	if len(daemon.leases) != 0 {
		t.Fatalf("a build-only run took a lease: %v", daemon.leases)
	}
	if verdict := readRunVerdict(t, filepath.Join(dir, iosrun.ResultFile)); verdict.State != iosrun.RunSucceeded || verdict.Summary != "Built Nter (Dev)." {
		t.Fatalf("verdict = %+v", verdict)
	}
}

func TestSimRun_NoBuildInstallsTheLastBuild(t *testing.T) {
	deps, _, calls, builds := configuredRunDeps(t, `"Nter"`, `"Dev"`)
	if _, errOut, err := executeCLI(t, deps, "sim", "run", "--no-build"); err != nil {
		t.Fatalf("sim run --no-build failed: %v\nstderr=%s", err, errOut)
	}
	if len(*builds) != 0 {
		t.Fatalf("--no-build ran %d builds", len(*builds))
	}
	if ranSimctl(*calls, "install") == nil || ranSimctl(*calls, "launch") == nil {
		t.Fatal("--no-build must still install and launch")
	}
}

func TestSimRun_NoBuildRefusesWhenNothingWasBuilt(t *testing.T) {
	deps, _, calls, _ := configuredRunDeps(t, `"Nter"`, `"Dev"`)
	settingsSay(&deps, map[string]string{"TARGET_BUILD_DIR": filepath.Join(t.TempDir(), "missing"), "FULL_PRODUCT_NAME": "Nter.app"})
	_, _, err := executeCLI(t, deps, "sim", "run", "--no-build")
	if err == nil || !strings.Contains(err.Error(), "no build of Nter (Dev) to run yet") {
		t.Fatalf("err = %v", err)
	}
	if ranSimctl(*calls, "install") != nil {
		t.Fatal("nothing to install, so nothing was installed")
	}
}

func TestSimRun_NoBuildCannotBeClean(t *testing.T) {
	deps, _, _, _ := configuredRunDeps(t, `"Nter"`, `"Dev"`)
	if _, _, err := executeCLI(t, deps, "sim", "run", "--no-build", "--clean"); err == nil {
		t.Fatal("--no-build --clean would delete the build it is asked to run")
	}
}

// settingsSay makes -showBuildSettings answer with these settings.
func settingsSay(deps *Deps, settings map[string]string) {
	inner := deps.CommandOutputInDir
	deps.CommandOutputInDir = func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "-showBuildSettings") {
			return json.Marshal([]map[string]any{{"buildSettings": settings}})
		}
		return inner(ctx, dir, name, args...)
	}
}

func TestSimRun_CleanDeletesOnlyThisWorktreesDerivedData(t *testing.T) {
	deps, _, _, builds := configuredRunDeps(t, `"Nter"`, `"Dev"`)
	worktree, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dd := filepath.Join(worktree, "derivedDataPath")
	if err := os.MkdirAll(filepath.Join(dd, "Build", "Products"), 0o750); err != nil {
		t.Fatal(err)
	}
	products := t.TempDir()
	if err := os.MkdirAll(filepath.Join(products, "Nter.app"), 0o750); err != nil {
		t.Fatal(err)
	}
	settingsSay(&deps, map[string]string{
		"BUILD_DIR": filepath.Join(dd, "Build", "Products"), "TARGET_BUILD_DIR": products, "FULL_PRODUCT_NAME": "Nter.app",
	})

	if _, errOut, err := executeCLI(t, deps, "sim", "run", "--clean", "--build-only"); err != nil {
		t.Fatalf("sim run --clean failed: %v\nstderr=%s", err, errOut)
	}
	if _, err := os.Stat(dd); !os.IsNotExist(err) {
		t.Fatalf("%s survived the clean: %v", dd, err)
	}
	if len(*builds) != 1 {
		t.Fatalf("a clean build builds once after cleaning, ran %d", len(*builds))
	}
}

func TestSimRun_CleanRefusesADerivedDataFolderThatIsNotItsOwn(t *testing.T) {
	deps, _, _, builds := configuredRunDeps(t, `"Nter"`, `"Dev"`)
	shared := filepath.Join(t.TempDir(), "DerivedData")
	if err := os.MkdirAll(filepath.Join(shared, "Build", "Products"), 0o750); err != nil {
		t.Fatal(err)
	}
	settingsSay(&deps, map[string]string{
		"BUILD_DIR": filepath.Join(shared, "Build", "Products"), "TARGET_BUILD_DIR": t.TempDir(), "FULL_PRODUCT_NAME": "Nter.app",
	})

	_, _, err := executeCLI(t, deps, "sim", "run", "--clean", "--build-only")
	if err == nil || !strings.Contains(err.Error(), "nothing was cleaned") {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if _, err := os.Stat(shared); err != nil {
		t.Fatalf("the shared DerivedData root was touched: %v", err)
	}
	if len(*builds) != 0 {
		t.Fatal("a refused clean builds nothing")
	}
}

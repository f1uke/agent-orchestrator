package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// releaseWebEnv points the release web at a throwaway directory beside a
// throwaway data dir and returns the directory.
func releaseWebEnv(t *testing.T) string {
	t.Helper()
	cfg := setConfigEnv(t)
	t.Setenv("AO_RELEASE_WEB_DIR", "")
	t.Setenv("AO_RELEASE_WEB_ASSETS", "")
	return filepath.Join(filepath.Dir(cfg.dataDir), "release-web")
}

// secretInKeyFile reads the raw key the way a person would paste it.
func secretInKeyFile(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "key.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(b, &f); err != nil || len(f.Key) < 16 {
		t.Fatal("key.json holds no key")
	}
	return f.Key
}

func failingExecutable() Deps {
	return Deps{Executable: func() (string, error) { return "/bin/false", nil }}
}

func TestReleaseWebRotateKeyAndStatusNeverPrintTheKey(t *testing.T) {
	dir := releaseWebEnv(t)

	out, errOut, err := executeCLI(t, failingExecutable(), "release-web", "rotate-key")
	if err != nil {
		t.Fatalf("rotate-key: %v\n%s", err, errOut)
	}
	key := secretInKeyFile(t, dir)
	for _, want := range []string{"generation 1", filepath.Join(dir, "key.json"), "everyone is signed out"} {
		if !strings.Contains(out, want) {
			t.Errorf("rotate-key output lacks %q:\n%s", want, out)
		}
	}
	all := out + errOut

	text, errOut, err := executeCLI(t, failingExecutable(), "release-web", "status")
	if err != nil {
		t.Fatal(err)
	}
	asJSON, errOut2, err := executeCLI(t, failingExecutable(), "release-web", "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	all += text + errOut + asJSON + errOut2
	if strings.Contains(all, key) {
		t.Fatal("a command printed the key")
	}
	if !strings.Contains(text, "release web: stopped") || !strings.Contains(text, "key: generation 1") {
		t.Errorf("status =\n%s", text)
	}
	var st struct {
		Running       bool `json:"running"`
		KeyPresent    bool `json:"keyPresent"`
		KeyGeneration int  `json:"keyGeneration"`
	}
	if err := json.Unmarshal([]byte(asJSON), &st); err != nil || st.Running || !st.KeyPresent || st.KeyGeneration != 1 {
		t.Fatalf("status --json = %s (%v)", asJSON, err)
	}

	if _, _, err := executeCLI(t, failingExecutable(), "release-web", "rotate-key"); err != nil {
		t.Fatal(err)
	}
	if secretInKeyFile(t, dir) == key {
		t.Fatal("rotate-key kept the key")
	}
}

func TestReleaseWebStartCreatesAKeyWithoutPrintingIt(t *testing.T) {
	dir := releaseWebEnv(t)
	// /bin/false never listens, so start fails after it has made the key.
	out, errOut, err := executeCLI(t, failingExecutable(), "release-web", "start")
	if err == nil {
		t.Fatal("start with a binary that never listens succeeded")
	}
	key := secretInKeyFile(t, dir)
	if !strings.Contains(out, "created a key at") {
		t.Errorf("start did not say it made a key:\n%s", out)
	}
	if strings.Contains(out+errOut+err.Error(), key) {
		t.Fatal("start printed the key")
	}
}

func TestReleaseWebStopWithNothingRunning(t *testing.T) {
	releaseWebEnv(t)
	out, _, err := executeCLI(t, failingExecutable(), "release-web", "stop")
	if err != nil || !strings.Contains(out, "stopped") {
		t.Fatalf("stop = %q, %v", out, err)
	}
}

func TestReleaseWebServeNeedsAKeyAndABundle(t *testing.T) {
	releaseWebEnv(t)
	assets := t.TempDir()

	_, _, err := executeCLI(t, failingExecutable(), "release-web", "serve", "--assets", assets)
	if err == nil || !strings.Contains(err.Error(), "rotate-key") {
		t.Fatalf("serve without a key = %v, want one that names rotate-key", err)
	}

	_, _, err = executeCLI(t, failingExecutable(), "release-web", "serve", "--assets", filepath.Join(assets, "missing"))
	if err == nil || !strings.Contains(err.Error(), "--assets") {
		t.Fatalf("serve without a bundle = %v, want one that names --assets", err)
	}

	_, _, err = executeCLI(t, failingExecutable(), "release-web", "serve", "--assets", assets, "--port", "0")
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("serve --port 0 = %v, want a usage error", err)
	}
}

func TestReleaseWebHelpSaysLoopbackAndThatExposingItIsTheHumansStep(t *testing.T) {
	releaseWebEnv(t)
	out, _, err := executeCLI(t, failingExecutable(), "release-web", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"127.0.0.1", "Tailscale Funnel", "your step"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
}

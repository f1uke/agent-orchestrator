package daemon

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/releaseweb"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

var webFixtureAt = time.Date(2026, 10, 15, 3, 0, 0, 0, time.UTC)

func quietLogger(buf *bytes.Buffer) *slog.Logger {
	var w io.Writer = io.Discard
	if buf != nil {
		w = buf
	}
	return slog.New(slog.NewTextHandler(w, nil))
}

// appDBWithHumanState builds a stand-in for the app's ao.db holding one plan
// and one hide, and returns its directory.
func appDBWithHumanState(t *testing.T) (dir string, app *sqlite.Store) {
	t.Helper()
	dir = t.TempDir()
	app, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	train := rt.TrainID{Number: 22, Year: 2026}
	plan := rt.LlbPlan{
		ID: "demo-llb", Name: "Demo cards", TargetKind: rt.TargetTrain, TargetTrain: &train,
		Branches:  []rt.PlanBranch{{App: "da", Branch: "feature/demo-llb"}},
		Accepted:  []rt.PlanAccepted{},
		UpdatedBy: "cli", UpdatedAt: webFixtureAt,
	}
	if _, err := app.PutReleaseLlbPlan(ctx, plan, 0); err != nil {
		t.Fatal(err)
	}
	hide := rt.Hide{Fingerprint: "a1b2c3", Severity: rt.SevRisk, Reason: "waiting on the approver", HiddenAt: webFixtureAt}
	if err := app.HideReleaseHit(ctx, "D3~v/21-2026/di", hide); err != nil {
		t.Fatal(err)
	}
	return dir, app
}

func newWebStore(t *testing.T, appDir string, logs *bytes.Buffer) releaseWebStore {
	t.Helper()
	web, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = web.Close() })
	return newReleaseWebStore(web, &appHumanState{path: filepath.Join(appDir, "ao.db"), log: quietLogger(logs)})
}

func TestReleaseWebStoreReadsTheAppsHumanStateFromAReadOnlyDB(t *testing.T) {
	appDir, app := appDBWithHumanState(t)
	// The app is still running: its connections are open and its WAL is live.
	t.Cleanup(func() { _ = app.Close() })
	store := newWebStore(t, appDir, nil)

	human, err := store.LoadReleaseHumanState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(human.Plans) != 1 || human.Plans[0].ID != "demo-llb" {
		t.Fatalf("plans = %+v, want the app's demo-llb plan", human.Plans)
	}
	if h := human.Hides["D3~v/21-2026/di"]; h.Reason != "waiting on the approver" {
		t.Fatalf("hides = %+v, want the app's hide", human.Hides)
	}
	plans, err := store.ListReleaseLlbPlans(context.Background())
	if err != nil || len(plans) != 1 {
		t.Fatalf("ListReleaseLlbPlans = %+v, %v", plans, err)
	}
}

func TestReleaseWebStoreReadsAnAppDBThatIsClosed(t *testing.T) {
	appDir, app := appDBWithHumanState(t)
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	store := newWebStore(t, appDir, nil)
	human, err := store.LoadReleaseHumanState(context.Background())
	if err != nil || len(human.Plans) != 1 || len(human.Hides) != 1 {
		t.Fatalf("human state = %+v, %v", human, err)
	}
}

func TestReleaseWebStoreRefusesEveryWriteAndLeavesTheAppDBAlone(t *testing.T) {
	appDir, app := appDBWithHumanState(t)
	t.Cleanup(func() { _ = app.Close() })
	store := newWebStore(t, appDir, nil)
	ctx := context.Background()
	if _, err := store.LoadReleaseHumanState(ctx); err != nil {
		t.Fatal(err)
	}
	on := true
	writes := map[string]error{
		"SetReleaseRuleSetting": store.SetReleaseRuleSetting(ctx, "C2", &on, webFixtureAt),
		"HideReleaseHit":        store.HideReleaseHit(ctx, "C2~v/21-2026/da", rt.Hide{Fingerprint: "ffff", HiddenAt: webFixtureAt}),
		"MarkReleaseCardNoMR":   store.MarkReleaseCardNoMR(ctx, "ABC-1", rt.NoMRMark{MarkedBy: "x", MarkedAt: webFixtureAt}),
	}
	_, writes["UnhideReleaseHits"] = store.UnhideReleaseHits(ctx, []rt.HitID{"D3~v/21-2026/di"})
	_, writes["PutReleaseLlbPlan"] = store.PutReleaseLlbPlan(ctx, rt.LlbPlan{ID: "other", Name: "Other"}, 0)
	_, writes["UnmarkReleaseCardNoMR"] = store.UnmarkReleaseCardNoMR(ctx, "ABC-1")
	for name, err := range writes {
		if !errors.Is(err, ErrReleaseWebReadOnly) {
			t.Errorf("%s err = %v, want ErrReleaseWebReadOnly", name, err)
		}
	}

	fromApp, err := app.LoadReleaseHumanState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fromApp.Plans) != 1 || len(fromApp.Hides) != 1 || len(fromApp.RuleSettings) != 0 || len(fromApp.NoMR) != 0 {
		t.Fatalf("app db changed: %+v", fromApp)
	}
}

func TestReleaseWebStoreCannotWriteThroughItsAppConnection(t *testing.T) {
	appDir, app := appDBWithHumanState(t)
	t.Cleanup(func() { _ = app.Close() })
	reader := &appHumanState{path: filepath.Join(appDir, "ao.db"), log: quietLogger(nil)}
	if _, err := reader.LoadReleaseHumanState(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := reader.store.HideReleaseHit(context.Background(), "C2~v/21-2026/da", rt.Hide{Fingerprint: "ffff", HiddenAt: webFixtureAt})
	if err == nil {
		t.Fatal("the connection to the app db accepted a write")
	}
}

func TestReleaseWebStoreIsEmptyWithoutAnAppDBAndPicksItUpWhenItAppears(t *testing.T) {
	appDir := t.TempDir()
	var logs bytes.Buffer
	store := newWebStore(t, appDir, &logs)
	ctx := context.Background()
	for range 3 {
		human, err := store.LoadReleaseHumanState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(human.Plans) != 0 || len(human.Hides) != 0 || human.RuleSettings == nil || human.NoMR == nil {
			t.Fatalf("human state without an app db = %+v, want empty with usable maps", human)
		}
	}
	if n := strings.Count(logs.String(), "release web:"); n != 1 {
		t.Fatalf("logged %d times, want once:\n%s", n, logs.String())
	}
	if _, err := os.Stat(filepath.Join(appDir, "ao.db")); err == nil {
		t.Fatal("the web created the app db")
	}

	app, err := sqlite.Open(appDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	if err := app.HideReleaseHit(ctx, "D3~v/21-2026/di", rt.Hide{Fingerprint: "a1b2c3", Severity: rt.SevInfo, HiddenAt: webFixtureAt}); err != nil {
		t.Fatal(err)
	}
	human, err := store.LoadReleaseHumanState(ctx)
	if err != nil || len(human.Hides) != 1 {
		t.Fatalf("after the app db appeared: %+v, %v", human, err)
	}
}

func TestReleaseWebStoreIsEmptyForAnUnreadableAppDB(t *testing.T) {
	appDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(appDir, "ao.db"), []byte("not a database, long enough to be a header......"), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	store := newWebStore(t, appDir, &logs)
	human, err := store.LoadReleaseHumanState(context.Background())
	if err != nil || len(human.Plans) != 0 || len(human.Hides) != 0 {
		t.Fatalf("human state = %+v, %v", human, err)
	}
	if !strings.Contains(logs.String(), "release web:") {
		t.Fatalf("nothing logged: %q", logs.String())
	}
}

func TestResolveReleaseWebAssetsPrefersFlagThenEnvThenBesideTheBinary(t *testing.T) {
	flagDir, envDir, exeDir := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(exeDir, "release-web"), 0o750); err != nil {
		t.Fatal(err)
	}
	exe := func() (string, error) { return filepath.Join(exeDir, "ao"), nil }
	t.Setenv(ReleaseWebAssetsEnv, envDir)
	if got, err := ResolveReleaseWebAssets(flagDir, exe); err != nil || got != flagDir {
		t.Fatalf("flag: %q, %v", got, err)
	}
	if got, err := ResolveReleaseWebAssets("", exe); err != nil || got != envDir {
		t.Fatalf("env: %q, %v", got, err)
	}
	t.Setenv(ReleaseWebAssetsEnv, "")
	if got, err := ResolveReleaseWebAssets("", exe); err != nil || got != filepath.Join(exeDir, "release-web") {
		t.Fatalf("beside the binary: %q, %v", got, err)
	}
}

func TestResolveReleaseWebAssetsNamesTheFlagWhenTheBundleIsMissing(t *testing.T) {
	t.Setenv(ReleaseWebAssetsEnv, "")
	exe := func() (string, error) { return filepath.Join(t.TempDir(), "ao"), nil }
	_, err := ResolveReleaseWebAssets("", exe)
	if err == nil || !strings.Contains(err.Error(), "--assets") {
		t.Fatalf("err = %v, want one that names --assets", err)
	}
}

func newControl(t *testing.T) *ReleaseWebControl {
	t.Helper()
	t.Setenv("AO_RELEASE_WEB_DIR", "")
	dataDir := filepath.Join(t.TempDir(), "data")
	c := NewReleaseWebControl(dataDir)
	c.Executable = func() (string, error) { return "/bin/false", nil }
	return c
}

func TestReleaseWebControlStartCreatesAKeyBeforeSpawning(t *testing.T) {
	c := newControl(t)
	if st, _ := c.Status(context.Background()); st.KeyPresent {
		t.Fatal("a fresh control already has a key")
	}
	// /bin/false exits before listening, so Start fails, but not before the key exists.
	if _, err := c.Start(context.Background()); err == nil {
		t.Fatal("Start with an exe that never listens succeeded")
	}
	st, _ := c.Status(context.Background())
	if !st.KeyPresent || st.KeyGeneration != 1 || st.Running {
		t.Fatalf("status after a failed start = %+v", st)
	}
}

func TestReleaseWebControlRotateAndStopWithoutAServer(t *testing.T) {
	c := newControl(t)
	ctx := context.Background()
	for want := 1; want <= 2; want++ {
		st, err := c.RotateKey(ctx)
		if err != nil || st.KeyGeneration != want {
			t.Fatalf("RotateKey #%d = %+v, %v", want, st, err)
		}
	}
	if st, err := c.Stop(ctx); err != nil || st.Running {
		t.Fatalf("Stop with nothing running = %+v, %v", st, err)
	}
}

func TestReleaseWebControlCopyKeyPipesTheKeyToTheClipboardCommand(t *testing.T) {
	c := newControl(t)
	ctx := context.Background()
	if err := c.CopyKey(ctx); !errors.Is(err, releaseweb.ErrNoKey) {
		t.Fatalf("CopyKey without a key = %v", err)
	}
	if _, err := c.RotateKey(ctx); err != nil {
		t.Fatal(err)
	}
	sink := filepath.Join(t.TempDir(), "clipboard")
	c.Clipboard = func() *exec.Cmd { return exec.Command("sh", "-c", `cat > "$1"`, "sh", sink) }
	if err := c.CopyKey(ctx); err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if err := releaseweb.CopyKey(c.Paths, &want); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(sink)
	if err != nil || !bytes.Equal(got, want.Bytes()) || len(got) == 0 {
		t.Fatalf("the clipboard command did not receive the key (%d bytes, %v)", len(got), err)
	}

	c.Clipboard = nil
	if err := c.CopyKey(ctx); !errors.Is(err, releaseweb.ErrNoClipboard) {
		t.Fatalf("CopyKey without a clipboard = %v", err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestRunReleaseWebServesUntilCancelledAndNeedsAKey(t *testing.T) {
	t.Setenv("AO_RELEASE_WEB_DIR", "")
	dataDir := filepath.Join(t.TempDir(), "data")
	assets := t.TempDir()
	if err := os.WriteFile(filepath.Join(assets, "index.html"), []byte("<!doctype html><title>board</title>"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := ReleaseWebOptions{DataDir: dataDir, Port: freePort(t), AssetsDir: assets, Logger: quietLogger(nil)}

	if err := RunReleaseWeb(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "rotate-key") {
		t.Fatalf("RunReleaseWeb without a key = %v, want one that names rotate-key", err)
	}

	paths := releaseweb.Paths{Dir: releaseweb.DirFor(dataDir)}
	if err := releaseweb.RotateKey(paths); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunReleaseWeb(ctx, opts) }()

	var st releaseweb.Status
	for range 400 {
		if st = releaseweb.ReadStatus(paths); st.Running {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !st.Running || st.Port != opts.Port {
		cancel()
		t.Fatalf("status = %+v, want running on port %d", st, opts.Port)
	}
	resp, err := http.Get(st.URL + "api/board")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET api/board without the key = %d, want 401", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(paths.DBDir(), "ao.db")); err != nil {
		t.Errorf("the web did not open its own database: %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunReleaseWeb after cancel = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunReleaseWeb did not return after cancel")
	}
	if releaseweb.ReadStatus(paths).Running {
		t.Fatal("still running after cancel")
	}
}

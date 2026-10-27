package releaseweb

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

// helperDirEnv turns the test binary into a release web server for Start.
const helperDirEnv = "RELEASEWEB_TEST_HELPER_DIR"

func TestMain(m *testing.M) {
	if dir := os.Getenv(helperDirEnv); dir != "" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
		err := Serve(ctx, testOptions(Paths{Dir: dir}))
		stop()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func testOptions(p Paths) Options {
	return Options{
		Paths:  p,
		Board:  &fakeBoard{},
		Assets: fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}},
		Addr:   "127.0.0.1:0",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

var noRedirect = &http.Client{
	Timeout:       5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func TestServeRefusesAnyAddressButLoopbackIPv4(t *testing.T) {
	p := Paths{Dir: t.TempDir()}
	if err := RotateKey(p); err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{"0.0.0.0:0", ":0", "localhost:0", "[::1]:0", "[::]:0", "192.0.2.10:0", "127.0.0.2:0", "nonsense"} {
		o := testOptions(p)
		o.Addr = addr
		err := Serve(context.Background(), o)
		if err == nil || !strings.Contains(err.Error(), "only 127.0.0.1") {
			t.Errorf("Serve(%q) = %v, want a refusal", addr, err)
		}
	}
	if exists(p.RunFile()) {
		t.Error("a refused Serve wrote run.json")
	}
}

func TestServeRecordsItselfAndCleansUp(t *testing.T) {
	p := Paths{Dir: t.TempDir()}
	if err := RotateKey(p); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, testOptions(p)) }()

	var st Status
	waitFor(t, "run.json", func() bool { st = ReadStatus(p); return st.Running })
	if st.PID != os.Getpid() || st.URL != fmt.Sprintf("http://127.0.0.1:%d/", st.Port) || !st.KeyPresent || st.KeyGeneration != 1 {
		t.Fatalf("status %+v", st)
	}
	if err := Serve(context.Background(), testOptions(p)); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("second Serve = %v, want already running", err)
	}

	res, err := noRedirect.PostForm(st.URL+"auth/signin", url.Values{"key": {secretOf(t, p)}})
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("sign-in over TCP = %d, want 303", res.StatusCode)
	}
	waitFor(t, "status.json to count the session", func() bool { return ReadStatus(p).Sessions == 1 })

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve returned %v after cancel", err)
	}
	if exists(p.RunFile()) || exists(p.StatusFile()) {
		t.Error("run.json or status.json left behind after a clean exit")
	}
	if ReadStatus(p).Running {
		t.Error("ReadStatus says running after Serve returned")
	}
}

func TestServeRefusesToStartWithoutAKey(t *testing.T) {
	p := Paths{Dir: t.TempDir()}
	if err := Serve(context.Background(), testOptions(p)); err == nil {
		t.Fatal("Serve started without a key")
	}
	if exists(p.RunFile()) {
		t.Error("Serve without a key wrote run.json")
	}
}

func TestStartStopRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the helper process waits for SIGTERM")
	}
	p := Paths{Dir: t.TempDir()}
	t.Setenv(helperDirEnv, p.Dir)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Start(p, exe, nil); err == nil {
		t.Fatal("Start without a key succeeded")
	}
	if err := RotateKey(p); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Stop(p) })

	st, err := Start(p, exe, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.PID == os.Getpid() || st.PID == 0 {
		t.Fatalf("Start status %+v", st)
	}
	res, err := noRedirect.Get(st.URL + "robots.txt")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("started server robots.txt = %d", res.StatusCode)
	}
	again, err := Start(p, exe, nil)
	if err != nil || again.PID != st.PID {
		t.Errorf("second Start = pid %d err %v, want the running pid %d", again.PID, err, st.PID)
	}

	if err := Stop(p); err != nil {
		t.Fatal(err)
	}
	if processalive.Alive(st.PID) {
		t.Error("server still alive after Stop")
	}
	if ReadStatus(p).Running || exists(p.RunFile()) || exists(p.StatusFile()) {
		t.Error("Stop left the server recorded as running")
	}
	if err := Stop(p); err != nil {
		t.Errorf("Stop with nothing running = %v, want nil", err)
	}
}

func TestStopClearsAStaleRecordWithoutSignalling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a recycled pid is told apart by its start time, which Windows does not read")
	}
	p := Paths{Dir: t.TempDir()}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	// This process is alive but started before the record, like a recycled pid.
	recycled := runInfo{PID: os.Getpid(), Port: 1, URL: "http://127.0.0.1:1/", StartedAt: time.Now().Add(-time.Hour)}
	for name, info := range map[string]runInfo{
		"dead pid":     {PID: dead.Process.Pid, Port: 1, StartedAt: time.Now()},
		"recycled pid": recycled,
	} {
		t.Run(name, func(t *testing.T) {
			if err := writeJSONFile(p.RunFile(), info); err != nil {
				t.Fatal(err)
			}
			if err := writeStatusFile(p, 3); err != nil {
				t.Fatal(err)
			}
			if ReadStatus(p).Running {
				t.Fatal("ReadStatus trusts a stale run.json")
			}
			if err := Stop(p); err != nil {
				t.Fatal(err)
			}
			if exists(p.RunFile()) || exists(p.StatusFile()) {
				t.Error("Stop kept the stale files")
			}
		})
	}
}

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
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

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

	if _, err := Start(p, exe, nil, os.Environ()); err == nil {
		t.Fatal("Start without a key succeeded")
	}
	if err := RotateKey(p); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Stop(p) })

	st, err := Start(p, exe, nil, os.Environ())
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
	again, err := Start(p, exe, nil, os.Environ())
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

func TestStopClearsTheRecordOfADeadPid(t *testing.T) {
	p := Paths{Dir: t.TempDir()}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFile(p.RunFile(), runInfo{PID: dead.Process.Pid, Port: 1, StartedAt: time.Now(), ProcessStart: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := writeStatusFile(p, 3); err != nil {
		t.Fatal(err)
	}
	if ReadStatus(p).Running {
		t.Fatal("ReadStatus trusts the record of a dead pid")
	}
	if err := Stop(p); err != nil {
		t.Fatal(err)
	}
	if exists(p.RunFile()) || exists(p.StatusFile()) {
		t.Error("Stop kept the stale files")
	}
}

func bystander(t *testing.T) (int, <-chan struct{}) {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-exited })
	return cmd.Process.Pid, exited
}

func assertStopRefuses(t *testing.T, p Paths, pid int, exited <-chan struct{}) {
	t.Helper()
	err := Stop(p)
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(pid)) {
		t.Errorf("Stop = %v, want an error naming pid %d", err, pid)
	}
	select {
	case <-exited:
		t.Error("Stop signalled a process it could not verify as the release web")
	case <-time.After(200 * time.Millisecond):
	}
	if !exists(p.RunFile()) {
		t.Error("Stop deleted run.json while the pid it names is alive")
	}
}

func TestStopNeverSignalsOrForgetsALivePidWhoseStartTimeDiffers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sleep(1)")
	}
	p := Paths{Dir: t.TempDir()}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	pid, exited := bystander(t)
	hourAgo := time.Now().Add(-time.Hour)
	if err := writeJSONFile(p.RunFile(), runInfo{PID: pid, Port: 1, StartedAt: hourAgo, ProcessStart: hourAgo}); err != nil {
		t.Fatal(err)
	}
	assertStopRefuses(t, p, pid, exited)
}

func TestStopNeverSignalsAPidWhoseStartTimeCannotBeRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sleep(1) and hides ps")
	}
	p := Paths{Dir: t.TempDir()}
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	pid, exited := bystander(t)
	started, err := processStartedAt(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFile(p.RunFile(), runInfo{PID: pid, Port: 1, StartedAt: started, ProcessStart: started}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	assertStopRefuses(t, p, pid, exited)
}

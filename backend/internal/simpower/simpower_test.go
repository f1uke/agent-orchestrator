package simpower

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simslim"
)

const testUDID = "11111111-2222-3333-4444-555555555555"

// recorder is a simctl.Runner that remembers what it was asked to run and
// answers with whatever the test decided, so every path here is exercised
// without Xcode, a mac or a device.
type recorder struct {
	mu   sync.Mutex
	args [][]string
	// reply is consulted per call; nil means success with no output.
	reply func(ctx context.Context, args []string) ([]byte, error)
}

func (r *recorder) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.args = append(r.args, append([]string{name}, args...))
	reply := r.reply
	r.mu.Unlock()
	if reply == nil {
		return nil, nil
	}
	return reply(ctx, args)
}

func (r *recorder) calls() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.args))
	copy(out, r.args)
	return out
}

func found(string) (string, error) { return "/usr/bin/xcrun", nil }

func missingSimslim(name string) (string, error) {
	if name == simslim.Binary {
		return "", errors.New("executable file not found in $PATH")
	}
	return "/usr/bin/xcrun", nil
}

// waitFor polls a condition instead of sleeping on it, so the test is quick
// when it passes and still fails rather than hanging when it does not.
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(msg)
}

func newTestPower(t *testing.T, rec *recorder) *Power {
	t.Helper()
	p := New(found, rec.run)
	t.Cleanup(p.wait)
	return p
}

func TestBoot_RunsBootstatusAndClearsWhenDone(t *testing.T) {
	rec := &recorder{}
	p := newTestPower(t, rec)

	if err := p.Start(context.Background(), testUDID, Boot, nil, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.wait()

	calls := rec.calls()
	if len(calls) != 1 {
		t.Fatalf("ran %d commands, want 1: %v", len(calls), calls)
	}
	// `bootstatus -b` rather than `boot`: it boots AND blocks until the device
	// has finished booting. `simctl list` reports Booted seconds before the
	// device can actually be driven, so polling state would report success on a
	// device nothing can touch yet.
	want := []string{"xcrun", "simctl", "bootstatus", testUDID, "-b"}
	if got := calls[0]; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("ran %v, want %v", got, want)
	}
	if _, ok := p.Status(testUDID); ok {
		t.Error("a finished boot left a status behind; the device's own state is the report once it is up")
	}
}

func TestShutdown_RunsShutdown(t *testing.T) {
	rec := &recorder{}
	p := newTestPower(t, rec)

	if err := p.Start(context.Background(), testUDID, Shutdown, nil, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.wait()

	calls := rec.calls()
	if len(calls) != 1 {
		t.Fatalf("ran %d commands, want 1: %v", len(calls), calls)
	}
	want := []string{"xcrun", "simctl", "shutdown", testUDID}
	if got := calls[0]; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("ran %v, want %v", got, want)
	}
}

func TestStart_ReportsRunningWhileItWorks(t *testing.T) {
	release := make(chan struct{})
	rec := &recorder{reply: func(context.Context, []string) ([]byte, error) {
		<-release
		return nil, nil
	}}
	p := newTestPower(t, rec)

	if err := p.Start(context.Background(), testUDID, Boot, nil, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	status, ok := p.Status(testUDID)
	if !ok {
		t.Fatal("no status while the boot is in flight; the pane would have nothing to show")
	}
	if status.State != Running || status.Op != Boot {
		t.Errorf("status = %+v, want a running boot", status)
	}
	if status.StartedAt.IsZero() {
		t.Error("StartedAt is zero, so the pane cannot say how long it has been trying")
	}
	close(release)
	p.wait()
}

func TestStart_RefusesASecondOpOnTheSameDevice(t *testing.T) {
	release := make(chan struct{})
	rec := &recorder{reply: func(context.Context, []string) ([]byte, error) {
		<-release
		return nil, nil
	}}
	p := newTestPower(t, rec)

	if err := p.Start(context.Background(), testUDID, Boot, nil, nil); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	err := p.Start(context.Background(), testUDID, Shutdown, nil, nil)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("second Start = %v, want ErrBusy", err)
	}
	close(release)
	p.wait()
	if calls := rec.calls(); len(calls) != 1 {
		t.Errorf("ran %d commands, want 1 - the refused op must not have reached the device", len(calls))
	}
}

func TestStart_KeepsTheFailureAndItsReason(t *testing.T) {
	rec := &recorder{reply: func(context.Context, []string) ([]byte, error) {
		return []byte("Unable to boot device in current state: Booted"), errors.New("exit status 164")
	}}
	p := newTestPower(t, rec)

	if err := p.Start(context.Background(), testUDID, Boot, nil, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.wait()

	status, ok := p.Status(testUDID)
	if !ok {
		t.Fatal("a failed boot left no status; the pane would sit on a spinner for ever")
	}
	if status.State != Failed {
		t.Errorf("state = %q, want %q", status.State, Failed)
	}
	// simctl's own words, not ours: a reason we invented would be a guess about
	// somebody else's machine.
	if !strings.Contains(status.Reason, "Unable to boot device in current state") {
		t.Errorf("reason = %q, want simctl's own diagnostic", status.Reason)
	}
}

func TestStart_TimesOutAndSaysSo(t *testing.T) {
	rec := &recorder{reply: func(ctx context.Context, _ []string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	p := New(found, rec.run)
	p.bootTimeout = 20 * time.Millisecond
	t.Cleanup(p.wait)

	if err := p.Start(context.Background(), testUDID, Boot, nil, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.wait()

	status, ok := p.Status(testUDID)
	if !ok {
		t.Fatal("a boot that timed out left no status")
	}
	if status.State != Failed {
		t.Errorf("state = %q, want %q", status.State, Failed)
	}
	if !strings.Contains(status.Reason, "did not finish booting") {
		t.Errorf("reason = %q, want it to name the timeout", status.Reason)
	}
}

// The half-booted device is left alone on purpose: shutting it down would be
// AO acting on the human's behalf, which is exactly what the memory guard
// forbids. The row offers Shut down instead.
func TestStart_TimeoutNeverShutsTheDeviceDown(t *testing.T) {
	rec := &recorder{reply: func(ctx context.Context, _ []string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	p := New(found, rec.run)
	p.bootTimeout = 20 * time.Millisecond
	t.Cleanup(p.wait)

	if err := p.Start(context.Background(), testUDID, Boot, nil, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.wait()

	for _, call := range rec.calls() {
		if strings.Contains(strings.Join(call, " "), "shutdown") {
			t.Fatalf("a timed-out boot shut the device down: %v", call)
		}
	}
}

func TestStart_UnavailableWithoutXcrun(t *testing.T) {
	rec := &recorder{}
	p := New(func(string) (string, error) { return "", errors.New("not found") }, rec.run)

	err := p.Start(context.Background(), testUDID, Boot, nil, nil)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Start = %v, want ErrUnavailable", err)
	}
	if len(rec.calls()) != 0 {
		t.Error("something ran on a machine with no xcrun")
	}
}

func TestStart_RejectsAnUnknownOp(t *testing.T) {
	rec := &recorder{}
	p := newTestPower(t, rec)

	if err := p.Start(context.Background(), testUDID, Op("reboot"), nil, nil); err == nil {
		t.Fatal("an op nobody implements was accepted")
	}
	if len(rec.calls()) != 0 {
		t.Error("an unknown op reached the device")
	}
}

// A udid is the key that keeps two ops off one device, so it has to be the
// same key however it is cased - the same rule domain.NormalizeSimUDID keeps
// for leases.
func TestStart_MatchesUDIDCaseInsensitively(t *testing.T) {
	release := make(chan struct{})
	rec := &recorder{reply: func(context.Context, []string) ([]byte, error) {
		<-release
		return nil, nil
	}}
	p := newTestPower(t, rec)

	if err := p.Start(context.Background(), strings.ToLower(testUDID), Boot, nil, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, ok := p.Status(strings.ToUpper(testUDID)); !ok {
		t.Error("the same device read as two different ones depending on case")
	}
	if err := p.Start(context.Background(), strings.ToUpper(testUDID), Boot, nil, nil); !errors.Is(err, ErrBusy) {
		t.Errorf("a second op on the same device in another case = %v, want ErrBusy", err)
	}
	close(release)
	p.wait()
}

func TestStart_RetryingClearsTheOldFailure(t *testing.T) {
	var attempt int
	rec := &recorder{reply: func(context.Context, []string) ([]byte, error) {
		attempt++
		if attempt == 1 {
			return []byte("boom"), errors.New("exit status 1")
		}
		return nil, nil
	}}
	p := newTestPower(t, rec)

	if err := p.Start(context.Background(), testUDID, Boot, nil, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.wait()
	if status, _ := p.Status(testUDID); status.State != Failed {
		t.Fatalf("first attempt did not fail: %+v", status)
	}
	if err := p.Start(context.Background(), testUDID, Boot, nil, nil); err != nil {
		t.Fatalf("retry: %v", err)
	}
	p.wait()
	if _, ok := p.Status(testUDID); ok {
		t.Error("the old failure survived a successful retry")
	}
}

func TestClear_DropsAFailureAndLeavesARunningOpAlone(t *testing.T) {
	release := make(chan struct{})
	var attempt int
	rec := &recorder{reply: func(context.Context, []string) ([]byte, error) {
		attempt++
		if attempt == 1 {
			return []byte("boom"), errors.New("exit status 1")
		}
		<-release
		return nil, nil
	}}
	p := newTestPower(t, rec)

	if err := p.Start(context.Background(), testUDID, Boot, nil, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.wait()
	p.Clear(testUDID)
	if _, ok := p.Status(testUDID); ok {
		t.Error("Clear left the failure behind")
	}

	if err := p.Start(context.Background(), testUDID, Boot, nil, nil); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	p.Clear(testUDID)
	if _, ok := p.Status(testUDID); !ok {
		t.Error("Clear dropped an op that is still running, so the pane would lose its spinner")
	}
	close(release)
	p.wait()
}

func TestAll_ReportsEveryDeviceWithSomethingInFlight(t *testing.T) {
	release := make(chan struct{})
	rec := &recorder{reply: func(context.Context, []string) ([]byte, error) {
		<-release
		return nil, nil
	}}
	p := newTestPower(t, rec)

	other := "99999999-8888-7777-6666-555555555555"
	for _, udid := range []string{testUDID, other} {
		if err := p.Start(context.Background(), udid, Boot, nil, nil); err != nil {
			t.Fatalf("Start %s: %v", udid, err)
		}
	}
	all := p.All()
	if len(all) != 2 {
		t.Fatalf("All reported %d devices, want 2: %v", len(all), all)
	}
	for _, udid := range []string{testUDID, other} {
		if all[udid].State != Running {
			t.Errorf("%s = %+v, want running", udid, all[udid])
		}
	}
	close(release)
	p.wait()
}

// The listing is cached for a couple of seconds, so a boot that finishes has
// to say so rather than wait for the cache to lapse - otherwise the device
// stays "booting" in the pane after it is up.
func TestStart_NotifiesWhenAnOpSettles(t *testing.T) {
	rec := &recorder{}
	settled := make(chan struct{}, 1)
	p := New(found, rec.run)
	p.OnSettled(func() { settled <- struct{}{} })
	t.Cleanup(p.wait)

	if err := p.Start(context.Background(), testUDID, Boot, nil, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-settled:
	case <-time.After(2 * time.Second):
		t.Fatal("nothing was notified when the boot finished")
	}
	p.wait()
}

// The request that asked for the boot is answered immediately, so the boot
// must not die with it.
func TestStart_SurvivesTheRequestThatAskedForIt(t *testing.T) {
	started := make(chan struct{})
	rec := &recorder{reply: func(ctx context.Context, _ []string) ([]byte, error) {
		close(started)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("cancelled with the request: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
			return nil, nil
		}
	}}
	p := newTestPower(t, rec)

	ctx, cancel := context.WithCancel(context.Background())
	if err := p.Start(ctx, testUDID, Boot, nil, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-started
	cancel()
	p.wait()

	if status, ok := p.Status(testUDID); ok {
		t.Fatalf("the boot died with the request that asked for it: %+v", status)
	}
}

// The regression guard for every project that has not opted in: a nil request
// must leave the boot byte-for-byte as it was before this feature existed.
func TestBoot_NilRequestRunsNoSimslimCommand(t *testing.T) {
	rec := &recorder{}
	p := newTestPower(t, rec)

	if err := p.Start(context.Background(), testUDID, Boot, nil, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.wait()

	for _, c := range rec.calls() {
		if c[0] == simslim.Binary {
			t.Fatalf("nil request still ran simslim: %v", rec.calls())
		}
	}
	if _, ok := p.Status(testUDID); ok {
		t.Fatal("a clean boot left an entry behind")
	}
}

// Order, not just presence: slimming a device that is not up yet is slimming
// nothing.
func TestBoot_SlimsOnlyAfterTheDeviceIsUp(t *testing.T) {
	rec := &recorder{}
	p := newTestPower(t, rec)
	req := &simslim.Request{Profile: &simslim.Profile{Keep: []string{"com.apple.apsd"}}}

	if err := p.Start(context.Background(), testUDID, Boot, req, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.wait()

	calls := rec.calls()
	if len(calls) < 2 {
		t.Fatalf("want bootstatus then simslim, got %v", calls)
	}
	if calls[0][0] != simctl.Binary || calls[0][1] != "simctl" {
		t.Fatalf("first call was not simctl: %v", calls[0])
	}
	if calls[1][0] != simslim.Binary {
		t.Fatalf("second call was not simslim: %v", calls[1])
	}
}

// A device that came up stock is not a failed boot, but it is not silence
// either.
func TestBoot_KeepsAWarningWhenTheDeviceIsStock(t *testing.T) {
	rec := &recorder{}
	p := New(missingSimslim, rec.run)
	t.Cleanup(p.wait)
	req := &simslim.Request{Profile: &simslim.Profile{Keep: []string{"com.apple.apsd"}}}

	if err := p.Start(context.Background(), testUDID, Boot, req, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.wait()

	st, ok := p.Status(testUDID)
	if !ok {
		t.Fatal("a stock device left no entry, so nobody is ever told")
	}
	if st.State != Warned {
		t.Fatalf("state = %q, want %q - the boot itself worked", st.State, Warned)
	}
	if st.Profile == nil || st.Profile.Outcome != simslim.Skipped {
		t.Fatalf("profile = %+v, want outcome %q", st.Profile, simslim.Skipped)
	}
}

func TestBoot_ClearsTheEntryWhenTheProfileLanded(t *testing.T) {
	rec := &recorder{}
	p := newTestPower(t, rec)
	req := &simslim.Request{Profile: &simslim.Profile{Keep: []string{"com.apple.apsd"}}}

	if err := p.Start(context.Background(), testUDID, Boot, req, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.wait()

	if st, ok := p.Status(testUDID); ok {
		t.Fatalf("a device that reached its profile left an entry: %+v", st)
	}
}

// A profile we could not work out must not read as a project that does not slim.
func TestBoot_ReportsAProfileItCouldNotResolve(t *testing.T) {
	rec := &recorder{}
	p := newTestPower(t, rec)
	req := &simslim.Request{Err: errors.New("project 7 is degraded")}

	if err := p.Start(context.Background(), testUDID, Boot, req, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.wait()

	st, ok := p.Status(testUDID)
	if !ok || st.State != Warned {
		t.Fatalf("status = %+v ok=%v, want a Warned entry", st, ok)
	}
	if st.Profile == nil || st.Profile.Outcome != simslim.Failed {
		t.Fatalf("profile = %+v, want outcome %q", st.Profile, simslim.Failed)
	}
	if !strings.Contains(st.Profile.Reason, "degraded") {
		t.Fatalf("reason = %q, want the resolver's own words", st.Profile.Reason)
	}
	for _, c := range rec.calls() {
		if c[0] == simslim.Binary {
			t.Fatalf("ran simslim despite an unresolved profile: %v", rec.calls())
		}
	}
}

// Shutdown has no profile step, whatever it is handed.
func TestShutdown_NeverSlims(t *testing.T) {
	rec := &recorder{}
	p := newTestPower(t, rec)
	req := &simslim.Request{Profile: &simslim.Profile{Keep: []string{"com.apple.apsd"}}}

	if err := p.Start(context.Background(), testUDID, Shutdown, req, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}
	p.wait()

	for _, c := range rec.calls() {
		if c[0] == simslim.Binary {
			t.Fatalf("shutdown ran simslim: %v", rec.calls())
		}
	}
}

// The Device tab renders this; without it a boot looks frozen for the tens of
// seconds the reboot takes.
func TestBoot_ReportsTheSlimmingPhaseWhileItRuns(t *testing.T) {
	release := make(chan struct{})
	rec := &recorder{reply: func(_ context.Context, args []string) ([]byte, error) {
		if len(args) > 0 && args[0] == "verify" {
			<-release
		}
		return nil, nil
	}}
	p := newTestPower(t, rec)
	req := &simslim.Request{Profile: &simslim.Profile{Keep: []string{"com.apple.apsd"}}}

	if err := p.Start(context.Background(), testUDID, Boot, req, nil); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitFor(t, func() bool {
		st, ok := p.Status(testUDID)
		return ok && st.Phase == PhaseSlimming
	}, "phase never reached "+PhaseSlimming)

	close(release)
	p.wait()
}

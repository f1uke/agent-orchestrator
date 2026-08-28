# Simslim Boot Profile Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When AO boots a simulator, bring it to the slim daemon profile its project asks for, and never let "this device is actually stock" pass silently.

**Architecture:** A new `internal/simslim` package wraps the `simslim` CLI and knows nothing about AO. `simpower` runs it as a second phase of a boot, after `simctl bootstatus -b` succeeds and before the operation settles, so there stays exactly one definition of "this device is ready". The profile is resolved from the project config by the controller and passed down as a parameter; neither `Screen` nor `Power` ever looks one up.

**Tech Stack:** Go 1.26, chi, the existing `simctl.LookPath`/`simctl.Runner` injection seams, React + generated OpenAPI TS types.

**Spec:** `docs/superpowers/specs/2026-08-28-simslim-boot-profile-design.md`

## Global Constraints

- **The memory cap is untouched.** `simBootMaxBooted` stays `2`. Do not change it, do not make it profile-aware. That is a later slice.
- **A project that has not opted in must behave exactly as it does today.** A nil profile runs no `simslim` command whatsoever. There is a test for this; keep it.
- **`simslim verify` before `simslim on`, always.** `simslim on` reboots the device every time, even when nothing changed (measured, 19-27s). Calling `on` unconditionally adds a second reboot to every boot forever.
- **A boot never fails because slimming failed.** `ao sim boot` exists to break the qa-creation deadlock (#265); failing a boot over a missing optional tool would regress that to buy nothing.
- **No outcome may mean "fine" without saying which case it was.** The four outcomes are `applied`, `already`, `skipped`, `failed`.
- Keep daemon logic in the daemon. `frontend/` is a thin supervisor/UI surface.
- After changing controller DTOs, run `npm run api` to regenerate the OpenAPI spec and the frontend TS types. Do not hand-edit `frontend/src/api/schema.ts`.
- Commit messages end with `Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>`.

---

### Task 1: The `simslim` package

**Files:**
- Create: `backend/internal/simslim/simslim.go`
- Test: `backend/internal/simslim/simslim_test.go`

**Interfaces:**
- Consumes: `simctl.LookPath`, `simctl.Runner` from `backend/internal/simctl`.
- Produces: `simslim.Binary`, `simslim.Profile{Keep []string}`, `simslim.Request{Profile *Profile; Err error}`, `simslim.Outcome` with constants `Applied`/`Already`/`Skipped`/`Failed`, `simslim.Result{Outcome Outcome; Reason string}`, and `simslim.Apply(ctx, lookPath, run, udid string, p Profile) Result`.

- [ ] **Step 1: Write the failing tests**

Create `backend/internal/simslim/simslim_test.go`:

```go
package simslim

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// recorder is a simctl.Runner that remembers what it was asked to run and
// answers with whatever the test decided, so every path here is exercised
// without Xcode, a mac or a device.
type recorder struct {
	mu    sync.Mutex
	args  [][]string
	reply func(args []string) ([]byte, error)
}

func (r *recorder) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.args = append(r.args, append([]string{name}, args...))
	reply := r.reply
	r.mu.Unlock()
	if reply == nil {
		return nil, nil
	}
	return reply(args)
}

func (r *recorder) calls() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.args))
	copy(out, r.args)
	return out
}

func found(string) (string, error) { return "/usr/local/bin/simslim", nil }

func missing(string) (string, error) { return "", errors.New("executable file not found in $PATH") }

const testUDID = "4754DB41-86C8-4326-81A7-172DDD41D5DA"

var testProfile = Profile{Keep: []string{"com.apple.apsd", "com.apple.swcd"}}

// A device already matching the profile must NOT be rebooted: `simslim on`
// reboots every time it runs, even when nothing changes, so calling it
// unconditionally would add a second reboot to every boot forever.
func TestApply_VerifyPassesLeavesTheDeviceAlone(t *testing.T) {
	rec := &recorder{}
	got := Apply(context.Background(), found, rec.run, testUDID, testProfile)

	if got.Outcome != Already {
		t.Fatalf("outcome = %q, want %q", got.Outcome, Already)
	}
	calls := rec.calls()
	if len(calls) != 1 {
		t.Fatalf("ran %d commands, want exactly 1 (verify): %v", len(calls), calls)
	}
	if calls[0][1] != "verify" {
		t.Fatalf("first command was %q, want verify", calls[0][1])
	}
	for _, c := range calls {
		if c[1] == "on" {
			t.Fatalf("`simslim on` ran on a device that already matched: %v", calls)
		}
	}
}

func TestApply_VerifyReportsDriftSoItSlims(t *testing.T) {
	rec := &recorder{reply: func(args []string) ([]byte, error) {
		if args[0] == "verify" {
			return []byte("drift: 3 daemons enabled that should not be"), errors.New("exit status 1")
		}
		return nil, nil
	}}

	got := Apply(context.Background(), found, rec.run, testUDID, testProfile)

	if got.Outcome != Applied {
		t.Fatalf("outcome = %q, want %q (reason %q)", got.Outcome, Applied, got.Reason)
	}
	calls := rec.calls()
	if len(calls) != 2 || calls[1][1] != "on" {
		t.Fatalf("want verify then on, got %v", calls)
	}
}

func TestApply_PassesTheKeepListToBothCommands(t *testing.T) {
	rec := &recorder{reply: func(args []string) ([]byte, error) {
		if args[0] == "verify" {
			return nil, errors.New("exit status 1")
		}
		return nil, nil
	}}

	Apply(context.Background(), found, rec.run, testUDID, testProfile)

	for _, c := range rec.calls() {
		joined := strings.Join(c, " ")
		if !strings.Contains(joined, "--keep com.apple.apsd,com.apple.swcd") {
			t.Fatalf("command missing the keep list: %q", joined)
		}
		if !strings.Contains(joined, testUDID) {
			t.Fatalf("command missing the udid: %q", joined)
		}
	}
}

// An empty (but present) Keep means a fully slim device, which simslim spells
// as `on` with no --keep at all.
func TestApply_EmptyKeepSendsNoKeepFlag(t *testing.T) {
	rec := &recorder{reply: func(args []string) ([]byte, error) {
		if args[0] == "verify" {
			return nil, errors.New("exit status 1")
		}
		return nil, nil
	}}

	Apply(context.Background(), found, rec.run, testUDID, Profile{})

	for _, c := range rec.calls() {
		if strings.Contains(strings.Join(c, " "), "--keep") {
			t.Fatalf("empty Keep still sent --keep: %v", c)
		}
	}
}

func TestApply_WithoutTheBinaryRunsNothingAndSaysSkipped(t *testing.T) {
	rec := &recorder{}
	got := Apply(context.Background(), missing, rec.run, testUDID, testProfile)

	if got.Outcome != Skipped {
		t.Fatalf("outcome = %q, want %q", got.Outcome, Skipped)
	}
	if got.Reason == "" {
		t.Fatal("Skipped carried no reason; a stock device must say why")
	}
	if n := len(rec.calls()); n != 0 {
		t.Fatalf("ran %d commands without the binary, want 0", n)
	}
}

func TestApply_FailureCarriesTheToolsOwnWords(t *testing.T) {
	rec := &recorder{reply: func(args []string) ([]byte, error) {
		if args[0] == "verify" {
			return nil, errors.New("exit status 1")
		}
		return []byte("context deadline exceeded while disabling 170 services"), errors.New("exit status 1")
	}}

	got := Apply(context.Background(), found, rec.run, testUDID, testProfile)

	if got.Outcome != Failed {
		t.Fatalf("outcome = %q, want %q", got.Outcome, Failed)
	}
	if !strings.Contains(got.Reason, "context deadline exceeded") {
		t.Fatalf("reason = %q, want the tool's own output", got.Reason)
	}
}

func TestApply_FailureWithNoOutputStillExplainsItself(t *testing.T) {
	rec := &recorder{reply: func(args []string) ([]byte, error) {
		return nil, errors.New("exit status 2")
	}}

	got := Apply(context.Background(), found, rec.run, testUDID, testProfile)

	if got.Outcome != Failed || got.Reason == "" {
		t.Fatalf("got %+v, want Failed with a reason", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd backend && go test ./internal/simslim/...`
Expected: FAIL — the package does not compile, `Apply`, `Profile`, `Outcome` and the constants are undefined.

- [ ] **Step 3: Write the implementation**

Create `backend/internal/simslim/simslim.go`:

```go
// Package simslim brings a booted simulator to a chosen set of running
// background daemons, by driving the `simslim` CLI.
//
// A stock iOS simulator runs a couple of hundred background daemons and costs
// several GB. Most of them are Siri, Spotlight, iCloud, News and photo
// analysis, which no test needs. simslim writes persistent `launchctl disable`
// entries into the simulator's own launchd database and reboots it. Measured on
// the machine this was built for: 217 processes and 3,671 MB stock became 86
// processes and 1,236 MB with the daemons an iOS app actually needs kept.
//
// This package knows the tool's command line and nothing about AO: no projects,
// no sessions, no config. It takes the same injected lookPath and runner every
// other sim package takes, so it is testable without Xcode, a mac or a device.
package simslim

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
)

// Binary is the tool's own name. It is optional: a machine without it boots
// stock devices and is told so.
const Binary = "simslim"

// Profile is the set of daemons a device keeps running. Everything simslim
// manages and is not named here is disabled. An empty Keep is a fully slim
// device - which is a real request, and why callers pass a *Profile rather than
// a Profile to mean "do not slim at all".
type Profile struct {
	Keep []string
}

// Request is what a caller knows about slimming for one boot.
//
// Err is the reason this type exists rather than a bare *Profile: "we could not
// work out which profile this project wants" must not be indistinguishable from
// "this project does not slim". The first is reported on the device; the second
// is silence.
type Request struct {
	Profile *Profile
	Err     error
}

// Outcome is what happened to a device's profile. There is deliberately no
// value meaning "fine" without saying which case it was: the failure this
// package exists to avoid is a device that is quietly stock while everything
// reports success.
type Outcome string

const (
	// Applied: the device had drifted, so `simslim on` ran and rebooted it.
	Applied Outcome = "applied"
	// Already: `simslim verify` passed and nothing was run. The common case.
	Already Outcome = "already"
	// Skipped: simslim is not installed. The device is stock.
	Skipped Outcome = "skipped"
	// Failed: simslim ran and refused. The device is stock.
	Failed Outcome = "failed"
)

// Result is an Outcome plus, when there is bad news, why.
type Result struct {
	Outcome Outcome `json:"outcome"`
	Reason  string  `json:"reason,omitempty"`
}

// Stock says whether this result leaves the caller with an unslimmed device,
// which is the one thing every reporting surface needs to ask.
func (r Result) Stock() bool { return r.Outcome == Skipped || r.Outcome == Failed }

// Apply brings a booted device to the profile. It is idempotent and cheap in
// the common case.
//
// ⚠ It verifies first and only runs `on` when the device has drifted, and that
// ordering is load-bearing rather than an optimisation: `simslim on` reboots the
// device EVERY time it runs, even when it changes nothing. Calling it
// unconditionally would add a second reboot to every boot for the life of this
// feature. `verify` does not reboot.
func Apply(ctx context.Context, lookPath simctl.LookPath, run simctl.Runner, udid string, p Profile) Result {
	if _, err := lookPath(Binary); err != nil {
		return Result{Outcome: Skipped, Reason: Binary + " is not on PATH, so this device is stock"}
	}
	if _, err := run(ctx, Binary, args("verify", udid, p)...); err == nil {
		return Result{Outcome: Already}
	}
	out, err := run(ctx, Binary, args("on", udid, p)...)
	if err != nil {
		return Result{Outcome: Failed, Reason: reason(out, err)}
	}
	return Result{Outcome: Applied}
}

// args builds one simslim invocation. An empty Keep sends no --keep at all,
// which is how simslim spells a fully slim device.
func args(verb, udid string, p Profile) []string {
	a := []string{verb, udid}
	if len(p.Keep) > 0 {
		a = append(a, "--keep", strings.Join(p.Keep, ","))
	}
	return a
}

// reason prefers the tool's own words, and falls back to the exit status when
// it said nothing at all.
func reason(out []byte, err error) string {
	if s := strings.TrimSpace(string(out)); s != "" {
		return s
	}
	return err.Error()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd backend && go test ./internal/simslim/...`
Expected: PASS, all seven tests.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/simslim/
git commit -m "feat(simslim): wrap the simslim CLI behind an injected runner

Verifies before it slims, because \`simslim on\` reboots the device
every time it runs - even when it changes nothing.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 2: Run the profile as the second phase of a boot

**Files:**
- Modify: `backend/internal/simpower/simpower.go` (State constants ~line 54, `Status` ~line 92, `Start` ~line 157, `execute` ~line 206)
- Test: `backend/internal/simpower/simpower_test.go`

**Interfaces:**
- Consumes: `simslim.Apply`, `simslim.Request`, `simslim.Profile`, `simslim.Result`, `simslim.Outcome` constants from Task 1.
- Produces: `simpower.Start(ctx context.Context, udid string, op Op, req *simslim.Request, done func()) error`; `simpower.Warned State`; `simpower.PhaseBooting`/`simpower.PhaseSlimming` string constants; `Status.Phase string` and `Status.Profile *simslim.Result`; `simpower.ProfileTimeout`.

- [ ] **Step 1: Write the failing tests**

Append to `backend/internal/simpower/simpower_test.go`:

```go
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
```

Add these helpers to the same file, next to `found`:

```go
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
```

Add `errors`, `strings`, and the `simctl`/`simslim` imports to the test file's import block if they are not already there.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd backend && go test ./internal/simpower/...`
Expected: FAIL — `Start` takes four arguments, and `Warned`, `PhaseSlimming`, `Status.Phase`, `Status.Profile` are undefined. Existing tests also fail to compile because `Start` gained a parameter; that is expected and Step 3 fixes them.

- [ ] **Step 3: Write the implementation**

In `backend/internal/simpower/simpower.go`:

Add the import `"github.com/aoagents/agent-orchestrator/backend/internal/simslim"`.

Extend the state constants (after `Failed`, ~line 61):

```go
	// Warned: the operation itself worked, but something about the device the
	// caller should know did not. Today that is only a boot whose profile step
	// left the device stock.
	//
	// It is not Failed. The boot succeeded and is reported as succeeding - a
	// missing optional tool must never fail a boot, because `ao sim boot` is
	// what breaks the deadlock in which qa can never be created. Warned exists
	// so that "this device is not slim" cannot be silent, which is the whole
	// failure mode this feature is shaped around: `xcrun simctl push` returns
	// exit 0 and prints "Notification sent" on a device whose apsd is disabled.
	Warned State = "warned"
```

Add the phase constants and the profile timeout near `BootTimeout`:

```go
// Phases of a boot. A boot that has to slim spends tens of seconds past the
// point simctl calls it up, and without a phase the pane looks frozen for all
// of them.
const (
	PhaseBooting  = "booting"
	PhaseSlimming = "slimming"
)

// ProfileTimeout bounds the profile step. `simslim on` measured 19-27s on the
// machine this was built for, but it disables ~170 services one launchctl call
// at a time and simslim's own docs warn that shared CI runners are far slower.
const ProfileTimeout = 10 * time.Minute
```

Extend `Status`:

```go
type Status struct {
	Op        Op        `json:"op"`
	State     State     `json:"state"`
	StartedAt time.Time `json:"startedAt"`
	Reason    string    `json:"reason,omitempty"`
	// Phase is which part of the operation is running now. Empty for shutdown,
	// which has only one.
	Phase string `json:"phase,omitempty"`
	// Profile is what happened to the device's daemon profile. Set only on a
	// boot that had one to apply.
	Profile *simslim.Result `json:"profile,omitempty"`
}
```

Add the `profileTimeout` field to `Power` beside `shutdownTimeout`, and set it in `New`:

```go
	profileTimeout  time.Duration
```
```go
		profileTimeout:  ProfileTimeout,
```

Change `Start` and the goroutine it launches:

```go
func (p *Power) Start(ctx context.Context, udid string, op Op, req *simslim.Request, done func()) error {
```

and inside, where the entry is first recorded:

```go
	p.entries[key] = Status{Op: op, State: Running, StartedAt: p.now(), Phase: PhaseBooting}
```

and the goroutine body:

```go
		p.execute(context.WithoutCancel(ctx), key, op, timeout, args, req)
```

Replace `execute` with:

```go
// execute runs the command, then - for a boot with a profile - brings the
// device to that profile, and records what happened.
//
// The profile step runs INSIDE the operation rather than after it, and the
// operation does not settle until it is done. That is the same decision plan()
// documents for choosing `bootstatus -b` over `boot`: there has to be exactly
// one definition of "this device is ready". `simslim on` reboots the device, so
// a boot that reported success before this step would hand `ao sim claim` a
// device that is on its way down.
func (p *Power) execute(ctx context.Context, key string, op Op, timeout time.Duration, args []string, req *simslim.Request) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	out, err := p.run(runCtx, simctl.Binary, args...)
	cancel()

	var profile *simslim.Result
	if err == nil && op == Boot && req != nil {
		profile = p.applyProfile(ctx, key, *req)
	}

	p.mu.Lock()
	switch {
	case err != nil:
		p.entries[key] = Status{
			Op:        op,
			State:     Failed,
			StartedAt: p.entries[key].StartedAt,
			Reason:    reason(ctx, op, timeout, out, err),
		}
	case profile != nil && profile.Stock():
		// The boot worked. The device is not slim, and saying so is the whole
		// point - see the Warned constant.
		p.entries[key] = Status{
			Op:        op,
			State:     Warned,
			StartedAt: p.entries[key].StartedAt,
			Profile:   profile,
		}
	default:
		// No "succeeded" entry: the device's own state is the answer now.
		delete(p.entries, key)
	}
	settled := p.onSettled
	p.mu.Unlock()

	if settled != nil {
		settled()
	}
}

// applyProfile runs the profile step, reporting the phase while it does.
//
// A request that could not be resolved never reaches simslim: there is nothing
// to apply, and the point is only that "we could not work out this project's
// profile" does not read the same as "this project does not slim".
func (p *Power) applyProfile(ctx context.Context, key string, req simslim.Request) *simslim.Result {
	if req.Err != nil {
		return &simslim.Result{Outcome: simslim.Failed, Reason: req.Err.Error()}
	}
	if req.Profile == nil {
		return nil
	}

	p.mu.Lock()
	if st, ok := p.entries[key]; ok {
		st.Phase = PhaseSlimming
		p.entries[key] = st
	}
	p.mu.Unlock()

	profCtx, cancel := context.WithTimeout(ctx, p.profileTimeout)
	defer cancel()
	r := simslim.Apply(profCtx, p.lookPath, p.run, key, *req.Profile)
	return &r
}
```

Update the existing tests in `simpower_test.go` that call `p.Start(ctx, testUDID, Boot, done)` to pass `nil` for the new request parameter: `p.Start(ctx, testUDID, Boot, nil, done)`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd backend && go test ./internal/simpower/...`
Expected: PASS, including every pre-existing test.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/simpower/
git commit -m "feat(simpower): bring a booted device to its daemon profile

The profile step runs inside the boot and the operation does not settle
until it is done, so there stays one definition of ready. A device that
ends up stock keeps a Warned entry: the boot succeeded, and that is
exactly why it must not be silent.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 3: The project config field

**Files:**
- Modify: `backend/internal/domain/projectconfig.go` (add the field after `HasIOSSimulator` ~line 85; add validation inside `Validate()` ~line 188)
- Test: `backend/internal/domain/projectconfig_test.go`

**Interfaces:**
- Produces: `domain.SimProfileConfig{Keep []string}` and the field `ProjectConfig.SimProfile *SimProfileConfig`.

- [ ] **Step 1: Write the failing tests**

Append to `backend/internal/domain/projectconfig_test.go`:

```go
func TestProjectConfig_SimProfileAbsentIsNotSlimming(t *testing.T) {
	var c ProjectConfig
	if c.SimProfile != nil {
		t.Fatal("a project that says nothing must not slim")
	}
}

// nil and an empty Keep are different instructions, which is why the field is
// a pointer: nil means do not slim, present-and-empty means slim everything.
func TestProjectConfig_EmptyKeepIsAValidFullSlim(t *testing.T) {
	c := ProjectConfig{SimProfile: &SimProfileConfig{}}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestProjectConfig_ValidatesDaemonLabels(t *testing.T) {
	for name, keep := range map[string][]string{
		"empty":      {""},
		"whitespace": {"com.apple.apsd "},
		"foreign":    {"org.example.daemon"},
	} {
		t.Run(name, func(t *testing.T) {
			c := ProjectConfig{SimProfile: &SimProfileConfig{Keep: keep}}
			if err := c.Validate(); err == nil {
				t.Fatalf("Validate accepted %q; a typo here is a daemon silently not kept", keep)
			}
		})
	}
}

func TestProjectConfig_AcceptsARealKeepList(t *testing.T) {
	c := ProjectConfig{SimProfile: &SimProfileConfig{Keep: []string{
		"com.apple.apsd",
		"com.apple.swcd",
		"com.apple.assetsd",
		"com.apple.telephonyutilities.callservicesd",
	}}}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestProjectConfig_SimProfileRoundTripsThroughJSON(t *testing.T) {
	in := ProjectConfig{SimProfile: &SimProfileConfig{Keep: []string{"com.apple.apsd"}}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out ProjectConfig
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.SimProfile == nil || len(out.SimProfile.Keep) != 1 || out.SimProfile.Keep[0] != "com.apple.apsd" {
		t.Fatalf("round trip lost the profile: %+v", out.SimProfile)
	}
	var bare ProjectConfig
	b2, err := json.Marshal(bare)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b2), "simProfile") {
		t.Fatalf("an unset profile was serialised: %s", b2)
	}
}
```

Add `encoding/json` and `strings` to the test file's imports if they are not already there.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd backend && go test ./internal/domain/... -run SimProfile`
Expected: FAIL — `SimProfileConfig` is undefined.

- [ ] **Step 3: Write the implementation**

In `backend/internal/domain/projectconfig.go`, after the `HasIOSSimulator` field:

```go
	// SimProfile is the set of background daemons this project's simulators
	// keep running. AO applies it when it boots a device.
	//
	// It is a POINTER because nil and present-but-empty are different
	// instructions: nil means AO does not slim this project's devices at all,
	// and an empty Keep means a fully slim device. A value type could not tell
	// those apart, and the second is a real request.
	//
	// It is opt-in (nil) for the same reason HasWebUI and HasIOSSimulator are:
	// a project that says nothing must behave exactly as it did before this
	// existed.
	SimProfile *SimProfileConfig `json:"simProfile,omitempty"`
```

Add the type near the other small config types in the same file:

```go
// SimProfileConfig is which daemons a slimmed simulator keeps.
type SimProfileConfig struct {
	// Keep names the daemons that stay enabled; everything the slimming tool
	// manages and is not named here is disabled. Empty means a fully slim
	// device.
	Keep []string `json:"keep,omitempty"`
}

// Validate rejects a label that cannot be a daemon.
//
// ⚠ A typo here does not fail loudly on its own: it is simply a daemon that is
// not kept, on a device that boots and looks fine, whose feature then quietly
// does nothing. That is the exact failure this whole area is shaped around, so
// it is worth catching at the config boundary as well as in the tool.
func (c SimProfileConfig) Validate() error {
	for i, label := range c.Keep {
		if label == "" {
			return fmt.Errorf("simProfile.keep[%d]: empty daemon label", i)
		}
		if strings.TrimSpace(label) != label {
			return fmt.Errorf("simProfile.keep[%d]: %q has surrounding whitespace", i, label)
		}
		if !strings.HasPrefix(label, "com.apple.") {
			return fmt.Errorf("simProfile.keep[%d]: %q is not a com.apple.* daemon label", i, label)
		}
	}
	return nil
}
```

In `ProjectConfig.Validate()`, before the final `return nil`:

```go
	if c.SimProfile != nil {
		if err := c.SimProfile.Validate(); err != nil {
			return err
		}
	}
```

Ensure `fmt` and `strings` are imported in `projectconfig.go`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd backend && go test ./internal/domain/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/domain/
git commit -m "feat(domain): per-project simulator daemon profile

A pointer, because nil (do not slim) and an empty keep list (slim
everything) are different instructions.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 4: Plumb the request through the screen, changing no behaviour

**Files:**
- Modify: `backend/internal/simstream/screen.go:147`
- Modify: `backend/internal/httpd/api.go:217`
- Modify: `backend/internal/httpd/controllers/sim_screen.go:58` (the `SimScreenProvider` interface) and `:909` (the call)
- Test: `backend/internal/httpd/controllers/sim_power_test.go`, `backend/internal/httpd/sim_stream_test.go`, `backend/internal/httpd/controllers/sim_screen_test.go` (fakes only)

**Interfaces:**
- Consumes: `simslim.Request` from Task 1.
- Produces: `Screen.StartPower(ctx context.Context, udid string, op simpower.Op, req *simslim.Request, done func()) error` and the matching `SimScreenProvider` method.

This task is a pure refactor: the controller passes `nil` and nothing slims yet. Task 5 makes it real. Splitting it this way means the signature change can be reviewed on its own, with every existing test still green.

- [ ] **Step 1: Change the signatures**

`backend/internal/simstream/screen.go`:

```go
// StartPower boots or shuts down a device, returning as soon as the work is
// under way. See internal/simpower for why this exists in the daemon and
// nowhere else.
//
// req is passed straight through and never inspected: a Screen is a
// device-level surface with no idea what a project is, and keeping it that way
// is what lets it be tested over a bare fake runner.
func (s *Screen) StartPower(ctx context.Context, udid string, op simpower.Op, req *simslim.Request, done func()) error {
	return s.power.Start(ctx, udid, op, req, done)
}
```

`backend/internal/httpd/api.go:217` and `backend/internal/httpd/controllers/sim_screen.go:58` — update the interface method to match:

```go
	StartPower(ctx context.Context, udid string, op simpower.Op, req *simslim.Request, done func()) error
```

`backend/internal/httpd/controllers/sim_screen.go:909`:

```go
	if err := c.Screen.StartPower(r.Context(), device.UDID, op, nil, done); err != nil {
```

Update every fake implementing `SimScreenProvider` in the three test files listed above to take the extra parameter.

- [ ] **Step 2: Run the full backend suite**

Run: `cd backend && go build ./... && go test ./...`
Expected: PASS with no behaviour change — nothing slims yet.

- [ ] **Step 3: Commit**

```bash
git add backend/
git commit -m "refactor(sim): carry a slimming request through StartPower

Signature only; the controller still passes nil. Screen and Power never
inspect it - they are device-level and know nothing about projects.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 5: Resolve the profile from the project

**Files:**
- Modify: `backend/internal/httpd/controllers/sim_screen.go` (add `SimProfileResolver`, add the field to `SimScreenController` ~line 221, use it in `power` ~line 909)
- Create: `backend/internal/httpd/simprofile.go`
- Modify: `backend/internal/httpd/api.go` (`APIDeps` ~line 32, and the sim screen controller construction ~line 109)
- Test: `backend/internal/httpd/controllers/sim_power_test.go`, `backend/internal/httpd/controllers/sim_screen_test.go`

**Interfaces:**
- Consumes: `Screen.StartPower(..., req *simslim.Request, ...)` from Task 4; `domain.ProjectConfig.SimProfile` from Task 3.
- Produces: `controllers.SimProfileResolver` with `SimProfileFor(ctx context.Context, id domain.SessionID) (*simslim.Profile, error)`; `httpd.APIDeps.SimProfiles`.

- [ ] **Step 1: Extend the existing test fakes**

In `backend/internal/httpd/controllers/sim_screen_test.go`, record the request on the existing `fakeScreen` (its `StartPower` is at line 71 and its `powerCall` type is just above `powered()`):

```go
func (f *fakeScreen) StartPower(_ context.Context, udid string, op simpower.Op, req *simslim.Request, done func()) error {
	f.mu.Lock()
	f.powerOps = append(f.powerOps, powerCall{UDID: udid, Op: op, Req: req})
	f.mu.Unlock()
	if done != nil {
		done()
	}
	return nil
}
```

Add `Req *simslim.Request` to the `powerCall` struct, and let `newScreenTestServer` take a resolver:

```go
func newScreenTestServer(t *testing.T, svc simsvc.Manager, screen httpd.SimScreen) *httptest.Server {
	t.Helper()
	return newScreenTestServerWithProfiles(t, svc, screen, nil)
}

func newScreenTestServerWithProfiles(t *testing.T, svc simsvc.Manager, screen httpd.SimScreen, profiles controllers.SimProfileResolver) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil,
		httpd.APIDeps{Sim: svc, SimScreen: screen, SimDrags: simgesture.NewDrags(), SimProfiles: profiles},
		httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}
```

Keeping `newScreenTestServer` as a two-line wrapper means none of the existing call sites change.

- [ ] **Step 2: Write the failing tests**

Append to `backend/internal/httpd/controllers/sim_power_test.go`:

```go
type fakeProfiles struct {
	profile *simslim.Profile
	err     error
	asked   domain.SessionID
}

func (f *fakeProfiles) SimProfileFor(_ context.Context, id domain.SessionID) (*simslim.Profile, error) {
	f.asked = id
	return f.profile, f.err
}

func TestSimPower_BootCarriesTheProjectsProfile(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	profiles := &fakeProfiles{profile: &simslim.Profile{Keep: []string{"com.apple.apsd"}}}
	srv := newScreenTestServerWithProfiles(t, &fakeSimService{}, screen, profiles)

	code, _ := postJSON(t, powerURL(srv.URL, "p-1", otherSimUDID), map[string]any{"state": "booted"})
	if code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", code)
	}

	ops := screen.powered()
	if len(ops) != 1 || ops[0].Req == nil || ops[0].Req.Profile == nil {
		t.Fatalf("powered %+v, want a boot carrying the project's profile", ops)
	}
	if ops[0].Req.Profile.Keep[0] != "com.apple.apsd" {
		t.Fatalf("keep = %v", ops[0].Req.Profile.Keep)
	}
	if profiles.asked != domain.SessionID("p-1") {
		t.Fatalf("resolved for %q, want the session named in the route", profiles.asked)
	}
}

func TestSimPower_BootWithoutAConfiguredProfileSlimsNothing(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	srv := newScreenTestServerWithProfiles(t, &fakeSimService{}, screen, &fakeProfiles{})

	postJSON(t, powerURL(srv.URL, "p-1", otherSimUDID), map[string]any{"state": "booted"})

	ops := screen.powered()
	if len(ops) != 1 || ops[0].Req != nil {
		t.Fatalf("powered %+v, want a nil request for a project that does not slim", ops)
	}
}

// A resolver that failed must not end up looking like a project that does not
// slim, and must not fail the boot either.
func TestSimPower_BootCarriesAResolverFailure(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	srv := newScreenTestServerWithProfiles(t, &fakeSimService{}, screen,
		&fakeProfiles{err: errors.New("project 7 is degraded")})

	code, _ := postJSON(t, powerURL(srv.URL, "p-1", otherSimUDID), map[string]any{"state": "booted"})
	if code != http.StatusAccepted {
		t.Fatalf("status %d, want 202: a boot must not fail over a profile", code)
	}

	ops := screen.powered()
	if len(ops) != 1 || ops[0].Req == nil || ops[0].Req.Err == nil {
		t.Fatalf("powered %+v, want the resolver error carried through", ops)
	}
}

// A daemon with no resolver behaves exactly as it did before this feature.
func TestSimPower_BootWithoutAResolverSlimsNothing(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	postJSON(t, powerURL(srv.URL, "p-1", otherSimUDID), map[string]any{"state": "booted"})

	ops := screen.powered()
	if len(ops) != 1 || ops[0].Req != nil {
		t.Fatalf("powered %+v, want a nil request with no resolver", ops)
	}
}

// Shutdown never resolves a profile; there is nothing to slim on the way down.
func TestSimPower_ShutdownNeverResolvesAProfile(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	profiles := &fakeProfiles{profile: &simslim.Profile{Keep: []string{"com.apple.apsd"}}}
	srv := newScreenTestServerWithProfiles(t, &fakeSimService{}, screen, profiles)

	postJSON(t, powerURL(srv.URL, "p-1", testSimUDID), map[string]any{"state": "shutdown"})

	if profiles.asked != "" {
		t.Fatalf("shutdown resolved a profile for %q", profiles.asked)
	}
}
```

`oneBooted()` returns two devices: `testSimUDID` is Booted and `otherSimUDID` is Shutdown. Boot tests target `otherSimUDID` and the shutdown test targets `testSimUDID`, exactly as the neighbouring tests do - the route answers 409 for a device already in the requested state.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd backend && go test ./internal/httpd/controllers/... -run TestSimPower`
Expected: FAIL — `SimProfileResolver`, `APIDeps.SimProfiles` and `powerCall.Req` are undefined.

- [ ] **Step 4: Write the implementation**

In `backend/internal/httpd/controllers/sim_screen.go`, beside the `SimScreenProvider` interface:

```go
// SimProfileResolver answers which daemon profile a session's project wants its
// simulators slimmed to. (nil, nil) means the project does not slim.
//
// It is a narrow interface on the controller, injected the way Leases is,
// because the alternative is teaching Screen and Power what a project is - and
// they are device-level surfaces whose testability rests on knowing nothing of
// the sort.
type SimProfileResolver interface {
	SimProfileFor(ctx context.Context, id domain.SessionID) (*simslim.Profile, error)
}
```

Add to `SimScreenController`:

```go
	// Profiles resolves the slimming profile for a boot. nil means this daemon
	// slims nothing, which is what every deployment did before it existed.
	Profiles SimProfileResolver
```

Add the method, and use it at the `StartPower` call:

```go
// profileFor works out what this boot should do about slimming.
//
// A resolver error is carried rather than swallowed: the boot goes ahead - it
// has to, because `ao sim boot` is what lets a qa be created at all - but
// "we could not work out this project's profile" must not end up looking
// identical to "this project does not slim".
func (c *SimScreenController) profileFor(ctx context.Context, op simpower.Op, id domain.SessionID) *simslim.Request {
	if op != simpower.Boot || c.Profiles == nil {
		return nil
	}
	prof, err := c.Profiles.SimProfileFor(ctx, id)
	if err != nil {
		return &simslim.Request{Err: err}
	}
	if prof == nil {
		return nil
	}
	return &simslim.Request{Profile: prof}
}
```

```go
	if err := c.Screen.StartPower(r.Context(), device.UDID, op, c.profileFor(r.Context(), op, sessionID), done); err != nil {
```

Create `backend/internal/httpd/simprofile.go`:

```go
package httpd

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/simslim"
)

// simProfiles resolves a session's project's simulator profile.
//
// It lives here rather than in the controller because this is the one place
// that already holds every service, and it is the only thing in the chain that
// needs to know a session belongs to a project.
type simProfiles struct {
	sessions controllers.SessionService
	projects projectsvc.Manager
}

var _ controllers.SimProfileResolver = simProfiles{}

// SimProfileFor returns (nil, nil) when the project does not slim - which is
// every project that has not opted in.
func (r simProfiles) SimProfileFor(ctx context.Context, id domain.SessionID) (*simslim.Profile, error) {
	session, err := r.sessions.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	got, err := r.projects.Get(ctx, session.ProjectID)
	if err != nil {
		return nil, err
	}
	if got.Project == nil || got.Project.Config == nil || got.Project.Config.SimProfile == nil {
		return nil, nil
	}
	return &simslim.Profile{Keep: got.Project.Config.SimProfile.Keep}, nil
}
```

In `backend/internal/httpd/api.go`, add to `APIDeps`:

```go
	// SimProfiles resolves a boot's slimming profile. Left nil, the router
	// builds one over Sessions and Projects; a test sets it to control the
	// answer without standing up either service.
	SimProfiles controllers.SimProfileResolver
```

and where the sim screen controller is constructed, resolve the default:

```go
	simProfileResolver := deps.SimProfiles
	if simProfileResolver == nil && deps.Sessions != nil && deps.Projects != nil {
		simProfileResolver = simProfiles{sessions: deps.Sessions, projects: deps.Projects}
	}
```

then set `Profiles: simProfileResolver` on the controller. `backend/internal/daemon/daemon.go` needs no change: it already passes `Sessions` and `Projects`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd backend && go build ./... && go test ./internal/httpd/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/
git commit -m "feat(sim): slim a booted device to its project's profile

The resolver's error is carried through rather than swallowed, so
'we could not work out the profile' never reads as 'this project
does not slim'.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 6: Expose the phase and the profile on the wire

**Files:**
- Modify: `backend/internal/httpd/controllers/sim_screen.go` (`SimDevicePowerView` ~line 91, `powerView` ~line 371)
- Test: `backend/internal/httpd/controllers/sim_power_test.go`
- Regenerate: `frontend/src/api/schema.ts` and the OpenAPI spec via `npm run api`

**Interfaces:**
- Consumes: `simpower.Status.Phase`, `simpower.Status.Profile`, `simpower.Warned` from Task 2.
- Produces: `SimDevicePowerView.Phase`, `.Profile`, `.ProfileReason` (all `string`).

- [ ] **Step 1: Write the failing tests**

Append to `backend/internal/httpd/controllers/sim_power_test.go`. These follow
`TestSimDevices_CarryAPowerOperationInFlight` (line 228) exactly: the same
`powerStatus` map on `fakeScreen`, the same inline anonymous struct, the same
`getJSON` helper. `oneBooted()` makes `testSimUDID` Booted and `otherSimUDID`
Shutdown.

```go
func TestSimDevices_ReportABootedDeviceThatCameUpStock(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted(), powerStatus: map[string]simpower.Status{
		testSimUDID: {
			Op:      simpower.Boot,
			State:   simpower.Warned,
			Profile: &simslim.Result{Outcome: simslim.Skipped, Reason: "simslim is not on PATH, so this device is stock"},
		},
	}}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	var out struct {
		Devices []struct {
			UDID  string `json:"udid"`
			Power *struct {
				State         string `json:"state"`
				Phase         string `json:"phase"`
				Profile       string `json:"profile"`
				ProfileReason string `json:"profileReason"`
			} `json:"power"`
		} `json:"devices"`
	}
	if code := getJSON(t, srv.URL+"/api/v1/sim/devices", &out); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}

	var found bool
	for _, d := range out.Devices {
		if d.UDID != testSimUDID {
			continue
		}
		found = true
		if d.Power == nil {
			t.Fatal("a stock device produced no power view, so the pane says nothing")
		}
		if d.Power.State != "warned" {
			t.Fatalf("state = %q, want warned - the boot itself worked", d.Power.State)
		}
		if d.Power.Profile != "skipped" {
			t.Fatalf("profile = %q, want skipped", d.Power.Profile)
		}
		if d.Power.ProfileReason == "" {
			t.Fatal("no reason reached the pane")
		}
	}
	if !found {
		t.Fatalf("%s missing from the listing", testSimUDID)
	}
}

func TestSimDevices_CarryTheSlimmingPhase(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted(), powerStatus: map[string]simpower.Status{
		testSimUDID: {Op: simpower.Boot, State: simpower.Running, Phase: simpower.PhaseSlimming},
	}}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	var out struct {
		Devices []struct {
			UDID  string `json:"udid"`
			Power *struct {
				Phase string `json:"phase"`
			} `json:"power"`
		} `json:"devices"`
	}
	if code := getJSON(t, srv.URL+"/api/v1/sim/devices", &out); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	for _, d := range out.Devices {
		if d.UDID == testSimUDID {
			if d.Power == nil || d.Power.Phase != "slimming" {
				t.Fatalf("power = %+v, want phase slimming", d.Power)
			}
		}
	}
}

// A Warned entry is ABOUT a device that is booted. The rule that drops a stale
// failure once the device reached the goal anyway must not touch it, or the
// warning is deleted the instant it becomes true.
func TestSimDevices_KeepAStockWarningOnABootedDevice(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted(), powerStatus: map[string]simpower.Status{
		testSimUDID: {Op: simpower.Boot, State: simpower.Warned,
			Profile: &simslim.Result{Outcome: simslim.Failed, Reason: "it refused"}},
	}}
	srv := newScreenTestServer(t, &fakeSimService{}, screen)

	var out struct {
		Devices []struct {
			UDID  string          `json:"udid"`
			Power *map[string]any `json:"power"`
		} `json:"devices"`
	}
	if code := getJSON(t, srv.URL+"/api/v1/sim/devices", &out); code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	for _, d := range out.Devices {
		if d.UDID == testSimUDID && d.Power == nil {
			t.Fatal("the warning was cleared because the device reached Booted")
		}
	}

	screen.mu.Lock()
	cleared := len(screen.cleared)
	screen.mu.Unlock()
	if cleared != 0 {
		t.Fatalf("ClearPower was called %d times on a Warned entry", cleared)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd backend && go test ./internal/httpd/controllers/... -run TestSimDevices`
Expected: FAIL — `Phase`, `Profile` and `ProfileReason` are not fields of `SimDevicePowerView`.

- [ ] **Step 3: Write the implementation**

Extend `SimDevicePowerView`:

```go
type SimDevicePowerView struct {
	Op        simpower.Op    `json:"op" description:"boot or shutdown."`
	State     simpower.State `json:"state" description:"running while the operation is in flight; failed when it did not work; warned when it worked but left the device unslimmed."`
	StartedAt time.Time      `json:"startedAt"`
	Reason    string         `json:"reason,omitempty" description:"Why it failed, in the machine's own words where there are any."`
	// Phase is which part of a boot is running: booting, then slimming. The
	// second takes tens of seconds, and without this the pane looks frozen for
	// all of them.
	Phase string `json:"phase,omitempty" description:"booting or slimming, while a boot is in flight."`
	// Profile and ProfileReason are what happened to the device's daemon
	// profile. skipped and failed both mean the device is stock.
	Profile       string `json:"profile,omitempty" description:"applied, already, skipped or failed. skipped and failed mean the device is stock."`
	ProfileReason string `json:"profileReason,omitempty" description:"Why the device is stock, in the tool's own words."`
}
```

Change the tail of `powerView` to carry them:

```go
	v := &SimDevicePowerView{
		Op: status.Op, State: status.State, StartedAt: status.StartedAt.UTC(), Reason: status.Reason,
		Phase: status.Phase,
	}
	if status.Profile != nil {
		v.Profile = string(status.Profile.Outcome)
		v.ProfileReason = status.Profile.Reason
	}
	return v
```

⚠ Leave the early return above it exactly as it is. It must keep testing
`status.State == simpower.Failed`, never `Warned`: it drops an entry once the
device reached the goal anyway, and a `Warned` entry is *about* a device that
reached the goal. Widening that condition would delete the warning the moment
it became true.

- [ ] **Step 4: Run the tests, then regenerate the API**

Run: `cd backend && go test ./internal/httpd/...`
Expected: PASS.

Run: `npm run api`
Expected: `frontend/src/api/schema.ts` and the OpenAPI spec gain `phase`, `profile` and `profileReason` on `ControllersSimDevicePowerView`.

- [ ] **Step 5: Commit**

```bash
git add backend/ frontend/src/api/ docs/
git commit -m "feat(sim): put the boot phase and profile outcome on the wire

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 7: Report the outcome from `ao sim boot`

**Files:**
- Modify: `backend/internal/cli/sim_boot.go` (`simBootResult` ~line 88, `simDevicePowerListing` ~line 109, the result construction ~line 395, `writeSimBoot` ~line 402)
- Test: `backend/internal/cli/sim_boot_test.go`

**Interfaces:**
- Consumes: `SimDevicePowerView.Phase`/`Profile`/`ProfileReason` from Task 6.
- Produces: `simBootResult.Profile string`, `simBootResult.ProfileReason string`.

- [ ] **Step 1: Write the failing tests**

Append to `backend/internal/cli/sim_boot_test.go`:

```go
func TestWriteSimBoot_SaysPlainlyWhenTheDeviceIsStock(t *testing.T) {
	var out bytes.Buffer
	result := simBootResult{
		UDID: "4754DB41-86C8-4326-81A7-172DDD41D5DA", Name: "AO scratch",
		Runtime: "iOS 26.3", State: "Booted", Note: simBootedDeviceNote,
		Profile:       "skipped",
		ProfileReason: "simslim is not on PATH, so this device is stock",
	}

	if err := writeSimBoot(&out, result); err != nil {
		t.Fatalf("writeSimBoot: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "STOCK") {
		t.Fatalf("output never says the device is stock:\n%s", got)
	}
	if !strings.Contains(got, "simslim is not on PATH") {
		t.Fatalf("output dropped the reason:\n%s", got)
	}
}

func TestWriteSimBoot_SaysNothingExtraWhenTheProfileLanded(t *testing.T) {
	var out bytes.Buffer
	result := simBootResult{
		UDID: "4754DB41-86C8-4326-81A7-172DDD41D5DA", Name: "AO scratch",
		Runtime: "iOS 26.3", State: "Booted", Note: simBootedDeviceNote,
		Profile: "applied",
	}

	if err := writeSimBoot(&out, result); err != nil {
		t.Fatalf("writeSimBoot: %v", err)
	}

	if strings.Contains(out.String(), "STOCK") {
		t.Fatalf("a slimmed device was reported as stock:\n%s", out.String())
	}
}

func TestWriteSimBoot_SaysNothingExtraForAProjectThatDoesNotSlim(t *testing.T) {
	var out bytes.Buffer
	result := simBootResult{
		UDID: "4754DB41-86C8-4326-81A7-172DDD41D5DA", Name: "AO scratch",
		Runtime: "iOS 26.3", State: "Booted", Note: simBootedDeviceNote,
	}

	if err := writeSimBoot(&out, result); err != nil {
		t.Fatalf("writeSimBoot: %v", err)
	}

	if strings.Contains(out.String(), "STOCK") {
		t.Fatalf("a project with no profile was warned at:\n%s", out.String())
	}
}
```

Add `bytes` and `strings` to the test file's imports if they are not already there.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd backend && go test ./internal/cli/... -run TestWriteSimBoot`
Expected: FAIL — `simBootResult` has no `Profile` field.

- [ ] **Step 3: Write the implementation**

Extend `simBootResult`:

```go
	// Profile is what happened to the device's daemon profile: applied,
	// already, skipped or failed. Empty when the project does not slim.
	Profile string `json:"profile,omitempty"`
	// ProfileReason says why the device is stock, when it is.
	ProfileReason string `json:"profileReason,omitempty"`
```

Extend `simDevicePowerListing` to mirror the view:

```go
	Phase         string `json:"phase,omitempty"`
	Profile       string `json:"profile,omitempty"`
	ProfileReason string `json:"profileReason,omitempty"`
```

Where `simBootResult` is built (~line 395), copy them across when the listing carries a `Power`:

```go
	if listing.Power != nil {
		result.Profile = listing.Power.Profile
		result.ProfileReason = listing.Power.ProfileReason
	}
```

In `writeSimBoot`, after the `Note:` line, replace the bare `return err` tail with:

```go
	if _, err := fmt.Fprintf(out, "Note: %s\n", result.Note); err != nil {
		return err
	}
	// A device that came up stock is the failure this whole feature is shaped
	// around: `xcrun simctl push` returns exit 0 and prints "Notification sent"
	// on a device whose apsd is disabled, so an agent that is not told here
	// will believe a notification landed when nothing was delivered.
	if result.Profile == "skipped" || result.Profile == "failed" {
		_, err := fmt.Fprintf(out,
			"Warning: this simulator is STOCK, not slimmed - %s\n"+
				"Features this project expects may silently do nothing.\n",
			result.ProfileReason)
		return err
	}
	return nil
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd backend && go test ./internal/cli/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/cli/
git commit -m "feat(cli): ao sim boot says when the device came up stock

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 8: Show the phase and the warning in the Device tab

**Files:**
- Modify: `frontend/src/renderer/components/SimDevicePicker.tsx` (~line 225 `running`, ~line 298 the failure row, ~line 333 the in-flight label)
- Test: `frontend/src/renderer/components/SimDevicePicker.test.tsx`

**Interfaces:**
- Consumes: the regenerated `ControllersSimDevicePowerView` from Task 6, with `phase`, `profile` and `profileReason`.

- [ ] **Step 1: Write the failing tests**

Append to `frontend/src/renderer/components/SimDevicePicker.test.tsx`, following the
existing `open([...]) / await openPicker()` pattern used by the boot-in-flight test
at line 177:

```tsx
// The slimming phase takes tens of seconds. Without a label the pane just looks
// stuck on a device that is already up.
it("names the slimming phase instead of looking frozen", async () => {
	open([
		device({
			state: "Booted",
			power: { op: "boot", state: "running", phase: "slimming", startedAt: "2026-08-20T09:00:00Z" },
		} as Partial<SimDevice>),
	]);
	await openPicker();

	expect(screen.getByTestId("sim-power-running")).toHaveTextContent(/slimming/i);
});

// The boot worked. The device is stock. Saying nothing is how an agent ends up
// trusting a push that was never delivered.
it("warns that a booted device came up stock", async () => {
	open([
		device({
			state: "Booted",
			power: {
				op: "boot",
				state: "warned",
				startedAt: "2026-08-20T09:00:00Z",
				profile: "skipped",
				profileReason: "simslim is not on PATH, so this device is stock",
			},
		} as Partial<SimDevice>),
	]);
	await openPicker();

	const stock = screen.getByTestId("sim-power-stock");
	expect(stock).toHaveTextContent(/stock/i);
	expect(stock).toHaveTextContent(/not on PATH/i);
});

it("says nothing extra when the profile landed", async () => {
	open([
		device({
			state: "Booted",
			power: { op: "boot", state: "running", phase: "booting", startedAt: "2026-08-20T09:00:00Z" },
		} as Partial<SimDevice>),
	]);
	await openPicker();

	expect(screen.queryByTestId("sim-power-stock")).not.toBeInTheDocument();
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npm test -- SimDevicePicker`
Expected: FAIL — no element carries `sim-power-stock`, and the in-flight row says "Booting" during the slimming phase.

- [ ] **Step 3: Write the implementation**

In `SimDevicePicker.tsx`, name the phase in the in-flight label (~line 333):

```tsx
{power.op === "boot" ? (power.phase === "slimming" ? "Slimming" : "Booting") : "Shutting down"}
, {elapsed} so far, giving up at {BOOT_TIMEOUT_LABEL}
```

Add the stock row beside the existing failure row (~line 298):

```tsx
{power?.state === "failed" ? <Failure reason={power.reason ?? "It did not work, and said nothing."} /> : null}
{power?.state === "warned" ? <Stock reason={power.profileReason ?? "It came up stock and said nothing."} /> : null}
```

Add `Stock` next to the existing `Failure` component. Style it as a warning, not
an error: the boot worked, and dressing a success as a failure is how people
learn to ignore a row.

```tsx
function Stock({ reason }: { reason: string }) {
	return (
		<p data-testid="sim-power-stock" className="text-xs text-amber-600 dark:text-amber-500">
			This simulator is stock, not slimmed - {reason} Features this project expects may silently do nothing.
		</p>
	);
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npm test -- SimDevicePicker`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/renderer/components/
git commit -m "feat(device-tab): name the slimming phase and flag a stock device

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 9: Verify the whole thing against a real simulator

**Files:**
- None. This task changes nothing; it checks the feature on the machine.

The unit tests all run over fakes, by design. Nothing so far has proved that the flags this code sends are flags the real `simslim` accepts.

- [ ] **Step 1: Run the full suite and the linter**

```bash
cd backend && go build ./... && go test ./... && golangci-lint run
npm test
```
Expected: all green. `golangci-lint` is the gate that catches what `go vet` does not; clean its cache first if it reports findings in worktrees that no longer exist.

- [ ] **Step 2: Check the tool's real flags**

```bash
simslim verify --help
simslim on --help
```
Expected: both accept `<udid>` positionally and `--keep <comma,separated>`. If the real tool disagrees with `args()` in Task 1, fix `args()` and its tests before going further.

If `simslim` is not installed, install it with `go install github.com/mobai-app/simslim/cmd/simslim@latest` — note the lowercase module path and the `cmd/simslim` suffix; the README's path does not build.

- [ ] **Step 3: Boot a scratch device through AO and watch it slim**

Set a project's config to:

```json
"simProfile": { "keep": [
  "com.apple.apsd", "com.apple.swcd", "com.apple.assetsd",
  "com.apple.telephonyutilities.callservicesd"
]}
```

Create a scratch device, boot it with `ao sim boot --udid <udid> --json`, and confirm the payload carries `"profile":"applied"`. Then confirm the device really is slim:

```bash
simslim status <udid>
```
Expected: `165/170 managed daemons disabled (partially slim)`.

- [ ] **Step 4: Boot it a second time and confirm there is no second reboot**

Shut the device down, boot it again through AO, and confirm the payload now says `"profile":"already"` and that the device did not reboot twice. This is the check that the `verify`-first ordering is actually working end to end.

- [ ] **Step 5: Confirm a machine without the tool still boots**

Temporarily move `simslim` off PATH, boot a device, and confirm the boot still succeeds, the CLI prints the stock warning, and the Device tab shows the amber row.

- [ ] **Step 6: Delete the scratch device**

```bash
xcrun simctl shutdown <udid>; xcrun simctl delete <udid>
```

- [ ] **Step 7: Author the smoke-test checklist and open the PR**

The runtime behaviour here — a real device rebooting, a real tool missing, an amber row in a live pane — is not something unit tests over fakes can cover. Author the checklist with `ao smoke set "$AO_CREW_ID" --from-file -` before opening the PR, with one case per manual check in Steps 3-5.

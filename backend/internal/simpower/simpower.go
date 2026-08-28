// Package simpower is the one place in AO that can change a local iOS
// Simulator's power state.
//
// It is a package of its own rather than three functions in internal/simctl on
// purpose. simctl documents itself as read-only against the machine, and that
// promise is load-bearing: it is what lets every caller of a device listing -
// the CLI, the daemon, a test - be sure that asking what exists cannot change
// what exists. Powering a device on and off is the opposite kind of operation,
// so it lives behind its own door.
//
// ⚠ Boot is reachable from an agent; taking a device DOWN is not, and the
// asymmetry is deliberate.
//
// Booting was human-only at first, on the grounds that an agent able to boot
// would quietly accumulate 4 GB virtual machines while nobody was watching -
// this machine has hit a true OOM from three booted at once. That held until it
// deadlocked something: a task gains its qa on its first simulator LEASE, a
// lease needs a booted device, so on a machine where nobody had left one running
// a qa could never appear at all. `ao sim boot` (backend/internal/cli/sim_boot.go)
// is the reversal, and it carries the memory argument with it rather than
// dropping it: the CLI refuses to make the third booted simulator, so the number
// that caused the OOM stays out of an agent's reach.
//
// Shutdown keeps the original answer, because none of that applies to it: a
// shutdown takes a device out from under whoever is on it, and it unblocks
// nothing. It stays behind the Device tab, where a human presses the button.
package simpower

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simslim"
)

// Op is what is being done to a device.
type Op string

// The two operations that exist. Reboot and erase are deliberately absent:
// neither is something the Device tab offers, and an operation nothing calls
// is an operation nothing guards. Only Boot is reachable from the CLI.
const (
	Boot     Op = "boot"
	Shutdown Op = "shutdown"
)

// State is how an operation is going.
type State string

const (
	// Running means the operation is in flight. A booting device is not yet
	// listed as Booted, and even once it is it may not be drivable.
	Running State = "running"
	// Failed means it did not work, and Reason says what the machine said. A
	// failure is kept rather than dropped, because the alternative is a
	// control that spins for ever and never says why.
	Failed State = "failed"
	// Warned means the operation itself worked, but something about the device the
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
)

// Sentinel refusals, as values because two surfaces phrase them: an HTTP
// status the renderer branches on, and a sentence a person reads.
var (
	// ErrUnavailable: this machine cannot power a simulator at all (no xcrun).
	ErrUnavailable = errors.New("simpower: unavailable")
	// ErrBusy: something is already being done to this device. One operation
	// per device at a time - `boot` and `shutdown` racing on one udid is a
	// device in whichever state won.
	ErrBusy = errors.New("simpower: an operation is already in flight on this device")
)

// BootTimeout bounds a boot.
//
// Two minutes, chosen from what a boot actually costs rather than from a round
// number: a warm device is up in tens of seconds, and the slow tail is the
// first boot of a runtime after an Xcode update. Two minutes clears that tail
// while still failing inside a wait a person will sit through - and a control
// that reports a failure is the whole point, because the thing being replaced
// was a spinner with nothing behind it.
const BootTimeout = 2 * time.Minute

// ShutdownTimeout bounds a shutdown, which is a message to a running process
// rather than a machine coming up, and so is a different order of wait.
const ShutdownTimeout = 30 * time.Second

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

// Status is one device's in-flight or failed operation. There is deliberately
// no "succeeded": once a boot works, the device's own state is the report, and
// a second place saying the same thing is a second place to be wrong.
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

// Power runs the operations and remembers what is in flight.
//
// The remembering is why this is a type rather than two functions. A boot takes
// tens of seconds and the HTTP request that asked for it is answered
// immediately, so the progress has to outlive the request - and it has to
// survive the popover being closed and the renderer being reloaded, both of
// which a human does while waiting. Keeping it in the daemon means the pane
// polls for it with the device listing it already polls for.
type Power struct {
	lookPath simctl.LookPath
	run      simctl.Runner
	now      func() time.Time

	// bootTimeout is a field rather than the constant so a test can watch the
	// timeout path without waiting two minutes for it.
	bootTimeout     time.Duration
	shutdownTimeout time.Duration
	profileTimeout  time.Duration

	mu        sync.Mutex
	entries   map[string]Status
	onSettled func()

	// wg tracks the detached operations, so a test can wait for them without
	// sleeping. Nothing in production waits on it.
	wg sync.WaitGroup
}

// New builds a Power over an injected simctl runner, so every path here is
// testable without Xcode, a mac or a device.
func New(lookPath simctl.LookPath, run simctl.Runner) *Power {
	return &Power{
		lookPath:        lookPath,
		run:             run,
		now:             time.Now,
		bootTimeout:     BootTimeout,
		shutdownTimeout: ShutdownTimeout,
		profileTimeout:  ProfileTimeout,
		entries:         map[string]Status{},
	}
}

// OnSettled registers a callback for when an operation finishes, however it
// finished. The daemon uses it to drop its cached device listing: that listing
// is reused for a couple of seconds, and without this a device would keep
// reading as "booting" in the pane for a beat after it was up.
func (p *Power) OnSettled(fn func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onSettled = fn
}

// Start begins an operation and returns at once. The work is detached from the
// caller's context on purpose: the request that asked for a boot is answered
// immediately, and a boot that died with it would never land.
//
// done, when it is not nil, is called once this operation has settled - the
// shutdown path uses it to give back the lease it took to arbitrate the
// shutdown, so the device is arbitrated for exactly as long as it is being
// powered off and not a moment longer.
func (p *Power) Start(ctx context.Context, udid string, op Op, req *simslim.Request, done func()) error {
	timeout, args, err := p.plan(op, domain.NormalizeSimUDID(udid))
	if err != nil {
		return err
	}
	if _, err := p.lookPath(simctl.Binary); err != nil {
		return fmt.Errorf("%w: %s not found on PATH", ErrUnavailable, simctl.Binary)
	}

	key := domain.NormalizeSimUDID(udid)
	p.mu.Lock()
	if current, ok := p.entries[key]; ok && current.State == Running {
		p.mu.Unlock()
		return fmt.Errorf("%w: %s is already in flight", ErrBusy, current.Op)
	}
	p.entries[key] = Status{Op: op, State: Running, StartedAt: p.now(), Phase: PhaseBooting}
	p.mu.Unlock()

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.execute(context.WithoutCancel(ctx), key, op, timeout, args, req)
		if done != nil {
			done()
		}
	}()
	return nil
}

// plan turns an operation into the command that performs it.
func (p *Power) plan(op Op, udid string) (time.Duration, []string, error) {
	switch op {
	case Boot:
		// `bootstatus -b` rather than `boot`, because it is the only form that
		// says when the device is actually up. `simctl boot` returns as soon as
		// the request is accepted and `simctl list` flips to Booted seconds
		// before SpringBoard is running, so a caller watching the state would
		// report success on a device that cannot yet be driven - which is
		// precisely the "it says it worked and nothing happens" failure this
		// control exists to avoid.
		return p.bootTimeout, []string{"simctl", "bootstatus", udid, "-b"}, nil
	case Shutdown:
		return p.shutdownTimeout, []string{"simctl", "shutdown", udid}, nil
	default:
		return 0, nil, fmt.Errorf("simpower: unknown operation %q", op)
	}
}

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
			Reason:    reason(runCtx, op, timeout, out, err),
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

// reason says why an operation failed, in the machine's own words wherever
// there are any. A timeout gets a sentence of ours because the machine said
// nothing - it was still trying.
//
// ⚠ A boot that times out leaves the half-booted device exactly where it is.
// Shutting it down here would be AO undoing something on the human's behalf,
// which is the one thing the memory guard forbids: they are told what happened
// and the row offers them Shut down.
func reason(ctx context.Context, op Op, timeout time.Duration, out []byte, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		if op == Boot {
			return fmt.Sprintf("the simulator did not finish booting within %s. It may still be coming up - "+
				"give it a moment and look again, or shut it down and try once more", timeout)
		}
		return fmt.Sprintf("the simulator did not shut down within %s", timeout)
	}
	if detail := simctl.Output(out); detail != "(no output)" {
		return detail
	}
	return err.Error()
}

// Status is what is happening to one device, if anything.
func (p *Power) Status(udid string) (Status, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	status, ok := p.entries[domain.NormalizeSimUDID(udid)]
	return status, ok
}

// All is every device with something in flight or a failure to report, keyed
// by normalized udid. It is a copy: callers read it while operations finish.
func (p *Power) All() map[string]Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]Status, len(p.entries))
	for udid, status := range p.entries {
		out[udid] = status
	}
	return out
}

// Clear drops a failure once it has been read. An operation that is still
// running is left alone: dismissing a spinner would not stop the boot behind
// it, it would only stop saying so.
func (p *Power) Clear(udid string) {
	key := domain.NormalizeSimUDID(udid)
	p.mu.Lock()
	defer p.mu.Unlock()
	if status, ok := p.entries[key]; ok && status.State != Running {
		delete(p.entries, key)
	}
}

// wait blocks until every detached operation has finished. Tests only.
func (p *Power) wait() { p.wg.Wait() }

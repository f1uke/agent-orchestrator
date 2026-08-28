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
// This package knows the tool's command line, and its vocabulary stops at the
// device: it never looks a profile up, so it holds no notion of a project, a
// session or a config file. The one concession is Request, which carries a
// profile a CALLER has already resolved together with the failure it may have
// hit doing so - it is a way of reporting the caller's own bad news down here,
// not a way of asking AO anything. It takes the same injected lookPath and
// runner every other sim package takes, so it is testable without Xcode, a mac
// or a device.
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

// Request is what a caller knows about slimming for one boot. Apply never takes
// one - it is plumbing between a resolver and whoever drives the boot, and it
// sits here only so that both ends speak this package's vocabulary rather than
// inventing a third.
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
	// Applied means the device had drifted, so `simslim on` ran and rebooted it.
	Applied Outcome = "applied"
	// Already means `simslim verify` passed and nothing was run. The common case.
	Already Outcome = "already"
	// Skipped means simslim is not installed. The device is stock.
	Skipped Outcome = "skipped"
	// Failed means simslim ran and refused. The device is stock.
	Failed Outcome = "failed"
)

// Result is an Outcome plus, when there is bad news, why.
type Result struct {
	Outcome Outcome `json:"outcome"`
	Reason  string  `json:"reason,omitempty"`
}

// Stock says whether an outcome leaves the caller with an unslimmed device,
// which is the one thing every reporting surface needs to ask. It is a function
// as well as a method because the surfaces that ask do not all hold a Result -
// the CLI has only the outcome's name, off the wire - and a fifth outcome must
// change the answer in ONE place rather than in however many string comparisons
// have accumulated.
func Stock(o Outcome) bool { return o == Skipped || o == Failed }

// Stock says whether this result leaves the caller with an unslimmed device.
func (r Result) Stock() bool { return Stock(r.Outcome) }

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
		// Just the fact, and not what it means: every surface that prints this
		// already says the device is stock in its own words, and a reason that
		// says it too renders as "stock ... so this device is stock".
		return Result{Outcome: Skipped, Reason: Binary + " is not on PATH"}
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

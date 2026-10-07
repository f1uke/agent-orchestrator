// Package simhealth answers one question about a session's simulator: is it
// worth driving right now? It is the engine behind `ao sim doctor`, and behind
// the scripts store's `bin/flow doctor`, which asks AO for these lines instead
// of re-implementing them.
//
// It is read-only by construction. Everything it learns arrives through
// Readers, and Readers has no field that could boot, claim, install or trust:
// a doctor that fixed what it found would change the very state it reports,
// and two doctors run back to back would disagree.
package simhealth

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbuild"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simtrust"
)

// Status is how one check came out.
type Status string

// Check statuses. WARN never fails a report: it marks something a run will
// fix by itself, or something the doctor was not asked to check.
const (
	StatusOK   Status = "OK"
	StatusWarn Status = "WARN"
	StatusFail Status = "FAIL"
)

// Check names, in the order a report lists them.
const (
	CheckDevice  = "device"
	CheckLease   = "lease"
	CheckApp     = "app"
	CheckProxyCA = "proxy CA"
)

// Check is one line of a report.
type Check struct {
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Message string `json:"message"`
}

// Report is every check that applied. OK is true when none of them failed.
type Report struct {
	Checks []Check `json:"checks"`
	OK     bool    `json:"ok"`
}

// Request is what the caller asked about.
type Request struct {
	// SessionID is the session asking: its assigned device, its lease and its
	// project's root CAs are what the checks compare against.
	SessionID domain.SessionID
	// UDID names a device. Empty means the session's assigned one.
	UDID string
	// App is a bundle id. Empty skips the build check.
	App string
	// Expect is a .app bundle on this Mac the installed App must be. Empty
	// reports the installed build without comparing it.
	Expect string
}

// Readers is everything the doctor reads.
type Readers struct {
	// Devices lists this machine's simulators, fresh.
	Devices func(ctx context.Context) ([]simctl.Device, error)
	// Assigned is the udid AO gave the session, or "" when it has none.
	Assigned func(ctx context.Context, id domain.SessionID) (string, error)
	// Leases is every live lease on the machine, other daemons' included.
	Leases func(ctx context.Context) ([]domain.SimLease, error)
	// CAFiles is the session's effective simtrust setting: its project's
	// override, else the global list.
	CAFiles func(ctx context.Context, id domain.SessionID) ([]string, error)
	// Run reads an app bundle through simbuild (plutil and codesign).
	Run simbuild.Runner
	// Trusted reports whether a device trust store holds a root with this
	// SHA-256. TrustStoreHas is the real one.
	Trusted func(ctx context.Context, store string, sum [sha256.Size]byte) (bool, error)
}

// deviceCheck is a check that needs the device the device line found. A
// device that is shut down still gets them: what is installed on it and what
// its trust store holds are files on this Mac, readable either way.
type deviceCheck func(ctx context.Context, r Readers, req Request, d simctl.Device) Check

// deviceChecks are the lines after `device`, in report order.
var deviceChecks = []deviceCheck{checkLease, checkApp, checkProxyCA}

// Diagnose runs every check that applies. A device line that found no device
// is the whole report, because every other line is about that device.
func Diagnose(ctx context.Context, r Readers, req Request) Report {
	line, device, found := checkDevice(ctx, r, req)
	checks := []Check{line}
	if found {
		for _, check := range deviceChecks {
			checks = append(checks, check(ctx, r, req, device))
		}
	}
	report := Report{Checks: checks, OK: true}
	for _, c := range checks {
		if c.Status == StatusFail {
			report.OK = false
		}
	}
	return report
}

func ok(name, format string, args ...any) Check {
	return Check{Name: name, Status: StatusOK, Message: fmt.Sprintf(format, args...)}
}

func warn(name, format string, args ...any) Check {
	return Check{Name: name, Status: StatusWarn, Message: fmt.Sprintf(format, args...)}
}

func fail(name, format string, args ...any) Check {
	return Check{Name: name, Status: StatusFail, Message: fmt.Sprintf(format, args...)}
}

// checkDevice finds the device the request is about and says whether it can
// be driven. found is false when there is no device to say anything else
// about.
func checkDevice(ctx context.Context, r Readers, req Request) (Check, simctl.Device, bool) {
	devices, err := r.Devices(ctx)
	if err != nil {
		return fail(CheckDevice, "could not list simulators: %v", err), simctl.Device{}, false
	}
	assigned, err := r.Assigned(ctx, req.SessionID)
	if err != nil {
		return fail(CheckDevice, "could not read this session's assigned simulator: %v", err), simctl.Device{}, false
	}
	assigned = domain.NormalizeSimUDID(assigned)
	want := domain.NormalizeSimUDID(req.UDID)
	if want == "" {
		want = assigned
	}
	if want == "" {
		booted := []string{}
		for _, d := range simctl.Booted(devices) {
			booted = append(booted, d.Name+" "+d.UDID)
		}
		list := strings.Join(booted, "; ")
		if list == "" {
			list = "none"
		}
		return fail(CheckDevice, "no device: pass --udid <udid>, or run in an AO session that was assigned one ($AO_SIM_UDID). Booted: %s", list),
			simctl.Device{}, false
	}
	for _, d := range devices {
		if domain.NormalizeSimUDID(d.UDID) != want {
			continue
		}
		label := fmt.Sprintf("%s (%s)", d.Name, d.UDID)
		switch {
		case !d.Booted():
			return fail(CheckDevice, "%s is %s: `ao sim boot --udid %s` (doctor never boots, claims or installs)", label, d.State, d.UDID), d, true
		case assigned != "" && want != assigned:
			return warn(CheckDevice, "%s booted, but this session's device is %s ($AO_SIM_UDID)", label, assigned), d, true
		default:
			return ok(CheckDevice, "%s booted", label), d, true
		}
	}
	return fail(CheckDevice, "%s: no such simulator - `ao sim list` shows what this machine has", want), simctl.Device{}, false
}

// checkLease says who holds the device. A device nobody holds is a warning,
// not a failure: the next run claims it.
func checkLease(ctx context.Context, r Readers, req Request, d simctl.Device) Check {
	leases, err := r.Leases(ctx)
	if err != nil {
		return fail(CheckLease, "could not read AO's simulator leases: %v", err)
	}
	key := domain.NormalizeSimUDID(d.UDID)
	for _, l := range leases {
		if domain.NormalizeSimUDID(l.UDID) != key {
			continue
		}
		until := l.ExpiresAt.UTC().Format(time.RFC3339)
		switch {
		case l.HeldElsewhere():
			return fail(CheckLease, "held by @%s through %s until %s - wait for it, or use another simulator", l.SessionID, l.OtherDaemon.Describe(), until)
		case l.SessionID == req.SessionID:
			return ok(CheckLease, "held by this session until %s", until)
		default:
			return fail(CheckLease, "held by @%s until %s - wait for it, or use another simulator", l.SessionID, until)
		}
	}
	return warn(CheckLease, "free: no AO session holds it, so a run will claim it")
}

// checkApp says which build of the app is installed and, given a bundle to
// compare with, whether it is that one. Both digests come from simbuild's
// Fingerprint, so they are only ever compared when made the same way.
func checkApp(ctx context.Context, r Readers, req Request, d simctl.Device) Check {
	if req.App == "" {
		return warn(CheckApp, "not checked: pass --app <bundle id> to check which build is installed")
	}
	installed, err := simbuild.Read(ctx, r.Run, d.DataPath, req.App)
	switch {
	case errors.Is(err, simbuild.ErrNoApp), errors.Is(err, simbuild.ErrUnknownApp):
		return fail(CheckApp, "%s is not installed on this simulator - put your build on it (`ao sim run` or `ao sim install`)", req.App)
	case err != nil:
		return fail(CheckApp, "could not read the installed %s: %v", req.App, err)
	}
	if req.Expect == "" {
		return warn(CheckApp, "%s installed; pass --expect <path.app> to check it is your build", installed.ID())
	}
	want, err := simbuild.ReadBundle(ctx, r.Run, req.Expect)
	if err != nil {
		return fail(CheckApp, "could not read --expect %s: %v", req.Expect, err)
	}
	if !strings.EqualFold(want.BundleID, installed.BundleID) {
		return fail(CheckApp, "--expect %s is %s, not %s", req.Expect, want.BundleID, installed.BundleID)
	}
	expected, err := simbuild.Fingerprint(ctx, r.Run, want)
	if err != nil {
		return fail(CheckApp, "could not fingerprint --expect %s: %v", req.Expect, err)
	}
	if expected.Digest != installed.Digest {
		return fail(CheckApp, "installed %s is not the build at %s (%s) - install it with `ao sim install %s`",
			installed.ID(), req.Expect, expected.Digest, req.Expect)
	}
	return ok(CheckApp, "%s is the build at %s", installed.ID(), req.Expect)
}

// trustedCA is one configured root CA that exists on this Mac.
type trustedCA struct {
	path string
	sum  [sha256.Size]byte
}

// checkProxyCA says whether the device trusts every root CA the session's
// project would make it trust. A configured file that is not on this Mac is
// skipped, exactly as simtrust skips it on a claim.
func checkProxyCA(ctx context.Context, r Readers, req Request, d simctl.Device) Check {
	configured, err := r.CAFiles(ctx, req.SessionID)
	if err != nil {
		return fail(CheckProxyCA, "could not work out which root CAs this session trusts: %v", err)
	}
	files := simtrust.Expand(configured)
	var present []trustedCA
	for _, path := range files {
		content, err := os.ReadFile(path) //nolint:gosec // a root-CA path from the simtrust setting
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fail(CheckProxyCA, "could not read %s: %v", path, err)
		}
		der, err := certificateDER(content)
		if err != nil {
			return fail(CheckProxyCA, "%s is not a PEM or DER certificate: %v", path, err)
		}
		present = append(present, trustedCA{path: path, sum: sha256.Sum256(der)})
	}
	if len(present) == 0 {
		if len(files) == 0 {
			return warn(CheckProxyCA, "no root CA is configured for this project, so none was checked")
		}
		return warn(CheckProxyCA, "none of the configured root CAs is on this Mac (%s), so none was checked", strings.Join(files, ", "))
	}
	store := TrustStorePath(d.DataPath)
	var trusted, missing []string
	for _, ca := range present {
		has, err := r.Trusted(ctx, store, ca.sum)
		if err != nil {
			return fail(CheckProxyCA, "could not read the simulator's trust store %s: %v", store, err)
		}
		if has {
			trusted = append(trusted, ca.path)
		} else {
			missing = append(missing, ca.path)
		}
	}
	if len(missing) > 0 {
		return fail(CheckProxyCA, "%s not trusted on this simulator - `ao sim claim` trusts it", strings.Join(missing, ", "))
	}
	return ok(CheckProxyCA, "%s trusted on this simulator", strings.Join(trusted, ", "))
}

// certificateDER is the certificate a CA file holds, as DER: the first
// CERTIFICATE block of a PEM file, or the file itself when it is DER already.
// The device's trust store keys a root by the SHA-256 of exactly these bytes.
func certificateDER(content []byte) ([]byte, error) {
	rest := content
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			return block.Bytes, nil
		}
		rest = next
	}
	if _, err := x509.ParseCertificate(content); err != nil {
		return nil, err
	}
	return content, nil
}

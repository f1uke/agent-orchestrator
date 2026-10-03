// Package simtrust makes a simulator trust the root CAs of the debugging proxy
// this Mac routes its traffic through.
//
// A simulator does NOT inherit the Mac's trust store. With a TLS-intercepting
// proxy (Proxyman, Charles, mitmproxy...) set as the system proxy, a fresh or
// erased device that has not been told to trust that proxy's root CA fails every
// HTTPS call (`-1200` / `-9802`, "Trust evaluate failure: [root AnchorTrusted]"),
// and the app sits on its splash screen - which reads exactly like an app or
// backend bug, far from the cause. So AO installs the CAs itself, every time it
// boots a device and every time a session claims one (a device a human booted
// in Xcode never passes through AO's boot, but every agent claims before it
// drives).
//
// Like simslim, the device half of this package holds no notion of a project or
// a session: Install takes the files a caller has already resolved. Which files
// those are is a setting (Settings, global, with a per-project override in
// domain.ProjectConfig.SimTrust), so switching proxy tools is a config change,
// never a code change - the human has already switched tools once.
package simtrust

import (
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
)

// InstallTimeout bounds one `simctl keychain add-root-cert`. It is a write to
// the device's trust store over the simulator's own IPC and returns in well
// under a second on a booted device; the bound only stops a wedged device from
// holding a boot or a claim open.
const InstallTimeout = 30 * time.Second

// Result is what one trust pass did to one device.
//
// A CA file that does not exist is in neither list, on purpose: the default
// setting names where Proxyman keeps its CA, and on a machine without Proxyman
// that file is simply absent. Saying so on every boot and claim would be noise
// about a tool the human does not use.
type Result struct {
	// Trusted are the CA files the device now trusts, as absolute paths.
	Trusted []string `json:"trusted,omitempty"`
	// Failed are the CA files that exist but could not be installed. A failure
	// never fails the boot or claim it rode on - it is reported instead.
	Failed []Failure `json:"failed,omitempty"`
	// At is when the pass ran.
	At time.Time `json:"at"`
}

// Failure is one CA file the device was not made to trust, and why.
type Failure struct {
	// File is the CA file, as an absolute path. Empty when the pass could not
	// work out which files to trust at all.
	File   string `json:"file,omitempty"`
	Reason string `json:"reason"`
}

// Empty reports a pass that had nothing to say: every configured file was
// absent, or none was configured.
func (r Result) Empty() bool { return len(r.Trusted) == 0 && len(r.Failed) == 0 }

// Install makes a booted device trust each CA file. It is idempotent - simctl
// records a root once however many times it is added - so it is safe to run on
// every boot and every claim. A file that does not exist is skipped silently;
// one that cannot be installed is reported in Failed and the rest still run.
func Install(ctx context.Context, run simctl.Runner, udid string, files []string) Result {
	result, _ := install(ctx, run, udid, files, nil)
	return result
}

// digest identifies a CA file by its content, so a proxy that regenerates its
// CA under the same path is installed again rather than taken as done.
type digest [sha256.Size]byte

// install is Install with a way to skip a file the device is already known to
// trust. It also returns the digest of every file it trusted, for the caller
// to remember.
func install(ctx context.Context, run simctl.Runner, udid string, files []string,
	already func(path string, sum digest) bool,
) (Result, map[string]digest) {
	result := Result{}
	trusted := map[string]digest{}
	for _, path := range Expand(files) {
		content, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			result.Failed = append(result.Failed, Failure{File: path, Reason: err.Error()})
			continue
		}
		sum := digest(sha256.Sum256(content))
		if already == nil || !already(path, sum) {
			runCtx, cancel := context.WithTimeout(ctx, InstallTimeout)
			out, err := run(runCtx, simctl.Binary, "simctl", "keychain", udid, "add-root-cert", path)
			cancel()
			if err != nil {
				result.Failed = append(result.Failed, Failure{File: path, Reason: reason(runCtx, out, err)})
				continue
			}
		}
		trusted[path] = sum
		result.Trusted = append(result.Trusted, path)
	}
	return result, trusted
}

// reason prefers what simctl said, and says so plainly when it said nothing
// because it never finished.
func reason(ctx context.Context, out []byte, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "simctl did not finish within " + InstallTimeout.String()
	}
	if detail := simctl.Output(out); detail != "(no output)" {
		return detail
	}
	return err.Error()
}

// Expand turns configured CA paths into absolute ones: `~/` is the user's home,
// blanks are dropped, and a file named twice is installed once.
func Expand(files []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		path, ok := expand(f)
		if !ok || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

// Present says, per configured path, whether that file is on this Mac right
// now - the difference between a CA that will be trusted and one that will be
// skipped.
func Present(files []string) []bool {
	out := make([]bool, len(files))
	for i, f := range files {
		if path, ok := expand(f); ok {
			_, err := os.Stat(path)
			out[i] = err == nil
		}
	}
	return out
}

// expand resolves one configured path, or reports that it names nothing.
func expand(f string) (string, bool) {
	path := strings.TrimSpace(f)
	if path == "" {
		return "", false
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", false
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	return filepath.Clean(path), true
}

// Truster runs Install and remembers each device's last pass.
//
// The memory is what lets `ao sim boot` say what it trusted. A boot runs
// detached inside simpower and its success leaves no status entry behind (the
// device being Booted is the report), so the boot's trust pass would otherwise
// have nowhere to be read from. The device listing carries this instead.
type Truster struct {
	run simctl.Runner
	now func() time.Time

	mu   sync.Mutex
	last map[string]Result
	// done is, per device, which CA contents one boot of it already trusts.
	// It is what keeps a claim - which agents renew every few minutes - from
	// paying the second or so `simctl keychain` costs every time. It is keyed
	// by the boot (simctl.Device.Boot) because nothing short of a new boot can
	// take a root away again: an erase needs the device shut down first.
	done map[string]bootTrust
}

// bootTrust is what one boot of a device has been made to trust.
type bootTrust struct {
	boot string
	sums map[string]digest
}

// NewTruster builds a Truster over an injected simctl runner, so every path is
// testable without Xcode, a mac or a device.
func NewTruster(run simctl.Runner) *Truster {
	return &Truster{run: run, now: time.Now, last: map[string]Result{}, done: map[string]bootTrust{}}
}

// Request is what a caller worked out about trust for one device: the files,
// or the reason it could not work them out. Err exists for the reason
// simslim.Request's does - "AO could not tell what to trust" must not read the
// same as "there was nothing to trust".
type Request struct {
	Files []string
	Err   error
}

// Apply trusts a request's files on a device and records the pass as that
// device's last.
//
// boot names the run of the device (simctl.Device.Boot). A file whose exact
// content this run already trusts is reported as trusted without asking simctl
// again; an empty boot - a caller that does not know it - always installs.
func (t *Truster) Apply(ctx context.Context, udid, boot string, req Request) Result {
	key := domain.NormalizeSimUDID(udid)
	var result Result
	if req.Err != nil {
		result.Failed = []Failure{{Reason: "could not work out which root CAs to trust: " + req.Err.Error()}}
	} else {
		t.mu.Lock()
		prior := t.done[key]
		t.mu.Unlock()
		already := func(path string, sum digest) bool {
			return boot != "" && prior.boot == boot && prior.sums[path] == sum
		}
		var trusted map[string]digest
		result, trusted = install(ctx, t.run, key, req.Files, already)
		if boot != "" {
			t.mu.Lock()
			if cur := t.done[key]; cur.boot == boot {
				for path, sum := range cur.sums {
					if _, ok := trusted[path]; !ok {
						trusted[path] = sum
					}
				}
			}
			t.done[key] = bootTrust{boot: boot, sums: trusted}
			t.mu.Unlock()
		}
	}
	result.At = t.now().UTC()
	t.mu.Lock()
	t.last[key] = result
	t.mu.Unlock()
	return result
}

// Last is a device's most recent pass, if it has had one since the daemon
// started.
func (t *Truster) Last(udid string) (Result, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r, ok := t.last[domain.NormalizeSimUDID(udid)]
	return r, ok
}

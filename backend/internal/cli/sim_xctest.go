package cli

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
)

// Reading the screen through the daemon's warm XCTest runner.
//
// The accessibility bridge reads the frontmost app's own process, which is
// blind to whatever another process draws into it: an ASWebAuthenticationSession
// login page, SpringBoard's alerts, the Paste callout, the keyboard. The daemon
// keeps an XCTest runner warm on every device a session holds
// (internal/simrunner), and XCTest sees all of them.
//
// The fallback is the bridge, always, and it is never silent: a read that did
// not come from the runner says why (nobody holds the device, the runner is
// still starting, it failed), because "the login page is not in the tree" is
// otherwise the first thing a caller concludes about the app.

// simAXRunnerWait is how long `ao sim ax` waits for a runner that is still
// starting. A runner answers 3-6 s after a claim (longer the first time on a
// machine, which also builds it), and a read through the bridge in the
// meantime misses exactly the screens this reader exists for, so waiting is
// the better answer for a command whose whole job is to read.
const simAXRunnerWait = 15 * time.Second

// simHierarchyResponse mirrors controllers.SimHierarchyResponse.
type simHierarchyResponse struct {
	Runner struct {
		State  string `json:"state"`
		Reason string `json:"reason,omitempty"`
	} `json:"runner"`
	Hierarchy *simbridge.XCTestHierarchy `json:"hierarchy,omitempty"`
}

// xctestReadingDriver reads through the runner and touches through the bridge.
// Only AX is overridden: every gesture still goes through the embedded driver,
// under the lease and the gesture hold, exactly as before.
type xctestReadingDriver struct {
	simbridge.Driver
	read func(ctx context.Context, udid string, wait time.Duration) (simHierarchyResponse, error)
	wait time.Duration
}

func (d xctestReadingDriver) AX(ctx context.Context, udid string) (simbridge.Snapshot, error) {
	var note string
	resp, err := d.read(ctx, udid, d.wait)
	switch {
	case err != nil:
		note = "the daemon could not be asked for the XCTest reader"
	case resp.Hierarchy != nil:
		snap := simbridge.SnapshotFromXCTest(*resp.Hierarchy)
		if snap.Usable() {
			return snap, nil
		}
		note = "the XCTest runner read an empty screen"
		if len(resp.Hierarchy.Errors) > 0 {
			note += " (" + strings.Join(resp.Hierarchy.Errors, "; ") + ")"
		}
	default:
		note = simRunnerNote(resp.Runner.State, resp.Runner.Reason)
	}
	snap, err := d.Driver.AX(ctx, udid)
	if err != nil {
		return snap, err
	}
	snap.Reader = &simbridge.Reader{Source: simbridge.SourceAccessibility, Note: note}
	return snap, nil
}

// simRunnerNote says why the runner did not answer, and what would make it.
func simRunnerNote(state, reason string) string {
	switch state {
	case "off":
		return "no session holds this simulator; claim it (`ao sim claim`) and the XCTest reader starts, " +
			"which also sees web sign-in sheets, system alerts, menus and the keyboard"
	case "starting":
		return "the XCTest reader is still starting; read again in a few seconds"
	case "unavailable":
		return reason
	}
	if reason == "" {
		return "the XCTest reader is " + state
	}
	return "the XCTest reader " + state + ": " + firstLine(reason)
}

// readSimHierarchy asks the daemon for the runner's read of a device.
func (c *commandContext) readSimHierarchy(ctx context.Context, udid string, wait time.Duration) (simHierarchyResponse, error) {
	path := "sim/devices/" + url.PathEscape(udid) + "/hierarchy"
	if wait > 0 {
		path += "?waitMs=" + strconv.FormatInt(wait.Milliseconds(), 10)
	}
	var resp simHierarchyResponse
	if err := c.getJSON(ctx, path, &resp); err != nil {
		return simHierarchyResponse{}, err
	}
	return resp, nil
}

// readerLine is the `Reader:` header of `ao sim ax`.
func readerLine(r *simbridge.Reader) string {
	if r != nil && r.Source == simbridge.SourceXCTest {
		return "XCTest (every process on screen)"
	}
	line := "accessibility bridge (frontmost app only)"
	if r != nil && r.Note != "" {
		line += " - " + r.Note
	}
	return line
}

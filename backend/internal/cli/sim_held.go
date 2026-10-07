package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simproc"
)

// readSimProcesses is the host's process table, where a simulator app held by
// a debugger or a SIGSTOP shows.
func (c *commandContext) readSimProcesses(ctx context.Context) (simproc.Table, error) {
	return simproc.Read(ctx, simproc.Runner(c.deps.CommandOutput))
}

// heldSimApp says whether the process with this pid is held by a debugger or
// a SIGSTOP. It is asked before any `sample` of an app that answers nothing:
// a held app cannot answer by construction, and sampling a stopped one is
// what turned a measured `ao sim ax` into a minute of waiting. A table that
// cannot be read says nothing, and the caller carries on as before.
func (c *commandContext) heldSimApp(ctx context.Context, pid int) (simproc.Hold, bool) {
	if pid <= 0 {
		return simproc.Hold{}, false
	}
	table, err := c.readSimProcesses(ctx)
	if err != nil {
		return simproc.Hold{}, false
	}
	app, ok := table.Get(pid)
	if !ok {
		return simproc.Hold{}, false
	}
	return table.HoldOf(app)
}

// refuseHeldSimApps refuses a flow run while any app on the device is held by
// a debugger or a SIGSTOP: a held app answers no accessibility query, so the
// flow would fail for a reason it cannot name, and a breakpoint hit mid-flow
// freezes it. A table that cannot be read is said and does not block the run.
func (c *commandContext) refuseHeldSimApps(ctx context.Context, errOut io.Writer, device simDevice) error {
	if device.DataPath == "" {
		return nil
	}
	table, err := c.readSimProcesses(ctx)
	if err != nil {
		_, werr := fmt.Fprintf(errOut, "Warning: could not check whether an app on %s is held by a debugger: %v\n", device.Name, err)
		return werr
	}
	holds := table.Holds(device.DataPath)
	if len(holds) == 0 {
		return nil
	}
	described := make([]string, 0, len(holds))
	for _, h := range holds {
		described = append(described, "  "+h.Describe())
	}
	return fmt.Errorf("not running the flow: an app on %s is held, so it answers no accessibility query and the flow "+
		"would fail for a reason it cannot name.\n%s", device.Label(), strings.Join(described, "\n"))
}

// heldSimAppReport is blockedSimAppReport for an app that is not running at
// all: there is no stack to show, only who holds it and how to let it go.
func heldSimAppReport(front simbridge.Frontmost, hold simproc.Hold) string {
	return fmt.Sprintf("%s: %s\n"+
		"A held app answers no accessibility query and processes no touch, so `ao sim tap` will report "+
		"success and change nothing, and a Maestro flow against it fails for a reason it cannot name.",
		frontmostLabel(front), hold.Describe())
}

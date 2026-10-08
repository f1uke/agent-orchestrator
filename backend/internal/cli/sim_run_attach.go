package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/iosrun"
)

var simRunAttachPoll = time.Second

const simRunTerminateWait = 10 * time.Second

func (c *commandContext) attachSimApp(ctx context.Context, out io.Writer, result simRunResult, report *runReport) error {
	pid, _ := strconv.Atoi(result.PID)
	if pid <= 0 {
		return nil
	}
	report.stage(iosrun.StageAppRunning)
	noteProgress(out, "\n%s is running (pid %d). Stop it from the run bar or press Ctrl-C.\n", result.BundleID, pid)
	console := consoleTail{path: result.Console}
	for {
		console.copyTo(out)
		if !c.deps.ProcessAlive(pid) {
			console.copyTo(out)
			noteProgress(out, "%s exited.\n", result.BundleID)
			report.ended(appExited)
			return nil
		}
		select {
		case <-ctx.Done():
			report.ended(appStoppedByUser)
			return c.terminateSimApp(out, result, pid)
		case <-time.After(simRunAttachPoll):
		}
	}
}

func (c *commandContext) terminateSimApp(out io.Writer, result simRunResult, pid int) error {
	noteProgress(out, "Stopping %s…\n", result.BundleID)
	ctx, cancel := context.WithTimeout(context.Background(), simRunTerminateWait)
	defer cancel()
	_, _ = c.deps.CommandOutput(ctx, "xcrun", "simctl", "terminate", result.UDID, result.BundleID)
	for c.deps.ProcessAlive(pid) {
		if ctx.Err() != nil {
			return fmt.Errorf("%s (pid %d) is still running after `simctl terminate`; `ao sim doctor` names what holds it", result.BundleID, pid)
		}
		c.deps.Sleep(100 * time.Millisecond)
	}
	noteProgress(out, "Stopped %s.\n", result.BundleID)
	return nil
}

type consoleTail struct {
	path   string
	offset int64
}

func (t *consoleTail) copyTo(out io.Writer) {
	if t.path == "" {
		return
	}
	f, err := os.Open(t.path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return
	}
	n, _ := io.Copy(out, f)
	t.offset += n
}

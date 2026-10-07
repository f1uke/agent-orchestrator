// Package simproc reads the host's process table for what it says about
// simulator apps: which process an installed app is running as, and whether a
// debugger or a SIGSTOP is holding it.
//
// A simulator app is an ordinary host process whose executable lives under the
// device's data directory, so one `ps` answers for every device at once. What
// `ps` can and cannot tell was measured, not assumed:
//
//   - A process a debugger is attached to carries `X` (traced) in STAT, and its
//     PPID becomes the debugserver, whose own parent is the lldb (or Xcode's
//     lldb-rpc-server).
//   - A process stopped at a BREAKPOINT still reads `SXs`, never `T`: lldb stops
//     it by suspending its mach task, which `ps` does not show. So "a debugger
//     is attached" is observable and "it is frozen at a breakpoint" is not -
//     every check here treats the first as the hazard.
//   - `T` is a SIGSTOP (`kill -STOP`, or `process detach --keep-stopped`).
package simproc

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// Runner runs a command and returns its combined output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// psArgs reads `comm` rather than `args`: on macOS it is the executable's full
// path with no arguments after it, so a path with spaces in it ("Cloud
// Atlas.app/Cloud Atlas") never has to be told apart from an argument.
var psArgs = []string{"-axo", "pid=,ppid=,stat=,comm="}

// Process is one row of the table.
type Process struct {
	PID  int    `json:"pid"`
	PPID int    `json:"ppid"`
	Stat string `json:"stat"`
	// Path is the executable's full path.
	Path string `json:"path"`
}

// Debugged reports that a debugger is attached.
func (p Process) Debugged() bool { return strings.Contains(p.Stat, "X") }

// Stopped reports that the process was stopped by SIGSTOP.
func (p Process) Stopped() bool { return strings.HasPrefix(p.Stat, "T") }

// Name is the executable's file name.
func (p Process) Name() string { return filepath.Base(p.Path) }

// appRoot is where CoreSimulator installs app bundles inside a device's data
// directory.
const appRoot = "/Containers/Bundle/Application/"

// InBundle is the executable's path from its .app down ("Nimbus.app/Nimbus",
// "Nimbus.app/PlugIns/NimbusWidget.appex/NimbusWidget") for an installed app,
// and the full path for anything else.
func (p Process) InBundle() string {
	i := strings.Index(p.Path, appRoot)
	if i < 0 {
		return p.Path
	}
	_, rest, found := strings.Cut(p.Path[i+len(appRoot):], "/")
	if !found {
		return p.Path
	}
	return rest
}

// Table is a snapshot of the host's processes.
type Table []Process

// Read runs `ps` once and parses it.
func Read(ctx context.Context, run Runner) (Table, error) {
	out, err := run(ctx, "ps", psArgs...)
	if err != nil {
		return nil, fmt.Errorf("ps failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return Parse(out), nil
}

// Parse reads `ps -o pid=,ppid=,stat=,comm=` output. A line that does not
// start with two numbers and a state is not a process and is skipped.
func Parse(out []byte) Table {
	var table Table
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, errPID := strconv.Atoi(fields[0])
		ppid, errPPID := strconv.Atoi(fields[1])
		if errPID != nil || errPPID != nil {
			continue
		}
		// The path is everything after the third field, spaces included.
		rest := strings.TrimLeft(line, " \t")
		for range 3 {
			rest = strings.TrimLeft(rest[strings.IndexAny(rest, " \t"):], " \t")
		}
		table = append(table, Process{PID: pid, PPID: ppid, Stat: fields[2], Path: strings.TrimRight(rest, " \t\r")})
	}
	return table
}

// Get is the process with this pid.
func (t Table) Get(pid int) (Process, bool) {
	for _, p := range t {
		if p.PID == pid {
			return p, true
		}
	}
	return Process{}, false
}

// AppsOn is every process running an app installed on the device whose data
// directory this is: main apps and their extensions alike.
func (t Table) AppsOn(dataPath string) []Process {
	prefix := strings.TrimRight(dataPath, "/") + appRoot
	var apps []Process
	for _, p := range t {
		if strings.HasPrefix(p.Path, prefix) {
			apps = append(apps, p)
		}
	}
	return apps
}

// Main is the app's own process: the executable directly inside the .app at
// appPath, not an extension under PlugIns/.
func (t Table) Main(appPath string) (Process, bool) {
	prefix := strings.TrimRight(appPath, "/") + "/"
	for _, p := range t {
		rest, found := strings.CutPrefix(p.Path, prefix)
		if found && rest != "" && !strings.Contains(rest, "/") {
			return p, true
		}
	}
	return Process{}, false
}

// HoldKind is what holds an app.
type HoldKind string

// Hold kinds.
const (
	HeldByDebugger HoldKind = "debugger"
	HeldBySIGSTOP  HoldKind = "sigstop"
)

// Hold is something keeping an app from running freely, and who to ask to let
// go. Debugserver and Debugger are the traced app's parent and grandparent,
// nil when the table no longer has them.
type Hold struct {
	Kind        HoldKind `json:"kind"`
	App         Process  `json:"app"`
	Debugserver *Process `json:"debugserver,omitempty"`
	Debugger    *Process `json:"debugger,omitempty"`
}

// HoldOf says what holds p, if anything. A debugger wins over a SIGSTOP: it is
// the one that has to let go first.
func (t Table) HoldOf(p Process) (Hold, bool) {
	switch {
	case p.Debugged():
		hold := Hold{Kind: HeldByDebugger, App: p}
		if server, ok := t.Get(p.PPID); ok {
			hold.Debugserver = &server
			if debugger, ok := t.Get(server.PPID); ok {
				hold.Debugger = &debugger
			}
		}
		return hold, true
	case p.Stopped():
		return Hold{Kind: HeldBySIGSTOP, App: p}, true
	default:
		return Hold{}, false
	}
}

// Holds is every held app process on the device whose data directory this is.
func (t Table) Holds(dataPath string) []Hold {
	var holds []Hold
	for _, p := range t.AppsOn(dataPath) {
		if hold, held := t.HoldOf(p); held {
			holds = append(holds, hold)
		}
	}
	return holds
}

// xcodeDebugger is the process Xcode debugs through; naming it as such tells a
// reader the fix is in Xcode, not in a terminal.
const xcodeDebugger = "lldb-rpc-server"

// State is what holds the app, without the fix: "attached by debugserver 950
// (lldb 900)" or "stopped by SIGSTOP".
func (h Hold) State() string {
	if h.Kind == HeldBySIGSTOP {
		return "stopped by SIGSTOP"
	}
	if h.Debugserver == nil {
		return fmt.Sprintf("attached by a debugger (pid %d)", h.App.PPID)
	}
	state := fmt.Sprintf("attached by %s %d", h.Debugserver.Name(), h.Debugserver.PID)
	if h.Debugger != nil {
		if h.Debugger.Name() == xcodeDebugger {
			return state + fmt.Sprintf(" (Xcode's %s %d)", xcodeDebugger, h.Debugger.PID)
		}
		state += fmt.Sprintf(" (%s %d)", h.Debugger.Name(), h.Debugger.PID)
	}
	return state
}

// Fix is the command that frees the app. It names processes by pid only:
// killing by pattern would take every other session's lldb with it.
func (h Hold) Fix() string {
	if h.Kind == HeldBySIGSTOP {
		return fmt.Sprintf("resume it with `kill -CONT %d`", h.App.PID)
	}
	switch {
	case h.Debugger != nil && h.Debugger.Name() == xcodeDebugger:
		return fmt.Sprintf("detach in Xcode (Debug > Detach), or end that debugger (pid %d) if it is yours", h.Debugger.PID)
	case h.Debugger != nil:
		name := h.Debugger.Name()
		return fmt.Sprintf("detach it in that %s (`process detach`), or end that %s (pid %d) if it is yours", name, name, h.Debugger.PID)
	default:
		return fmt.Sprintf("detach it in the debugger that attached (`process detach`), or end that debugger (pid %d) if it is yours", h.App.PPID)
	}
}

// Describe is the whole sentence: which process, what holds it, and the fix.
func (h Hold) Describe() string {
	return fmt.Sprintf("%s (pid %d) is %s: %s", h.App.InBundle(), h.App.PID, h.State(), h.Fix())
}

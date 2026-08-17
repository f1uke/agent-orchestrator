package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simlog"
)

// `ao sim log` is how a session reads what an app SAYS, as opposed to what its
// screen shows.
//
// It exists because the alternative is a hazard. Without it, an agent that
// needs a response body reaches for `xcrun simctl launch --console-pipe`, which
// hands the app's stdout to a pipe: the moment something stops draining that
// pipe the 64 KB buffer fills and the app blocks in write() ON ITS MAIN THREAD.
// The app is then wedged - no accessibility, no touches, a frozen screen - and
// nothing about the symptoms points at the capture that caused them.
//
// This route cannot do that. `log` reads the device's unified log, which never
// blocks the process that wrote to it, so no amount of not-reading this command
// can affect the app. AO deliberately offers NO way to attach a pipe to an
// app's stdout: see the --stdout note in the help below.
//
// Like `ao sim shot` and `ao sim ax` this is a read: it takes no lease, is
// never blocked by one, and always reports who holds the device.

const (
	// defaultSimLogSince is how far back a plain `ao sim log` looks. Long
	// enough to cover the interaction that just happened, short enough that
	// reading it costs a couple of seconds on a busy device.
	defaultSimLogSince = 2 * time.Minute
	// defaultSimLogMaxLines caps a history read. A simulator logs tens of
	// thousands of entries a minute, and an unbounded dump is worse than no
	// answer: it crowds out the reasoning that is supposed to happen next.
	defaultSimLogMaxLines = 200
	// simLogPrintNote is the limitation an agent will otherwise read as "this
	// command is broken". It is repeated wherever an empty result is reported.
	simLogPrintNote = "`print` and `debugPrint` write to the app's stdout, and an app launched by " +
		"SpringBoard has its stdout discarded - that output does not exist anywhere, so no log command can show it. " +
		"`NSLog(...)`, `os_log` and `Logger` DO reach this log. To read a payload, add a temporary NSLog probe, " +
		"run the flow, and take it out again."
)

// simLogResult is the `ao sim log --json` payload for a history read.
type simLogResult struct {
	UDID              string `json:"udid"`
	Name              string `json:"name"`
	Runtime           string `json:"runtime"`
	RuntimeIdentifier string `json:"runtimeIdentifier"`
	// Since is the window that was read, and Follow says this was a live
	// stream instead. Both are echoed so a stored result says what produced it.
	Since   string                `json:"since,omitempty"`
	Follow  bool                  `json:"follow"`
	Process string                `json:"process,omitempty"`
	Grep    string                `json:"grep,omitempty"`
	Entries []simlog.Entry        `json:"entries"`
	Matched int                   `json:"matched"`
	Scanned int                   `json:"scanned"`
	Dropped int                   `json:"dropped,omitempty"`
	Note    string                `json:"note,omitempty"`
	Lease   simLeaseView          `json:"lease"`
	Sources []simlog.ProcessCount `json:"sources,omitempty"`
}

type simLogOptions struct {
	udid     string
	since    string
	process  string
	grep     string
	follow   bool
	maxLines int
	json     bool
}

func newSimLogCommand(ctx *commandContext) *cobra.Command {
	opts := simLogOptions{}
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Read what an app on a booted simulator writes to the unified log",
		Long: "Read the device's unified log - what the app itself says, rather than what its screen shows.\n\n" +
			"WHAT THIS CAN SEE: anything logged with `NSLog`, `os_log` or `Logger`, from any process on the " +
			"device. Filter it down with --process (the executable's name) and --grep (a regular expression " +
			"over the whole entry).\n\n" +
			"WHAT IT CANNOT SEE: `print` and `debugPrint`. They write to the app's stdout, and an app launched " +
			"by SpringBoard - tapped on the home screen, or started with `simctl launch` - has its stdout " +
			"discarded. That output does not exist anywhere on the device, so an empty result for a `print` you " +
			"can see in Xcode means exactly that, not a broken command. To read a payload, add a temporary " +
			"`NSLog(\"...\\(body)\")` probe, run the flow, read it here, and take the probe out again.\n\n" +
			"WHY THERE IS NO --stdout FLAG: the only way to capture stdout is to launch the app with a pipe " +
			"attached (`xcrun simctl launch --console-pipe`). When anything stops draining that pipe, the 64 KB " +
			"buffer fills and the app blocks in write() on its MAIN THREAD - no accessibility, no touches, a " +
			"frozen screen - and none of those symptoms points back at the capture. AO will not offer a mode " +
			"whose failure wedges the app under test. This command cannot: the unified log never blocks the " +
			"process that wrote to it.\n\n" +
			"This is a read: it needs no claim on the device and is never blocked by one, but it always reports " +
			"who holds it. " + simNeverBootsNote,
		Example: `  ao sim log --process Nimbus
  ao sim log --since 10m --grep "checkout|payment"
  ao sim log --follow --process Nimbus`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.runSimLog(cmd, opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.udid, "udid", "", "Read this simulator instead of the booted one")
	f.StringVar(&opts.since, "since", "", "How far back to read (30s, 2m, 1h). Default 2m; not valid with --follow")
	f.StringVar(&opts.process, "process", "", "Only entries from this process (the executable's name, not the bundle id)")
	f.StringVar(&opts.grep, "grep", "", "Only entries matching this regular expression")
	f.BoolVarP(&opts.follow, "follow", "f", false, "Stream entries as they happen instead of reading history")
	f.IntVar(&opts.maxLines, "max-lines", defaultSimLogMaxLines, "Keep at most this many of the most recent entries")
	f.BoolVar(&opts.json, "json", false, "Output entries as JSON (one object per line with --follow)")
	return cmd
}

func (c *commandContext) runSimLog(cmd *cobra.Command, opts simLogOptions) error {
	window, filter, err := simLogPlan(opts)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	device, err := c.resolveBootedSimDevice(ctx, opts.udid)
	if err != nil {
		return err
	}

	if opts.follow {
		// The CLI installs no signal handling of its own, and every other
		// command finishes on its own so it never needed any. A follow does
		// not: without this, Ctrl-C would kill this process outright and skip
		// the cleanup that stops the child.
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
	}

	stream, err := c.deps.StartStream(ctx, simctl.Binary, simLogArgs(device.UDID, opts.follow, window)...)
	if err != nil {
		return fmt.Errorf("could not read the log on %s: %w", device.Label(), err)
	}
	defer func() { _ = stream.Close() }()
	// A read parked on a pipe cannot be interrupted by a context; stopping the
	// child is what ends it. This is also what guarantees no `log` is left
	// behind - see simLogArgs for why that works.
	reading := make(chan struct{})
	defer close(reading)
	go func() {
		select {
		case <-ctx.Done():
			_ = stream.Close()
		case <-reading:
		}
	}()

	out := cmd.OutOrStdout()
	if opts.follow {
		return c.streamSimLog(ctx, out, stream, device, opts, filter)
	}
	return c.showSimLog(ctx, out, stream, device, opts, window, filter)
}

// simLogPlan turns the flags into the window and the filter, refusing misuse
// before anything is started. Every failure here is exit 2: nothing reached the
// device.
func simLogPlan(opts simLogOptions) (time.Duration, simlog.Filter, error) {
	window := defaultSimLogSince
	if raw := strings.TrimSpace(opts.since); raw != "" {
		if opts.follow {
			// They are different questions, and `log stream` has no answer to
			// the first: it starts now.
			return 0, simlog.Filter{}, usageError{fmt.Errorf(
				"--since reads history and --follow streams what happens next, so they cannot be combined; "+
					"run `ao sim log --since %s` first, then `ao sim log --follow`", raw)}
		}
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return 0, simlog.Filter{}, usageError{fmt.Errorf("--since %q is not a duration: use 30s, 2m or 1h", raw)}
		}
		if parsed <= 0 {
			return 0, simlog.Filter{}, usageError{fmt.Errorf("--since %s reads nothing at all", raw)}
		}
		window = parsed
	}
	if opts.maxLines <= 0 {
		return 0, simlog.Filter{}, usageError{fmt.Errorf("--max-lines must be positive, got %d", opts.maxLines)}
	}
	filter, err := simlog.NewFilter(opts.process, opts.grep)
	if err != nil {
		return 0, simlog.Filter{}, usageError{err}
	}
	return window, filter, nil
}

// simLogArgs builds the simctl invocation.
//
// It carries NO predicate and no process filter, on purpose. A `log` running
// inside the simulator is a child of the guest's launchd: it is in nobody's
// process group here, and no signal of ours reaches it. The one thing that ends
// it is a failed write to the pipe this process holds - so it has to keep
// writing. A stream filtered at the source that matches nothing writes nothing,
// never notices its reader has gone, and outlives everything: this machine had
// five such orphans on it, left by the incident this command came from.
// Filtering in AO costs a few hundred kilobytes a minute over a local pipe and
// makes the leak impossible.
func simLogArgs(udid string, follow bool, window time.Duration) []string {
	args := []string{"simctl", "spawn", udid, "log"}
	if follow {
		args = append(args, "stream")
	} else {
		// Whole seconds: `log show --last` takes a plain unit suffix, and a Go
		// duration string ("2m0s") is not one.
		args = append(args, "show", "--last", strconv.Itoa(int(window.Seconds()))+"s")
	}
	return append(args, "--style", "compact")
}

// streamSimLog is `--follow`: every entry is printed the moment it is complete.
func (c *commandContext) streamSimLog(
	ctx context.Context, out io.Writer, stream ProcessStream, device simDevice, opts simLogOptions, filter simlog.Filter,
) error {
	if !opts.json {
		views, reachable := c.simLeaseViews(ctx)
		if err := writeSimLogHeader(out, device, simLeaseFor(views, device.UDID, reachable), opts, "following"); err != nil {
			return err
		}
	}
	_, readErr := simlog.Follow(stream, filter, func(entry simlog.Entry) error {
		if opts.json {
			// One object per line: a stream has no end, so it cannot be one
			// object, and an indented one would not be readable line by line
			// while it is still arriving.
			return writeJSONLine(out, entry)
		}
		_, err := fmt.Fprintln(out, entry.Raw)
		return err
	})
	// Being stopped is how a follow ends: by a signal, by a parent that went
	// away, or by a harness that timed it out. None of those is a failure.
	if ctx.Err() != nil {
		return nil
	}
	if readErr != nil {
		return readErr
	}
	return simLogChildError(device, stream)
}

// showSimLog is a history read: collect, then print the most recent.
func (c *commandContext) showSimLog(
	ctx context.Context, out io.Writer, stream ProcessStream, device simDevice,
	opts simLogOptions, window time.Duration, filter simlog.Filter,
) error {
	// A ring, because the newest entries are the ones worth keeping: the window
	// ends at "now", which is where whatever is being investigated just
	// happened.
	kept := make([]simlog.Entry, 0, opts.maxLines)
	matched := 0
	scan, readErr := simlog.Read(stream, filter, func(entry simlog.Entry) error {
		matched++
		if len(kept) == opts.maxLines {
			kept = kept[1:]
		}
		kept = append(kept, entry)
		return nil
	})
	if readErr != nil {
		return readErr
	}
	if err := simLogChildError(device, stream); err != nil {
		return err
	}

	views, reachable := c.simLeaseViews(ctx)
	result := simLogResult{
		UDID:              device.UDID,
		Name:              device.Name,
		Runtime:           device.Runtime,
		RuntimeIdentifier: device.RuntimeIdentifier,
		Since:             window.String(),
		Process:           opts.process,
		Grep:              opts.grep,
		Entries:           kept,
		Matched:           matched,
		Scanned:           scan.Entries,
		Dropped:           matched - len(kept),
		Lease:             simLeaseFor(views, device.UDID, reachable),
	}
	if matched == 0 {
		result.Note = simLogEmptyNote(opts, window, scan)
		result.Sources = scan.Processes
	}
	if opts.json {
		return writeJSON(out, result)
	}
	return writeSimLog(out, result, opts)
}

// simLogChildError reports a `log` child that failed. Its own message names the
// flag or the udid it did not like, which is more useful than anything that
// could be said here.
func simLogChildError(device simDevice, stream ProcessStream) error {
	if err := stream.Err(); err != nil {
		return fmt.Errorf("`log` failed on %s: %w", device.Label(), err)
	}
	return nil
}

// simLogEmptyNote is the answer to "why did I get nothing", which is the one
// result an agent cannot act on. Silence would leave three very different
// causes indistinguishable: the wrong process name, an empty window, and output
// that never reached this log at all.
func simLogEmptyNote(opts simLogOptions, window time.Duration, scan simlog.Scan) string {
	var b strings.Builder
	switch {
	case scan.Entries == 0:
		fmt.Fprintf(&b, "The device logged nothing at all in the last %s, which is unusual for a booted simulator. ", window)
		b.WriteString("Check the udid with `ao sim list`.")
	case opts.process != "" && !loggedIn(scan, opts.process):
		fmt.Fprintf(&b, "Nothing from a process called %q in the last %s, and no process by that name logged at all. ",
			opts.process, window)
		fmt.Fprintf(&b, "--process takes the EXECUTABLE's name, not the bundle id. These did log: %s.", processList(scan))
	default:
		fmt.Fprintf(&b, "No entry in the last %s matched. ", window)
		if opts.grep != "" {
			fmt.Fprintf(&b, "The pattern was %q; ", opts.grep)
		}
		fmt.Fprintf(&b, "%d entries were read, from %s.", scan.Entries, processList(scan))
	}
	b.WriteString("\n" + simLogPrintNote)
	return b.String()
}

func loggedIn(scan simlog.Scan, process string) bool {
	for _, p := range scan.Processes {
		if strings.EqualFold(p.Name, process) {
			return true
		}
	}
	return false
}

func processList(scan simlog.Scan) string {
	if len(scan.Processes) == 0 {
		return "no process at all"
	}
	parts := make([]string, 0, len(scan.Processes))
	for _, p := range scan.Processes {
		parts = append(parts, fmt.Sprintf("%s (%d)", p.Name, p.Entries))
	}
	return strings.Join(parts, ", ")
}

func writeSimLogHeader(out io.Writer, device simDevice, lease simLeaseView, opts simLogOptions, verb string) error {
	scope := "every process"
	if opts.process != "" {
		scope = "process " + strconv.Quote(opts.process)
	}
	if opts.grep != "" {
		scope += " matching " + strconv.Quote(opts.grep)
	}
	if _, err := fmt.Fprintf(out, "%s - %s %s\n", device.Label(), verb, scope); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "Lease: %s\n\n", lease.captureLine(strings.TrimSpace(os.Getenv("AO_SESSION_ID"))))
	return err
}

func writeSimLog(out io.Writer, result simLogResult, opts simLogOptions) error {
	if err := writeSimLogHeader(out, simDevice{Device: simctl.Device{
		UDID: result.UDID, Name: result.Name, Runtime: result.Runtime,
	}}, result.Lease, opts, "the last "+result.Since+" from"); err != nil {
		return err
	}
	for _, entry := range result.Entries {
		if _, err := fmt.Fprintln(out, entry.Raw); err != nil {
			return err
		}
	}
	if result.Matched == 0 {
		_, err := fmt.Fprintf(out, "No entries matched.\n%s\n", result.Note)
		return err
	}
	if result.Dropped > 0 {
		if _, err := fmt.Fprintf(out, "\n%d of %d matching entries shown (the most recent). "+
			"Re-run with --max-lines %d, a shorter --since or a narrower --grep to see the rest.\n",
			len(result.Entries), result.Matched, result.Matched); err != nil {
			return err
		}
		return nil
	}
	_, err := fmt.Fprintf(out, "\n%d of %d entries matched.\n", result.Matched, result.Scanned)
	return err
}

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/simhang"
)

// `ao sim ax` is how a session actually checks a screen.
//
// It reads the accessibility tree, which is the primary signal - not the
// screenshot. A picture tells an agent what a screen looks like; the tree tells
// it what is there, whether it is enabled, and exactly where to touch it. The
// tap point is computed here so nothing downstream ever estimates a coordinate
// from an image, which is the way a tap lands on the wrong thing.
//
// It takes no lease, for the same reason `ao sim shot` takes none: it never
// touches HID, so it cannot corrupt anybody's gesture, and a read that anyone
// can take at any time is most of the value. It always reports who holds the
// device, so reading a screen is never mistaken for permission to drive it.

// defaultSimAXMaxNodes caps how much tree one read returns.
//
// A real app screen can report far more elements than an agent can usefully
// read, and an unbounded dump crowds out the reasoning that is supposed to
// happen next. 500 is what the upstream tooling caps at, and a home screen
// measures ~22, so the cap is a guard rail rather than a limit anyone meets by
// accident. Unlike upstream, a truncated read says how many elements the device
// really reported and how to ask for them.
const defaultSimAXMaxNodes = 500

// simAXSettleDelay is how long to wait before reading a screen again when the
// first read came back as nothing but status bar. Long enough for an app that
// has just been foregrounded to publish its screen, short enough that a caller
// does not notice - and only ever paid in that one case.
var simAXSettleDelay = 600 * time.Millisecond

// simAXResult is the `ao sim ax --json` payload: the snapshot, plus which
// device it came from and who holds it.
type simAXResult struct {
	simbridge.Snapshot
	UDID              string       `json:"udid"`
	Name              string       `json:"name"`
	Runtime           string       `json:"runtime"`
	RuntimeIdentifier string       `json:"runtimeIdentifier"`
	Lease             simLeaseView `json:"lease"`
}

func newSimAXCommand(ctx *commandContext) *cobra.Command {
	var opts struct {
		udid     string
		maxNodes int
		json     bool
		format   string
	}
	cmd := &cobra.Command{
		Use:   "ax",
		Short: "Read a booted simulator's accessibility tree, with a tap point per element",
		Long: "Read what is on a booted iOS Simulator's screen as a structured tree.\n\n" +
			"Every element carries the point to tap it - normalized 0..1, exactly what " +
			"`ao sim tap` takes - so acting on what you read never involves coordinate " +
			"maths or guessing a position from a screenshot.\n\n" +
			"This is a read: it needs no claim on the device and is never blocked by one, " +
			"but it always reports who holds it. " + simNeverBootsNote,
		Example: `  ao sim ax
  ao sim ax --json
  ao sim ax --format maestro
  ao sim ax --max-nodes 2000 --udid 00000000-0000-0000-0000-000000000000`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := resolveSimAXFormat(cmd, opts.format, opts.json)
			if err != nil {
				return err
			}
			if opts.maxNodes <= 0 {
				return usageError{fmt.Errorf("--max-nodes must be positive, got %d", opts.maxNodes)}
			}
			result, err := ctx.readSimAX(cmd.Context(), opts.udid, opts.maxNodes)
			if err != nil {
				return err
			}
			switch format {
			case "json":
				return writeJSON(cmd.OutOrStdout(), result)
			case "maestro":
				return writeSimAXMaestro(cmd.OutOrStdout(), result)
			default:
				return writeSimAX(cmd.OutOrStdout(), result, strings.TrimSpace(os.Getenv("AO_SESSION_ID")))
			}
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.udid, "udid", "", "Read this simulator instead of the booted one")
	f.IntVar(&opts.maxNodes, "max-nodes", defaultSimAXMaxNodes, "Stop after this many elements")
	f.BoolVar(&opts.json, "json", false, "Output the tree as JSON")
	f.StringVar(&opts.format, "format", "text", "Output format: text, json, or maestro (Maestro selectors, one block per element)")
	return cmd
}

// resolveSimAXFormat folds the older --json flag into --format without letting
// the two disagree silently. A caller who passes both and means different
// things by them has a bug, and printing one of the two anyway would hide it.
func resolveSimAXFormat(cmd *cobra.Command, format string, jsonFlag bool) (string, error) {
	formatSet := cmd.Flags().Changed("format")
	switch format {
	case "text", "json", "maestro":
	default:
		return "", usageError{fmt.Errorf("--format must be text, json or maestro, got %q", format)}
	}
	if jsonFlag {
		if formatSet && format != "json" {
			return "", usageError{fmt.Errorf("--json and --format %s disagree; pass only --format", format)}
		}
		return "json", nil
	}
	return format, nil
}

// writeSimAXMaestro prints one Maestro block per element, in tree order.
//
// It is a starting point, not a flow: the selectors are real and the caveats
// are attached, but which of them belong in a test, in what order, and behind
// which waits is a judgement the caller makes. Emitting a runnable flow from a
// single screen would be inventing intent nobody expressed.
func writeSimAXMaestro(out io.Writer, result simAXResult) error {
	if _, err := fmt.Fprintf(out, "# %s - %.0fx%.0f points, %d elements (%d on screen, %d off screen)\n",
		result.Name, result.Screen.Width, result.Screen.Height,
		result.NodeCount, result.OnScreenCount, result.OffScreenCount); err != nil {
		return err
	}
	if result.Frontmost.BundleID != "" {
		if _, err := fmt.Fprintf(out, "# Foreground app: %s\n", result.Frontmost.BundleID); err != nil {
			return err
		}
	}
	if result.Truncated {
		if _, err := fmt.Fprintf(out, "# %d of %d elements shown, so the ambiguity counts below are a lower bound. Re-run with --max-nodes %d to see the rest.\n",
			result.NodeCount, result.TotalNodeCount, result.TotalNodeCount); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(out, "# device: %s\n#\n# selectors only - assemble the flow yourself, and check it with `ao sim flow check`\n\n", result.UDID); err != nil {
		return err
	}

	// A container - the root, a row, a wrapper - has no label and no id but
	// still has a tap point, so it falls to the point rung the same as a real
	// leaf control would. On a real screen that drowns the useful selectors in
	// brittle coordinates nobody would ever tap by name. A leaf with no label
	// or id, on the other hand, IS a real control, and keeps its point
	// fallback - it is skipped only when it has children to recurse into
	// instead.
	var walk func(els []simbridge.Element) error
	walk = func(els []simbridge.Element) error {
		for _, el := range els {
			choice := simflow.For(result.Snapshot, el)
			skip := (choice.Rung == simflow.RungPoint || choice.Rung == simflow.RungNone) && len(el.Children) > 0
			if !skip {
				block := simflow.Render(choice, el.Label)
				if _, err := io.WriteString(out, block); err != nil {
					return err
				}
			}
			if err := walk(el.Children); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(result.Elements)
}

func (c *commandContext) readSimAX(ctx context.Context, udid string, maxNodes int) (simAXResult, error) {
	device, err := c.resolveBootedSimDevice(ctx, udid)
	if err != nil {
		return simAXResult{}, err
	}
	driver, err := c.simDriver()
	if err != nil {
		return simAXResult{}, err
	}
	snapshot, err := driver.AX(ctx, device.UDID)
	if err != nil {
		return simAXResult{}, err
	}
	// For a second after an app comes to the front, the tree is the clock and
	// the battery and nothing else. One more read is cheaper than an agent
	// concluding the app is blank and acting on it.
	if snapshot.OnlyStatusBar {
		select {
		case <-ctx.Done():
			return simAXResult{}, ctx.Err()
		case <-time.After(simAXSettleDelay):
		}
		if second, retryErr := driver.AX(ctx, device.UDID); retryErr == nil {
			snapshot = second
		}
	}
	if !snapshot.Usable() {
		return simAXResult{}, c.explainEmptySimTree(ctx, device, snapshot.Frontmost)
	}

	views, reachable := c.simLeaseViews(ctx)
	return simAXResult{
		Snapshot:          snapshot.Truncate(maxNodes),
		UDID:              device.UDID,
		Name:              device.Name,
		Runtime:           device.Runtime,
		RuntimeIdentifier: device.RuntimeIdentifier,
		Lease:             simLeaseFor(views, device.UDID, reachable),
	}, nil
}

// explainEmptySimTree decides WHICH empty-tree message to send.
//
// The tree can be empty because the app has nothing to show, or because the app
// cannot answer at all - and those need opposite responses. Only the second one
// is worth a probe, and only after the read has already failed, so nothing on a
// working command's path pays for it. A probe that cannot tell says so by
// returning nothing, and the caller says exactly what it said before.
func (c *commandContext) explainEmptySimTree(ctx context.Context, device simDevice, front simbridge.Frontmost) error {
	if diag, ok := simhang.Diagnose(ctx, c.deps.LookPath, c.deps.CommandOutput, front.PID); ok && diag.Blocked {
		return blockedSimAppError(device, front, diag)
	}
	return emptySimTreeError(device, front)
}

// blockedSimAppError is the message that replaces several rounds of reading the
// wrong code.
func blockedSimAppError(device simDevice, front simbridge.Frontmost, diag simhang.Diagnosis) error {
	return fmt.Errorf("%s returned an empty accessibility tree because the app in the foreground cannot answer.\n%s",
		device.Label(), blockedSimAppReport(front, diag))
}

// blockedSimAppNote is the same finding for a command that does NOT already
// have a tree in its hands: it reads the screen once to learn which process is
// in front, then asks whether that process can answer at all.
//
// Failure paths only. It costs a screen read and a second of sampling, and no
// command that is about to report success may pay either.
func (c *commandContext) blockedSimAppNote(ctx context.Context, device simDevice) string {
	driver, err := c.simDriver()
	if err != nil {
		return ""
	}
	snapshot, err := driver.AX(ctx, device.UDID)
	if err != nil {
		return ""
	}
	diag, ok := simhang.Diagnose(ctx, c.deps.LookPath, c.deps.CommandOutput, snapshot.Frontmost.PID)
	if !ok || !diag.Blocked {
		return ""
	}
	return blockedSimAppReport(snapshot.Frontmost, diag)
}

// blockedSimAppReport names the thread, shows the samples that agree, and -
// when the stack says so - names the one cause AO can both explain and offer a
// way out of. Shared by every command that finds an app which cannot answer,
// because the finding is the same one however it was reached.
func blockedSimAppReport(front simbridge.Frontmost, diag simhang.Diagnosis) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (pid %d) has a BLOCKED MAIN THREAD: %d of %d samples were in this same stack, and it is not the run loop's wait.\n",
		frontmostLabel(front), front.PID, diag.Samples, diag.Samples)
	fmt.Fprintf(&b, "  %s\n", strings.Join(diag.Frames, " <- "))
	b.WriteString("A blocked main thread answers no accessibility query and processes no touch, " +
		"so `ao sim tap` will report success and change nothing. Accessibility is not the problem here.\n")
	if diag.StdoutWrite {
		b.WriteString("That stack is a write to the app's own stdout. Something attached a pipe to it " +
			"(`xcrun simctl launch --console-pipe`) and stopped draining it, so the 64 KB pipe buffer filled and " +
			"`print` will never return. Stop whatever is holding that pipe and relaunch the app without one.\n" +
			"Read the app's output with `ao sim log` instead: it goes through the unified log, which never blocks the app.")
	} else {
		fmt.Fprintf(&b, "Find out what that call is waiting for; `sample %d` prints the whole stack again.", front.PID)
	}
	return b.String()
}

// frontmostLabel names the app for a message about it, without pretending to
// know a bundle id the device did not report.
func frontmostLabel(front simbridge.Frontmost) string {
	if front.BundleID == "" {
		return "The foreground app"
	}
	return front.BundleID
}

// emptySimTreeError refuses to report "no elements" as a finding. An empty tree
// means accessibility answered with nothing, and which app is frontmost is
// usually the whole explanation - most often that the app under test is not the
// one on screen.
func emptySimTreeError(device simDevice, front simbridge.Frontmost) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s reported no accessibility elements, so there is nothing to act on.", device.Label())
	if front.BundleID != "" {
		fmt.Fprintf(&b, "\nThe app in the foreground is %s", front.BundleID)
		if front.BundleID == "com.apple.springboard" {
			b.WriteString(" - that is the home screen, not an app. Launch the app you want to check first")
		}
		b.WriteString(".")
	}
	b.WriteString("\nRun `ao sim shot` to see what is actually on screen.")
	return fmt.Errorf("%s", b.String())
}

// resolveBottedSimDevice is the shared front half of every screen command: one
// simctl listing, then the single resolution rule slice 1 established.
func (c *commandContext) resolveBootedSimDevice(ctx context.Context, udid string) (simDevice, error) {
	devices, err := c.listSimDevices(ctx)
	if err != nil {
		return simDevice{}, err
	}
	return resolveSimDevice(devices, udid)
}

// simDriver builds the mechanism. Everything above this line is written against
// the Driver interface, so replacing the vendored bridge with Apple's own
// device-interaction route later touches this function and nothing else.
func (c *commandContext) simDriver() (simbridge.Driver, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if c.deps.SimDriver != nil {
		return c.deps.SimDriver(cfg.DataDir)
	}
	return simbridge.NewNodeDriver(cfg.DataDir, c.deps.LookPath, nil)
}

func writeSimAX(out io.Writer, result simAXResult, sessionID string) error {
	// The split, not just the total: on a scrolling screen most of the tree is
	// usually below the fold, and nothing else on this page says so.
	if _, err := fmt.Fprintf(out, "%s - %.0fx%.0f points, %d elements (%d on screen, %d off screen)\n",
		result.Name, result.Screen.Width, result.Screen.Height, result.NodeCount,
		result.OnScreenCount, result.OffScreenCount); err != nil {
		return err
	}
	if result.Frontmost.BundleID != "" {
		if _, err := fmt.Fprintf(out, "Foreground app: %s (pid %d)\n", result.Frontmost.BundleID, result.Frontmost.PID); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(out, "Device: %s\nLease: %s\n", result.UDID, result.Lease.captureLine(sessionID)); err != nil {
		return err
	}
	if result.OnlyStatusBar {
		// Said as a possibility, because a genuinely blank screen looks the same
		// from here - and read twice already, so the caller knows it is not a
		// timing accident.
		if _, err := fmt.Fprint(out, "Note: the tree is the status bar and nothing else, read twice. "+
			"The app may not have published its screen yet - run `ao sim shot` to see what is actually there.\n"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	if err := writeSimAXElements(out, result.Elements, 0); err != nil {
		return err
	}
	if result.Truncated {
		if _, err := fmt.Fprintf(out, "\n%d of %d elements shown. Re-run with --max-nodes %d to see the rest.\n",
			result.NodeCount, result.TotalNodeCount, result.TotalNodeCount); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(out, "\nTap an element with its own point: `ao sim tap <x> <y>` (claim the device first with `ao sim claim`).\n")
	return err
}

// writeSimAXElements prints the tree with the tap point on every line, so the
// readable form carries the same actionable coordinate the JSON does.
func writeSimAXElements(out io.Writer, elements []simbridge.Element, depth int) error {
	for _, e := range elements {
		label := e.Label
		if label == "" {
			label = e.Value
		}
		line := strings.TrimSpace(e.Type + " " + quoteIfPresent(label))
		if e.Value != "" && e.Value != label {
			line += " = " + quoteIfPresent(e.Value)
		}
		if !e.Enabled {
			line += " (disabled)"
		}
		// Where to touch it, or that there is nowhere to - never a coordinate
		// that reaches something else.
		switch {
		case e.Tap != nil:
			line += fmt.Sprintf("  tap %.3f %.3f", e.Tap.X, e.Tap.Y)
		case e.OffScreen:
			line += "  off screen"
		}
		// The element's own rectangle, in the tap point's units: left,top to
		// right,bottom. For something below the fold it is also the distance -
		// a top edge of 1.36 is a third of a screen further down.
		if e.Box != nil {
			line += fmt.Sprintf("  box %.3f,%.3f->%.3f,%.3f", e.Box.X1, e.Box.Y1, e.Box.X2, e.Box.Y2)
		}
		if _, err := fmt.Fprintf(out, "%s%s  [%s]\n", strings.Repeat("  ", depth), line, e.Path); err != nil {
			return err
		}
		if err := writeSimAXElements(out, e.Children, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func quoteIfPresent(s string) string {
	if s == "" {
		return ""
	}
	return `"` + s + `"`
}

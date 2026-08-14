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
  ao sim ax --max-nodes 2000 --udid 00000000-0000-0000-0000-000000000000`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if opts.maxNodes <= 0 {
				return usageError{fmt.Errorf("--max-nodes must be positive, got %d", opts.maxNodes)}
			}
			result, err := ctx.readSimAX(cmd.Context(), opts.udid, opts.maxNodes)
			if err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			return writeSimAX(cmd.OutOrStdout(), result, strings.TrimSpace(os.Getenv("AO_SESSION_ID")))
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.udid, "udid", "", "Read this simulator instead of the booted one")
	f.IntVar(&opts.maxNodes, "max-nodes", defaultSimAXMaxNodes, "Stop after this many elements")
	f.BoolVar(&opts.json, "json", false, "Output the tree as JSON")
	return cmd
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
		return simAXResult{}, emptySimTreeError(device, snapshot.Frontmost)
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

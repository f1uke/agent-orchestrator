package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/simflow"
)

// `ao sim record` turns what a session drives on a device - through
// `ao sim tap`/`swipe`/`drag`/`type`/`button`, and through a human driving the
// same session's Device tab - into a Maestro flow it can hand back to the
// team's suite.
//
// It never boots, claims or drives a device on its own: `start` requires a
// live claim this session already holds and refuses rather than take one,
// `status` only reads what has been captured, and `stop` writes the flow
// wherever `ao sim shot` would put a screenshot - a session's own artifact
// directory, outside every repository - so a generated flow can never be
// committed by accident.

// simRecordingClient mirrors domain.SimRecording on the wire.
type simRecordingClient struct {
	UDID      string     `json:"udid"`
	SessionID string     `json:"sessionId"`
	Name      string     `json:"name"`
	StartedAt time.Time  `json:"startedAt"`
	StoppedAt *time.Time `json:"stoppedAt,omitempty"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// simRecordingStepClient mirrors domain.SimRecordingStep on the wire.
type simRecordingStepClient struct {
	Seq               int64     `json:"seq"`
	At                time.Time `json:"at"`
	Kind              string    `json:"kind"`
	Selector          string    `json:"selector,omitempty"`
	SelectorRung      int64     `json:"selectorRung,omitempty"`
	SelectorIndex     int64     `json:"selectorIndex,omitempty"`
	SelectorAnchor    string    `json:"selectorAnchor,omitempty"`
	SelectorAnchorRel string    `json:"selectorAnchorRel,omitempty"`
	Ambiguity         int64     `json:"ambiguity,omitempty"`
	OffScreen         bool      `json:"offScreen,omitempty"`
	ScreenChange      bool      `json:"screenChange,omitempty"`
	X                 float64   `json:"x"`
	Y                 float64   `json:"y"`
	ToX               float64   `json:"toX"`
	ToY               float64   `json:"toY"`
	DurationMS        int64     `json:"durationMs,omitempty"`
	Text              string    `json:"text,omitempty"`
	Detail            string    `json:"detail,omitempty"`
}

// startSimRecordingRequest mirrors controllers.StartSimRecordingInput.
type startSimRecordingRequest struct {
	Name string `json:"name,omitempty"`
}

// simRecordingResponse mirrors controllers.SimRecordingResponse.
type simRecordingResponse struct {
	Recording simRecordingClient `json:"recording"`
}

// simRecordingWithStepsResponse mirrors controllers.SimRecordingWithStepsResponse.
type simRecordingWithStepsResponse struct {
	Recording simRecordingClient       `json:"recording"`
	Steps     []simRecordingStepClient `json:"steps"`
}

func newSimRecordCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "record",
		Short: "Capture this session's gestures on a claimed simulator as a Maestro flow",
		Long: "Record everything this session drives on a device - `ao sim tap`, `swipe`, " +
			"`drag`, `type`, `button`, and a human driving the same session's Device tab - and " +
			"turn it into a Maestro flow.\n\n" +
			"A recording needs a live claim on the device (`ao sim claim`); `start` never takes " +
			"one itself. `status` reports what has been captured without stopping it. `stop` " +
			"closes the recording and writes the flow.",
	}
	cmd.AddCommand(newSimRecordStartCommand(ctx))
	cmd.AddCommand(newSimRecordStatusCommand(ctx))
	cmd.AddCommand(newSimRecordStopCommand(ctx))
	return cmd
}

// --- ao sim record start ----------------------------------------------------

// simRecordStartResult is the `ao sim record start --json` payload.
type simRecordStartResult struct {
	UDID          string    `json:"udid"`
	DeviceName    string    `json:"deviceName"`
	Runtime       string    `json:"runtime"`
	RecordingName string    `json:"recordingName,omitempty"`
	StartedAt     time.Time `json:"startedAt"`
}

func newSimRecordStartCommand(ctx *commandContext) *cobra.Command {
	var opts struct {
		udid string
		name string
		json bool
	}
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start recording this session's gestures on a claimed simulator",
		Long: "Open a recording on a device this session already holds.\n\n" +
			"It never claims the device itself, and it is refused - naming why - on a device " +
			"this session has not claimed, on one someone else holds, or on one that already has " +
			"a recording open. Run `ao sim claim` first.",
		Example: `  ao sim claim
  ao sim record start
  ao sim record start --name "sign up flow"`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := ctx.startSimRecording(cmd.Context(), opts.udid, opts.name)
			if err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			return writeSimRecordStart(cmd.OutOrStdout(), result)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.udid, "udid", "", "Record this simulator instead of the booted one")
	f.StringVar(&opts.name, "name", "", "Optional label for the recording, e.g. the flow it will become")
	f.BoolVar(&opts.json, "json", false, "Output the recording as JSON")
	return cmd
}

func (c *commandContext) startSimRecording(ctx context.Context, udid, name string) (simRecordStartResult, error) {
	sessionID, err := simSessionID("`ao sim record start`")
	if err != nil {
		return simRecordStartResult{}, err
	}
	device, err := c.resolveBootedSimDevice(ctx, udid)
	if err != nil {
		return simRecordStartResult{}, err
	}

	var res simRecordingResponse
	path := "sessions/" + url.PathEscape(sessionID) + "/sim-recordings/" + url.PathEscape(device.UDID)
	body := startSimRecordingRequest{Name: strings.TrimSpace(name)}
	if err := c.postJSON(ctx, path, body, &res); err != nil {
		return simRecordStartResult{}, c.explainSimRecordingRefusal(device, err)
	}
	return simRecordStartResult{
		UDID:          res.Recording.UDID,
		DeviceName:    device.Name,
		Runtime:       device.Runtime,
		RecordingName: res.Recording.Name,
		StartedAt:     res.Recording.StartedAt.UTC(),
	}, nil
}

func writeSimRecordStart(out io.Writer, r simRecordStartResult) error {
	if _, err := fmt.Fprintf(out, "Recording started on %s (%s, %s) at %s.\n",
		r.DeviceName, r.Runtime, r.UDID, r.StartedAt.Format(time.RFC3339)); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out,
		"Drive it with `ao sim tap`/`swipe`/`drag`/`type`/`button`, or the Device tab. Stop it with `ao sim record stop`.")
	return err
}

// --- ao sim record status ---------------------------------------------------

// simRecordStatusResult is the `ao sim record status --json` payload.
type simRecordStatusResult struct {
	UDID       string `json:"udid"`
	DeviceName string `json:"deviceName"`
	Runtime    string `json:"runtime"`
	// Found: a recording exists on this device, open or stopped. False means
	// nothing has ever been started - which is an answer, not an error.
	Found         bool       `json:"found"`
	Open          bool       `json:"open"`
	SessionID     string     `json:"sessionId,omitempty"`
	RecordingName string     `json:"recordingName,omitempty"`
	StartedAt     *time.Time `json:"startedAt,omitempty"`
	StoppedAt     *time.Time `json:"stoppedAt,omitempty"`
	StepCount     int        `json:"stepCount"`
}

func newSimRecordStatusCommand(ctx *commandContext) *cobra.Command {
	var opts struct {
		udid string
		json bool
	}
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show what a device's recording has captured so far, without stopping it",
		Long: "Report a device's recording: whether one is open, when it started, and how many " +
			"steps it has captured.\n\n" +
			"A device with nothing being recorded is not an error - the command says so plainly " +
			"and exits 0.",
		Example: `  ao sim record status`,
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := ctx.simRecordingStatus(cmd.Context(), opts.udid)
			if err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			return writeSimRecordStatus(cmd.OutOrStdout(), result)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.udid, "udid", "", "Report this simulator instead of the booted one")
	f.BoolVar(&opts.json, "json", false, "Output the status as JSON")
	return cmd
}

func (c *commandContext) simRecordingStatus(ctx context.Context, udid string) (simRecordStatusResult, error) {
	sessionID, err := simSessionID("`ao sim record status`")
	if err != nil {
		return simRecordStatusResult{}, err
	}
	device, err := c.resolveBootedSimDevice(ctx, udid)
	if err != nil {
		return simRecordStatusResult{}, err
	}

	var res simRecordingWithStepsResponse
	path := "sessions/" + url.PathEscape(sessionID) + "/sim-recordings/" + url.PathEscape(device.UDID)
	if err := c.getJSON(ctx, path, &res); err != nil {
		var apiErr apiResponseError
		if errors.As(err, &apiErr) && apiErr.ErrorBody.Code == "SIM_NOT_FOUND" {
			return simRecordStatusResult{UDID: device.UDID, DeviceName: device.Name, Runtime: device.Runtime}, nil
		}
		return simRecordStatusResult{}, err
	}

	result := simRecordStatusResult{
		UDID: device.UDID, DeviceName: device.Name, Runtime: device.Runtime,
		Found: true, SessionID: res.Recording.SessionID, RecordingName: res.Recording.Name,
		StepCount: len(res.Steps),
	}
	startedAt := res.Recording.StartedAt.UTC()
	result.StartedAt = &startedAt
	if res.Recording.StoppedAt != nil {
		stoppedAt := res.Recording.StoppedAt.UTC()
		result.StoppedAt = &stoppedAt
	} else {
		result.Open = true
	}
	return result, nil
}

func writeSimRecordStatus(out io.Writer, r simRecordStatusResult) error {
	if !r.Found {
		_, err := fmt.Fprintf(out, "Nothing is being recorded on %s (%s, %s).\n", r.DeviceName, r.Runtime, r.UDID)
		return err
	}
	if r.Open {
		_, err := fmt.Fprintf(out, "Recording open on %s (%s, %s), held by @%s, since %s - %d step(s) captured so far.\n",
			r.DeviceName, r.Runtime, r.UDID, r.SessionID, r.StartedAt.Format(time.RFC3339), r.StepCount)
		return err
	}
	_, err := fmt.Fprintf(out, "Recording on %s (%s, %s) stopped at %s - %d step(s) were captured before it was stopped.\n",
		r.DeviceName, r.Runtime, r.UDID, r.StoppedAt.Format(time.RFC3339), r.StepCount)
	return err
}

// --- ao sim record stop -----------------------------------------------------

// simRecordStopResult is the `ao sim record stop --json` payload.
type simRecordStopResult struct {
	UDID       string `json:"udid"`
	DeviceName string `json:"deviceName"`
	Runtime    string `json:"runtime"`
	Path       string `json:"path"`
	StepCount  int    `json:"stepCount"`
	Bytes      int    `json:"bytes"`
}

func newSimRecordStopCommand(ctx *commandContext) *cobra.Command {
	var opts struct {
		udid  string
		out   string
		entry string
		json  bool
	}
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop recording and write what was captured as a Maestro flow",
		Long: "Close this device's open recording and emit everything it captured as a Maestro " +
			"flow.\n\n" +
			"The flow never invents an entry point: a recording begins wherever the app already " +
			"was, and the header says so unless --entry names a shared entry-point flow, which is " +
			"prepended as `runFlow`. Nothing here ever fabricates `launchApp`.\n\n" +
			"The flow lands under this session's own artifact directory " +
			"(<AO data dir>/sim/<session id>/), outside any repository, so it can never be " +
			"committed by accident. Use --out to write somewhere else.",
		Example: `  ao sim record stop
  ao sim record stop --entry ../flows/sign-in.yaml
  ao sim record stop --out /tmp/flow.yaml --json`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := ctx.stopSimRecording(cmd.Context(), opts.udid, opts.out, opts.entry)
			if err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			return writeSimRecordStop(cmd.OutOrStdout(), result)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.udid, "udid", "", "Stop recording this simulator instead of the booted one")
	f.StringVar(&opts.out, "out", "", "Write the flow here instead of the session artifact directory")
	f.StringVar(&opts.entry, "entry", "",
		"Path to a shared entry-point flow, emitted as `runFlow` before the recorded steps")
	f.BoolVar(&opts.json, "json", false, "Output the result as JSON")
	return cmd
}

func (c *commandContext) stopSimRecording(ctx context.Context, udid, out, entry string) (simRecordStopResult, error) {
	sessionID, err := simSessionID("`ao sim record stop`")
	if err != nil {
		return simRecordStopResult{}, err
	}
	device, err := c.resolveBootedSimDevice(ctx, udid)
	if err != nil {
		return simRecordStopResult{}, err
	}

	var res simRecordingWithStepsResponse
	path := "sessions/" + url.PathEscape(sessionID) + "/sim-recordings/" + url.PathEscape(device.UDID)
	if err := c.deleteJSON(ctx, path, &res); err != nil {
		return simRecordStopResult{}, c.explainSimRecordingStopFailure(device, err)
	}

	steps := make([]simflow.Step, 0, len(res.Steps))
	for _, s := range res.Steps {
		steps = append(steps, simRecordingStepToFlow(s))
	}

	recordedAt := c.deps.Now().UTC()
	if res.Recording.StoppedAt != nil {
		recordedAt = res.Recording.StoppedAt.UTC()
	}
	// EmitOptions carries no Frontmost field - see its own doc comment in
	// internal/simflow/emit.go for why: nothing upstream of this call
	// persists which app was frontmost when the recording started, and a
	// header line that would always read "unknown" is worse than no line.
	flow, err := simflow.Emit(steps, simflow.EmitOptions{
		Device:     device.Name,
		Runtime:    device.Runtime,
		RecordedAt: recordedAt.Format(time.RFC3339),
		Entry:      strings.TrimSpace(entry),
	})
	if err != nil {
		// The recording already stopped on the daemon side - that side effect
		// happened and cannot be undone here - so the failure has to say so
		// rather than read as "nothing happened".
		return simRecordStopResult{}, fmt.Errorf("recording stopped on %s, but the flow could not be built: %w",
			device.Label(), err)
	}

	writePath := strings.TrimSpace(out)
	if writePath == "" {
		writePath, err = simSessionRecordingPath(recordedAt, device.UDID)
		if err != nil {
			return simRecordStopResult{}, err
		}
	} else if writePath, err = filepath.Abs(writePath); err != nil {
		return simRecordStopResult{}, fmt.Errorf("resolve --out path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(writePath), 0o750); err != nil {
		return simRecordStopResult{}, fmt.Errorf("create recording directory: %w", err)
	}
	if err := os.WriteFile(writePath, []byte(flow), 0o600); err != nil {
		return simRecordStopResult{}, fmt.Errorf("write flow to %s: %w", writePath, err)
	}

	return simRecordStopResult{
		UDID: device.UDID, DeviceName: device.Name, Runtime: device.Runtime,
		Path: writePath, StepCount: len(steps), Bytes: len(flow),
	}, nil
}

func writeSimRecordStop(out io.Writer, r simRecordStopResult) error {
	if _, err := fmt.Fprintf(out, "Stopped recording on %s (%s, %s): %d step(s) captured.\n",
		r.DeviceName, r.Runtime, r.UDID, r.StepCount); err != nil {
		return err
	}
	// The path gets a line of its own, the same way `ao sim shot` prints it, so
	// it can be read straight off the terminal and handed to a file read.
	_, err := fmt.Fprintln(out, r.Path)
	return err
}

// simSessionRecordingPath puts the emitted flow in this session's own artifact
// directory under the AO data dir, by the same rule `ao sim shot` uses for its
// screenshots (simSessionShotPath in sim.go): per-session so concurrent
// workers cannot clobber each other, and outside every repository so a
// generated flow can never be committed by accident.
func simSessionRecordingPath(recordedAt time.Time, udid string) (string, error) {
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sessionID == "" {
		return "", errors.New("ao sim record stop writes into the current session's artifact directory, but " +
			"AO_SESSION_ID is not set; pass --out <path> to write outside an AO session")
	}
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	name := recordedAt.Format(simShotStampLayout) + "Z-" + udid + ".yaml"
	return filepath.Join(cfg.DataDir, "sim", sessionID, name), nil
}

// simRecordingStepToFlow turns one recorded step, as the daemon returns it,
// into simflow.Step - Emit's own mechanism-neutral shape (Task 4).
//
// Choice.Index (which of several same-label matches this step resolved to)
// round-trips through SelectorIndex (0039_sim_recording_step_index.sql):
// without it, re-emitting a flow from a stored recording would always
// address the FIRST element sharing a label, silently substituting it for
// whichever one was actually tapped - a flow whose selector text reads
// correctly while touching the wrong element. That is a correctness bug, not
// a cosmetic one, which is why it has its own column rather than a
// documented gap.
//
// Choice.Anchor/Relation round-trip through their own columns
// (0041_sim_recording_step_anchor.sql) for the same reason: an anchor resolved
// at record time and then lost would fall back to the index it exists to
// replace, quietly reintroducing the failure it was added to remove.
//
// Choice.Escaped - whether the stored selector text needed regex escaping -
// has no column of its own and needs none: it is RECOVERED here rather than
// remembered, because a stored text selector is always the escaped form, so
// unescaping it recovers both the plain label and the fact that it was
// escaped in the first place. Both are used, and neither is cosmetic:
//
//   - Plain is what scrollUntilVisible matches on for an off-screen element,
//     and render.go's rule is that it is the label a human reads. Handing it
//     the stored (escaped) text emitted `element: "See all \\(12\\)"` for a
//     label that reads "See all (12)".
//   - Escaped drives the "# escaped: ..." comment that tells a reader why the
//     selector above it is full of backslashes.
func simRecordingStepToFlow(step simRecordingStepClient) simflow.Step {
	choice := simflow.Choice{
		Rung:      simflow.Rung(step.SelectorRung),
		Index:     int(step.SelectorIndex),
		Ambiguity: int(step.Ambiguity),
		OffScreen: step.OffScreen,
	}
	// plain is the label as a human reads it; it stays empty for a rung whose
	// selector is not text, so Render falls back to the id the way it does for
	// a Choice that never had a label.
	var plain string
	switch choice.Rung {
	case simflow.RungText, simflow.RungTextIndex:
		choice.Text = step.Selector
		plain, choice.Escaped = simflow.Unescape(step.Selector)
	case simflow.RungTextAnchor:
		choice.Text = step.Selector
		plain, choice.Escaped = simflow.Unescape(step.Selector)
		choice.Anchor = step.SelectorAnchor
		_, choice.AnchorEscaped = simflow.Unescape(step.SelectorAnchor)
		choice.Relation = simflow.Relation(step.SelectorAnchorRel)
	case simflow.RungID:
		choice.ID = step.Selector
	case simflow.RungPoint:
		choice.PercentX = simRecordPercent(step.X)
		choice.PercentY = simRecordPercent(step.Y)
	}
	if choice.OffScreen {
		// The element's own box - what scrollDirectionFor (internal/simflow)
		// would otherwise decide this from - is never persisted either. DOWN
		// is that same function's fallback "when an element's edges are not
		// known", which is exactly this case.
		choice.ScrollDirection = simflow.ScrollDown
	}
	return simflow.Step{
		Seq:          step.Seq,
		Kind:         simRecordingStepKind(step.Kind),
		Choice:       choice,
		Plain:        plain,
		ScreenChange: step.ScreenChange,
		X:            step.X, Y: step.Y, ToX: step.ToX, ToY: step.ToY,
		Text:   step.Text,
		Detail: step.Detail,
	}
}

// simRecordingStepKind maps a recorded step's Kind onto simflow's coarser
// vocabulary (Task 4). A drag is emitted exactly like a swipe (spec §8), so
// both "swipe" and "drag" - along with the daemon's own "drag-begin"/
// "drag-move"/"drag-end" intent kinds, which the Device tab may still send
// under a single hold - map onto StepSwipe. Anything this switch does not
// recognize passes straight through: Emit refuses an untranslatable step by
// name rather than dropping it silently, and inventing a second "unknown
// kind" error here would just repeat that refusal worse.
func simRecordingStepKind(kind string) simflow.StepKind {
	switch kind {
	case "tap":
		return simflow.StepTap
	case "type":
		return simflow.StepType
	case "swipe", "drag", "drag-begin", "drag-move", "drag-end":
		return simflow.StepSwipe
	case "button":
		return simflow.StepButton
	default:
		return simflow.StepKind(kind)
	}
}

// simRecordPercent mirrors internal/simflow's own unexported percent(): a
// normalized 0..1 coordinate rounded to the whole percent Maestro takes,
// clamped to 0..100. Duplicated rather than exported because it is three
// lines and the alternative is widening simflow's API for a single caller.
func simRecordPercent(v float64) int {
	p := int(math.Round(v * 100))
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// explainSimRecordingRefusal turns the daemon's SIM_RECORDING_REFUSED 409
// into the sentence that says what to do about it. `ao sim record start`
// never claims a device itself - this only explains why the daemon declined.
func (c *commandContext) explainSimRecordingRefusal(device simDevice, err error) error {
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) || apiErr.ErrorBody.Code != "SIM_RECORDING_REFUSED" {
		return err
	}
	reason, _ := apiErr.ErrorBody.Details["reason"].(string)
	switch reason {
	case "already_open":
		return fmt.Errorf("%s already has a recording open, so nothing was started.\n"+
			"Run `ao sim record status` to see what it has captured, or `ao sim record stop` to close it first",
			device.Label())
	case "leased_by_other":
		holder, _ := apiErr.ErrorBody.Details["holder"].(string)
		left := ""
		if raw, ok := apiErr.ErrorBody.Details["expiresAt"].(string); ok {
			if expiresAt, parseErr := time.Parse(time.RFC3339, raw); parseErr == nil {
				expiresAt = expiresAt.UTC()
				left = fmt.Sprintf(" for another %s", simRemaining(&expiresAt, c.deps.Now().UTC()))
			}
		}
		return fmt.Errorf("%s is leased by @%s%s, so this session may not record it.\n"+
			"`ao sim record start` never claims a device - wait for the lease to lapse, or ask @%s to run `ao sim release`",
			device.Label(), holder, left, holder)
	default:
		return fmt.Errorf("%s is not claimed by this session, so it may not be recorded.\n"+
			"Run `ao sim claim` first - `ao sim record start` never claims a device on your behalf",
			device.Label())
	}
}

// explainSimRecordingStopFailure turns "no open recording" into plain advice
// instead of a generic daemon error.
func (c *commandContext) explainSimRecordingStopFailure(device simDevice, err error) error {
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) || apiErr.ErrorBody.Code != "SIM_NOT_FOUND" {
		return err
	}
	return fmt.Errorf("nothing is being recorded on %s, so there is nothing to stop.\n"+
		"Run `ao sim record start` first", device.Label())
}

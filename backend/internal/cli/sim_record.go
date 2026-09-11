package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/simvideo"
)

// `ao sim record` records a simulator's SCREEN to a video file.
//
// It is the moving sibling of `ao sim shot`, and it is deliberately built to
// feel like one: the same device rule (`--udid`, else the device this session
// was assigned, else the only booted one), the same `Lease:` line saying who is
// driving, the same session artifact directory outside every repository, and a
// path on a line of its own so it can be read straight off the terminal.
//
// Like `ao sim shot`, and unlike `ao sim flow record`, it takes NO LEASE. A
// recording cannot corrupt anybody's gesture any more than a screenshot can,
// and filming a device while a human drives it is one of the things this is
// for. What it does have is ownership: the session that started a recording is
// the only one that can stop it, and a second start on the same device is
// refused naming the holder rather than letting two recorders fight over one
// file.
//
// Start and stop are separate invocations because `simctl io recordVideo` runs
// until it is signalled. The process itself belongs to the DAEMON (see
// internal/simvideo), which is the only part of AO alive between two commands -
// and the only part that can promise nothing is left recording when a session
// ends or the daemon stops.
//
// It used to be the gesture recorder. That moved to `ao sim flow record`, where
// the noun it produces already lives, and there is no alias for the old
// spelling: a name that still works is the confusion the move exists to remove.

// simVideoClient mirrors controllers.SimVideoView on the wire.
type simVideoClient struct {
	UDID               string `json:"udid"`
	SessionID          string `json:"sessionId"`
	Path               string `json:"path"`
	StartedAt          string `json:"startedAt"`
	MaxDurationSeconds int    `json:"maxDurationSeconds"`
	StoppedAt          string `json:"stoppedAt,omitempty"`
	StopReason         string `json:"stopReason,omitempty"`
	Bytes              int64  `json:"bytes"`
}

// startSimVideoRequest mirrors controllers.StartSimVideoInput.
type startSimVideoRequest struct {
	MaxDurationSeconds int `json:"maxDurationSeconds,omitempty"`
}

// simVideoResponse mirrors controllers.SimVideoResponse.
type simVideoResponse struct {
	Video simVideoClient `json:"video"`
}

func newSimRecordCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "record",
		Short: "Record a simulator's screen to a video file",
		Long: "Record what a booted simulator shows, and write it to a video file this " +
			"session can read.\n\n" +
			"Recording is explicit at both ends: `start` opens one and returns, `status` " +
			"says whether one is open and for how long, and `stop` closes it and prints " +
			"the file's path. Nothing starts a recording for you, and no `ao sim flow run` " +
			"is filmed unless you asked for it.\n\n" +
			"It takes no lease, exactly as `ao sim shot` takes none: a recording cannot " +
			"corrupt anyone's gesture, and filming a device a human is driving is the " +
			"point. It does report who holds the device, and only the session that " +
			"started a recording can stop it.\n\n" +
			"To record the GESTURES you drive as a replayable Maestro flow instead, see " +
			"`ao sim flow record`.",
	}
	cmd.AddCommand(newSimRecordStartCommand(ctx))
	cmd.AddCommand(newSimRecordStatusCommand(ctx))
	cmd.AddCommand(newSimRecordStopCommand(ctx))
	return cmd
}

// --- ao sim record start ----------------------------------------------------

// simRecordStartResult is the `ao sim record start --json` payload.
type simRecordStartResult struct {
	UDID               string `json:"udid"`
	DeviceName         string `json:"deviceName"`
	Runtime            string `json:"runtime"`
	Path               string `json:"path"`
	StartedAt          string `json:"startedAt"`
	MaxDurationSeconds int    `json:"maxDurationSeconds"`
	// Lease is reported and never required, the same way `ao sim shot` reports
	// it: a recording of somebody else's device is legitimate, and knowing
	// whose gestures are being filmed is the thing a reader needs.
	Lease simLeaseView `json:"lease"`
}

func newSimRecordStartCommand(ctx *commandContext) *cobra.Command {
	var opts struct {
		udid        string
		maxDuration time.Duration
		json        bool
	}
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start recording a booted simulator's screen",
		Long: "Open a screen recording on a booted simulator.\n\n" +
			"It returns once the device is actually being recorded, not merely once the " +
			"recorder has been spawned, so whatever you drive next is in the video.\n\n" +
			"A device that is already being recorded is refused, naming the session that " +
			"holds it. The recording stops itself after --max-duration (10 minutes by " +
			"default) if nothing stops it first, and it never outlives this session.\n\n" +
			"The video lands under this session's own artifact directory " +
			"(<AO data dir>/sim/<session id>/videos/), outside any repository, so it can " +
			"never be committed by accident.",
		Example: `  ao sim record start
  ao sim record start --max-duration 2m
  ao sim record start --udid 00000000-0000-0000-0000-000000000000 --json`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := ctx.startSimVideo(cmd.Context(), opts.udid, opts.maxDuration)
			if err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			return writeSimRecordStart(cmd.OutOrStdout(), result, strings.TrimSpace(os.Getenv("AO_SESSION_ID")))
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.udid, "udid", "", "Record this simulator instead of the booted one")
	f.DurationVar(&opts.maxDuration, "max-duration", 0,
		fmt.Sprintf("Stop the recording automatically after this long (default %s, at most %s)",
			simvideo.DefaultMaxDuration, simvideo.MaxMaxDuration))
	f.BoolVar(&opts.json, "json", false, "Output the recording as JSON")
	return cmd
}

func (c *commandContext) startSimVideo(ctx context.Context, udid string, maxDuration time.Duration) (simRecordStartResult, error) {
	sessionID, err := simSessionID("`ao sim record start`")
	if err != nil {
		return simRecordStartResult{}, err
	}
	device, err := c.resolveBootedSimDevice(ctx, udid)
	if err != nil {
		return simRecordStartResult{}, err
	}

	var res simVideoResponse
	body := startSimVideoRequest{MaxDurationSeconds: int(maxDuration.Seconds())}
	if err := c.postJSON(ctx, simVideoPath(sessionID, device.UDID), body, &res); err != nil {
		return simRecordStartResult{}, explainSimVideoRefusal(device, err)
	}

	views, reachable := c.simLeaseViews(ctx)
	return simRecordStartResult{
		UDID:               res.Video.UDID,
		DeviceName:         device.Name,
		Runtime:            device.Runtime,
		Path:               res.Video.Path,
		StartedAt:          res.Video.StartedAt,
		MaxDurationSeconds: res.Video.MaxDurationSeconds,
		Lease:              simLeaseFor(views, device.UDID, reachable),
	}, nil
}

func writeSimRecordStart(out io.Writer, r simRecordStartResult, sessionID string) error {
	if _, err := fmt.Fprintf(out, "Recording the screen of %s (%s, %s) from %s.\n",
		r.DeviceName, r.Runtime, r.UDID, r.StartedAt); err != nil {
		return err
	}
	// The path is printed at START as well as at stop, because the one thing a
	// reader wants from an open recording is where it will be - and an agent
	// that only learns the path from `stop` cannot mention it to anyone before
	// then. The file is not readable yet, and the line below says so.
	if _, err := fmt.Fprintln(out, r.Path); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out,
		"The file is written when the recording stops - `ao sim record stop`. It stops itself after %s.\n",
		simRecordDuration(r.MaxDurationSeconds)); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "Lease: %s\n", r.Lease.captureLine(sessionID))
	return err
}

// --- ao sim record status ---------------------------------------------------

// simRecordStatusResult is the `ao sim record status --json` payload.
type simRecordStatusResult struct {
	UDID       string `json:"udid"`
	DeviceName string `json:"deviceName"`
	Runtime    string `json:"runtime"`
	// Recording: something is being recorded right now. False is an answer, not
	// an error - the command says so plainly and exits 0.
	Recording          bool   `json:"recording"`
	SessionID          string `json:"sessionId,omitempty"`
	Path               string `json:"path,omitempty"`
	StartedAt          string `json:"startedAt,omitempty"`
	MaxDurationSeconds int    `json:"maxDurationSeconds,omitempty"`
	// ElapsedSeconds is how long it has been recording. Reported because the
	// question an agent actually has is "how much of my scenario is in this",
	// and it is the number the cap is measured against.
	ElapsedSeconds int `json:"elapsedSeconds,omitempty"`
}

// This is the same SHAPE as `ao sim flow record status` - resolve a device, ask
// the daemon, print prose or JSON - and none of the same meaning: two different
// commands, on two different routes, with help text a reader has to find beside
// the command it belongs to. Folding them into one factory to satisfy a
// token-similarity count would make both harder to read AND couple the screen
// recorder to the flow recorder, which is the confusion this change exists to
// remove.
//
//nolint:dupl // structurally alike, unrelated commands - see the note above.
func newSimRecordStatusCommand(ctx *commandContext) *cobra.Command {
	var opts struct {
		udid string
		json bool
	}
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Say whether a simulator's screen is being recorded, and for how long",
		Long: "Report a device's screen recording: whether one is open, who holds it, where " +
			"its file will land and how long it has been running.\n\n" +
			"A device with nothing being recorded is not an error - the command says so " +
			"plainly and exits 0.",
		Example: `  ao sim record status`,
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := ctx.simVideoStatus(cmd.Context(), opts.udid)
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

func (c *commandContext) simVideoStatus(ctx context.Context, udid string) (simRecordStatusResult, error) {
	sessionID, err := simSessionID("`ao sim record status`")
	if err != nil {
		return simRecordStatusResult{}, err
	}
	device, err := c.resolveBootedSimDevice(ctx, udid)
	if err != nil {
		return simRecordStatusResult{}, err
	}

	result := simRecordStatusResult{UDID: device.UDID, DeviceName: device.Name, Runtime: device.Runtime}
	var res simVideoResponse
	if err := c.getJSON(ctx, simVideoPath(sessionID, device.UDID), &res); err != nil {
		if isSimVideoNotFound(err) {
			return result, nil
		}
		return simRecordStatusResult{}, err
	}
	result.Recording = true
	result.SessionID = res.Video.SessionID
	result.Path = res.Video.Path
	result.StartedAt = res.Video.StartedAt
	result.MaxDurationSeconds = res.Video.MaxDurationSeconds
	if startedAt, err := time.Parse(time.RFC3339, res.Video.StartedAt); err == nil {
		if elapsed := c.deps.Now().UTC().Sub(startedAt); elapsed > 0 {
			result.ElapsedSeconds = int(elapsed.Seconds())
		}
	}
	return result, nil
}

func writeSimRecordStatus(out io.Writer, r simRecordStatusResult) error {
	if !r.Recording {
		_, err := fmt.Fprintf(out, "Nothing is recording the screen of %s (%s, %s).\n",
			r.DeviceName, r.Runtime, r.UDID)
		return err
	}
	if _, err := fmt.Fprintf(out,
		"Recording the screen of %s (%s, %s), held by @%s, for %s of at most %s.\n",
		r.DeviceName, r.Runtime, r.UDID, r.SessionID,
		simRecordDuration(r.ElapsedSeconds), simRecordDuration(r.MaxDurationSeconds)); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out, r.Path)
	return err
}

// --- ao sim record stop -----------------------------------------------------

// simRecordStopResult is the `ao sim record stop --json` payload.
type simRecordStopResult struct {
	UDID       string `json:"udid"`
	DeviceName string `json:"deviceName"`
	Runtime    string `json:"runtime"`
	Path       string `json:"path"`
	Bytes      int64  `json:"bytes"`
	StartedAt  string `json:"startedAt"`
	StoppedAt  string `json:"stoppedAt"`
	// StopReason is what actually ended it. It matters because a recording that
	// hit its cap looks exactly like one you stopped, and the difference is
	// whether the end of the scenario is in the file.
	StopReason     string `json:"stopReason"`
	ElapsedSeconds int    `json:"elapsedSeconds"`
}

func newSimRecordStopCommand(ctx *commandContext) *cobra.Command {
	var opts struct {
		udid string
		json bool
	}
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop recording a simulator's screen and print the video's path",
		Long: "Close this device's open screen recording and print where the video landed.\n\n" +
			"It waits for the recorder to finalize the file rather than killing it: an " +
			"interrupted recording is a playable video, and a killed one is a truncated " +
			"file that opens in nothing.\n\n" +
			"Only the session that started a recording may stop it.",
		Example: `  ao sim record stop
  ao sim record stop --json`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := ctx.stopSimVideo(cmd.Context(), opts.udid)
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
	f.BoolVar(&opts.json, "json", false, "Output the result as JSON")
	return cmd
}

func (c *commandContext) stopSimVideo(ctx context.Context, udid string) (simRecordStopResult, error) {
	sessionID, err := simSessionID("`ao sim record stop`")
	if err != nil {
		return simRecordStopResult{}, err
	}
	device, err := c.resolveBootedSimDevice(ctx, udid)
	if err != nil {
		return simRecordStopResult{}, err
	}

	var res simVideoResponse
	if err := c.deleteJSON(ctx, simVideoPath(sessionID, device.UDID), &res); err != nil {
		if isSimVideoNotFound(err) {
			return simRecordStopResult{}, fmt.Errorf(
				"nothing is recording the screen of %s, so there is nothing to stop.\n"+
					"Run `ao sim record start` first", device.Label())
		}
		return simRecordStopResult{}, explainSimVideoRefusal(device, err)
	}

	result := simRecordStopResult{
		UDID: device.UDID, DeviceName: device.Name, Runtime: device.Runtime,
		Path: res.Video.Path, Bytes: res.Video.Bytes,
		StartedAt: res.Video.StartedAt, StoppedAt: res.Video.StoppedAt,
		StopReason: res.Video.StopReason,
	}
	startedAt, startErr := time.Parse(time.RFC3339, res.Video.StartedAt)
	stoppedAt, stopErr := time.Parse(time.RFC3339, res.Video.StoppedAt)
	if startErr == nil && stopErr == nil {
		if elapsed := stoppedAt.Sub(startedAt); elapsed > 0 {
			result.ElapsedSeconds = int(elapsed.Seconds())
		}
	}
	return result, nil
}

func writeSimRecordStop(out io.Writer, r simRecordStopResult) error {
	summary := fmt.Sprintf("Stopped recording %s (%s, %s): %s of screen, %s",
		r.DeviceName, r.Runtime, r.UDID, simRecordDuration(r.ElapsedSeconds), simRecordBytes(r.Bytes))
	// The cap is only mentioned when it fired, for the same reason the flow's
	// review count is: a line that always reads "stopped as asked" is a line
	// nobody reads on the day it says something else.
	if r.StopReason == string(simvideo.StopReasonMaxDuration) {
		summary += " - it reached its maximum duration and stopped itself, so the end of what you drove may not be in it"
	}
	if _, err := fmt.Fprintln(out, summary+"."); err != nil {
		return err
	}
	// The path gets a line of its own, the same way `ao sim shot` prints it.
	if _, err := fmt.Fprintln(out, r.Path); err != nil {
		return err
	}
	// Said once, at the only moment it is actionable: the video is on disk now,
	// and an empty one means the screen never changed rather than that
	// recording failed.
	if r.Bytes == 0 {
		_, err := fmt.Fprintln(out,
			"The file is empty: simctl records a frame only when the screen changes, so a device that sat still produces nothing.")
		return err
	}
	return nil
}

// --- shared -----------------------------------------------------------------

func simVideoPath(sessionID, udid string) string {
	return "sessions/" + url.PathEscape(sessionID) + "/sim-videos/" + url.PathEscape(udid)
}

// isSimVideoNotFound is "no recording is open on this device", which is an
// answer for `status` and a refusal for `stop`.
func isSimVideoNotFound(err error) bool {
	var apiErr apiResponseError
	return errors.As(err, &apiErr) && apiErr.ErrorBody.Code == "SIM_VIDEO_NOT_FOUND"
}

// explainSimVideoRefusal turns the daemon's 409 into the sentence that says
// what to do about it. Anything else passes through untouched.
func explainSimVideoRefusal(device simDevice, err error) error {
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) || apiErr.ErrorBody.Code != "SIM_VIDEO_HELD" {
		return err
	}
	holder, _ := apiErr.ErrorBody.Details["holder"].(string)
	path, _ := apiErr.ErrorBody.Details["path"].(string)
	var msg string
	// Telling somebody to ask THEMSELVES to stop it is the kind of sentence
	// that makes a reader doubt everything else the command says. The common
	// case - a session that forgot it already had one open - gets the
	// instruction it can actually follow.
	if holder != "" && holder == strings.TrimSpace(os.Getenv("AO_SESSION_ID")) {
		msg = fmt.Sprintf("%s already has a recording open, started by this session, so nothing was started or stopped.\n"+
			"Run `ao sim record stop` to close it, or `ao sim record status` to see how long it has been running",
			device.Label())
	} else {
		msg = fmt.Sprintf("%s is already being recorded by @%s, so nothing was started or stopped.\n"+
			"A recording belongs to the session that opened it: ask @%s to run `ao sim record stop`, "+
			"or wait for it to reach its maximum duration",
			device.Label(), holder, holder)
	}
	if path != "" {
		msg += "\nIts video will land at " + path
	}
	return errors.New(msg)
}

// simRecordDuration renders seconds the way a person reads them, and never as
// a bare "0s" where a count of seconds is the whole point.
func simRecordDuration(seconds int) string {
	if seconds <= 0 {
		return "less than a second"
	}
	return (time.Duration(seconds) * time.Second).String()
}

// simRecordBytes renders a file size. Video sizes are the one number here a
// reader compares against their disk, so they are printed in the units they
// think in rather than as a raw byte count.
func simRecordBytes(bytes int64) string {
	switch {
	case bytes <= 0:
		return "an empty file"
	case bytes < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(bytes)/(1<<10))
	default:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	}
}

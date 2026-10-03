package cli

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simtype"
)

// `ao sim type` through the daemon's XCTest runner - the default route.
//
// Key presses carry key POSITIONS and the simulator decides what each one
// becomes, so a guest on a Thai input mode turns "lf86428" into "สดคุภ/ค" and
// Thai text has no key at all; the pasteboard carries characters, but as one
// paste rather than keystrokes. XCTest types
// CHARACTERS through the software keyboard, in any process on screen, and the
// daemon proves on screen that they arrived (internal/simtype).
//
// The older routes stay: `--paste` and `--raw-keys` ask for them, and they are
// the fallback when the runner cannot type - which is never silent. The result
// says which route it took and why.

// simTypeRunnerWait is how long a type waits for a runner that is still
// starting before it falls back. A runner answers 3-6 s after a claim, and
// the fallback is the route that fails on exactly the fields this one exists
// for, so waiting is the better answer.
const simTypeRunnerWait = 15 * time.Second

// simTypeInput mirrors controllers.SimTypeInput.
type simTypeInput struct {
	Text   string `json:"text"`
	WaitMs int    `json:"waitMs,omitempty"`
}

// simTypeResponse mirrors controllers.SimTypeResponse.
type simTypeResponse struct {
	UDID  string `json:"udid"`
	App   string `json:"app"`
	Field struct {
		Type        string `json:"type"`
		ID          string `json:"id,omitempty"`
		Label       string `json:"label,omitempty"`
		Placeholder string `json:"placeholder,omitempty"`
	} `json:"field"`
	Landed   simPasteLanding `json:"landed"`
	Detail   string          `json:"detail"`
	Keyboard bool            `json:"keyboard"`
	TypingMs int             `json:"typingMs"`
	Warning  string          `json:"warning,omitempty"`

	KeyboardSwitchedTo string `json:"keyboardSwitchedTo,omitempty"`
	KeyboardRestored   bool   `json:"keyboardRestored,omitempty"`
}

// xctestTypeUnsuitable says why text cannot go through XCTest, or "". A
// control character is a KEY - Return submits, Tab moves focus - and keys go
// as HID presses, which is also how `ao sim key` sends them.
func xctestTypeUnsuitable(text string) string {
	for _, r := range text {
		if unicode.IsControl(r) {
			return fmt.Sprintf("the text holds the control character %q, which is a key press rather than "+
				"text - type the text, then press the key with `ao sim key`", string(r))
		}
	}
	return ""
}

// runSimTypeXCTest types text through the runner, a chunk per request.
//
// It returns a fallback when NOTHING was typed and another route may be
// taken: the runner is not there, XCTest refused before a single character
// arrived, or the field would drop characters typed through the keyboard on
// screen. Every other outcome is final - a type that may have landed is never
// sent a second way, or the text goes in twice.
func (c *commandContext) runSimTypeXCTest(cmd *cobra.Command, opts simTouchOptions, device simDevice, text string) (
	fallback simTypeFallbackRoute, err error,
) {
	ctx := cmd.Context()
	sessionID, err := simSessionID("`ao sim type`")
	if err != nil {
		return fallback, err
	}
	path := "sessions/" + url.PathEscape(sessionID) + "/sim-devices/" + url.PathEscape(device.UDID) + "/type"
	total := utf8.RuneCountInString(text)
	chunks := simtype.Chunks(text, simtype.ChunkRunes)
	started := c.deps.Now()
	var resp simTypeResponse
	typingMs, done := 0, 0
	for i, chunk := range chunks {
		in := simTypeInput{Text: chunk}
		if i == 0 {
			in.WaitMs = int(simTypeRunnerWait.Milliseconds())
		}
		resp = simTypeResponse{}
		if err := c.postJSON(ctx, path, in, &resp); err != nil {
			if i == 0 {
				if fallback := simTypeFallback(err); fallback.Reason != "" {
					return fallback, nil
				}
			}
			err = c.explainSimTypeFailure(device, err)
			if i > 0 {
				err = fmt.Errorf("the first %d of %d characters were typed and proven on screen, then the rest "+
					"failed - type only what is missing, or the start goes in twice: %w", done, total, err)
			}
			return fallback, err
		}
		typingMs += resp.TypingMs
		done += utf8.RuneCountInString(chunk)
	}

	elapsed := c.deps.Now().Sub(started)
	out := simGestureResult{
		UDID:               device.UDID,
		Name:               device.Name,
		Runtime:            device.Runtime,
		RuntimeIdentifier:  device.RuntimeIdentifier,
		Action:             "type",
		Detail:             simCharacters(total),
		Via:                simTypeViaXCTest,
		App:                resp.App,
		KeyboardSwitchedTo: resp.KeyboardSwitchedTo,
		KeyboardRestored:   resp.KeyboardRestored,
		Landed:             &resp.Landed,
		Note:               simSharedDeviceNote,
	}
	if opts.json {
		return fallback, writeJSON(cmd.OutOrStdout(), out)
	}
	return fallback, writeSimTypeXCTest(cmd.OutOrStdout(), out, resp, typingMs, elapsed)
}

// simTypeViaXCTest is the `via` of a type that went through the runner.
const simTypeViaXCTest = "xctest"

// simTypeFallbackRoute is why `type` takes another route than XCTest.
type simTypeFallbackRoute struct {
	// Reason is the `Fallback:` line. Empty when there is no fallback.
	Reason string
	// Paste: the pasteboard is the route, not whatever the keyboard planner
	// picks - XCTest looked at the field and said key-by-key entry loses
	// characters there.
	Paste bool
	// RunnerDown: the runner itself could not be reached, so a paste goes
	// straight to Command-V rather than asking it to use the edit menu.
	RunnerDown bool
}

// simTypeFallback is the route to take after the runner typed nothing, or a
// zero value when the failure is final.
func simTypeFallback(err error) simTypeFallbackRoute {
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) {
		return simTypeFallbackRoute{}
	}
	if apiErr.StatusCode == http.StatusNotFound && apiErr.ErrorBody.Code == "" {
		// A daemon this old answers an unknown route without the envelope.
		apiErr.ErrorBody.Code = "ROUTE_NOT_FOUND"
	}
	switch apiErr.ErrorBody.Code {
	case "SIM_RUNNER_NOT_READY":
		state, _ := apiErr.ErrorBody.Details["state"].(string)
		reason, _ := apiErr.ErrorBody.Details["reason"].(string)
		return simTypeFallbackRoute{Reason: "XCTest typing was not available (" + simRunnerNote(state, reason) + ")",
			RunnerDown: true}
	case "SIM_TYPE_REFUSED":
		return simTypeFallbackRoute{
			Reason: "XCTest could not type and nothing arrived (" + firstLine(apiErr.ErrorBody.Message) + ")"}
	case "SIM_TYPE_UNSUITABLE":
		return simTypeFallbackRoute{Reason: apiErr.ErrorBody.Message, Paste: true}
	case "ROUTE_NOT_FOUND":
		return simTypeFallbackRoute{
			Reason:     "the running daemon predates XCTest typing; restart it (`ao stop && ao start`) to get it",
			RunnerDown: true}
	}
	return simTypeFallbackRoute{}
}

// explainSimTypeFailure says what went wrong AND what is in the field now,
// because only one of "nothing arrived" and "something may have" is safe to
// retry.
func (c *commandContext) explainSimTypeFailure(device simDevice, err error) error {
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("`ao sim type` failed on %s: %w", device.Label(), err)
	}
	switch apiErr.ErrorBody.Code {
	case "SIM_DEVICE_BUSY":
		return c.explainSimHoldRefusal(device, err)
	case "SIM_TYPE_NO_FOCUS":
		return fmt.Errorf("nothing was typed into %s: %s.\n"+
			"Tap the field first with `ao sim tap`, then type", device.Label(), apiErr.ErrorBody.Message)
	case "SIM_TYPE_NOT_DELIVERED":
		return fmt.Errorf("nothing was typed into %s: XCTest sent the text, but %s", device.Label(),
			apiErr.ErrorBody.Message)
	case "SIM_TYPE_NOT_PROVEN":
		// Not a failure to deliver: a failure to SHOW that it delivered, which
		// reads the same to a caller in a hurry. Said first.
		return fmt.Errorf("the text may be in the field on %s, but `ao sim type` could not prove it: %s",
			device.Label(), apiErr.ErrorBody.Message)
	}
	return fmt.Errorf("`ao sim type` failed on %s: %w", device.Label(), err)
}

// writeSimTypeXCTest reports a type through the runner: where it landed, on
// what evidence, and that the keyboard's language played no part.
func writeSimTypeXCTest(out io.Writer, result simGestureResult, resp simTypeResponse, typingMs int,
	elapsed time.Duration,
) error {
	field := resp.Field.Type
	if resp.Field.Label != "" {
		field += fmt.Sprintf(" %q", resp.Field.Label)
	}
	if _, err := fmt.Fprintf(out, "Typed %s into %s on %s (%s, %s)\n",
		result.Detail, field, result.Name, result.Runtime, result.UDID); err != nil {
		return err
	}
	if resp.Detail != "" {
		if _, err := fmt.Fprintf(out, "Checked on screen: %s\n", resp.Detail); err != nil {
			return err
		}
	}
	if resp.KeyboardSwitchedTo != "" {
		// The keyboard is the device's, and a person may be looking at it.
		line := fmt.Sprintf("Keyboard: switched to %q for this secure field, which takes only what the keyboard "+
			"on screen can type, and switched back afterwards.\n", resp.KeyboardSwitchedTo)
		if !resp.KeyboardRestored {
			line = fmt.Sprintf("WARNING: the simulator keyboard was switched to %q for this secure field and could "+
				"NOT be switched back - its globe key returns it.\n", resp.KeyboardSwitchedTo)
		}
		if _, err := io.WriteString(out, line); err != nil {
			return err
		}
	}
	if resp.Warning != "" {
		if _, err := fmt.Fprintf(out, "XCTest complained while typing, though the text is there: %s\n",
			resp.Warning); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(out, "Route: XCTest (characters, not key presses - the keyboard's language does not "+
		"change what arrives; typed in %s, %s in all)\n",
		(time.Duration(typingMs) * time.Millisecond).Round(10*time.Millisecond), elapsed.Round(10*time.Millisecond))
	return err
}

// simKeyMaxTimes bounds `--times`: enough to clear any field AO types into,
// short enough that a typo in the count does not hold the device for minutes.
const simKeyMaxTimes = 200

func newSimKeyCommand(ctx *commandContext) *cobra.Command {
	opts := simTouchOptions{}
	times := 1
	cmd := &cobra.Command{
		Use:   "key <name>",
		Short: "Press a keyboard key (Return, Backspace, Tab, arrows) on a claimed simulator",
		Long: "Press one keyboard key: " + strings.Join(simbridge.KeyNames(), ", ") + ".\n\n" +
			"These go as hardware key presses, and they mean the same thing whatever language the " +
			"keyboard is set to - which is why they are separate from `ao sim type`, whose text goes " +
			"through XCTest as characters. `--times` repeats the key, e.g. to clear a field with " +
			"`backspace`. Nothing checks what the key did: read the screen with `ao sim ax`.\n\n" +
			"A hardware key press makes iOS treat a hardware keyboard as attached, and it MINIMIZES the " +
			"on-screen keyboard - for every field tapped afterwards, not just this one. So the command " +
			"shows the on-screen keyboard again after the key, and says on a `Keyboard:` line whether it " +
			"saw it come back (it can only look while a field has focus).\n\n" +
			"The device must be claimed by this session (`ao sim claim`) first.",
		Example: `  ao sim key enter
  ao sim key backspace --times 20`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if times < 1 || times > simKeyMaxTimes {
				return usageError{fmt.Errorf("--times must be between 1 and %d", simKeyMaxTimes)}
			}
			press, err := simbridge.Key(args[0])
			if err != nil {
				return usageError{err}
			}
			events := make([]simbridge.Event, 0, len(press)*times)
			for range times {
				events = append(events, press...)
			}
			detail := args[0]
			if times > 1 {
				detail += " x" + strconv.Itoa(times)
			}
			return ctx.runSimGesture(cmd, opts, simGesture{action: "key", detail: detail, events: events, name: args[0],
				showKeyboard: true})
		},
	}
	cmd.Flags().IntVar(&times, "times", 1, "Press the key this many times")
	opts.bind(cmd)
	return cmd
}

// simCharacters is "1 character" or "n characters".
func simCharacters(n int) string {
	if n == 1 {
		return "1 character"
	}
	return strconv.Itoa(n) + " characters"
}

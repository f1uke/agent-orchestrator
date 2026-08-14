// Package simpaste puts text into a simulator's focused field through the
// guest's pasteboard instead of through its keyboard.
//
// It exists because the keyboard cannot be trusted to deliver characters. The
// HID path sends US key usages and the guest turns them into whatever its own
// input mode says they mean, so on a guest set to Thai "fa12345" arrives as
// "ดฟๅ/_ภถ" (see internal/simkeyboard). The pasteboard sidesteps that entirely:
// the text is transferred as text, and Command-V is the one keystroke the guest
// matches WITHOUT running it through the input mode - verified on a real device
// set to Thai, including into a secure field, which nothing else here can fill
// correctly.
//
// Two properties are not negotiable, and both come from the bug this whole
// change is about.
//
// The paste is PROVEN, never assumed. An app can refuse paste, and a field that
// never took focus swallows it silently; reporting success in either case would
// be the same "reports success, wrong data" failure in a new costume. So the
// screen is read before and after, and a paste that cannot be shown to have
// landed is an error. A secure field is exactly where this matters and exactly
// where it still works: it will not report its text, but it reports one dot per
// character, and that count is real evidence.
//
// The payload does NOT outlive the command. Whatever the guest had on its
// pasteboard is put back on every path out, including failures - a password is
// the common payload, and the pasteboard is readable by every app on the
// device. What this cannot do is make that window not exist: between the write
// and the restore the payload IS on the guest's pasteboard, and if the restore
// itself fails the caller is told so rather than left to assume.
package simpaste

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
)

// ErrNotDelivered is a paste that changed nothing on screen. It is separated
// from "the wrong amount arrived" because the two need different advice: this
// one means the field never had focus or the app refuses paste, and nothing is
// in the field to clean up.
var ErrNotDelivered = errors.New("simpaste: the paste did not reach a field")

// Pasteboard is the guest's own clipboard.
type Pasteboard interface {
	Read(ctx context.Context, udid string) (string, error)
	Write(ctx context.Context, udid, text string) error
}

// Result is what happened around the paste itself.
type Result struct {
	// Restored says the guest pasteboard was put back to what it held before.
	Restored bool
	// RestoreErr is why it could not be, when it could not be. The payload is
	// then still on the guest's pasteboard and the caller must say so.
	RestoreErr error
}

// Simctl is a Pasteboard over `xcrun simctl pbcopy` / `pbpaste`.
type Simctl struct{ Run simctl.Runner }

func (s Simctl) Read(ctx context.Context, udid string) (string, error) {
	out, err := s.Run(ctx, simctl.Binary, "simctl", "pbpaste", udid)
	if err != nil {
		return "", fmt.Errorf("could not read the simulator's pasteboard: %w: %s", err, simctl.Output(out))
	}
	return string(out), nil
}

// Write sends the text on stdin, the way `simctl pbcopy` expects it. The runner
// interface has no stdin, so the text is handed to `sh -c` - which is also why
// it is single-quoted with every quote escaped rather than interpolated.
func (s Simctl) Write(ctx context.Context, udid, text string) error {
	script := fmt.Sprintf("printf '%%s' %s | %s simctl pbcopy %s",
		shellQuote(text), simctl.Binary, shellQuote(udid))
	out, err := s.Run(ctx, "/bin/sh", "-c", script)
	if err != nil {
		return fmt.Errorf("could not write the simulator's pasteboard: %w: %s", err, simctl.Output(out))
	}
	return nil
}

// shellQuote wraps a string so a shell reads it as one literal argument,
// whatever is in it. Single quotes cannot appear inside single quotes, so each
// one is closed, escaped and reopened.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Run delivers text through the pasteboard and proves it arrived.
//
// The order matters on every line. The hold is taken first, so a device that is
// mid-gesture is refused before the pasteboard is touched at all. The restore
// is deferred immediately after the payload is written, so no path out - not a
// failed gesture, not a failed read, not a panic - leaves the payload behind.
func Run(
	ctx context.Context,
	holder simgesture.Holder,
	driver simbridge.Driver,
	pb Pasteboard,
	udid, text string,
) (result Result, err error) {
	// Named returns, because the deferred restore below records its outcome ON
	// the result after the return value has been chosen.

	// Read first: there is no point disturbing the pasteboard for a gesture
	// that is about to be refused, and this is also the value we owe back.
	saved, readErr := pb.Read(ctx, udid)

	events := simbridge.Paste()
	// This takes the hold itself rather than delegating to simgesture.Run,
	// because a paste's hold has to cover MORE than the keystroke: the
	// pasteboard write before it and the two screen reads that prove it. A hold
	// that only spanned the Command-V would let another command take the device
	// between the write and the proof, and then the proof would be about
	// somebody else's screen. simgesture.Run's own job - the recovery lift - has
	// nothing to do here anyway, since a paste never puts a finger down.
	token, err := holder.Acquire(ctx, udid, pasteHoldFor(events))
	if err != nil {
		return result, err
	}
	defer holder.Release(ctx, udid, token)

	if err := pb.Write(ctx, udid, text); err != nil {
		return result, err
	}
	defer func() {
		if readErr != nil {
			// The previous contents were never known, so there is nothing
			// faithful to put back. Clearing is still better than leaving a
			// password on it.
			saved = ""
		}
		if restoreErr := pb.Write(ctx, udid, saved); restoreErr != nil {
			result.RestoreErr = restoreErr
			return
		}
		result.Restored = true
	}()

	before, err := driver.AX(ctx, udid)
	if err != nil {
		return result, fmt.Errorf("could not read the screen before pasting, so the paste could not be "+
			"proven and was not attempted: %w", err)
	}
	if _, err := driver.Perform(ctx, udid, events); err != nil {
		return result, &simgesture.FailedError{Action: "paste", Cause: err}
	}
	after, err := driver.AX(ctx, udid)
	if err != nil {
		return result, fmt.Errorf("the paste was sent but the screen could not be read back, so it could not "+
			"be proven - check the field with `ao sim ax`: %w", err)
	}
	return result, Verify(before, after, text)
}

// screenReads is the allowance the hold needs on top of the keystroke, for the
// two accessibility reads that prove the paste. The first read on a device can
// take a second or two while the translator attaches, and a hold that lapsed
// halfway through would hand the device away mid-proof.
const screenReads = 10 * time.Second

func pasteHoldFor(events []simbridge.Event) time.Duration {
	return simbridge.Duration(events) + screenReads + simgesture.HoldSlack
}

// Verify proves a paste landed by comparing the screen before and after.
//
// It checks LENGTH, not text, and deliberately so: a field may transform what
// it is given (an on-screen keyboard capitalises the first letter, a form field
// trims, a secure field replaces every character with a dot) and comparing the
// text would call those correct pastes failures. Length in RUNES is the one
// property that survives the transformation a secure field applies, which is
// the field this path exists for.
//
// The rule is "grew by AT LEAST the payload", not "exactly", and that asymmetry
// is deliberate. Under-delivery is the dangerous direction - nothing arrived, or
// only half of it - and that is what this catches. Growing by MORE means the
// text is there and the field added something of its own, which is common and
// harmless: iOS smart-insert puts a space in when pasting next to existing text
// (observed on a real device: a 12-character paste into a field holding one
// character produced 14), and a phone or card mask inserts its own punctuation.
// Failing those would make the command cry wolf on pastes that worked, and a
// check nobody can trust gets ignored - which would put us back where we
// started.
func Verify(before, after simbridge.Snapshot, text string) error {
	want := len([]rune(text))
	was := values(before)
	var changed []string

	for path, now := range values(after) {
		then, seen := was[path]
		if seen && then == now {
			continue
		}
		if len([]rune(now))-len([]rune(then)) >= want {
			return nil
		}
		changed = append(changed, fmt.Sprintf("%q went from %d to %d characters", path, len([]rune(then)), len([]rune(now))))
	}

	if len(changed) == 0 {
		return fmt.Errorf("%w: nothing on screen changed, so the text did not go anywhere. "+
			"Tap the field first so it has keyboard focus - and note that some apps refuse paste outright",
			ErrNotDelivered)
	}
	return fmt.Errorf("the paste was sent but no field on screen gained the %d character(s) it carried: %s. "+
		"Some of the text may be there and some not - a field with a length limit truncates silently - "+
		"so read it back with `ao sim ax` before trusting it",
		want, strings.Join(changed, "; "))
}

// values flattens a snapshot to every element's value, keyed by the path that
// identifies it in both trees.
func values(snapshot simbridge.Snapshot) map[string]string {
	out := map[string]string{}
	var walk func([]simbridge.Element)
	walk = func(elements []simbridge.Element) {
		for _, e := range elements {
			out[e.Path] = e.Value
			walk(e.Children)
		}
	}
	walk(snapshot.Elements)
	return out
}

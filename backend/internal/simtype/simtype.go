// Package simtype types text into a simulator's focused field through AO's
// XCTest runner (internal/simrunner), and proves on screen that it arrived.
//
// Why a third route. Key presses (internal/simbridge) send US key USAGES, and
// the guest turns each into whatever its input mode says that key means: on a
// guest set to Thai "lf86428" arrives as "สดคุภ/ค", and Thai text has no key to
// send at all. The pasteboard (internal/simpaste) carries characters, but an
// app that watches each keystroke sees one paste, and the text sits on the
// device's pasteboard meanwhile. XCTest's typeText sends CHARACTERS through the
// software keyboard, so what arrives does not depend on the Mac's or the
// simulator's keyboard language, and it reaches every process on screen.
//
// The same two rules as a paste hold here, for the same reasons:
//
//   - It runs inside the device's gesture hold, taken before anything is sent
//     and held until the proof is read, so the proof is about this device's
//     screen and nobody else's touch.
//   - It is PROVEN, never assumed. The screen is read before and after and
//     simpaste.Verify decides, so "typed" and "pasted" mean the same thing by
//     the same test - including in a secure field, which shows one dot per
//     character and nothing more.
package simtype

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpaste"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrunner"
)

// ErrUnavailable is a type that never reached the runner: it is not running,
// still starting, or failed. Nothing was typed, so another route may be taken
// - and the caller must say that it was.
var ErrUnavailable = errors.New("simtype: the XCTest runner could not type")

// ErrNoFocus is a screen with no element holding keyboard focus. Nothing was
// typed, and no other route would do better: the field has to be tapped.
var ErrNoFocus = errors.New("simtype: no element has keyboard focus")

// ErrUnsuitable is a field XCTest would type into wrongly. Nothing was typed,
// so another route may be taken - the pasteboard, which carries the exact
// characters whatever keyboard is up.
var ErrUnsuitable = errors.New("simtype: XCTest would not type this text faithfully into this field")

// UnsuitableError is ErrUnsuitable with the reason, for the caller to report.
type UnsuitableError struct{ Reason string }

func (e *UnsuitableError) Error() string { return e.Reason }

func (e *UnsuitableError) Unwrap() error { return ErrUnsuitable }

// secureFieldPlan says how to type text into field, or why not to.
//
// XCTest types through the software keyboard, and a SECURE field takes only
// what the keyboard on screen can type: with the Thai keyboard up, "abc" into
// a password field arrived as nothing and "123" as three dots, while the same
// letters into an ordinary field arrived in full; with an English keyboard up,
// "abc" arrived (nter web sign-in and a plain web page, iOS 26.3, 2026-10-02).
// Thai is worse: on the Thai keyboard "กขค" arrived as three dots but "รหัส",
// whose vowel mark combines, as two. So for a secure field:
//
//   - plain-ASCII text is typed on a Latin layout: as it is when one is up,
//     otherwise after the runner switches the keyboard to one with its globe
//     key, the way a person does, and back afterwards;
//   - anything else, and any field whose keyboard is not on screen (a hardware
//     keyboard, or the simulator believing in one after HID key presses), so
//     its layout cannot be seen, goes by the pasteboard, which a secure field
//     takes whole.
func secureFieldPlan(field simrunner.TypeField, keyboard *simbridge.KeyboardState, text string) (
	opts simrunner.TypeOptions, unsuitable string,
) {
	if field.Type != "SecureTextField" {
		return opts, ""
	}
	const secure = "the focused field is a secure field, which takes only what the keyboard on screen can type, and "
	for _, r := range text {
		if r > unicode.MaxASCII {
			return opts, secure + fmt.Sprintf("%q is not plain ASCII, which a keyboard does not type there reliably",
				string(r))
		}
	}
	if keyboard == nil {
		return opts, secure + "no software keyboard is on screen to show which layout that is"
	}
	if !keyboard.Latin {
		opts.Layout = simrunner.LayoutLatin
	}
	return opts, ""
}

// ErrRefused is XCTest refusing to type, with nothing on screen changed. No
// character arrived, so another route may be taken.
var ErrRefused = errors.New("simtype: XCTest could not type")

// Typist is the runner's typing route.
type Typist interface {
	// Focus says what a Type would go into, without typing.
	Focus(ctx context.Context, udid string) (simrunner.TypeAnswer, error)
	Type(ctx context.Context, udid, text string, opts simrunner.TypeOptions) (simrunner.TypeAnswer, error)
}

// Reader reads the whole screen.
type Reader interface {
	AX(ctx context.Context, udid string) (simbridge.Snapshot, error)
}

// Result is a type that was proven on screen.
type Result struct {
	// App is the bundle id of the application holding the field.
	App   string
	Field simrunner.TypeField
	// Keyboard: the software keyboard was on screen before typing.
	Keyboard bool
	TypingMs int
	// Landing is the field the text was proven to have reached, and on what
	// evidence. A caller is expected to REPORT it.
	Landing simpaste.Landing
	// Warning is what XCTest said while typing, when it complained about a
	// type the screen shows did land.
	Warning string
	// KeyboardSwitchedTo is the layout the software keyboard was switched to
	// for a secure field, and KeyboardRestored that it was switched back. A
	// caller reports both: the device's keyboard is shared state.
	KeyboardSwitchedTo string
	KeyboardRestored   bool
}

// NoFocusError is ErrNoFocus with what was looked at.
type NoFocusError struct {
	// Keyboard: the software keyboard was up all the same.
	Keyboard bool
	// Checked is every application looked in.
	Checked []string
}

func (e *NoFocusError) Error() string {
	where := "on screen"
	if len(e.Checked) > 0 {
		where = "in " + strings.Join(e.Checked, ", ")
	}
	keyboard := "the keyboard is not up either"
	if e.Keyboard {
		keyboard = "the keyboard is up, but nothing it would type into is focused"
	}
	return fmt.Sprintf("no element %s has keyboard focus (%s)", where, keyboard)
}

func (e *NoFocusError) Unwrap() error { return ErrNoFocus }

// ChunkRunes is the most characters one Run types. A gesture hold lasts at
// most a minute and has to cover the typing and both proof reads, and the
// daemon answers a request within a minute, so a longer text is typed in
// chunks (Chunks), each with its own hold and its own proof.
const ChunkRunes = 100

// PartialError is a long text whose first chunks were proven to have arrived
// before a later one failed. Sending the whole text again would type that
// part twice, which is why it is its own error.
type PartialError struct {
	Typed, Total int
	Cause        error
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("the first %d of %d characters were typed and proven on screen, then the rest failed - "+
		"type only what is missing, or the start goes in twice: %v", e.Typed, e.Total, e.Cause)
}

func (e *PartialError) Unwrap() error { return e.Cause }

// Chunks splits text into pieces of at most limit characters, never inside a
// character a person sees as one: a Thai consonant keeps its vowel and tone
// marks, an emoji its modifiers and joiners. A space near the limit is
// preferred, so a word is not split either. Only a single character longer
// than the limit makes a chunk longer than it.
func Chunks(text string, limit int) []string {
	runes := []rune(text)
	var out []string
	for len(runes) > limit {
		cut := limit
		for cut > 0 && !boundary(runes, cut) {
			cut--
		}
		for space := cut; space > cut-limit/4 && space > 0; space-- {
			if unicode.IsSpace(runes[space-1]) {
				cut = space
				break
			}
		}
		if cut == 0 {
			// One character longer than the limit: end the chunk after it
			// rather than inside it. A chunk a few runes over is still well
			// inside a hold; a split character is two wrong ones.
			cut = limit + 1
			for cut < len(runes) && !boundary(runes, cut) {
				cut++
			}
		}
		out = append(out, string(runes[:cut]))
		runes = runes[cut:]
	}
	if len(runes) > 0 || len(out) == 0 {
		out = append(out, string(runes))
	}
	return out
}

// boundary says whether a chunk may end before runes[i].
func boundary(runes []rune, i int) bool {
	if i <= 0 || i >= len(runes) {
		return true
	}
	next, prev := runes[i], runes[i-1]
	const zwj, vs16 = '\u200d', '\ufe0f'
	switch {
	case prev == zwj || next == zwj || next == vs16:
		return false
	case unicode.Is(unicode.Mn, next) || unicode.Is(unicode.Me, next) || unicode.Is(unicode.Mc, next):
		return false
	case next >= 0x1F3FB && next <= 0x1F3FF: // emoji skin-tone modifier
		return false
	}
	return true
}

// Run takes the hold, reads the screen, types, reads it again and proves the
// text arrived. text is one chunk: at most ChunkRunes characters.
//
// The order is the same as a paste's, for the same reason: the hold first, so
// a device mid-gesture is refused before anything is sent; the proof inside
// it, so another command cannot change the screen between the type and the
// read that judges it.
func Run(
	ctx context.Context,
	holder simgesture.Holder,
	reader Reader,
	typist Typist,
	udid, text string,
) (result Result, err error) {
	if text == "" {
		return result, errors.New("nothing to type")
	}
	if len(Chunks(text, ChunkRunes)) > 1 {
		return result, fmt.Errorf("%d characters is more than one type carries (%d); split it with Chunks",
			utf8.RuneCountInString(text), ChunkRunes)
	}
	token, err := holder.Acquire(ctx, udid, HoldFor(text))
	if err != nil {
		return result, err
	}
	// err is a named return: Performed is whatever the function is about to
	// say, so a type that could not be proven is never recorded as one.
	defer func() { holder.Release(ctx, udid, token, simgesture.Outcome{Performed: err == nil}) }()

	before, err := reader.AX(ctx, udid)
	if err != nil {
		return result, fmt.Errorf("could not read the screen before typing, so the text could not be "+
			"proven and was not typed: %w", err)
	}

	// Look before typing: a field that cannot take this text through the
	// keyboard on screen must not be typed at, because what it drops cannot
	// be told apart afterwards from what it took - and a half-typed password
	// cannot be retried.
	focus, err := typist.Focus(ctx, udid)
	switch {
	case errors.Is(err, simrunner.ErrNotReady):
		return result, fmt.Errorf("%w: %w", ErrUnavailable, err)
	case err != nil:
		return result, fmt.Errorf("%w: could not ask the XCTest runner what has focus: %w", ErrUnavailable, err)
	case focus.Error != nil && focus.Error.Code == simrunner.TypeNoFocus:
		return result, &NoFocusError{Keyboard: focus.Keyboard, Checked: focus.Checked}
	case focus.Error != nil:
		return result, fmt.Errorf("%w: %s", ErrUnavailable, focus.Error.Message)
	}
	var opts simrunner.TypeOptions
	if focus.Field != nil {
		var reason string
		opts, reason = secureFieldPlan(*focus.Field, before.Keyboard, text)
		if reason != "" {
			return result, &UnsuitableError{Reason: reason}
		}
	}

	answer, typeErr := typist.Type(ctx, udid, text, opts)
	if errors.Is(typeErr, simrunner.ErrNotReady) {
		return result, fmt.Errorf("%w: %w", ErrUnavailable, typeErr)
	}
	result.App, result.Keyboard, result.TypingMs = answer.App, answer.Keyboard, answer.TypingMs
	result.KeyboardSwitchedTo, result.KeyboardRestored = answer.KeyboardSwitchedTo, answer.KeyboardRestored
	if answer.Field != nil {
		result.Field = *answer.Field
	}
	if typeErr == nil && answer.App != "" && focus.App != "" && answer.App != focus.App {
		// Focus moved between the look and the type. Not refused - the proof
		// below judges what arrived - but said, since the look was about
		// another field.
		result.Warning = fmt.Sprintf("focus moved from %s to %s while typing", focus.App, answer.App)
	}
	if typeErr == nil && answer.Error != nil {
		switch answer.Error.Code {
		case simrunner.TypeNoFocus:
			// The runner looked before it typed and typed nothing.
			return result, &NoFocusError{Keyboard: answer.Keyboard, Checked: answer.Checked}
		case simrunner.TypeNoLayout:
			return result, &UnsuitableError{Reason: "the focused field is a secure field, which takes only what the " +
				"keyboard on screen can type, and " + answer.Error.Message}
		case simrunner.TypeFailed:
		default:
			return result, fmt.Errorf("the XCTest runner refused the text: %s", answer.Error.Message)
		}
	}

	// Typed, failed partway or lost in transit: in every case some of the text
	// may be in the field, and only the screen can say.
	after, err := reader.AX(ctx, udid)
	if err != nil {
		return result, fmt.Errorf("the text was sent but the screen could not be read back, so it could not "+
			"be proven - check the field with `ao sim ax`: %w", err)
	}
	landing, verifyErr := simpaste.Verify(before, after, text)
	complaint := ""
	switch {
	case typeErr != nil:
		complaint = typeErr.Error()
	case answer.Error != nil:
		complaint = answer.Error.Message
	}
	if verifyErr == nil {
		result.Landing = landing
		if complaint != "" {
			result.Warning = complaint
		}
		return result, nil
	}
	if complaint != "" && errors.Is(verifyErr, simpaste.ErrNotDelivered) {
		// XCTest said it could not type, and nothing changed: nothing arrived.
		return result, fmt.Errorf("%w: %s", ErrRefused, complaint)
	}
	if complaint != "" {
		return result, fmt.Errorf("%w (XCTest also reported: %s)", verifyErr, complaint)
	}
	return result, verifyErr
}

// HoldFor is the gesture hold one chunk needs: the typing itself, and the two
// screen reads that prove it. A runner read is 0.05-0.3 s, so each read is
// allowed less than a bridge read (simgesture.ScreenRead) - which is what
// keeps a full chunk inside the one-minute ceiling.
func HoldFor(text string) time.Duration {
	return simrunner.TypeTimeout(text) + 2*proofRead + simgesture.HoldSlack
}

const proofRead = 5 * time.Second

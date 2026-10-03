// Package simpaste puts text into a simulator's focused field through the
// guest's pasteboard instead of through its keyboard.
//
// It exists because the keyboard cannot be trusted to deliver characters. The
// HID path sends US key usages and the guest turns them into whatever its own
// input mode says they mean, so on a guest set to Thai "lf86428" arrives as
// "สดคุภ/ค" (see internal/simkeyboard), and XCTest types into a secure field
// only what the keyboard on screen can type. The pasteboard sidesteps that
// entirely: the text is transferred as text, including into a secure field.
//
// The paste itself is a gesture, and there are two (Paster). The default holds
// the field and taps Paste in its edit menu (MenuPaster): touches only.
// Command-V (KeyPaster) is the fallback, because it is a hardware key press,
// and a hardware key press minimizes the software keyboard for every field
// tapped after it (see simbridge.ShowKeyboard) - so that route shows the
// keyboard again afterwards, and says so.
//
// Two properties are not negotiable, and both come from the bug this whole
// change is about.
//
// The paste is PROVEN, never assumed. An app can refuse paste, and a field that
// never took focus swallows it silently; reporting success in either case would
// be the same "reports success, wrong data" failure in a new costume. So the
// screen is read before and after, and a paste that cannot be shown to have
// landed is an error - one that says what WAS seen rather than claiming nothing
// arrived. The proof is the text itself: a field on screen now holds a copy of
// it that was not there before (see Verify). A secure field is exactly where
// this matters and exactly where it still works: it will not report its text,
// but it reports one dot per character, and that count is real evidence.
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
	"math"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrunner"
)

// ErrNotDelivered is a paste that changed nothing on screen. It is separated
// from "the wrong amount arrived" because the two need different advice: this
// one means the field never had focus or the app refuses paste, and nothing is
// in the field to clean up.
var ErrNotDelivered = errors.New("simpaste: the paste did not reach a field")

// ErrNotProven is a paste whose text could not be found on screen afterwards,
// on a screen that DID change. It is deliberately not the same error as
// ErrNotDelivered: this one means the text may well be in the field - a field
// that reports its value only once it loses focus looks exactly like a field
// with a length limit that took half of it - so the answer is to read the field
// back, never to send the text a second time.
var ErrNotProven = errors.New("simpaste: the paste could not be proven on screen")

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
	// Landing is the field the text was proven to have reached, and on what
	// evidence. It is set only on the path where Run returns no error, and a
	// caller is expected to REPORT it: "pasted" that does not say where the
	// text went is how a command ends up believed about work it never did.
	Landing Landing
	// Pasted is how the paste was performed, which a caller reports too: a
	// Command-V pressed a hardware key, and the keyboard it minimized is part
	// of what the command did to the device.
	Pasted Pasted
	// Warning is what the gesture complained about while the screen shows
	// the text did land.
	Warning string
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
//
// LC_CTYPE is not decoration. `simctl pbcopy` decodes stdin using the
// environment's character encoding, and with no locale set it reads the bytes
// as MacRoman: "สวัสดี" reaches the guest as "‡∏™‡∏ß‡∏±‡∏™‡∏î‡∏µ". A terminal
// has a UTF-8 locale and an agent's process usually does not, so this path -
// the one that exists BECAUSE the keyboard cannot carry non-ASCII - corrupted
// exactly the text it was reached for, and reported success (confirmed on a
// device, iOS 26.3). The locale is pinned here rather than left to the caller
// because the caller is a daemon as often as a shell.
func (s Simctl) Write(ctx context.Context, udid, text string) error {
	script := fmt.Sprintf("printf '%%s' %s | LC_CTYPE=UTF-8 %s simctl pbcopy %s",
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

// Reader reads the whole screen: the before and after reads that prove a
// paste. simbridge.Driver is one.
type Reader interface {
	AX(ctx context.Context, udid string) (simbridge.Snapshot, error)
}

// Paster performs the gesture that pastes, once the text is on the
// pasteboard. There are two, and the difference between them is the bug that
// made them two: MenuPaster presses no key, KeyPaster presses Command-V - a
// hardware key press, which leaves the software keyboard minimized for every
// field after it (see simbridge.ShowKeyboard).
type Paster interface {
	// Paste pastes into whatever has keyboard focus. before is the screen as
	// it was read just now. A Paster that changes the screen before it pastes
	// returns the read the proof has to start from in Pasted.Before.
	//
	// An error wrapping ErrNotPasted means nothing was pasted and the screen
	// was not proven - another Paster may be tried. Any other error is a
	// paste that may have landed, and the screen decides.
	Paste(ctx context.Context, udid string, before simbridge.Snapshot) (Pasted, error)
	// Hold is how long the gesture may take, for the hold that covers it.
	Hold() time.Duration
}

// Pasted is how a paste was performed.
type Pasted struct {
	Via Via
	// MenuItem is the edit menu item tapped, in the device's language.
	MenuItem string
	// App is the bundle id of the application holding the field, when known.
	App string
	// Before, when set, is the read the proof starts from instead of the one
	// handed to Paste.
	Before *simbridge.Snapshot
	// Keyboard is what was done about the software keyboard after a paste
	// that pressed keys.
	Keyboard *simgesture.Keyboard
}

// Via is how a paste was performed.
type Via string

const (
	// ViaEditMenu is the field held and Paste tapped in its edit menu,
	// through the XCTest runner. Touches only.
	ViaEditMenu Via = "edit-menu"
	// ViaCommandV is Command-V, as hardware key presses.
	ViaCommandV Via = "command-v"
)

// ErrNotPasted is a Paster that pasted nothing: it was not available, no
// field had focus, or the field offered no Paste. Nothing arrived, so another
// route may be taken.
var ErrNotPasted = errors.New("simpaste: nothing was pasted")

// NotPastedError is ErrNotPasted with why, for the caller to report.
type NotPastedError struct {
	// Code is the reason, one of the NotPasted* codes.
	Code   string
	Reason string
	// Menu is what the edit menu offered, for NotPastedNoItem.
	Menu []string
}

func (e *NotPastedError) Error() string { return e.Reason }

func (e *NotPastedError) Unwrap() error { return ErrNotPasted }

// The NotPastedError codes.
const (
	// NotPastedUnavailable: the route could not run at all.
	NotPastedUnavailable = "unavailable"
	// NotPastedNoFocus: no element has keyboard focus. No other route does
	// better - the field has to be tapped.
	NotPastedNoFocus = "no_focus"
	// NotPastedNoItem: the field was held and its edit menu had no Paste.
	NotPastedNoItem = "no_paste_item"
)

// Run delivers text through the pasteboard and proves it arrived.
//
// The order matters on every line. The hold is taken first, so a device that is
// mid-gesture is refused before the pasteboard is touched at all. The restore
// is deferred immediately after the payload is written, so no path out - not a
// failed gesture, not a failed read, not a panic - leaves the payload behind.
func Run(
	ctx context.Context,
	holder simgesture.Holder,
	reader Reader,
	paster Paster,
	pb Pasteboard,
	udid, text string,
) (result Result, err error) {
	// Named returns, because the deferred restore below records its outcome ON
	// the result after the return value has been chosen.

	// Read first: there is no point disturbing the pasteboard for a gesture
	// that is about to be refused, and this is also the value we owe back.
	saved, readErr := pb.Read(ctx, udid)

	// This takes the hold itself rather than delegating to simgesture.Run,
	// because a paste's hold has to cover MORE than the gesture: the
	// pasteboard write before it and the two screen reads that prove it. A hold
	// that only spanned the gesture would let another command take the device
	// between the write and the proof, and then the proof would be about
	// somebody else's screen.
	token, err := holder.Acquire(ctx, udid, paster.Hold()+2*screenReads+simgesture.HoldSlack)
	if err != nil {
		return result, err
	}
	// err is a named return, so by the time this runs it holds whatever the
	// function is actually about to return - nil only when the paste was
	// written, sent and verified on screen. That is what "performed" means
	// here: a write that failed, a gesture that failed, or a paste that could
	// not be verified must not be recorded as one that happened.
	defer func() { holder.Release(ctx, udid, token, simgesture.Outcome{Performed: err == nil}) }()

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

	before, err := reader.AX(ctx, udid)
	if err != nil {
		return result, fmt.Errorf("could not read the screen before pasting, so the paste could not be "+
			"proven and was not attempted: %w", err)
	}
	pasted, pasteErr := paster.Paste(ctx, udid, before)
	result.Pasted = pasted
	if errors.Is(pasteErr, ErrNotPasted) {
		return result, pasteErr
	}
	if pasted.Before != nil {
		before = *pasted.Before
	}
	after, err := reader.AX(ctx, udid)
	if err != nil {
		return result, fmt.Errorf("the paste was sent but the screen could not be read back, so it could not "+
			"be proven - check the field with `ao sim ax`: %w", err)
	}
	landing, verifyErr := Verify(before, after, text)
	result.Landing = landing
	switch {
	case verifyErr == nil:
		if pasteErr != nil {
			result.Warning = pasteErr.Error()
		}
		return result, nil
	case pasteErr != nil && errors.Is(verifyErr, ErrNotDelivered):
		// The gesture failed and nothing changed: nothing arrived, and the
		// gesture's own complaint is the better reason.
		return result, fmt.Errorf("%w: %w", ErrNotDelivered, pasteErr)
	case pasteErr != nil:
		return result, fmt.Errorf("%w (the paste gesture also reported: %w)", verifyErr, pasteErr)
	}
	return result, verifyErr
}

// screenReads is the allowance the hold needs for each accessibility read
// that proves the paste. The first read on a device can take a second or two
// while the translator attaches, and a hold that lapsed halfway through would
// hand the device away mid-proof.
const screenReads = 10 * time.Second

// keyboardSettle is how long the software keyboard is given to go away after
// the waking key press, before the screen is read again.
const keyboardSettle = 500 * time.Millisecond

// MenuFocus is the XCTest runner's look at what has keyboard focus, which
// also says how the edit menu spells Paste in the device's language
// (internal/simrunner).
type MenuFocus interface {
	Focus(ctx context.Context, udid string) (simrunner.TypeAnswer, error)
}

// MenuPaster pastes the way a person does: it holds the focused field and
// taps Paste in the edit menu that comes up.
//
// It presses no key. Command-V is a hardware key press, and a hardware key
// press tells iOS a hardware keyboard is attached: the software keyboard is
// minimized, and every field tapped afterwards comes up without one - the
// next tap, the next recording and the next script on the device all met a
// keyboard that was not there. The edit menu is also the paste that lands in
// a web sign-in sheet, where Command-V did not.
//
// The touches are the bridge's, as `ao sim tap`'s are; the XCTest runner only
// reads - which field has focus, how the menu spells Paste, where the item
// came up. XCTest's own touches wait for the app to go idle first, and a web
// sign-in sheet was seen never to: each touch then held the runner for a
// minute, and every read behind it timed out.
type MenuPaster struct {
	Runner MenuFocus
	// Reader reads the screen through the runner, which is what sees the
	// focused field and the edit menu.
	Reader Reader
	Driver simbridge.Driver
}

// pressHold is how long the field is held. iOS opens the edit menu on
// release after a press of about half a second.
const pressHold = 800 * time.Millisecond

// menuWait is how long the edit menu is given to come up after the release,
// and menuPoll how often the screen is read meanwhile.
const (
	menuWait = 2500 * time.Millisecond
	menuPoll = 250 * time.Millisecond
)

// Hold covers the focus look, the press, the wait for the menu and the tap.
func (m MenuPaster) Hold() time.Duration {
	return screenReads/2 + pressHold + menuWait + screenReads/2 + time.Second
}

// Paste holds the focused field and taps Paste in its edit menu.
func (m MenuPaster) Paste(ctx context.Context, udid string, before simbridge.Snapshot) (Pasted, error) {
	pasted := Pasted{Via: ViaEditMenu}
	focus, err := m.Runner.Focus(ctx, udid)
	pasted.App = focus.App
	switch {
	case errors.Is(err, simrunner.ErrNotReady):
		return pasted, &NotPastedError{Code: NotPastedUnavailable,
			Reason: "the XCTest runner is not ready to find the field: " + err.Error()}
	case err != nil:
		return pasted, &NotPastedError{Code: NotPastedUnavailable,
			Reason: "could not ask the XCTest runner what has focus: " + err.Error()}
	case focus.Error != nil && focus.Error.Code == simrunner.TypeNoFocus:
		return pasted, &NotPastedError{Code: NotPastedNoFocus, Reason: focus.Error.Message}
	case focus.Error != nil:
		return pasted, &NotPastedError{Code: NotPastedUnavailable,
			Reason: "the XCTest runner could not find the field: " + focus.Error.Message}
	}
	field, ok := onlyFocused(flatten(before))
	if !ok {
		return pasted, &NotPastedError{Code: NotPastedNoFocus,
			Reason: "no single element on screen shows keyboard focus, so there is no field to hold"}
	}
	press, err := simbridge.Press(PressPoint(field, before.Screen), pressHold)
	if err != nil {
		return pasted, &NotPastedError{Code: NotPastedUnavailable, Reason: err.Error()}
	}
	if _, err := m.Driver.Perform(ctx, udid, press); err != nil {
		// Held, at most: a long press selects or moves the cursor, it puts
		// no text anywhere.
		return pasted, &NotPastedError{Code: NotPastedUnavailable, Reason: "could not hold the field: " + err.Error()}
	}
	labels := focus.PasteLabels
	if len(labels) == 0 {
		labels = []string{"Paste"}
	}
	item, menu, err := m.menuItem(ctx, udid, labels)
	if err != nil {
		return pasted, &NotPastedError{Code: NotPastedUnavailable, Reason: err.Error()}
	}
	if item == nil {
		shown := "no edit menu came up"
		if len(menu) > 0 {
			shown = "the edit menu offered " + quoteAll(menu, ", ")
		}
		// The menu is left as it is: closing it is another touch, and the
		// caller's next touch closes it anyway.
		return pasted, &NotPastedError{Code: NotPastedNoItem, Menu: menu,
			Reason: shown + " and no " + quoteAll(labels, " or ") + " - the field may refuse paste, or draw a menu of its own"}
	}
	pasted.MenuItem = item.Label
	tap, err := simbridge.Tap(*item.Tap)
	if err != nil {
		return pasted, &NotPastedError{Code: NotPastedUnavailable, Reason: err.Error()}
	}
	if _, err := m.Driver.Perform(ctx, udid, tap); err != nil {
		// The tap may or may not have reached the item: the screen decides.
		return pasted, fmt.Errorf("could not tap %q: %w", item.Label, err)
	}
	return pasted, nil
}

// menuItem waits for the edit menu's Paste item, and for it to stop moving:
// the menu animates in, and a tap sent at the first point it was read at
// landed while it was still on its way and pasted nothing (nter web sign-in,
// iOS 26.3). So the item is tapped once two reads in a row put it at the same
// point. It answers nil with what the menu offered instead when no Paste came
// up in time.
func (m MenuPaster) menuItem(ctx context.Context, udid string, labels []string) (*simbridge.Element, []string, error) {
	deadline := time.Now().Add(menuWait)
	var menu []string
	var seen *simbridge.Point
	for {
		snap, err := m.Reader.AX(ctx, udid)
		if err != nil {
			return nil, nil, fmt.Errorf("could not read the screen for the edit menu: %w", err)
		}
		menu = menu[:0]
		var found *simbridge.Element
		for _, e := range flatten(snap) {
			if e.Type != "MenuItem" || e.OffScreen || e.Tap == nil {
				continue
			}
			if found == nil && slices.Contains(labels, e.Label) {
				found = &e
				continue
			}
			if e.Label != "" {
				menu = append(menu, e.Label)
			}
		}
		if found != nil {
			if seen != nil && math.Abs(seen.X-found.Tap.X) < 0.002 && math.Abs(seen.Y-found.Tap.Y) < 0.002 {
				return found, nil, nil
			}
			seen = found.Tap
		}
		if time.Now().After(deadline) {
			if found != nil {
				// Up, but still moving when time ran out: tap where it was
				// last seen rather than call a menu with Paste in it one
				// without.
				return found, nil, nil
			}
			return nil, menu, nil
		}
		select {
		case <-time.After(menuPoll):
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
}

// PressPoint is where to hold a field so the paste goes at the END of what
// it holds, as Command-V after a tap would: the press moves the cursor to
// where it lands, and the end of a field's text is towards its right edge (a
// single line) or its bottom-right corner (a text view). A button inside the
// field - a clear button, a show-password eye - is kept clear of, because a
// press that ends on a button is a tap on it.
func PressPoint(field simbridge.Element, screen simbridge.Size) simbridge.Point {
	const inset = 12.0
	left, top := field.Frame.X, field.Frame.Y
	right, bottom := left+field.Frame.Width, top+field.Frame.Height
	if screen.Width > 0 && screen.Height > 0 {
		left, top = max(left, 0), max(top, 0)
		right, bottom = min(right, screen.Width), min(bottom, screen.Height)
	}
	midX := (left + right) / 2
	edge := right
	var walk func([]simbridge.Element)
	walk = func(elements []simbridge.Element) {
		for _, e := range elements {
			centre := e.Frame.X + e.Frame.Width/2
			if e.Type == "Button" && centre > midX && e.Frame.X < edge && e.Frame.X > left {
				edge = e.Frame.X
			}
			walk(e.Children)
		}
	}
	walk(field.Children)
	x := max(left+min(inset, (right-left)/2), edge-inset)
	y := (top + bottom) / 2
	if field.Type == "TextView" && bottom-top > 3*inset {
		y = bottom - inset
	}
	if screen.Width <= 0 || screen.Height <= 0 {
		return simbridge.Point{X: x, Y: y}
	}
	return simbridge.Point{X: x / screen.Width, Y: y / screen.Height}
}

func quoteAll(items []string, sep string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = fmt.Sprintf("%q", item)
	}
	return strings.Join(quoted, sep)
}

// KeyPaster pastes with Command-V, as hardware key presses through the
// accessibility bridge. It is the route when the XCTest runner cannot paste,
// and the Device tab's, whose person is typing on a hardware keyboard anyway.
type KeyPaster struct {
	Driver simbridge.Driver
	// ShowKeyboard brings the software keyboard back after the Command-V,
	// which minimizes it for every field after (see simbridge.ShowKeyboard).
	ShowKeyboard bool
}

// Hold covers the wake, Command-V and, when asked, the keyboard check.
func (k KeyPaster) Hold() time.Duration {
	// The read after waking the keyboard is allowed less than a proof read:
	// the hold has a one-minute ceiling, and the two proof reads come first.
	hold := simbridge.Duration(simbridge.WakeKeyboard()) + simbridge.Duration(simbridge.Paste()) +
		screenReads/2 + keyboardSettle
	if k.ShowKeyboard {
		hold += simgesture.KeyboardCheck
	}
	return hold
}

// Paste presses Command-V, waking a software keyboard first, and shows the
// keyboard again afterwards when asked.
func (k KeyPaster) Paste(ctx context.Context, udid string, before simbridge.Snapshot) (Pasted, error) {
	pasted := Pasted{Via: ViaCommandV}
	if before.Keyboard != nil {
		// The software keyboard is up, and the first key event would be spent
		// hiding it (see simbridge.WakeKeyboard) - the Command-V would be lost.
		// Spend a bare Command on it instead, and read the screen it leaves.
		if _, err := k.Driver.Perform(ctx, udid, simbridge.WakeKeyboard()); err != nil {
			return pasted, &simgesture.FailedError{Action: "paste", Cause: err}
		}
		select {
		case <-time.After(keyboardSettle):
		case <-ctx.Done():
			return pasted, &NotPastedError{Code: NotPastedUnavailable, Reason: ctx.Err().Error()}
		}
		read, err := k.Driver.AX(ctx, udid)
		if err != nil {
			return pasted, &NotPastedError{Code: NotPastedUnavailable, Reason: "could not read the screen before " +
				"pasting, so the paste could not be proven and was not attempted: " + err.Error()}
		}
		pasted.Before = &read
	}
	events := simbridge.Paste()
	if _, err := k.Driver.Perform(ctx, udid, events); err != nil {
		return pasted, &simgesture.FailedError{Action: "paste", Cause: err}
	}
	if k.ShowKeyboard {
		pasted.Keyboard = simgesture.ShowKeyboardAfter(ctx, k.Driver, udid, events)
	}
	return pasted, nil
}

// Verify proves a paste landed by finding the text on screen afterwards.
//
// What counts as proof is the whole question here, and the rule it used to
// apply - "some element's value grew by at least the payload's length" - was
// wrong in both directions.
//
// It said FAILURE for work it had done. An empty field reports its PLACEHOLDER
// as its accessibility value: an untouched login field reads
// "example@email.com", and a correct paste of "qa@test.io" takes it from 17
// characters to 10. A replacement can only grow a field by the payload's length
// when the field was empty AND had no placeholder, so on a real login screen
// that rule failed every time, whether the text pasted was shorter, longer or
// the same length (all three confirmed on a device).
//
// It also said SUCCESS for work it had not done. An element's path is its
// POSITION in the tree, so a keyboard coming up or a suggestion list appearing
// renumbers everything after it - and an element whose path was not in the
// previous read was compared against the empty string, which any newly arrived
// label five characters long satisfies. Observed on a device: a paste reported
// as delivered while the field it was aimed at still read its placeholder, on a
// screen whose elements had renumbered under it. That is the same failure
// `ao sim tap --label`
// and `ao sim run` were fixed for - a command reporting success for work it had
// not done - arriving here by the back door.
//
// So identity by path is not used for the proof at all. The screen is asked one
// question - does it hold a copy of this text that it did not hold before? - and
// the evidence is counted across the whole tree, which is immune to reflow. Four
// kinds of evidence are accepted, strongest first, because a field is allowed to
// transform what it is given:
//
//	exact        a field now contains the text, character for character
//	case         ... apart from capitalisation an on-screen keyboard applied
//	masked       a secure field shows at least one more dot per character sent
//	reformatted  ... with punctuation of its own around it, as a card or phone
//	             mask inserts (the letters and digits are there, in order)
//
// Under-delivery is what this exists to catch, and it still catches it: a field
// with a five-character limit given eighteen characters holds none of those
// copies, and is reported. What changed is what the report SAYS - it names the
// field, quotes what it reads now, and tells the caller to read it back rather
// than send the text again, because "could not be proven" and "did not arrive"
// are different facts and only the second one is safe to retry.
func Verify(before, after simbridge.Snapshot, text string) (Landing, error) {
	was, now := flatten(before), flatten(after)
	want := len([]rune(text))
	// The field with keyboard focus, when the reader knows it, is where the
	// text went: its own value before and after is a sharper witness than a
	// count across the screen, which cannot tell one "a" from the "a" in every
	// label around it.
	for _, rule := range rules {
		if landing, ok := rule.gainedInFocused(was, now, text); ok {
			return landing, nil
		}
	}
	for _, rule := range rules {
		if landing, ok := rule.gained(was, now, text); ok {
			return landing, nil
		}
	}

	changed := changes(was, now)
	if len(changed) == 0 {
		// Route-neutral: `ao sim type` proves its XCTest typing with this too,
		// and each caller adds the advice its own route needs.
		return Landing{}, fmt.Errorf("%w: nothing on screen changed, so the text did not go anywhere. "+
			"Tap the field first so it has keyboard focus", ErrNotDelivered)
	}
	return Landing{}, fmt.Errorf("%w: it was sent, but nothing on screen can be shown to hold the %d "+
		"character(s) it carried. What changed: %s. Some of it may be there and some not - a field with a "+
		"length limit truncates silently - and a field that reports its text only once it loses focus shows "+
		"nothing either way. Read it back with `ao sim ax` rather than sending the text again",
		ErrNotProven, want, strings.Join(changed, "; "))
}

// Evidence is the basis on which a paste was called delivered. It is reported
// rather than kept private: "the field now reads what you sent" and "a secure
// field grew by eight dots" are both proof, but they are not the same promise,
// and a caller deciding whether to trust the field needs to know which it has.
type Evidence string

const (
	// EvidenceExact is a field that now contains the text, character for
	// character. The strongest thing this package can say.
	EvidenceExact Evidence = "exact"
	// EvidenceCase is the text apart from capitalisation, which an on-screen
	// keyboard applies to the first letter on its own.
	EvidenceCase Evidence = "case"
	// EvidenceMasked is a secure field showing one dot per character. It never
	// reports its text, so its dots are the only evidence that exists.
	EvidenceMasked Evidence = "masked"
	// EvidenceReformatted is the text's letters and digits, in order, with
	// punctuation of the field's own around them - a card or phone mask.
	EvidenceReformatted Evidence = "reformatted"
)

// Landing is where the text was proven to have arrived, and how.
type Landing struct {
	// Field is the element's accessibility label, empty when it has none.
	Field string
	// Path is its path in `ao sim ax`, which every element has. It is what a
	// caller types to go and look at the field itself.
	Path string
	// Shown is what that element reads now - dots, for a secure field.
	Shown string
	How   Evidence
}

// String says where the text went, in the words a caller can act on.
func (l Landing) String() string {
	if l.Path == "" {
		return ""
	}
	name := "[" + l.Path + "]"
	if l.Field != "" {
		name = fmt.Sprintf("%q %s", l.Field, name)
	}
	switch l.How {
	case EvidenceMasked:
		return fmt.Sprintf("the secure field %s shows %d dots, one per character - it never reports its text, "+
			"so that count is the whole proof", name, len([]rune(l.Shown)))
	case EvidenceCase:
		return fmt.Sprintf("the field %s now reads %q, which is the text with its capitalisation changed",
			name, elide(l.Shown))
	case EvidenceReformatted:
		return fmt.Sprintf("the field %s now reads %q, which is the text with formatting of the field's own "+
			"around it", name, elide(l.Shown))
	default:
		return fmt.Sprintf("the field %s now reads %q", name, elide(l.Shown))
	}
}

// rules are the kinds of evidence, strongest first. The order is what a caller
// is told about, so a paste that can be proven exactly is never reported as a
// reformatting.
var rules = []rule{
	{how: EvidenceExact, normalize: func(s string) string { return s }},
	{how: EvidenceCase, normalize: strings.ToLower},
	{how: EvidenceMasked},
	{how: EvidenceReformatted, normalize: lettersAndDigits},
}

// rule is one kind of evidence. A nil normalize is the secure-field rule, which
// counts dots rather than text.
type rule struct {
	how       Evidence
	normalize func(string) string
}

func (r rule) gained(before, after []simbridge.Element, text string) (Landing, bool) {
	if r.normalize == nil {
		return gainedDots(before, after, len([]rune(text)))
	}
	needle := r.normalize(text)
	if needle == "" {
		// Nothing to look for: a payload that is entirely punctuation has no
		// letters or digits, and "every value contains the empty string" would
		// prove every paste.
		return Landing{}, false
	}
	if copies(after, needle, r.normalize) <= copies(before, needle, r.normalize) {
		return Landing{}, false
	}
	// The screen gained a copy, so one of these elements holds it. Preferring
	// one whose value changed only picks WHICH to name when several match; the
	// verdict was already decided by the count.
	was := byPath(before)
	var fallback *simbridge.Element
	for i, e := range after {
		if !strings.Contains(r.normalize(e.Value), needle) {
			continue
		}
		if then, seen := was[e.Path]; !seen || then.Value != e.Value {
			return landing(e, r.how), true
		}
		if fallback == nil {
			fallback = &after[i]
		}
	}
	if fallback != nil {
		return landing(*fallback, r.how), true
	}
	return Landing{}, false
}

// gainedInFocused is the rule applied to the focused field alone: the one
// element with keyboard focus after, and the same field (type, label and id)
// before. A screen without exactly one focused element, or whose field cannot
// be found in the earlier read, proves nothing here and is left to the
// screen-wide count.
func (r rule) gainedInFocused(before, after []simbridge.Element, text string) (Landing, bool) {
	field, ok := onlyFocused(after)
	if !ok {
		return Landing{}, false
	}
	prior, ok := sameField(before, field)
	if !ok {
		return Landing{}, false
	}
	was, now := typedValue(prior, field), typedValue(field, field)
	if r.normalize == nil {
		want := len([]rune(text))
		if want == 0 || !allDots(now) || len([]rune(now))-dotsIn(was) < want {
			return Landing{}, false
		}
		return landing(field, EvidenceMasked), true
	}
	needle := r.normalize(text)
	if needle == "" || strings.Count(r.normalize(now), needle) <= strings.Count(r.normalize(was), needle) {
		return Landing{}, false
	}
	return landing(field, r.how), true
}

func onlyFocused(elements []simbridge.Element) (simbridge.Element, bool) {
	var found []simbridge.Element
	for _, e := range elements {
		if e.Focused {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		return simbridge.Element{}, false
	}
	return found[0], true
}

// sameField finds field in an earlier read: by focus first, then by what it
// is. A path is a position and moves when the keyboard comes up, so it is not
// used; a match that is not unique is no match.
func sameField(elements []simbridge.Element, field simbridge.Element) (simbridge.Element, bool) {
	same := func(e simbridge.Element) bool {
		return e.Type == field.Type && e.Label == field.Label && e.ID == field.ID
	}
	for _, pick := range []func(simbridge.Element) bool{
		func(e simbridge.Element) bool { return e.Focused && same(e) },
		same,
	} {
		var found []simbridge.Element
		for _, e := range elements {
			if pick(e) {
				found = append(found, e)
			}
		}
		if len(found) == 1 {
			return found[0], true
		}
	}
	return simbridge.Element{}, false
}

// typedValue is what a field holds that somebody typed, without its hint. An
// empty field reports its placeholder as its value - with the placeholder
// itself reported apart only once the field has text - so a value equal to
// the field's placeholder in either read is no text at all.
func typedValue(e, field simbridge.Element) string {
	switch e.Value {
	case "", e.Placeholder, field.Placeholder:
		return ""
	}
	return e.Value
}

func dotsIn(value string) int {
	if !allDots(value) {
		return 0
	}
	return len([]rune(value))
}

// dots are what a secure field shows instead of its text. iOS uses U+2022; the
// others are here because an app is free to draw its own mask and some do.
const dots = "•●∙·*"

// gainedDots proves a paste into a field that will not report its text. The
// count is taken across every all-dot value on screen rather than per element,
// for the same reason the text rules count copies: the tree reflows.
//
// "At least" rather than "exactly", like the text rules: iOS smart-insert adds
// a space when pasting next to existing text, and in a secure field that space
// is one more dot.
func gainedDots(before, after []simbridge.Element, want int) (Landing, bool) {
	if want == 0 || masked(after)-masked(before) < want {
		return Landing{}, false
	}
	was := byPath(before)
	best := -1
	for i, e := range after {
		if !allDots(e.Value) {
			continue
		}
		grew := len([]rune(e.Value))
		if then, seen := was[e.Path]; seen && allDots(then.Value) {
			grew -= len([]rune(then.Value))
		}
		if best == -1 || grew > len([]rune(after[best].Value)) {
			best = i
		}
	}
	if best == -1 {
		return Landing{}, false
	}
	return landing(after[best], EvidenceMasked), true
}

// masked totals the dots on screen, counting only values that are ENTIRELY
// dots: a sentence with a bullet point in it is not a secure field.
func masked(elements []simbridge.Element) int {
	total := 0
	for _, e := range elements {
		if allDots(e.Value) {
			total += len([]rune(e.Value))
		}
	}
	return total
}

func allDots(value string) bool {
	if value == "" {
		return false
	}
	return strings.Trim(value, dots) == ""
}

// copies is how many times the screen holds the text, added up over every
// element. A count is used rather than a per-element comparison because element
// paths are positions and move; a total cannot.
func copies(elements []simbridge.Element, needle string, normalize func(string) string) int {
	total := 0
	for _, e := range elements {
		total += strings.Count(normalize(e.Value), needle)
	}
	return total
}

// lettersAndDigits drops everything a field might have inserted or substituted
// - spaces, a card mask's separators, a smart quote for a straight one - from
// both the payload and the value, so what is compared is the part the field was
// not free to change.
func lettersAndDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// changes says what the screen reports now that it did not before, for the
// report on a paste that could not be proven. It pairs elements by path, which
// is unreliable across a reflow - so it describes what each path READS rather
// than claiming an element changed, and stops at a handful: a reflowed tree
// would otherwise bury the one line that matters.
func changes(before, after []simbridge.Element) []string {
	const most = 5
	was := byPath(before)
	var out []string
	extra := 0
	for _, e := range after {
		then, seen := was[e.Path]
		if seen && then.Value == e.Value {
			continue
		}
		if e.Value == "" && (!seen || then.Value == "") {
			continue
		}
		if len(out) == most {
			extra++
			continue
		}
		name := "[" + e.Path + "]"
		if e.Label != "" {
			name = fmt.Sprintf("%q %s", e.Label, name)
		}
		if !seen {
			out = append(out, fmt.Sprintf("%s reads %q and was not on screen before", name, elide(e.Value)))
			continue
		}
		out = append(out, fmt.Sprintf("%s reads %q, where it read %q", name, elide(e.Value), elide(then.Value)))
	}
	if extra > 0 {
		out = append(out, fmt.Sprintf("and %d more", extra))
	}
	return out
}

// elide keeps a quoted value short enough to read in a terminal. A field can
// hold a paragraph, and the point of quoting it is recognition, not transcript.
func elide(s string) string {
	const most = 60
	runes := []rune(s)
	if len(runes) <= most {
		return s
	}
	return string(runes[:most]) + "..."
}

func landing(e simbridge.Element, how Evidence) Landing {
	return Landing{Field: e.Label, Path: e.Path, Shown: e.Value, How: how}
}

// flatten is every element in the tree, in the order the tree reports them, so
// that naming a field is deterministic.
func flatten(snapshot simbridge.Snapshot) []simbridge.Element {
	var out []simbridge.Element
	var walk func([]simbridge.Element)
	walk = func(elements []simbridge.Element) {
		for _, e := range elements {
			out = append(out, e)
			walk(e.Children)
		}
	}
	walk(snapshot.Elements)
	return out
}

func byPath(elements []simbridge.Element) map[string]simbridge.Element {
	out := make(map[string]simbridge.Element, len(elements))
	for _, e := range elements {
		out[e.Path] = e
	}
	return out
}

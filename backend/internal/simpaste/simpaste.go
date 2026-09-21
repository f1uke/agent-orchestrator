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
	"strings"
	"time"
	"unicode"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
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
	// err is a named return, so by the time this runs it holds whatever the
	// function is actually about to return - nil only when the paste was
	// written, sent and verified on screen. That is what "performed" means
	// here: a write that failed, a keystroke that failed, or a paste that could
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
	landing, err := Verify(before, after, text)
	result.Landing = landing
	return result, err
}

// screenReads is the allowance the hold needs on top of the keystroke, for the
// two accessibility reads that prove the paste. The first read on a device can
// take a second or two while the translator attaches, and a hold that lapsed
// halfway through would hand the device away mid-proof.
const screenReads = 10 * time.Second

func pasteHoldFor(events []simbridge.Event) time.Duration {
	return simbridge.Duration(events) + screenReads + simgesture.HoldSlack
}

// Verify proves a paste landed by finding the text on screen afterwards.
//
// What counts as proof is the whole question here, and the rule it used to
// apply - "some element's value grew by at least the payload's length" - was
// wrong in both directions.
//
// It said FAILURE for work it had done. An empty field reports its PLACEHOLDER
// as its accessibility value: an untouched login field reads
// "example@email.com", and a correct paste of "r8t3@a.com" takes it from 17
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
	for _, rule := range rules {
		if landing, ok := rule.gained(was, now, text); ok {
			return landing, nil
		}
	}

	changed := changes(was, now)
	if len(changed) == 0 {
		return Landing{}, fmt.Errorf("%w: nothing on screen changed, so the text did not go anywhere. "+
			"Tap the field first so it has keyboard focus - and note that some apps refuse paste outright",
			ErrNotDelivered)
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

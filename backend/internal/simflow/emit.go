package simflow

import (
	"fmt"
	"strings"
)

// StepKind is which action a Step performed, at the granularity Emit needs in
// order to choose a YAML shape.
//
// It is deliberately coarser than the intent kinds a recording captures at
// the HTTP boundary ("drag-begin", "drag-move", "drag-end" already resolve
// to one recorded step by the time Emit sees it - see the recorder's own
// "one hold, one step" rule). A caller building a Step from a stored step
// maps whatever it drove onto one of these.
type StepKind string

// Step kinds Emit knows how to translate. A drag is emitted exactly like a
// swipe (§8, "swipe / drag" is one row), so it maps onto StepSwipe too -
// there is no separate StepDrag.
const (
	StepTap    StepKind = "tap"
	StepType   StepKind = "type"
	StepSwipe  StepKind = "swipe"
	StepButton StepKind = "button"
	// StepKey is one named keyboard key - Enter, Backspace, Tab, an arrow.
	// It is not StepType: typing promises characters and is written as
	// inputText, while this promises a key and is written as pressKey. A flow
	// that turned Enter into inputText "\n" would submit nothing.
	StepKey StepKind = "key"
)

// Step is one recorded gesture or observation, shaped for Emit.
//
// It is deliberately not domain.SimRecordingStep: this package stays free of
// storage types, so a caller that has a SimRecordingStep in hand builds one
// of these. Choice and Plain mirror Render's own parameters exactly, because
// Emit hands a tap step straight to Render - there is no second
// reconstruction of what a selector means, and the two can never disagree
// about how one gets written.
type Step struct {
	// Seq identifies this step for a human, in an error naming the step Emit
	// could not translate. It is the recording's own step number, carried
	// through rather than recomputed here.
	Seq int64

	Kind StepKind

	// Choice and Plain are the selector this step resolved to when it was
	// recorded, in exactly the shape For/Render already use. They are handed
	// straight to Render for a StepTap, and also (only to decide whether a
	// stable target exists, never to re-render one) for the extendedWaitUntil
	// stanza below.
	Choice Choice
	Plain  string

	// ScreenChange says this step caused a screen transition. It was computed
	// at record time against an accessibility tree Emit never sees, and must
	// be read here rather than re-derived: Emit has no tree to compare
	// against.
	ScreenChange bool

	// X, Y is where a swipe began, ToX, ToY where it ended - normalized 0..1
	// exactly as simbridge reports them. Unused for every other Kind.
	X, Y, ToX, ToY float64

	// Text is what was typed, for StepType.
	Text string

	// Detail names the button pressed, for StepButton ("home",
	// "app-switcher", ...).
	Detail string
}

// EmitOptions is the provenance a flow's header records, plus what to do
// about the entry point a recording never captures.
//
// There is deliberately no AppID field here. House style keeps
// `appId: ${APP_ID}` an environment variable (spec §5), and Emit always
// writes that literal placeholder regardless of what device the recording
// ran on - so a field a caller could set and watch silently do nothing would
// be worse than no field at all. If a bundle id ever needs to be explained to
// a reader, it belongs in the header's own comment where a reader of the flow
// sees it, not in a struct field nobody reads.
//
// There is also deliberately no Frontmost field. A "frontmost at start" line
// sounds useful, but nothing upstream of this package persists which app was
// in the foreground when a recording began (that would need a column on
// sim_recording, which no caller has today), so the only honest value any
// caller could pass is a permanent "unknown" - and a header line that always
// reads "unknown" is noise a reader learns to skip past, which is exactly
// where the entry-point instruction lives. If provenance turns out to matter,
// it is a migration and a real value, not a placeholder shipped ahead of one.
type EmitOptions struct {
	// Device and Runtime describe the simulator the recording ran on, e.g.
	// "iPhone 17 Pro Max" and "iOS 18.4".
	Device  string
	Runtime string
	// RecordedAt is an already-formatted RFC3339 timestamp - Emit does not
	// format one itself, so it stays a pure function of what it is given.
	RecordedAt string
	// Entry, when set, is a path to a shared entry-point flow. It becomes
	// `- runFlow: <Entry>` as the very first step, in place of the comment
	// that otherwise tells a human to add their own.
	Entry string
}

// Emit turns a recording's steps into a house-style Maestro flow.
//
// Two things it will never do, both load-bearing (spec §8.2):
//   - fabricate a launchApp. A recording starts wherever the app already
//     was; the header states that fact, it does not invent the step that got
//     there.
//   - write a literal bundle id into the appId line. That line is always the
//     `${APP_ID}` environment-variable placeholder.
//
// And one thing it refuses to do silently: a step this package has no
// Maestro translation for - today, only the app-switcher button - fails Emit
// outright, naming the step, rather than being dropped from the output. A
// flow that quietly skips a step the recording says happened is worse than
// one that refuses to be written.
func Emit(steps []Step, opts EmitOptions) (string, error) {
	var b strings.Builder

	b.WriteString("appId: ${APP_ID}\n---\n")
	fmt.Fprintf(&b, "# recorded by ao sim at %s, device %s (%s)\n", opts.RecordedAt, opts.Device, opts.Runtime)
	// Beside the provenance, not below the entry point: both lines say what
	// this file IS, and a reader listing flows reads this one back.
	writeCounts(&b, steps)
	if opts.Entry != "" {
		// Quoted like every other scalar this package writes: a path holding a
		// colon or a '#' is ordinary on disk and unparseable as bare YAML.
		fmt.Fprintf(&b, "- runFlow: %q\n", opts.Entry)
	} else {
		b.WriteString("# add your own entry point above if this flow must start from a cold app,\n")
		b.WriteString("#   e.g. `- runFlow: ../flows/<entry>.yaml`\n")
	}

	writeReviewHeader(&b, steps)

	for _, step := range steps {
		if step.ScreenChange && actsOnAnElement(step.Kind) {
			writeExtendedWait(&b, step.Choice)
		}
		if err := writeStep(&b, step); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// writeReviewHeader states, at the top of the flow, how many steps the
// generator could not resolve with confidence.
//
// A reader opening a generated flow decides in the first two seconds whether
// to trust it. Per-step "# REVIEW:" comments are the detail; this is the thing
// that makes a human go looking for them at all, and it is why the count is
// stated rather than a bare "some steps need review". Nothing is written when
// every step resolved - a banner that is always there is a banner nobody
// reads.
//
// The condition is Choice.NeedsReview, the same one Render writes its marker
// from, so the header can never claim a flow is clean while a step below it
// carries a marker.
func writeReviewHeader(b *strings.Builder, steps []Step) {
	guessed := ReviewCount(steps)
	if guessed == 0 {
		return
	}
	fmt.Fprintf(b, "#\n# REVIEW REQUIRED: %d of %d steps could not be resolved to one element\n", guessed, len(steps))
	b.WriteString("# with confidence, and are marked \"# REVIEW:\" below. They will RUN - and may\n")
	b.WriteString("# pass while touching the wrong element. Check them before relying on this flow.\n#\n")
}

// actsOnAnElement says whether a step's Choice describes something the step
// actually targeted - which is what makes a wait on it honest.
//
// A tap and a swipe both go to a place on screen, so the selector recorded for
// them was resolved from that place. A type and a button press do not: their
// intent carries no coordinates at all, so the recorder hit-tests (0,0) and
// resolves whatever happens to sit in the top-left corner - a status bar
// clock, usually. Waiting for THAT element to appear before typing is a wait
// on something the step has nothing to do with, which is the same untruth as
// an invented coordinate: it either passes for the wrong reason or fails for
// one. The step itself is still emitted; only the wait in front of it is
// dropped.
func actsOnAnElement(kind StepKind) bool {
	return kind == StepTap || kind == StepSwipe
}

// writeExtendedWait emits the "wait for the new screen" stanza (spec §8.1)
// before a step that changed screens - and only when there is something
// stable to name. Each rung was considered on its own:
//   - RungText / RungTextIndex: waited on, as a bare string. That is a text
//     matcher in Maestro.
//   - RungID: waited on too - an accessibility id is MORE stable than a text
//     label, not less, so skipping it would leave exactly the screens whose
//     only landmark is an id with no wait at all. It cannot use the bare
//     string form, though: under `visible:` a bare string is always a text
//     matcher, so an id has to be nested (`visible: { id: "..." }`) or
//     Maestro would search for an element whose *text* equals the id.
//   - RungPoint: skipped. A point already breaks on any layout change;
//     waiting for a point to become "visible" is not a thing Maestro does.
//   - RungNone, or OffScreen regardless of rung: skipped. There is either
//     nothing to name, or the thing named was not on screen at record time -
//     neither is an honest target to wait on.
//
// In every skipped case the step itself is still emitted; only the wait in
// front of it is omitted.
func writeExtendedWait(b *strings.Builder, c Choice) {
	if c.OffScreen {
		return
	}
	switch c.Rung {
	case RungText, RungTextIndex, RungTextAnchor:
		// The anchor rung waits on its text like the other two. The anchor
		// itself is deliberately NOT part of the wait: the wait exists to say
		// "the new screen has arrived", and the target's own text is what
		// answers that. Nesting the relative selector here would make the wait
		// fail for a reason that has nothing to do with the screen having
		// loaded.
		b.WriteString("- extendedWaitUntil:\n")
		fmt.Fprintf(b, "    visible: %q\n", c.Text)
		b.WriteString("    timeout: 10000\n")
	case RungID:
		b.WriteString("- extendedWaitUntil:\n")
		b.WriteString("    visible:\n")
		fmt.Fprintf(b, "      id: %q\n", c.ID)
		b.WriteString("    timeout: 10000\n")
	default:
		return
	}
}

// writeStep emits the one command a recorded step becomes, per the mapping
// in spec §8.
func writeStep(b *strings.Builder, step Step) error {
	switch step.Kind {
	case StepTap:
		// Render owns every rule about how a selector becomes YAML - escaping,
		// quoting, the off-screen scroll, the ambiguity comment. Re-deriving
		// any of that here would let this path and Render's disagree about how
		// a selector is written, which is exactly the trap this package has
		// already been caught by once (see the escaping test at the bottom of
		// this file).
		b.WriteString(Render(step.Choice, step.Plain))
	case StepType:
		fmt.Fprintf(b, "- inputText: %q\n", step.Text)
	case StepSwipe:
		// A swipe's coordinates are exact, so it always replays - but if the
		// recorder could not say WHAT it was made on, the flow has to say that
		// rather than look complete. The condition is Choice.NeedsReview, the
		// same one the banner counts, so the two cannot disagree.
		//
		// ⚠ Before this, a swipe the recorder could not describe came out as a
		// bare `- swipe:` line with nothing to find, while the banner above it
		// counted it as needing review.
		if step.Choice.NeedsReview() {
			fmt.Fprintf(b, "%s the screen this swipe was made on could not be described, so it replays as\n", reviewMarker)
			b.WriteString("#   coordinates alone and will not notice if the screen underneath has changed.\n")
		}
		fmt.Fprintf(b, "- swipe: {start: \"%d%%,%d%%\", end: \"%d%%,%d%%\"}\n",
			percent(step.X), percent(step.Y), percent(step.ToX), percent(step.ToY))
	case StepButton:
		return writePressKey(b, step, maestroButtonKey)
	case StepKey:
		return writePressKey(b, step, maestroKeyName)
	default:
		return fmt.Errorf("step %d: kind %q has no Maestro translation", step.Seq, step.Kind)
	}
	return nil
}

// writePressKey emits the one YAML shape a hardware button and a keyboard key
// share, and refuses by name rather than skipping.
//
// ⚠ `ao sim flow check` accepts ANY string after `pressKey:` - it parses, it
// does not validate the enum ("Nonsense Key" checks OK). So a name reaching
// here has to have been observed pressing something on a real device; parsing
// is not evidence. That is why these tables are short and why the record lists
// what was watched happening.
func writePressKey(b *strings.Builder, step Step, lookup func(string) (string, bool)) error {
	key, ok := lookup(step.Detail)
	if !ok {
		return fmt.Errorf("step %d: %s %q has no Maestro key code and cannot be translated to a flow step",
			step.Seq, step.Kind, step.Detail)
	}
	fmt.Fprintf(b, "- pressKey: %s\n", key)
	return nil
}

// maestroButtonKey maps a recorded hardware button onto Maestro's KeyCode.
// Only Home has one - Maestro's KeyCode enum has no app-switcher entry at all
// (spec §8's Step → YAML mapping table), so "app-switcher", and any other
// button name this package does not recognize, falls through to the
// "no translation" branch in writeStep rather than being silently skipped.
func maestroButtonKey(name string) (string, bool) {
	if name == "home" {
		return "Home", true
	}
	return "", false
}

// maestroKeyName maps a recorded keyboard key onto Maestro's own spelling.
func maestroKeyName(name string) (string, bool) {
	key, ok := map[string]string{
		"enter":       "Enter",
		"backspace":   "Backspace",
		"tab":         "Tab",
		"arrow-up":    "Arrow Up",
		"arrow-down":  "Arrow Down",
		"arrow-left":  "Arrow Left",
		"arrow-right": "Arrow Right",
	}[name]
	return key, ok
}

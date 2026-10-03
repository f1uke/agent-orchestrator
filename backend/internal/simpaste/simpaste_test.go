package simpaste_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpaste"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrunner"
)

// --- the verification, which is what stops this becoming the same bug ------

// field is one element as the accessibility tree reports it: a path, the label
// a person sees, and the value - which for an EMPTY text field is its
// placeholder, and that single fact is what this whole fix is about.
type field struct{ path, label, value string }

func screen(fields ...field) simbridge.Snapshot {
	elements := make([]simbridge.Element, 0, len(fields))
	for _, f := range fields {
		elements = append(elements, simbridge.Element{Path: f.path, Label: f.label, Value: f.value})
	}
	return simbridge.Snapshot{Elements: elements}
}

func snapshot(values map[string]string) simbridge.Snapshot {
	elements := make([]simbridge.Element, 0, len(values))
	for path, value := range values {
		elements = append(elements, simbridge.Element{Path: path, Value: value})
	}
	return simbridge.Snapshot{Elements: elements}
}

func TestVerify_AcceptsAFieldThatGrewByThePayload(t *testing.T) {
	before := snapshot(map[string]string{"0.1": "", "0.2": "untouched"})
	after := snapshot(map[string]string{"0.1": "hunter2", "0.2": "untouched"})
	landing, err := simpaste.Verify(before, after, "hunter2")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if landing.Path != "0.1" || landing.How != simpaste.EvidenceExact {
		t.Fatalf("landing = %+v, want the field that gained the text, proven exactly", landing)
	}
}

// The bug. An empty field reports its PLACEHOLDER as its value, so replacing it
// SHRINKS the field - and the old rule, "some element grew by the payload's
// length", called every one of these a failure. All three lengths were confirmed
// on a real device (iPhone 17 Pro Max, iOS 26.3) before this was changed.
func TestVerify_AcceptsTextThatReplacedAPlaceholder(t *testing.T) {
	for _, tc := range []struct {
		name, typed string
	}{
		{"shorter than the placeholder", "qa@test.io"},
		{"the same length as the placeholder", "exactly@email.xy"},
		{"longer than the placeholder", "a-very-long-address@example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := screen(field{"0.11", "email", "example@email.com"})
			after := screen(field{"0.11", "email", tc.typed})
			landing, err := simpaste.Verify(before, after, tc.typed)
			if err != nil {
				t.Fatalf("a field that now READS the text received it, whatever that did to its length: %v", err)
			}
			if landing.Field != "email" || landing.Shown != tc.typed {
				t.Fatalf("landing = %+v, want the email field and what it reads now", landing)
			}
		})
	}
}

func TestVerify_AcceptsASecureFieldByItsDots(t *testing.T) {
	// The case the whole paste path exists for. A secure field never reports
	// its text, but it does report one dot per character - which is enough to
	// tell "the password went in" from "nothing happened", and is the only
	// evidence available anywhere in this system for a field like that.
	//
	// Its placeholder is in the way too: on a real device the field read
	// "Password" (8 characters) and then eight dots, a gain of nothing at all.
	before := screen(field{"0.14", "password", "Password"})
	after := screen(field{"0.14", "password", "••••••••"})
	landing, err := simpaste.Verify(before, after, "Pa55word")
	if err != nil {
		t.Fatalf("eight dots must satisfy an eight-character payload: %v", err)
	}
	if landing.How != simpaste.EvidenceMasked {
		t.Fatalf("how = %q, want the caller told this was proven by dots, not by reading the text",
			landing.How)
	}
	if !strings.Contains(landing.String(), "8 dots") {
		t.Fatalf("the report must say what the evidence was: %q", landing.String())
	}
}

func TestVerify_RefusesASecureFieldThatTookOnlySomeOfIt(t *testing.T) {
	// Dots are a count, and the count is the whole proof - so a secure field
	// with a length limit has to fail on it.
	before := screen(field{"0.14", "password", "Password"})
	after := screen(field{"0.14", "password", "•••••"})
	if _, err := simpaste.Verify(before, after, "Pa55word"); !errors.Is(err, simpaste.ErrNotProven) {
		t.Fatalf("err = %v, want ErrNotProven: five dots are not eight characters", err)
	}
}

func TestVerify_AcceptsAppendingToAFieldThatAlreadyHadText(t *testing.T) {
	before := snapshot(map[string]string{"0.1": "abc"})
	after := snapshot(map[string]string{"0.1": "abcdef"})
	if _, err := simpaste.Verify(before, after, "def"); err != nil {
		t.Fatalf("the text arrived next to what was there: %v", err)
	}
}

func TestVerify_AcceptsASecondCopyOfTextThatWasAlreadyOnScreen(t *testing.T) {
	// Pasting what the field already holds is the case a "does the screen
	// contain it" check gets wrong. Copies are COUNTED, so a second one is a
	// gain even though the first was there before the paste.
	before := screen(field{"0.1", "code", "8421"})
	after := screen(field{"0.1", "code", "84218421"})
	if _, err := simpaste.Verify(before, after, "8421"); err != nil {
		t.Fatalf("the screen gained a copy, which is what delivery looks like here: %v", err)
	}
}

func TestVerify_CountsRunesNotBytes(t *testing.T) {
	// Non-ASCII is the paste path's own reason to exist, so its length must be
	// measured the way a person counts it. In bytes "สวัสดี" is 18 and would
	// never match the 6 characters that appeared.
	before := screen(field{"0.1", "greeting", ""})
	after := screen(field{"0.1", "greeting", "••••••"})
	if _, err := simpaste.Verify(before, after, "สวัสดี"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerify_RefusesAPasteThatChangedNothing(t *testing.T) {
	// The failure this exists to catch: an app that blocks paste, or a field
	// that never had focus. Reporting success here would be the original bug
	// wearing a different costume.
	same := map[string]string{"0.1": "", "0.2": "untouched"}
	_, err := simpaste.Verify(snapshot(same), snapshot(same), "hunter2")
	if !errors.Is(err, simpaste.ErrNotDelivered) {
		t.Fatalf("err = %v, want ErrNotDelivered", err)
	}
	if !strings.Contains(err.Error(), "did not") {
		t.Fatalf("error must say plainly that nothing arrived: %v", err)
	}
}

func TestVerify_AcceptsAFieldThatAddedFormattingOfItsOwn(t *testing.T) {
	// Observed on a real device: iOS smart-insert adds a space when pasting
	// next to existing text, so a 12-character paste can legitimately grow a
	// field by 13. Failing those would make the check cry wolf on pastes that
	// worked, and a check nobody trusts is a check nobody reads.
	before := screen(field{"0.1", "note", "ผ"})
	after := screen(field{"0.1", "note", "ผ สวัสดี ทดสอบ"})
	if _, err := simpaste.Verify(before, after, "สวัสดี ทดสอบ"); err != nil {
		t.Fatalf("a field that added its own spacing still received the text: %v", err)
	}
}

func TestVerify_AcceptsAFieldThatReformattedTheText(t *testing.T) {
	// A card or phone mask inserts punctuation of its own, and an on-screen
	// keyboard capitalises. Neither is the field refusing the text.
	before := screen(field{"0.1", "card", "Card number"})
	after := screen(field{"0.1", "card", "4111 1111 1111 1111"})
	landing, err := simpaste.Verify(before, after, "4111111111111111")
	if err != nil {
		t.Fatalf("the digits are all there, in order: %v", err)
	}
	if landing.How != simpaste.EvidenceReformatted {
		t.Fatalf("how = %q, want the caller told the field changed what it was given", landing.How)
	}

	before = screen(field{"0.1", "name", "Your name"})
	after = screen(field{"0.1", "name", "Somchai"})
	landing, err = simpaste.Verify(before, after, "somchai")
	if err != nil {
		t.Fatalf("an on-screen keyboard capitalising the first letter is not a failed paste: %v", err)
	}
	if landing.How != simpaste.EvidenceCase {
		t.Fatalf("how = %q, want the capitalisation said out loud", landing.How)
	}
}

func TestVerify_RefusesAFieldThatGainedLessThanWasSent(t *testing.T) {
	// Under-delivery is the direction that matters: a field with a length limit
	// truncates silently, and reporting that as success is the original bug.
	before := screen(field{"0.13", "maxlength 5", "up to five chars"})
	after := screen(field{"0.13", "maxlength 5", "trunc"})
	_, err := simpaste.Verify(before, after, "truncate-me-please")
	if err == nil {
		t.Fatal("a field that took only part of the text must be reported, not waved through")
	}
	if errors.Is(err, simpaste.ErrNotDelivered) {
		t.Fatal("something DID arrive; calling it 'not delivered' sends the reader down the wrong path")
	}
	if !errors.Is(err, simpaste.ErrNotProven) {
		t.Fatalf("err = %v, want ErrNotProven", err)
	}
	for _, want := range []string{"18", `"maxlength 5"`, `"trunc"`, `"up to five chars"`, "ao sim ax"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error must contain %s so the caller can go and look: %v", want, err)
		}
	}
}

// The other half of the same bug, and the expensive half. An element's path is
// its POSITION in the tree, so a keyboard coming up or a suggestion list
// appearing renumbers everything below it. The old check compared values by
// path and treated a path it had not seen as empty, so any label that arrived
// with the reflow "proved" the paste - which is how a paste that went nowhere
// reported success on a real device.
func TestVerify_RefusesAPasteProvenOnlyByATreeThatReflowed(t *testing.T) {
	before := screen(
		field{"0.0", "email", "example@email.com"},
		field{"0.1", "Continue", ""},
	)
	after := screen(
		field{"0.0", "email", "example@email.com"}, // untouched: nothing was pasted
		field{"0.1", "Google Suggestions", ""},     // the list arrived and pushed everything down
		field{"0.2", "", "18:44"},
		field{"0.3", "Continue", ""},
	)
	_, err := simpaste.Verify(before, after, "qa@test.io")
	if err == nil {
		t.Fatal("a screen that merely MOVED must never prove a paste: the field still reads its placeholder")
	}
	if !errors.Is(err, simpaste.ErrNotProven) {
		t.Fatalf("err = %v, want ErrNotProven", err)
	}
}

func TestVerify_NamesTheFieldItFound(t *testing.T) {
	// "Pasted 10 characters" is the report that leaves a caller guessing. The
	// field's label and its path are both here so the claim can be checked with
	// `ao sim ax` rather than taken on trust.
	before := screen(field{"0.11", "email", "example@email.com"})
	after := screen(field{"0.11", "email", "qa@test.io"})
	landing, err := simpaste.Verify(before, after, "qa@test.io")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	for _, want := range []string{`"email"`, "[0.11]", `"qa@test.io"`} {
		if !strings.Contains(landing.String(), want) {
			t.Fatalf("report %q must contain %s", landing.String(), want)
		}
	}
}

// --- the sequence ----------------------------------------------------------

type fakePasteboard struct {
	mu       sync.Mutex
	content  string
	writes   []string
	readErr  error
	writeErr error
}

func (p *fakePasteboard) Read(context.Context, string) (string, error) {
	if p.readErr != nil {
		return "", p.readErr
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.content, nil
}

func (p *fakePasteboard) Write(_ context.Context, _, text string) error {
	if p.writeErr != nil {
		return p.writeErr
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.content = text
	p.writes = append(p.writes, text)
	return nil
}

type fakeDriver struct {
	snapshots  []simbridge.Snapshot
	reads      int
	events     [][]simbridge.Event
	performErr error
	axErr      error
}

func (d *fakeDriver) AX(context.Context, string) (simbridge.Snapshot, error) {
	if d.axErr != nil {
		return simbridge.Snapshot{}, d.axErr
	}
	i := d.reads
	d.reads++
	if i < len(d.snapshots) {
		return d.snapshots[i], nil
	}
	return d.snapshots[len(d.snapshots)-1], nil
}

func (d *fakeDriver) Perform(_ context.Context, _ string, events []simbridge.Event) (simbridge.PerformResult, error) {
	d.events = append(d.events, events)
	return simbridge.PerformResult{}, d.performErr
}

func (d *fakeDriver) Hold(context.Context, string, []simbridge.Event) error {
	return errors.New("a paste is a whole gesture")
}

type fakeHolder struct {
	acquired int
	released int
	err      error
	// lastPerformed is what the most recent Release call was told about
	// whether the paste actually landed.
	lastPerformed bool
}

func (h *fakeHolder) Acquire(context.Context, string, time.Duration) (string, error) {
	if h.err != nil {
		return "", h.err
	}
	h.acquired++
	return "tok", nil
}

func (h *fakeHolder) Release(_ context.Context, _, _ string, outcome simgesture.Outcome) {
	h.released++
	h.lastPerformed = outcome.Performed
}

// keyRun is Run with the Command-V paster, the Device tab's - which is the
// route these tests were written against, and the one that touches the
// bridge.
func keyRun(holder simgesture.Holder, driver *fakeDriver, pb simpaste.Pasteboard, udid, text string) (simpaste.Result, error) {
	return simpaste.Run(context.Background(), holder, driver, simpaste.KeyPaster{Driver: driver}, pb, udid, text)
}

func pasted(from, to string) *fakeDriver {
	return &fakeDriver{snapshots: []simbridge.Snapshot{
		snapshot(map[string]string{"0.1": from}),
		snapshot(map[string]string{"0.1": to}),
	}}
}

func TestRun_PutsTheTextOnThePasteboardAndTakesItBackOff(t *testing.T) {
	pb := &fakePasteboard{content: "what the human had copied"}
	driver := pasted("", "hunter2")
	holder := &fakeHolder{}

	result, err := keyRun(holder, driver, pb, "UDID-1", "hunter2")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(pb.writes) != 2 || pb.writes[0] != "hunter2" {
		t.Fatalf("writes = %q, want the payload then the restore", pb.writes)
	}
	if pb.content != "what the human had copied" {
		t.Fatalf("guest pasteboard left holding %q - the payload must not outlive the command", pb.content)
	}
	if !result.Restored {
		t.Fatal("the result must say the pasteboard was put back, because sometimes it cannot be")
	}
	if holder.acquired != 1 || holder.released != 1 {
		t.Fatalf("hold taken %d, released %d - a paste is a gesture like any other", holder.acquired, holder.released)
	}
	if result.Landing.Path != "0.1" {
		t.Fatalf("landing = %+v - the caller has to be able to say WHERE the text went", result.Landing)
	}
}

func TestRun_RestoresThePasteboardEvenWhenThePasteFailed(t *testing.T) {
	// The payload is a password often enough that leaving it behind on a failure
	// would be the worst possible moment to leave it behind.
	pb := &fakePasteboard{content: "original"}
	driver := pasted("", "")
	driver.performErr = errors.New("bridge exploded")

	if _, err := keyRun(&fakeHolder{}, driver, pb, "UDID-1", "hunter2"); err == nil {
		t.Fatal("a failed paste must be reported")
	}
	if pb.content != "original" {
		t.Fatalf("guest pasteboard left holding %q after a failure", pb.content)
	}
}

func TestRun_FailsLoudlyWhenNothingWasPasted(t *testing.T) {
	pb := &fakePasteboard{content: "original"}
	driver := pasted("", "") // the field never changed

	_, err := keyRun(&fakeHolder{}, driver, pb, "UDID-1", "hunter2")
	if !errors.Is(err, simpaste.ErrNotDelivered) {
		t.Fatalf("err = %v, want ErrNotDelivered - a paste that did nothing must never report success", err)
	}
	if pb.content != "original" {
		t.Fatalf("guest pasteboard left holding %q", pb.content)
	}
}

func TestRun_SaysSoWhenThePasteboardCouldNotBePutBack(t *testing.T) {
	// The honest failure: the text landed, but the payload is still sitting on
	// the guest's pasteboard where any app on it can read it.
	pb := &fakePasteboard{content: "original"}
	driver := pasted("", "hunter2")
	result, err := keyRun(&fakeHolder{}, driver, pb, "UDID-1", "hunter2")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Restored {
		t.Fatal("precondition: this one restores fine")
	}

	pb2 := &restoreFails{fakePasteboard{content: "original"}}
	result, err = keyRun(&fakeHolder{}, pasted("", "hunter2"), pb2, "UDID-1", "hunter2")
	if err != nil {
		t.Fatalf("a pasteboard that could not be put back must not fail a paste that worked: %v", err)
	}
	if result.Restored {
		t.Fatal("Restored must be false so the caller can warn about the payload left behind")
	}
	if result.RestoreErr == nil {
		t.Fatal("the reason must travel with it")
	}
}

// restoreFails writes once (the payload) and then refuses, which is the shape
// of "the device went away mid-command".
type restoreFails struct{ fakePasteboard }

func (p *restoreFails) Write(ctx context.Context, udid, text string) error {
	p.mu.Lock()
	n := len(p.writes)
	p.mu.Unlock()
	if n > 0 {
		return errors.New("device went away")
	}
	return p.fakePasteboard.Write(ctx, udid, text)
}

func TestRun_RefusesWhenTheHoldIsNotGranted(t *testing.T) {
	// No hold, no gesture - and then the pasteboard must not have been touched
	// at all, because nothing is going to use it.
	pb := &fakePasteboard{content: "original"}
	holder := &fakeHolder{err: errors.New("device is mid-gesture")}

	if _, err := keyRun(holder, pasted("", ""), pb, "UDID-1", "hunter2"); err == nil {
		t.Fatal("a refused hold must refuse the paste")
	}
	if pb.content != "original" || len(pb.writes) != 0 {
		t.Fatalf("the pasteboard was written (%q) for a gesture that never ran", pb.writes)
	}
}

// --- the pasteboard itself -------------------------------------------------

func TestSimctlWrite_PinsAUTF8LocaleForThePayload(t *testing.T) {
	// `simctl pbcopy` decodes stdin with the environment's encoding, and a
	// process with no locale - which is what an agent or a daemon has - makes
	// it read UTF-8 bytes as MacRoman. The guest then holds
	// "‡∏™‡∏ß‡∏±‡∏™‡∏î‡∏µ" where "สวัสดี" was sent, on the one path that
	// exists precisely because the keyboard cannot carry those characters.
	var script string
	pb := simpaste.Simctl{Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "/bin/sh" || len(args) != 2 {
			t.Fatalf("pbcopy must go through a shell to get stdin: %s %q", name, args)
		}
		script = args[1]
		return nil, nil
	}}
	if err := pb.Write(context.Background(), "UDID-1", "สวัสดี"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.Contains(script, "LC_CTYPE=UTF-8") {
		t.Fatalf("script %q must pin a UTF-8 locale, or non-ASCII text arrives mangled", script)
	}
	if !strings.Contains(script, "'สวัสดี'") {
		t.Fatalf("script %q must carry the payload as one quoted literal", script)
	}
}

// --- the focused field, and the keyboard that eats the first key -----------

// focusedScreen is the XCTest reader's view: it knows which element has
// keyboard focus, and an empty field reports its placeholder AS its value
// until it holds text.
func focusedScreen(kind, value, placeholder string, keyboardUp bool) simbridge.Snapshot {
	return focusedScreenAt(kind, value, placeholder, keyboardUp, 580)
}

// minimizedScreen is focusedScreen with the software keyboard minimized: still
// in the tree, below the bottom of the screen, which is how the XCTest reader
// reports it after a hardware key press.
func minimizedScreen(kind, value, placeholder string) simbridge.Snapshot {
	return focusedScreenAt(kind, value, placeholder, true, 950)
}

func focusedScreenAt(kind, value, placeholder string, keyboardUp bool, keyboardY float64) simbridge.Snapshot {
	children := []simbridge.XCTestNode{
		{Type: "StaticText", Label: "name", Value: "name", Enabled: true, Frame: simbridge.Rect{X: 20, Y: 100, Width: 80, Height: 20}},
		{Type: kind, Label: "name", Value: value, Placeholder: placeholder, Enabled: true, Focused: true,
			Frame: simbridge.Rect{X: 20, Y: 130, Width: 360, Height: 44}},
	}
	if keyboardUp {
		children = append(children, simbridge.XCTestNode{Type: "Keyboard", Enabled: true,
			Frame:    simbridge.Rect{Y: keyboardY, Width: 402, Height: 290},
			Children: []simbridge.XCTestNode{{Type: "Key", Label: "q", Enabled: true, Frame: simbridge.Rect{X: 10, Y: keyboardY + 20, Width: 30, Height: 40}}}})
	}
	return simbridge.SnapshotFromXCTest(simbridge.XCTestHierarchy{
		Screen: simbridge.Size{Width: 402, Height: 874},
		Apps: []simbridge.XCTestApp{{BundleID: "com.example.app", Tree: simbridge.XCTestNode{
			Type: "Application", Enabled: true, Frame: simbridge.Rect{Width: 402, Height: 874}, Children: children,
		}}},
	})
}

func TestVerify_ProvesOneCharacterInTheFocusedField(t *testing.T) {
	// The label "name" and the placeholder "your name" both hold an "a"; a
	// count across the screen is one before and one after. The focused field
	// went from no text to "a".
	landing, err := simpaste.Verify(focusedScreen("TextField", "your name", "", false),
		focusedScreen("TextField", "a", "your name", false), "a")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if landing.How != simpaste.EvidenceExact || landing.Shown != "a" {
		t.Fatalf("landing = %+v", landing)
	}
}

func TestVerify_FocusedFieldTypedTwiceIsTwoCopies(t *testing.T) {
	if _, err := simpaste.Verify(focusedScreen("TextField", "a", "your name", false),
		focusedScreen("TextField", "aa", "your name", false), "a"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerify_FocusedSecureFieldCountsItsOwnDots(t *testing.T) {
	landing, err := simpaste.Verify(focusedScreen("SecureTextField", "password", "", false),
		focusedScreen("SecureTextField", "•••", "password", false), "abc")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if landing.How != simpaste.EvidenceMasked {
		t.Fatalf("evidence = %q, want masked", landing.How)
	}
	// Two of three is not proof.
	if _, err := simpaste.Verify(focusedScreen("SecureTextField", "password", "", false),
		focusedScreen("SecureTextField", "••", "password", false), "abc"); !errors.Is(err, simpaste.ErrNotProven) {
		t.Fatalf("err = %v, want simpaste.ErrNotProven for a secure field that took two of three", err)
	}
}

func TestVerify_TypingThePlaceholderItselfIsNotMistakenForAnEmptyField(t *testing.T) {
	// Not proven, and not wrongly proven either: a value equal to its own hint
	// reads as empty, and the screen-wide count has nothing new to find.
	_, err := simpaste.Verify(focusedScreen("TextField", "your name", "", false),
		focusedScreen("TextField", "your name", "your name", false), "your name")
	if err == nil {
		t.Fatal("a field reading its own placeholder proved a paste of the placeholder")
	}
}

func TestRun_WakesTheKeyboardBeforeCommandV(t *testing.T) {
	// The first key event a simulator gets while its software keyboard is up
	// is spent hiding the keyboard: Command-V alone pasted nothing on a device,
	// and the same Command-V a moment later pasted the text.
	driver := &fakeDriver{snapshots: []simbridge.Snapshot{
		focusedScreen("TextField", "your name", "", true),
		focusedScreen("TextField", "your name", "", false), // the keyboard went away
		focusedScreen("TextField", "hello", "your name", false),
	}}
	if _, err := keyRun(&fakeHolder{}, driver, &fakePasteboard{}, "udid", "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(driver.events) != 2 {
		t.Fatalf("performed %d gestures, want the wake and then the paste", len(driver.events))
	}
	if !reflect.DeepEqual(driver.events[0], simbridge.WakeKeyboard()) || !reflect.DeepEqual(driver.events[1], simbridge.Paste()) {
		t.Fatalf("events = %+v, want WakeKeyboard then Paste", driver.events)
	}
	if driver.reads != 3 {
		t.Fatalf("read the screen %d times, want 3: before, after the wake, after the paste", driver.reads)
	}
}

func TestRun_DoesNotWakeAKeyboardThatIsNotUp(t *testing.T) {
	driver := &fakeDriver{snapshots: []simbridge.Snapshot{
		focusedScreen("TextField", "your name", "", false),
		focusedScreen("TextField", "hello", "your name", false),
	}}
	if _, err := keyRun(&fakeHolder{}, driver, &fakePasteboard{}, "udid", "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(driver.events) != 1 {
		t.Fatalf("performed %d gestures, want only the paste", len(driver.events))
	}
}

// --- the edit menu, which presses no key -----------------------------------

type fakeFocus struct {
	answer simrunner.TypeAnswer
	err    error
}

func (f *fakeFocus) Focus(context.Context, string) (simrunner.TypeAnswer, error) {
	return f.answer, f.err
}

func focusedOn(labels ...string) *fakeFocus {
	return &fakeFocus{answer: simrunner.TypeAnswer{App: "com.example.app", PasteLabels: labels,
		Field: &simrunner.TypeField{Type: "SecureTextField", Label: "name"}}}
}

// withMenu is a screen with the edit menu up, offering these items.
func withMenu(snap simbridge.Snapshot, items ...string) simbridge.Snapshot {
	for i, label := range items {
		snap.Elements = append(snap.Elements, simbridge.Element{Path: "9." + strconv.Itoa(i), Type: "MenuItem",
			Label: label, Tap: &simbridge.Point{X: 0.3 + 0.1*float64(i), Y: 0.2}})
	}
	return snap
}

func menuRun(focus *fakeFocus, driver *fakeDriver, pb simpaste.Pasteboard, text string) (simpaste.Result, error) {
	paster := simpaste.MenuPaster{Runner: focus, Reader: driver, Driver: driver}
	return simpaste.Run(context.Background(), &fakeHolder{}, driver, paster, pb, "udid", text)
}

func TestRun_MenuPasteHoldsTheFieldTapsPasteAndPressesNoKey(t *testing.T) {
	driver := &fakeDriver{snapshots: []simbridge.Snapshot{
		focusedScreen("SecureTextField", "password", "", true),                              // before
		withMenu(focusedScreen("SecureTextField", "password", "", true), "AutoFill", "วาง"), // the menu, coming up
		withMenu(focusedScreen("SecureTextField", "password", "", true), "AutoFill", "วาง"), // ... and settled
		focusedScreen("SecureTextField", "•••••", "password", true),                         // the proof
	}}
	pb := &fakePasteboard{content: "original"}

	result, err := menuRun(focusedOn("วาง", "Paste"), driver, pb, "รหัส1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(driver.events) != 2 {
		t.Fatalf("performed %+v, want the press and the tap", driver.events)
	}
	press, tap := driver.events[0], driver.events[1]
	if press[0].Type != "begin" || press[1].Kind != "sleep" || press[1].MS < 500 || press[2].Type != "end" {
		t.Fatalf("press = %+v, want the field held long enough for its edit menu", press)
	}
	if tap[0].X != 0.4 || tap[0].Y != 0.2 {
		t.Fatalf("tap = %+v, want the วาง item's own point", tap)
	}
	for _, events := range driver.events {
		for _, e := range events {
			if e.Kind == "key" {
				t.Fatalf("pressed a key %+v - a hardware key press minimizes the software keyboard for every "+
					"field after it", e)
			}
		}
	}
	if result.Pasted.Via != simpaste.ViaEditMenu || result.Pasted.MenuItem != "วาง" || result.Pasted.App != "com.example.app" {
		t.Fatalf("pasted = %+v, want the edit menu route, its item and its app reported", result.Pasted)
	}
	if result.Pasted.Keyboard != nil || result.Landing.How != simpaste.EvidenceMasked || pb.content != "original" {
		t.Fatalf("result = %+v, pasteboard %q", result, pb.content)
	}
}

func TestRun_MenuPasteThatPastedNothingSaysWhy(t *testing.T) {
	before := focusedScreen("TextField", "x", "", true)
	for _, tc := range []struct {
		name     string
		focus    *fakeFocus
		screens  []simbridge.Snapshot
		code     string
		touches  int
		wantMenu []string
	}{
		{"runner not ready", &fakeFocus{err: simrunner.ErrNotReady}, []simbridge.Snapshot{before},
			simpaste.NotPastedUnavailable, 0, nil},
		{"no focus", &fakeFocus{answer: simrunner.TypeAnswer{Error: &simrunner.TypeError{
			Code: simrunner.TypeNoFocus, Message: "nothing has focus"}}}, []simbridge.Snapshot{before},
			simpaste.NotPastedNoFocus, 0, nil},
		{"no paste item", focusedOn("Paste"), []simbridge.Snapshot{before, withMenu(before, "AutoFill")},
			simpaste.NotPastedNoItem, 1, []string{"AutoFill"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			driver := &fakeDriver{snapshots: tc.screens}
			pb := &fakePasteboard{content: "original"}
			_, err := menuRun(tc.focus, driver, pb, "hello")
			var notPasted *simpaste.NotPastedError
			if !errors.As(err, &notPasted) || !errors.Is(err, simpaste.ErrNotPasted) || notPasted.Code != tc.code {
				t.Fatalf("err = %v, want NotPastedError %q - the caller may take another route only when nothing "+
					"was pasted", err, tc.code)
			}
			if len(driver.events) != tc.touches || pb.content != "original" {
				t.Fatalf("touches %+v, pasteboard %q", driver.events, pb.content)
			}
			if !reflect.DeepEqual(notPasted.Menu, tc.wantMenu) {
				t.Fatalf("menu = %q, want %q", notPasted.Menu, tc.wantMenu)
			}
		})
	}
}

func TestPressPoint_HoldsTheEndOfTheTextAndKeepsOffTheFieldsButtons(t *testing.T) {
	screen := simbridge.Size{Width: 400, Height: 800}
	field := simbridge.Element{Type: "TextField", Frame: simbridge.Rect{X: 20, Y: 100, Width: 360, Height: 40}}
	if got := simpaste.PressPoint(field, screen); got.X != (380-12)/400.0 || got.Y != 120/800.0 {
		t.Fatalf("plain field: %+v, want the right end, mid-height", got)
	}
	// A clear button inside the field: a press that ends on it is a tap on it.
	field.Children = []simbridge.Element{{Type: "Button", Label: "Clear text", Frame: simbridge.Rect{X: 340, Y: 105, Width: 30, Height: 30}}}
	if got := simpaste.PressPoint(field, screen); got.X != (340-12)/400.0 {
		t.Fatalf("with a clear button: x = %v, want left of the button", got.X*400)
	}
	view := simbridge.Element{Type: "TextView", Frame: simbridge.Rect{X: 20, Y: 100, Width: 360, Height: 200}}
	if got := simpaste.PressPoint(view, screen); got.Y != (300-12)/800.0 {
		t.Fatalf("text view: y = %v, want near the bottom, where its text ends", got.Y*800)
	}
}

// --- Command-V, and the keyboard it minimizes ------------------------------

func TestRun_CommandVShowsTheKeyboardItMinimized(t *testing.T) {
	driver := &fakeDriver{snapshots: []simbridge.Snapshot{
		focusedScreen("TextField", "your name", "", false),
		minimizedScreen("TextField", "hello", "your name"),     // after Command-V
		focusedScreen("TextField", "hello", "your name", true), // after the toggle
		focusedScreen("TextField", "hello", "your name", true), // the proof
	}}
	result, err := simpaste.Run(context.Background(), &fakeHolder{}, driver,
		simpaste.KeyPaster{Driver: driver, ShowKeyboard: true}, &fakePasteboard{}, "udid", "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(driver.events) != 2 || !reflect.DeepEqual(driver.events[1], simbridge.ShowKeyboard()) {
		t.Fatalf("events = %+v, want Command-V and then, apart from it, the keyboard toggle", driver.events)
	}
	if result.Pasted.Via != simpaste.ViaCommandV || result.Pasted.Keyboard == nil ||
		!result.Pasted.Keyboard.Shown || !result.Pasted.Keyboard.Seen {
		t.Fatalf("pasted = %+v, want Command-V reported with the keyboard shown and seen", result.Pasted)
	}
}

func TestRun_TheDeviceTabsCommandVLeavesTheKeyboardAlone(t *testing.T) {
	driver := pasted("", "hunter2")
	result, err := keyRun(&fakeHolder{}, driver, &fakePasteboard{}, "udid", "hunter2")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(driver.events) != 1 || result.Pasted.Keyboard != nil {
		t.Fatalf("events = %+v: a person typing on the Mac's keyboard is a hardware keyboard, and the "+
			"keyboard it minimizes is theirs to have minimized", driver.events)
	}
}

func TestPasters_FitTheGestureHoldsCeiling(t *testing.T) {
	// The lease service refuses a hold over a minute, and Run asks for the
	// paster's own time plus the two proof reads and the slack. Command-V
	// that shows the keyboard again is the longest.
	const ceiling = time.Minute
	for name, paster := range map[string]simpaste.Paster{
		"edit menu":              simpaste.MenuPaster{},
		"command-v":              simpaste.KeyPaster{},
		"command-v and keyboard": simpaste.KeyPaster{ShowKeyboard: true},
	} {
		var asked time.Duration
		holder := &ttlHolder{ttl: &asked}
		_, _ = simpaste.Run(context.Background(), holder, &fakeDriver{}, paster, &fakePasteboard{}, "udid", "x")
		if asked <= 0 || asked > ceiling {
			t.Errorf("%s asks for a %s hold; the ceiling is %s", name, asked, ceiling)
		}
	}
}

type ttlHolder struct{ ttl *time.Duration }

func (h *ttlHolder) Acquire(_ context.Context, _ string, ttl time.Duration) (string, error) {
	*h.ttl = ttl
	return "", errors.New("measured")
}

func (h *ttlHolder) Release(context.Context, string, string, simgesture.Outcome) {}

func TestRun_MenuPasteWaitsForTheItemToStopMoving(t *testing.T) {
	// The menu animates in: a tap at the first point the item is read at
	// lands while it is still moving, and pastes nothing.
	moving := withMenu(focusedScreen("TextField", "your name", "", true), "Paste")
	moving.Elements[len(moving.Elements)-1].Tap = &simbridge.Point{X: 0.3, Y: 0.25}
	driver := &fakeDriver{snapshots: []simbridge.Snapshot{
		focusedScreen("TextField", "your name", "", true),
		moving,
		withMenu(focusedScreen("TextField", "your name", "", true), "Paste"),
		withMenu(focusedScreen("TextField", "your name", "", true), "Paste"),
		focusedScreen("TextField", "hello", "your name", true),
	}}
	if _, err := menuRun(focusedOn("Paste"), driver, &fakePasteboard{}, "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tap := driver.events[1][0]; tap.Y != 0.2 {
		t.Fatalf("tapped %+v, want where the item settled", tap)
	}
}

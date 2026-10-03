package sim_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simflow"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrecord"
)

// fakeLiveReader is a ScreenReader that can also read in front of a gesture,
// the way the daemon's XCTest runner can. live is what that read answers (ok
// false when nil), and every point it was asked about is kept.
type fakeLiveReader struct {
	fakeScreenReader
	live      *simbridge.Snapshot
	liveCalls int
	asked     []*simbridge.Point
}

func (f *fakeLiveReader) LiveAX(_ context.Context, _ string, at *simbridge.Point) (simbridge.Snapshot, bool) {
	f.liveCalls++
	f.asked = append(f.asked, at)
	if f.live == nil {
		return simbridge.Snapshot{}, false
	}
	return *f.live, true
}

func newLiveRecording(t *testing.T, reader *fakeLiveReader) (*sim.Service, domain.SessionID) {
	t.Helper()
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	svc, store := newServiceWithOpts(t, fixedClock(now), sim.WithRecorder(reader),
		sim.WithScreenRefreshRunner(func(f func()) { f() }), sim.WithScreenRefreshDelay(0))
	owner := newSession(t, store, now)
	if _, err := svc.Acquire(context.Background(), owner, udidProMax, 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := svc.StartRecording(context.Background(), owner, udidProMax, "flow"); err != nil {
		t.Fatalf("start recording: %v", err)
	}
	return svc, owner
}

func perform(t *testing.T, svc *sim.Service, owner domain.SessionID, intent sim.GestureIntent) {
	t.Helper()
	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, intent)
	if err != nil {
		t.Fatalf("hold %+v: %v", intent, err)
	}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true}); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func stopSteps(t *testing.T, svc *sim.Service, owner domain.SessionID) []domain.SimRecordingStep {
	t.Helper()
	_, steps, err := svc.StopRecording(context.Background(), owner, udidProMax)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	return steps
}

// xctestScreen is a screen as the XCTest reader reports it: one root per
// application, frontmost first, each root the whole screen.
func xctestScreen(roots ...simbridge.Element) simbridge.Snapshot {
	return simbridge.Snapshot{
		Screen:    simbridge.Size{Width: 400, Height: 800},
		Frontmost: simbridge.Frontmost{BundleID: "com.app"},
		Reader:    &simbridge.Reader{Source: simbridge.SourceXCTest},
		Elements:  roots,
	}
}

func appRoot(path, id string, children ...simbridge.Element) simbridge.Element {
	return simbridge.Element{Path: path, Type: "Application", ID: id, Label: id,
		Box: &simbridge.Box{X1: 0, Y1: 0, X2: 1, Y2: 1}, Children: children}
}

func control(path, typ, label string, x1, y1, x2, y2 float64) simbridge.Element {
	return simbridge.Element{Path: path, Type: typ, Label: label,
		Box: &simbridge.Box{X1: x1, Y1: y1, X2: x2, Y2: y2},
		Tap: &simbridge.Point{X: (x1 + x2) / 2, Y: (y1 + y2) / 2}}
}

// 🗝 The screen a tap is described from is the screen of the tap.
//
// Measured on nter's web sign-in: the maintained screen was read two seconds
// after the system sheet's Continue, while the web page was still loading, and
// every tap on the page was then recorded as the app itself. A read taken in
// front of the tap sees the page - and is asked about the tap's own point.
func TestRecord_ATapIsDescribedFromAReadTakenAtTheTap(t *testing.T) {
	loading := xctestScreen(appRoot("0", "com.app"))
	page := xctestScreen(appRoot("0", "com.apple.SafariViewService",
		control("0.0", "TextField", "Email", 0.2, 0.5, 0.8, 0.56)), appRoot("1", "com.app"))
	page.Reached = &simbridge.Reached{Path: "0.0"}
	reader := &fakeLiveReader{fakeScreenReader: fakeScreenReader{snap: loading}, live: &page}
	svc, owner := newLiveRecording(t, reader)

	perform(t, svc, owner, sim.GestureIntent{Kind: "tap", X: 0.5, Y: 0.53})

	steps := stopSteps(t, svc, owner)
	if len(steps) != 1 || steps[0].Selector != "Email" || steps[0].SelectorRung != int64(simflow.RungText) {
		t.Fatalf("steps = %+v, want one tap on Email", steps)
	}
	if len(reader.asked) != 1 || reader.asked[0] == nil || reader.asked[0].X != 0.5 || reader.asked[0].Y != 0.53 {
		t.Fatalf("live reads asked about %v, want the tap's own point once", reader.asked)
	}
}

// What the reader says a touch reaches beats the tree's order: a background
// image listed after a button is not drawn over it.
func TestRecord_TheReachedElementWinsOverTreeOrder(t *testing.T) {
	screen := xctestScreen(appRoot("0", "com.app",
		control("0.0", "Button", "Buy", 0.1, 0.1, 0.5, 0.2),
		control("0.1", "Image", "", 0, 0, 1, 0.5)))
	screen.Reached = &simbridge.Reached{Path: "0.0"}
	reader := &fakeLiveReader{fakeScreenReader: fakeScreenReader{snap: screen}, live: &screen}
	svc, owner := newLiveRecording(t, reader)

	perform(t, svc, owner, sim.GestureIntent{Kind: "tap", X: 0.3, Y: 0.15})

	if steps := stopSteps(t, svc, owner); len(steps) != 1 || steps[0].Selector != "Buy" {
		t.Fatalf("steps = %+v, want the tap on Buy", steps)
	}
}

// Without a Reached answer, geometry decides - walking the roots frontmost
// first, and never answering with a root. Walked last-first, the app's empty
// root under a web sheet won every point: `tapOn: "Finnomena"`.
func TestRecord_GeometryWalksTheRootsFrontmostFirstAndNeverAnswersARoot(t *testing.T) {
	screen := xctestScreen(
		appRoot("0", "com.apple.SafariViewService", control("0.0", "Button", "Cancel", 0, 0.1, 0.2, 0.15)),
		appRoot("1", "Finnomena"))
	reader := &fakeLiveReader{fakeScreenReader: fakeScreenReader{snap: screen}, live: &screen}
	svc, owner := newLiveRecording(t, reader)

	perform(t, svc, owner, sim.GestureIntent{Kind: "tap", X: 0.1, Y: 0.12})
	// Nothing but the roots under this one.
	perform(t, svc, owner, sim.GestureIntent{Kind: "tap", X: 0.7, Y: 0.7})

	steps := stopSteps(t, svc, owner)
	if len(steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(steps))
	}
	if steps[0].Selector != "Cancel" {
		t.Errorf("step 1 selector = %q, want Cancel on the sheet in front", steps[0].Selector)
	}
	if steps[1].Selector == "Finnomena" || steps[1].SelectorRung != int64(simflow.RungPoint) {
		t.Errorf("step 2 = %+v, want an honest coordinate, not the app's root", steps[1])
	}
}

// A tap on the software keyboard is typing. A flow that looked for a button
// labelled "q" would assert the keyboard's layout instead of typing a q.
func TestRecord_ATapOnAKeyboardKeyIsWhatTheKeyTypes(t *testing.T) {
	// The Thai layout, as nter's sign-in shows it: special keys are named in
	// the keyboard's language and known by their id; return is a Button.
	withID := func(el simbridge.Element, id string) simbridge.Element { el.ID = id; return el }
	keyboard := simbridge.Element{Path: "0.1", Type: "Keyboard", Box: &simbridge.Box{X1: 0, Y1: 0.7, X2: 1, Y2: 1},
		Children: []simbridge.Element{
			control("0.1.0", "Key", "ก", 0, 0.7, 0.1, 0.75),
			withID(control("0.1.1", "Key", "ลบ", 0.9, 0.8, 1, 0.85), "delete"),
			withID(control("0.1.2", "Button", "ปุ่ม Shift", 0, 0.8, 0.1, 0.85), "shift"),
			withID(control("0.1.3", "Button", "ไป", 0.8, 0.9, 1, 0.95), "Go"),
			withID(control("0.1.4", "Key", "รับคำเสนอ", 0.3, 0.9, 0.7, 0.95), "space"),
		}}
	field := control("0.0", "TextField", "Search", 0.1, 0.1, 0.9, 0.15)
	field.Focused = true
	screen := xctestScreen(appRoot("0", "com.app", field, keyboard))
	reader := &fakeLiveReader{fakeScreenReader: fakeScreenReader{snap: screen}, live: &screen}
	svc, owner := newLiveRecording(t, reader)

	for _, path := range []string{"0.1.0", "0.1.1", "0.1.2", "0.1.3", "0.1.4"} {
		s := screen
		s.Reached = &simbridge.Reached{Path: path}
		reader.live = &s
		perform(t, svc, owner, sim.GestureIntent{Kind: "tap", X: 0.05, Y: 0.72})
	}

	steps := stopSteps(t, svc, owner)
	if len(steps) != 5 {
		t.Fatalf("steps = %+v, want 5", steps)
	}
	if steps[0].Kind != "type" || steps[0].Text != "ก" {
		t.Errorf("ก key = %q %q, want typing ก", steps[0].Kind, steps[0].Text)
	}
	if steps[3].Kind != "key" || steps[3].Detail != "enter" {
		t.Errorf("return key = %q %q, want the enter key", steps[3].Kind, steps[3].Detail)
	}
	if steps[4].Kind != "type" || steps[4].Text != " " {
		t.Errorf("space bar = %q %q, want typing a space whatever it was showing", steps[4].Kind, steps[4].Text)
	}
	if steps[1].Kind != "key" || steps[1].Detail != "backspace" {
		t.Errorf("delete key (ลบ) = %q %q, want the backspace key", steps[1].Kind, steps[1].Detail)
	}
	if steps[2].Kind != "tap" || steps[2].SelectorRung != int64(simflow.RungPoint) || steps[2].Selector != "" {
		t.Errorf("shift key = %+v, want a coordinate marked for review, not `tapOn: shift`", steps[2])
	}
}

// 🗝 What goes into a secure field is never kept, and never typed by the flow.
//
// The step says it was secure and names the field; its text is empty in the
// database, the run of typing is one step however it was chunked, and the
// flow pastes instead (mobile-ui-scripts rule 11). The keystrokes after the
// first do not read the screen again.
func TestRecord_TypingIntoASecureFieldKeepsNoTextAndBecomesThePaste(t *testing.T) {
	password := control("0.0", "SecureTextField", "Password", 0.1, 0.4, 0.9, 0.45)
	password.Focused = true
	screen := xctestScreen(appRoot("0", "com.app", password))
	reader := &fakeLiveReader{fakeScreenReader: fakeScreenReader{snap: screen}, live: &screen}
	svc, owner := newLiveRecording(t, reader)

	perform(t, svc, owner, sim.GestureIntent{Kind: "type", Text: "hunter"})
	perform(t, svc, owner, sim.GestureIntent{Kind: "type", Text: "2!"})
	if reader.liveCalls != 1 {
		t.Fatalf("live reads = %d, want 1: only the first keystroke of a run looks", reader.liveCalls)
	}
	if reader.asked[0] != nil {
		t.Errorf("typing asked about a point %+v; it has none", reader.asked[0])
	}

	steps := stopSteps(t, svc, owner)
	if len(steps) != 1 {
		t.Fatalf("steps = %+v, want one secure step", steps)
	}
	if !steps[0].Secure || steps[0].Text != "" || steps[0].Selector != "Password" {
		t.Fatalf("step = %+v, want secure, no text, the field named", steps[0])
	}
	flow, err := simflow.Emit(simrecord.Steps(steps), simflow.EmitOptions{})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if strings.Contains(flow, "hunter") || strings.Contains(flow, "inputText") {
		t.Fatalf("the flow carries the typed text:\n%s", flow)
	}
	if !strings.Contains(flow, "      - longPressOn: \"Password\"\n") || !strings.Contains(flow, "${MAESTRO_ACCOUNT_PASSWORD_DOTS}") {
		t.Fatalf("want the paste into Password, got:\n%s", flow)
	}
}

// Plain typing is recorded exactly as before: the text, no selector.
func TestRecord_TypingIntoAPlainFieldIsUnchanged(t *testing.T) {
	email := control("0.0", "TextField", "Email", 0.1, 0.4, 0.9, 0.45)
	email.Focused = true
	screen := xctestScreen(appRoot("0", "com.app", email))
	reader := &fakeLiveReader{fakeScreenReader: fakeScreenReader{snap: screen}, live: &screen}
	svc, owner := newLiveRecording(t, reader)

	perform(t, svc, owner, sim.GestureIntent{Kind: "type", Text: "someone@example.com"})

	steps := stopSteps(t, svc, owner)
	if len(steps) != 1 || steps[0].Secure || steps[0].Text != "someone@example.com" || steps[0].Selector != "" {
		t.Fatalf("steps = %+v, want the text and no selector", steps)
	}
}

// A drag streamed from the Device tab does not wait for a read: its first
// segment carries the finger, and a stall there is what used to drop drags.
// And a reader that cannot answer leaves the old path in charge.
func TestRecord_AStreamedDragAndAnUnansweredReadUseTheMaintainedScreen(t *testing.T) {
	remembered := xctestScreen(appRoot("0", "com.app", control("0.0", "Button", "Continue", 0.1, 0.1, 0.5, 0.3)))
	reader := &fakeLiveReader{fakeScreenReader: fakeScreenReader{snap: remembered}}
	svc, owner := newLiveRecording(t, reader)

	hold, err := svc.AcquireHold(context.Background(), owner, udidProMax, 0, sim.GestureIntent{Kind: "drag-begin", X: 0.3, Y: 0.2})
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if reader.liveCalls != 0 {
		t.Fatalf("a streamed drag read %d times in front of the finger; want 0", reader.liveCalls)
	}
	end := &simbridge.Point{X: 0.3, Y: 0.6}
	if err := svc.ReleaseHold(context.Background(), udidProMax, hold.Token, sim.GestureOutcome{Performed: true, End: end}); err != nil {
		t.Fatalf("release: %v", err)
	}

	perform(t, svc, owner, sim.GestureIntent{Kind: "tap", X: 0.3, Y: 0.2})
	if reader.liveCalls != 1 {
		t.Fatalf("live reads = %d, want 1 (the tap asked, and was not answered)", reader.liveCalls)
	}
	steps := stopSteps(t, svc, owner)
	if len(steps) != 2 || steps[1].Selector != "Continue" {
		t.Fatalf("steps = %+v, want the tap described from the maintained screen", steps)
	}
}

// A web form's password box has no label, only a placeholder: it is named by
// the placeholder (Maestro's hintText). One with neither is long-pressed at
// its own point, never at the 0%,0% a typing step's absent coordinates gave.
func TestRecord_ASecureFieldWithoutALabelIsStillNamedOrPointedAt(t *testing.T) {
	box := control("0.0", "SecureTextField", "", 0.2, 0.5, 0.8, 0.56)
	box.Focused = true
	box.Placeholder = "Enter password"
	screen := xctestScreen(appRoot("0", "com.app", box))
	reader := &fakeLiveReader{fakeScreenReader: fakeScreenReader{snap: screen}, live: &screen}
	svc, owner := newLiveRecording(t, reader)
	perform(t, svc, owner, sim.GestureIntent{Kind: "type", Text: "pw"})
	steps := stopSteps(t, svc, owner)
	if len(steps) != 1 || steps[0].Selector != "Enter password" || steps[0].SelectorRung != int64(simflow.RungText) {
		t.Fatalf("steps = %+v, want the field named by its placeholder", steps)
	}

	box.Placeholder = ""
	bare := xctestScreen(appRoot("0", "com.app", box))
	reader2 := &fakeLiveReader{fakeScreenReader: fakeScreenReader{snap: bare}, live: &bare}
	svc2, owner2 := newLiveRecording(t, reader2)
	perform(t, svc2, owner2, sim.GestureIntent{Kind: "type", Text: "pw"})
	flow, err := simflow.Emit(simrecord.Steps(stopSteps(t, svc2, owner2)), simflow.EmitOptions{})
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(flow, `point: "50%,53%"`) {
		t.Fatalf("want the long press at the field's own point, got:\n%s", flow)
	}
}

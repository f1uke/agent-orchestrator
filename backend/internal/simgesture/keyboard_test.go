package simgesture_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
)

// screens is a driver whose reads answer a script, one snapshot per read (the
// last repeats), and whose performs are recorded.
type screens struct {
	reads     []simbridge.Snapshot
	read      int
	readErr   error
	performed [][]simbridge.Event
}

func (s *screens) AX(context.Context, string) (simbridge.Snapshot, error) {
	if s.readErr != nil {
		return simbridge.Snapshot{}, s.readErr
	}
	i := min(s.read, len(s.reads)-1)
	s.read++
	return s.reads[i], nil
}

func (s *screens) Perform(_ context.Context, _ string, events []simbridge.Event) (simbridge.PerformResult, error) {
	s.performed = append(s.performed, events)
	return simbridge.PerformResult{}, nil
}

func (s *screens) Hold(context.Context, string, []simbridge.Event) error { return nil }

type keyboardState int

const (
	keyboardUp keyboardState = iota
	keyboardMinimized
	keyboardAbsent
)

// screen is what the XCTest reader reports: a field (focused or not) and the
// software keyboard up, minimized (still in the tree, below the screen) or
// absent.
func screen(focused bool, keyboard keyboardState) simbridge.Snapshot {
	children := []simbridge.XCTestNode{{Type: "TextField", Label: "name", Enabled: true, Focused: focused,
		Frame: simbridge.Rect{X: 20, Y: 130, Width: 360, Height: 44}}}
	y := 580.0
	if keyboard == keyboardMinimized {
		y = 950
	}
	if keyboard != keyboardAbsent {
		children = append(children, simbridge.XCTestNode{Type: "Keyboard", Enabled: true,
			Frame: simbridge.Rect{Y: y, Width: 402, Height: 290}})
	}
	return simbridge.SnapshotFromXCTest(simbridge.XCTestHierarchy{
		Screen: simbridge.Size{Width: 402, Height: 874},
		Apps: []simbridge.XCTestApp{{BundleID: "com.example.app", Tree: simbridge.XCTestNode{
			Type: "Application", Enabled: true, Frame: simbridge.Rect{Width: 402, Height: 874}, Children: children,
		}}},
	})
}

func backspace(t *testing.T) []simbridge.Event {
	t.Helper()
	events, err := simbridge.Key("backspace")
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func toggles(performed [][]simbridge.Event) int {
	n := 0
	for _, events := range performed {
		if reflect.DeepEqual(events, simbridge.ShowKeyboard()) {
			n++
		}
	}
	return n
}

func TestShowKeyboardAfter_ShowsAMinimizedKeyboardAndSeesItBack(t *testing.T) {
	driver := &screens{reads: []simbridge.Snapshot{screen(true, keyboardMinimized), screen(true, keyboardUp)}}
	got := simgesture.ShowKeyboardAfter(context.Background(), driver, "udid", backspace(t))
	if got == nil || !got.Shown || !got.Seen || got.Problem != "" {
		t.Fatalf("keyboard = %+v, want shown and seen", got)
	}
	if toggles(driver.performed) != 1 {
		t.Fatalf("performed %+v, want exactly one toggle", driver.performed)
	}
}

func TestShowKeyboardAfter_LeavesAKeyboardThatIsUpAlone(t *testing.T) {
	// The toggle is a toggle: sent at a keyboard that is showing, it hides it.
	driver := &screens{reads: []simbridge.Snapshot{screen(true, keyboardUp)}}
	got := simgesture.ShowKeyboardAfter(context.Background(), driver, "udid", backspace(t))
	if got == nil || got.Shown || !got.Seen {
		t.Fatalf("keyboard = %+v, want seen up and left alone", got)
	}
	if len(driver.performed) != 0 {
		t.Fatalf("performed %+v at a keyboard that was up", driver.performed)
	}
}

func TestShowKeyboardAfter_TogglesOnWhatIsKnownWhenItCannotLook(t *testing.T) {
	// A key that is not a modifier always leaves the keyboard minimized, so
	// with nothing to look at the toggle is still right - and that it was not
	// seen is said, not hidden.
	for name, driver := range map[string]*screens{
		"no field has focus":    {reads: []simbridge.Snapshot{screen(false, keyboardMinimized)}},
		"the bridge read it":    {reads: []simbridge.Snapshot{{Reader: &simbridge.Reader{Source: simbridge.SourceAccessibility}}}},
		"the read failed":       {readErr: errors.New("gone")},
		"a picker, no keyboard": {reads: []simbridge.Snapshot{screen(true, keyboardAbsent)}},
	} {
		t.Run(name, func(t *testing.T) {
			got := simgesture.ShowKeyboardAfter(context.Background(), driver, "udid", backspace(t))
			if got == nil || !got.Shown || got.Seen || got.Unseen == "" {
				t.Fatalf("keyboard = %+v, want shown, not seen, and why", got)
			}
			if toggles(driver.performed) != 1 {
				t.Fatalf("performed %+v, want one toggle", driver.performed)
			}
		})
	}
}

func TestShowKeyboardAfter_SaysSoWhenTheKeyboardStaysMinimized(t *testing.T) {
	driver := &screens{reads: []simbridge.Snapshot{screen(true, keyboardMinimized)}}
	got := simgesture.ShowKeyboardAfter(context.Background(), driver, "udid", backspace(t))
	if got == nil || got.Problem == "" || !strings.HasPrefix(got.String(), "WARNING") {
		t.Fatalf("keyboard = %+v, want a problem the caller prints as a warning", got)
	}
}

func TestShowKeyboardAfter_IgnoresKeysThatDoNotMinimize(t *testing.T) {
	driver := &screens{reads: []simbridge.Snapshot{screen(true, keyboardMinimized)}}
	if got := simgesture.ShowKeyboardAfter(context.Background(), driver, "udid", simbridge.WakeKeyboard()); got != nil {
		t.Fatalf("keyboard = %+v for a bare Command, which minimizes nothing", got)
	}
	if driver.read != 0 || len(driver.performed) != 0 {
		t.Fatal("nothing to check, so nothing read or sent")
	}
}

func TestRun_ShowKeyboardRestoresAfterTheKeysUnderTheSameHold(t *testing.T) {
	driver := &screens{reads: []simbridge.Snapshot{screen(true, keyboardMinimized), screen(true, keyboardUp)}}
	rec := &recorder{}
	result, err := simgesture.Run(context.Background(), rec, driver, "udid",
		simgesture.Gesture{Action: "key", Events: backspace(t), ShowKeyboard: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Keyboard == nil || !result.Keyboard.Seen {
		t.Fatalf("keyboard = %+v", result.Keyboard)
	}
	if len(driver.performed) != 2 || !reflect.DeepEqual(driver.performed[0], backspace(t)) {
		t.Fatalf("performed %+v, want the key and then, apart, the toggle", driver.performed)
	}
	if rec.ttl < simbridge.Duration(backspace(t))+simgesture.HoldSlack+simgesture.KeyboardCheck || rec.ttl > time.Minute {
		t.Fatalf("hold ttl %s must cover the keyboard check and stay inside a minute", rec.ttl)
	}
	if rec.order() != "acquire,release" {
		t.Fatalf("hold steps %q", rec.order())
	}
}

func TestRun_WithoutShowKeyboardLeavesTheKeyboardAlone(t *testing.T) {
	// The Device tab: a person typing on the Mac's keyboard.
	driver := &screens{reads: []simbridge.Snapshot{screen(true, keyboardMinimized)}}
	result, err := simgesture.Run(context.Background(), &recorder{}, driver, "udid",
		simgesture.Gesture{Action: "key", Events: backspace(t)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Keyboard != nil || len(driver.performed) != 1 || driver.read != 0 {
		t.Fatalf("keyboard = %+v, performed %+v", result.Keyboard, driver.performed)
	}
}

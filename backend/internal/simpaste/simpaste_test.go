package simpaste_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpaste"
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
		{"shorter than the placeholder", "r8t3@a.com"},
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
	_, err := simpaste.Verify(before, after, "r8t3@a.com")
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
	after := screen(field{"0.11", "email", "r8t3@a.com"})
	landing, err := simpaste.Verify(before, after, "r8t3@a.com")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	for _, want := range []string{`"email"`, "[0.11]", `"r8t3@a.com"`} {
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

	result, err := simpaste.Run(context.Background(), holder, driver, pb, "UDID-1", "hunter2")
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

	if _, err := simpaste.Run(context.Background(), &fakeHolder{}, driver, pb, "UDID-1", "hunter2"); err == nil {
		t.Fatal("a failed paste must be reported")
	}
	if pb.content != "original" {
		t.Fatalf("guest pasteboard left holding %q after a failure", pb.content)
	}
}

func TestRun_FailsLoudlyWhenNothingWasPasted(t *testing.T) {
	pb := &fakePasteboard{content: "original"}
	driver := pasted("", "") // the field never changed

	_, err := simpaste.Run(context.Background(), &fakeHolder{}, driver, pb, "UDID-1", "hunter2")
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
	result, err := simpaste.Run(context.Background(), &fakeHolder{}, driver, pb, "UDID-1", "hunter2")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Restored {
		t.Fatal("precondition: this one restores fine")
	}

	pb2 := &restoreFails{fakePasteboard{content: "original"}}
	result, err = simpaste.Run(context.Background(), &fakeHolder{}, pasted("", "hunter2"), pb2, "UDID-1", "hunter2")
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

	if _, err := simpaste.Run(context.Background(), holder, pasted("", ""), pb, "UDID-1", "hunter2"); err == nil {
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

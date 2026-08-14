package simpaste_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpaste"
)

// --- the verification, which is what stops this becoming the same bug ------

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
	if err := simpaste.Verify(before, after, "hunter2"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerify_AcceptsASecureFieldByItsDots(t *testing.T) {
	// The case the whole paste path exists for. A secure field never reports
	// its text, but it does report one dot per character - which is enough to
	// tell "the password went in" from "nothing happened", and is the only
	// evidence available anywhere in this system for a field like that.
	before := snapshot(map[string]string{"0.1": ""})
	after := snapshot(map[string]string{"0.1": "••••••••"})
	if err := simpaste.Verify(before, after, "Pa55word"); err != nil {
		t.Fatalf("eight dots must satisfy an eight-character payload: %v", err)
	}
}

func TestVerify_AcceptsAppendingToAFieldThatAlreadyHadText(t *testing.T) {
	before := snapshot(map[string]string{"0.1": "abc"})
	after := snapshot(map[string]string{"0.1": "abcdef"})
	if err := simpaste.Verify(before, after, "def"); err != nil {
		t.Fatalf("the growth is what is checked, not the total: %v", err)
	}
}

func TestVerify_CountsRunesNotBytes(t *testing.T) {
	// Non-ASCII is the paste path's own reason to exist, so its length must be
	// measured the way a person counts it. In bytes "สวัสดี" is 18 and would
	// never match the 6 characters that appeared.
	before := snapshot(map[string]string{"0.1": ""})
	after := snapshot(map[string]string{"0.1": "สวัสดี"})
	if err := simpaste.Verify(before, after, "สวัสดี"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerify_RefusesAPasteThatChangedNothing(t *testing.T) {
	// The failure this exists to catch: an app that blocks paste, or a field
	// that never had focus. Reporting success here would be the original bug
	// wearing a different costume.
	same := map[string]string{"0.1": "", "0.2": "untouched"}
	err := simpaste.Verify(snapshot(same), snapshot(same), "hunter2")
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
	// field by 13. A phone or card mask does the same with its punctuation.
	// Failing those would make the check cry wolf on pastes that worked, and a
	// check nobody trusts is a check nobody reads.
	before := snapshot(map[string]string{"0.1": "ผ"})
	after := snapshot(map[string]string{"0.1": "ผ สวัสดี ทดสอบ"})
	if err := simpaste.Verify(before, after, "สวัสดี ทดสอบ"); err != nil {
		t.Fatalf("a field that added its own spacing still received the text: %v", err)
	}
}

func TestVerify_RefusesAFieldThatGainedLessThanWasSent(t *testing.T) {
	// Under-delivery is the direction that matters: a field with a length limit
	// truncates silently, and reporting that as success is the original bug.
	before := snapshot(map[string]string{"0.1": ""})
	after := snapshot(map[string]string{"0.1": "hunt"})
	err := simpaste.Verify(before, after, "hunter2")
	if err == nil {
		t.Fatal("a field that took only part of the text must be reported, not waved through")
	}
	if errors.Is(err, simpaste.ErrNotDelivered) {
		t.Fatal("something DID arrive; calling it 'not delivered' sends the reader down the wrong path")
	}
	if !strings.Contains(err.Error(), "7") {
		t.Fatalf("error must say how much was sent: %v", err)
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
}

func (h *fakeHolder) Acquire(context.Context, string, time.Duration) (string, error) {
	if h.err != nil {
		return "", h.err
	}
	h.acquired++
	return "tok", nil
}

func (h *fakeHolder) Release(context.Context, string, string) { h.released++ }

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

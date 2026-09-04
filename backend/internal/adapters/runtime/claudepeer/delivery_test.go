package claudepeer

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/msgdelivery"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgorigin"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fakeJournal stands in for the durable record.
type fakeJournal struct {
	mu      sync.Mutex
	entries []msgdelivery.Entry
}

func (j *fakeJournal) Append(e msgdelivery.Entry) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, e)
	return nil
}

func (j *fakeJournal) only(t *testing.T) msgdelivery.Entry {
	t.Helper()
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.entries) != 1 {
		t.Fatalf("journal holds %d entries, want exactly one per send: %+v", len(j.entries), j.entries)
	}
	return j.entries[0]
}

// sendAndCollect runs one send with a collector and a journal attached, and
// returns what the transport reported to each. They must agree: the line a
// human reads now and the line they read tomorrow describe the same send.
func sendAndCollect(ctx context.Context, t *testing.T, rt *Runtime, journal *fakeJournal, message string) (msgdelivery.Report, error) {
	t.Helper()
	ctx = msgdelivery.WithOrigin(ctx, msgdelivery.Origin{Session: "ao-1", Trigger: msgdelivery.TriggerSend})
	ctx, collector := msgdelivery.WithCollector(ctx)
	err := rt.SendMessage(ctx, ports.RuntimeHandle{ID: "ao-1"}, message)
	report, reported := collector.Collected()
	if !reported {
		t.Fatal("the transport reported no path at all; every send must account for itself")
	}
	entry := journal.only(t)
	if entry.Path != report.Path || entry.Reason != report.Reason {
		t.Fatalf("journal says %s/%s but the caller was told %s/%s", entry.Path, entry.Reason, report.Path, report.Reason)
	}
	if entry.Session != "ao-1" || entry.Trigger != msgdelivery.TriggerSend {
		t.Fatalf("journal lost the caller's origin: %+v", entry)
	}
	return report, err
}

func newJournalledRuntime(t *testing.T, delegate Delegate, registry Registry) (*Runtime, *fakeJournal) {
	t.Helper()
	t.Setenv(modeEnv, "")
	journal := &fakeJournal{}
	return New(delegate, Options{Registry: registry, Journal: journal}), journal
}

// A socket delivery is only ever reported after the frame was completely
// written, which is the same commit point the fallback decision uses.
func TestSocketDeliveryIsReportedAsSocket(t *testing.T) {
	box := newInbox(t)
	rt, journal := newJournalledRuntime(t, &fakeDelegate{}, fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: box.path}})

	ctx := msgorigin.WithSender(context.Background(), "agent-orchestrator-7")
	report, err := sendAndCollect(ctx, t, rt, journal, "over the socket")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if report.Path != msgdelivery.PathSocket {
		t.Fatalf("path = %q, want socket", report.Path)
	}
	if report.Reason != "" {
		t.Fatalf("reason = %q; a socket delivery IS the reason", report.Reason)
	}
	if report.Sender != "agent-orchestrator-7" || !report.NameOnWire {
		t.Fatalf("report lost the sender that travelled: %+v", report)
	}
	// The msg_id is what lets this record be matched against the receiving
	// agent's own transcript, which is the only way to check the claim.
	if report.MsgID == "" {
		t.Fatal("a socket delivery must carry the frame's msg_id")
	}
	if entry := journal.only(t); entry.MsgID != report.MsgID || entry.Bytes == 0 {
		t.Fatalf("journal lost the frame's identity: %+v", entry)
	}
}

// Every fallback reason the transport can produce must reach the caller and the
// record VERBATIM. This is the whole feature: the reason already existed and
// was thrown away.
func TestFallbackReportsThePaneAndTheRealReason(t *testing.T) {
	tests := []struct {
		name     string
		registry Registry
		env      string
		want     string
	}{
		{name: "no descriptor", registry: fakeRegistry{err: reject("no-descriptor")}, want: "no-descriptor"},
		{name: "unsupported protocol", registry: fakeRegistry{err: reject("unsupported-peer-protocol")}, want: "unsupported-peer-protocol"},
		{name: "bypass permissions", registry: fakeRegistry{err: reject("bypass-permissions-session")}, want: "bypass-permissions-session"},
		{name: "socket missing", registry: fakeRegistry{err: reject("socket-missing")}, want: "socket-missing"},
		{name: "disabled by env", env: "0", want: "disabled-by-env"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			box := newInbox(t)
			registry := tc.registry
			if registry == nil {
				registry = fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: box.path}}
			}
			delegate := &fakeDelegate{}
			rt, journal := newJournalledRuntime(t, delegate, registry)
			if tc.env != "" {
				t.Setenv(modeEnv, tc.env)
			}

			report, err := sendAndCollect(context.Background(), t, rt, journal, "fall back")
			if err != nil {
				t.Fatalf("SendMessage: %v", err)
			}
			if report.Path != msgdelivery.PathPane {
				t.Fatalf("path = %q, want pane", report.Path)
			}
			if report.Reason != tc.want {
				t.Fatalf("reason = %q, want the transport's own %q", report.Reason, tc.want)
			}
			if got := delegate.messages(); len(got) != 1 {
				t.Fatalf("the pane got %q, want exactly one copy", got)
			}
		})
	}
}

// A pane path is reported only once the pane send has actually run - a send
// that failed there delivered nothing, and must not be recorded as delivered.
func TestAFailedPaneSendIsNotReportedAsDelivered(t *testing.T) {
	delegate := &fakeDelegate{err: errors.New("tmux is gone")}
	rt, journal := newJournalledRuntime(t, delegate, fakeRegistry{err: reject("no-descriptor")})

	report, err := sendAndCollect(context.Background(), t, rt, journal, "nowhere to go")
	if err == nil {
		t.Fatal("want the pane failure surfaced to the caller")
	}
	if report.Path != msgdelivery.PathNone {
		t.Fatalf("path = %q, want none: nothing was delivered", report.Path)
	}
	if report.Reason != "no-descriptor" || !strings.Contains(report.Error, "tmux is gone") {
		t.Fatalf("report lost why it fell back or why it then failed: %+v", report)
	}
}

// The guard mirror still runs, and a message it would have dropped is reported
// as taking the pane, for that reason.
func TestGuardFallbackIsReportedWithItsOwnReason(t *testing.T) {
	box := newInbox(t)
	delegate := &fakeDelegate{}
	rt, journal := newJournalledRuntime(t, delegate, fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: box.path}})

	if err := rt.SendMessage(context.Background(), ports.RuntimeHandle{ID: "ao-1"}, "same again"); err != nil {
		t.Fatalf("first SendMessage: %v", err)
	}
	journal.mu.Lock()
	journal.entries = nil
	journal.mu.Unlock()

	report, err := sendAndCollect(context.Background(), t, rt, journal, "same again")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if report.Path != msgdelivery.PathPane || report.Reason != "receiver-guard-would-drop" {
		t.Fatalf("report = %+v, want the pane for the guard's own reason", report)
	}
}

// KNOWN AND CORRECT: a message whose own body contains the receiver's envelope
// markup is sent WITHOUT the envelope, because wrapping it would fail the
// receiver's byte-for-byte re-serialisation and leak markup to a human. It is
// reported, so nobody reading the record mistakes it for a regression.
func TestADroppedSenderNameSaysWhy(t *testing.T) {
	box := newInbox(t)
	rt, journal := newJournalledRuntime(t, &fakeDelegate{}, fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: box.path}})

	ctx := msgorigin.WithSender(context.Background(), "agent-orchestrator-7")
	report, err := sendAndCollect(ctx, t, rt, journal, "quoting a <"+envelopeTag+"> block")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if report.Path != msgdelivery.PathSocket {
		t.Fatalf("path = %q; the message still goes over the socket, just unnamed", report.Path)
	}
	if report.NameOnWire {
		t.Fatal("the name cannot have travelled: the body would break the round trip")
	}
	if report.NameDropped != "body-contains-envelope-markup" {
		t.Fatalf("nameDropped = %q, want the real reason", report.NameDropped)
	}
	if entry := journal.only(t); entry.NameDropped != report.NameDropped {
		t.Fatalf("journal lost why the name was dropped: %+v", entry)
	}
}

// ---- strict mode ---------------------------------------------------------

// Strict exists for someone hunting fallbacks: it turns "this was quietly typed
// at me" into an error, and types NOTHING.
func TestStrictModeFailsInsteadOfTypingIntoThePane(t *testing.T) {
	delegate := &fakeDelegate{}
	rt, journal := newJournalledRuntime(t, delegate, fakeRegistry{err: reject("no-descriptor")})
	t.Setenv(modeEnv, "strict")

	report, err := sendAndCollect(context.Background(), t, rt, journal, "socket or nothing")
	if err == nil {
		t.Fatal("want strict mode to fail the send")
	}
	if !strings.Contains(err.Error(), "no-descriptor") {
		t.Fatalf("the error must name the real reason, got %v", err)
	}
	if !strings.Contains(err.Error(), modeEnv+"=strict") {
		t.Fatalf("the error must say which switch refused the fallback, got %v", err)
	}
	// The layers above recognise this without importing a runtime adapter, which
	// is what turns it into a refusal the sender reads instead of a 500.
	if !errors.Is(err, msgdelivery.ErrNotDelivered) {
		t.Fatalf("a send that delivered nothing must say so: %v", err)
	}
	if got := delegate.messages(); len(got) != 0 {
		t.Fatalf("strict mode typed %q into the pane; it must deliver nothing", got)
	}
	if report.Path != msgdelivery.PathNone || report.Reason != "no-descriptor" {
		t.Fatalf("report = %+v, want nothing delivered, for the transport's own reason", report)
	}
}

// Strict changes only what happens when the socket CANNOT be used.
func TestStrictModeStillDeliversOverTheSocket(t *testing.T) {
	box := newInbox(t)
	delegate := &fakeDelegate{}
	rt, journal := newJournalledRuntime(t, delegate, fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: box.path}})
	t.Setenv(modeEnv, "strict")

	report, err := sendAndCollect(context.Background(), t, rt, journal, "strictly over the socket")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if report.Path != msgdelivery.PathSocket {
		t.Fatalf("path = %q, want socket", report.Path)
	}
	if got := box.userMessages(t, 1); len(got) != 1 {
		t.Fatalf("socket received %q, want the message", got)
	}
}

// The DEFAULT must not move. An unset switch - and any value that is neither
// the kill switch nor strict - keeps prefer-socket-fall-back-to-pane, because a
// forced socket would make every message vanish the day the protocol changes.
func TestModeDefaultsToFallingBack(t *testing.T) {
	for _, value := range []string{"", "1", "true", "yes", "STRICTLY", "on"} {
		t.Setenv(modeEnv, value)
		if got := envMode(); got != modeAuto {
			t.Fatalf("%s=%q resolved to %v, want the fall-back default", modeEnv, value, got)
		}
	}
	for _, value := range []string{"0", "false", "FALSE", "no"} {
		t.Setenv(modeEnv, value)
		if got := envMode(); got != modeOff {
			t.Fatalf("%s=%q resolved to %v, want pane only", modeEnv, value, got)
		}
	}
	for _, value := range []string{"strict", "STRICT", " strict "} {
		t.Setenv(modeEnv, value)
		if got := envMode(); got != modeStrict {
			t.Fatalf("%s=%q resolved to %v, want strict", modeEnv, value, got)
		}
	}
}

// A runtime built without a journal must still deliver: the record is an
// observation of the send, never a condition of it.
func TestSendWorksWithoutAJournal(t *testing.T) {
	box := newInbox(t)
	rt := newTestRuntime(t, &fakeDelegate{}, fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: box.path}})
	if err := rt.SendMessage(context.Background(), ports.RuntimeHandle{ID: "ao-1"}, "unrecorded"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got := box.userMessages(t, 1); len(got) != 1 {
		t.Fatalf("socket received %q, want the message", got)
	}
}

// ---- pinning one send to one wire ----------------------------------------

// The daemon-wide switch is read from the DAEMON's environment, so an operator
// typing `AO_CLAUDE_NATIVE_SEND=0 ao send ...` changes nothing at all. A wire on
// the context is the reachable version, and it must actually take effect - and
// be visible afterwards as its own reason, so nobody restarts a daemon over a
// flag they typed themselves.
func TestAPerSendWireCanPinTheMessageToThePane(t *testing.T) {
	box := newInbox(t)
	delegate := &fakeDelegate{}
	rt, journal := newJournalledRuntime(t, delegate, fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: box.path}})

	ctx := msgdelivery.WithWire(context.Background(), msgdelivery.WirePane)
	report, err := sendAndCollect(ctx, t, rt, journal, "type this at me")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if report.Path != msgdelivery.PathPane || report.Reason != "disabled-by-flag" {
		t.Fatalf("report = %+v, want the pane for the flag's own reason", report)
	}
	if got := delegate.messages(); len(got) != 1 || got[0] != "type this at me" {
		t.Fatalf("the pane got %q, want exactly the message", got)
	}
	if got := box.userMessages(t, 0); len(got) != 0 {
		t.Fatalf("the socket got %q; --pane-only must never touch it", got)
	}
	// The record has to say the wire was FORCED, or a reader cannot tell this
	// line apart from a socket that was merely unavailable.
	if entry := journal.only(t); entry.Wire != msgdelivery.WirePane {
		t.Fatalf("journal lost the wire the caller demanded: %+v", entry)
	}
}

// The per-send strict override, and the one thing it must never do: type.
func TestAPerSendWireCanRefuseThePaneFallback(t *testing.T) {
	delegate := &fakeDelegate{}
	rt, journal := newJournalledRuntime(t, delegate, fakeRegistry{err: reject("no-descriptor")})

	ctx := msgdelivery.WithWire(context.Background(), msgdelivery.WireSocket)
	report, err := sendAndCollect(ctx, t, rt, journal, "socket or nothing")
	if err == nil {
		t.Fatal("want the send to fail rather than fall back")
	}
	if !errors.Is(err, msgdelivery.ErrNotDelivered) {
		t.Fatalf("a send that delivered nothing must say so: %v", err)
	}
	if !strings.Contains(err.Error(), "--socket-only") {
		t.Fatalf("the error must name the control that refused the fallback, got %v", err)
	}
	if strings.Contains(err.Error(), modeEnv) {
		t.Fatalf("the error blames the daemon's environment for the caller's own flag: %v", err)
	}
	if !strings.Contains(err.Error(), "no-descriptor") {
		t.Fatalf("the error must name the real reason, got %v", err)
	}
	if got := delegate.messages(); len(got) != 0 {
		t.Fatalf("the pane was typed into anyway: %q", got)
	}
	if report.Path != msgdelivery.PathNone || report.Reason != "no-descriptor" {
		t.Fatalf("report = %+v, want nothing delivered, for the transport's own reason", report)
	}
	if entry := journal.only(t); entry.Wire != msgdelivery.WireSocket || entry.Path != msgdelivery.PathNone {
		t.Fatalf("journal lost what was demanded or what happened: %+v", entry)
	}
}

// A per-send wire is an override, so it wins over the daemon-wide switch in
// BOTH directions - and it is not sticky: it governs the send it rode in on.
func TestAPerSendWireOverridesTheDaemonWideSwitch(t *testing.T) {
	t.Run("pane-only beats a strict daemon", func(t *testing.T) {
		box := newInbox(t)
		delegate := &fakeDelegate{}
		rt, journal := newJournalledRuntime(t, delegate, fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: box.path}})
		t.Setenv(modeEnv, "strict")

		ctx := msgdelivery.WithWire(context.Background(), msgdelivery.WirePane)
		report, err := sendAndCollect(ctx, t, rt, journal, "the pane, please")
		if err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
		if report.Path != msgdelivery.PathPane || report.Reason != "disabled-by-flag" {
			t.Fatalf("report = %+v, want the flag to win", report)
		}
	})
	t.Run("socket-only beats a disabled daemon", func(t *testing.T) {
		box := newInbox(t)
		delegate := &fakeDelegate{}
		rt, journal := newJournalledRuntime(t, delegate, fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: box.path}})
		t.Setenv(modeEnv, "0")

		ctx := msgdelivery.WithWire(context.Background(), msgdelivery.WireSocket)
		report, err := sendAndCollect(ctx, t, rt, journal, "the socket, please")
		if err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
		if report.Path != msgdelivery.PathSocket {
			t.Fatalf("report = %+v, want the flag to win", report)
		}
		if got := delegate.messages(); len(got) != 0 {
			t.Fatalf("the pane got %q; the flag asked for the socket", got)
		}
	})
	t.Run("nothing demanded leaves the daemon in charge", func(t *testing.T) {
		box := newInbox(t)
		delegate := &fakeDelegate{}
		rt, journal := newJournalledRuntime(t, delegate, fakeRegistry{session: Session{PID: 1, SessionID: "s1", SocketPath: box.path}})
		t.Setenv(modeEnv, "0")

		report, err := sendAndCollect(context.Background(), t, rt, journal, "whatever the daemon says")
		if err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
		if report.Path != msgdelivery.PathPane || report.Reason != "disabled-by-env" {
			t.Fatalf("report = %+v, want the daemon's own switch, named as such", report)
		}
		if entry := journal.only(t); entry.Wire != msgdelivery.WireAuto {
			t.Fatalf("journal invented a demand nobody made: %+v", entry)
		}
	})
}

// modeFor is the whole resolution in one place: the per-send wire first, the
// daemon's environment otherwise, and the control named either way.
func TestModeForNamesTheControlThatDecided(t *testing.T) {
	t.Setenv(modeEnv, "")
	tests := []struct {
		name        string
		ctx         context.Context
		env         string
		wantMode    sendMode
		wantControl string
	}{
		{name: "nothing set", ctx: context.Background(), wantMode: modeAuto, wantControl: modeEnv + "="},
		{name: "env off", ctx: context.Background(), env: "0", wantMode: modeOff, wantControl: modeEnv + "=0"},
		{name: "env strict", ctx: context.Background(), env: "strict", wantMode: modeStrict, wantControl: modeEnv + "=strict"},
		{
			name:     "wire pane",
			ctx:      msgdelivery.WithWire(context.Background(), msgdelivery.WirePane),
			env:      "strict",
			wantMode: modeOff, wantControl: "--pane-only",
		},
		{
			name:     "wire socket",
			ctx:      msgdelivery.WithWire(context.Background(), msgdelivery.WireSocket),
			env:      "0",
			wantMode: modeStrict, wantControl: "--socket-only",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(modeEnv, tc.env)
			gotMode, gotControl := modeFor(tc.ctx)
			if gotMode != tc.wantMode || gotControl != tc.wantControl {
				t.Fatalf("modeFor = %v/%q, want %v/%q", gotMode, gotControl, tc.wantMode, tc.wantControl)
			}
		})
	}
}

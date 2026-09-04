// Package msgdelivery says which wire a message to a session actually
// travelled, and keeps that answer where a human can read it later.
//
// AO has two ways to put a message in front of a claude-code agent: the
// session's own messaging socket (internal/adapters/runtime/claudepeer) or
// typing it into the tmux pane. Which one a given message took is decided deep
// in the transport, on facts only the transport has - and it is exactly the
// question asked hours later, about a message nobody watched being sent.
//
// The rule this package exists to enforce: the reason reported is the one the
// TRANSPORT produced. Nothing here re-derives it, and no layer above may guess
// at one, because a plausible wrong reason is worse than no reason - it will be
// believed. A send nothing reported on is recorded as unreported, not as a
// pane send.
//
// Two directions, both flowing from the transport outward:
//
//   - the CALLER's answer: a collector on the context, so `ao send` can print
//     the path it took while the human is still looking at the terminal.
//   - the DURABLE answer: an append-only JSON Lines journal under the data dir,
//     because the message delivered at 02:00 is the one that needs explaining
//     at 09:00.
package msgdelivery

import (
	"context"
	"sync"
	"time"
)

// Path is the wire a message travelled.
type Path string

const (
	// PathSocket means the framed bytes were COMPLETELY written to the
	// claude-code session's own messaging socket. It is never reported for
	// anything less than a complete write, because a partial frame delivers
	// nothing.
	PathSocket Path = "socket"
	// PathPane means the message was typed into the session's terminal pane.
	PathPane Path = "pane"
	// PathNone means nothing was delivered. Only strict mode produces this - it
	// refuses the pane fallback and fails the send instead.
	PathNone Path = "none"
)

// Triggers name WHY a message was sent. They are caller-supplied context, never
// a stand-in for the transport's reason, and they exist so the journal can tell
// a human's `ao send` apart from the three kinds of message nobody types.
const (
	TriggerSend            = "send"             // ao send / the app's send box / crew send
	TriggerQueueDrain      = "queue-drain"      // a held message delivered once the agent was listening
	TriggerNudge           = "nudge"            // lifecycle: CI failed, review posted, merge conflict
	TriggerSmokeReport     = "smoke-report"     // the Tests tab reporting results back
	TriggerReviewNotify    = "review-notify"    // the reviewer pane being handed its brief
	TriggerCrewNotice      = "crew-notice"      // AO's own housekeeping notices to a session
	TriggerCommentDispatch = "comment-dispatch" // a review thread forwarded to the worker
)

// Report is what the transport observed about one send. Every field is the
// transport's own account of what it did.
type Report struct {
	// Path is the wire the message actually took.
	Path Path
	// Reason is the transport's short, stable word for why. Empty on a plain
	// socket delivery, where the path IS the reason.
	Reason string
	// Sender is the display name AO asserted on the wire, empty when none was.
	Sender string
	// NameOnWire is true when that name actually travelled inside the envelope.
	// A name can be known and still not travel - see NameDropped.
	NameOnWire bool
	// NameDropped names why a known sender was left off the wire. The one that
	// fires in practice is a body that itself contains the envelope markup:
	// wrapping it would fail the receiver's byte-for-byte re-serialisation and
	// leak markup to a human, so the message goes out unwrapped on purpose.
	NameDropped string
	// MsgID is the frame's msg_id, which the receiver records alongside the
	// message. It is what lets a persisted line here be matched against the
	// receiving agent's own transcript.
	MsgID string
	// Bytes is the size of the framed message, socket path only.
	Bytes int
	// Error is the failure that ended the send, when one did.
	Error string
}

// Origin is what the CALLER knows about a send: who it is for, and why it is
// happening. The transport knows neither - it sees a runtime handle - so this
// rides the context beside the message.
type Origin struct {
	// Session is the AO session the message is addressed to.
	Session string
	// Trigger is one of the Trigger constants.
	Trigger string
}

type originKey struct{}

// WithOrigin returns a context naming who a message is for and why it is being
// sent. Callers set it; the transport reads it when it records.
func WithOrigin(ctx context.Context, origin Origin) context.Context {
	return context.WithValue(ctx, originKey{}, origin)
}

// OriginOf reports the origin set on ctx, or the zero Origin. A zero origin is
// recorded as-is rather than filled in with a guess: an unattributed send is a
// real thing to know about.
func OriginOf(ctx context.Context) Origin {
	origin, _ := ctx.Value(originKey{}).(Origin)
	return origin
}

// Collector receives the transport's report for one send.
type Collector struct {
	mu     sync.Mutex
	report Report
	got    bool
}

type collectorKey struct{}

// WithCollector returns a context the transport will report into, and the
// collector to read the answer from once the send returns.
func WithCollector(ctx context.Context) (context.Context, *Collector) {
	c := &Collector{}
	return context.WithValue(ctx, collectorKey{}, c), c
}

// report hands the transport's account of a send to whoever asked for it. It is
// a no-op when nobody did, so a transport can call it unconditionally.
//
// The FIRST report wins. A send that falls back reports once, at the end, with
// the path it really took; this only guards against a wrapper reporting over
// the transport that actually did the work.
func report(ctx context.Context, r Report) {
	c, _ := ctx.Value(collectorKey{}).(*Collector)
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.got {
		return
	}
	c.report = r
	c.got = true
}

// Collected reports what the transport said, and whether it said anything at
// all. "Nothing reported" is a distinct answer, not an empty path: it means no
// transport on this platform accounted for the send, and the caller must say so
// rather than invent a path.
func (c *Collector) Collected() (Report, bool) {
	if c == nil {
		return Report{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.report, c.got
}

// Entry is one persisted delivery record: the caller's origin plus the
// transport's report, stamped with a time.
type Entry struct {
	At      time.Time `json:"at"`
	Session string    `json:"session,omitempty"`
	// Handle is the runtime handle the message was addressed to (the tmux
	// session), kept because it is what the transport itself resolved against.
	Handle      string `json:"handle,omitempty"`
	Trigger     string `json:"trigger,omitempty"`
	Path        Path   `json:"path"`
	Reason      string `json:"reason,omitempty"`
	Sender      string `json:"sender,omitempty"`
	NameOnWire  bool   `json:"nameOnWire,omitempty"`
	NameDropped string `json:"nameDropped,omitempty"`
	MsgID       string `json:"msgId,omitempty"`
	Bytes       int    `json:"bytes,omitempty"`
	Error       string `json:"error,omitempty"`
}

// Journal persists delivery records. A nil Journal is a valid one that keeps
// nothing, so a transport can be built without one in tests.
type Journal interface {
	Append(e Entry) error
}

// Record writes one send to the journal and hands it to the caller's collector,
// in one call, so a transport cannot do half of it. journal may be nil.
//
// It takes the Report by value and does not touch it: this is where the
// transport's account is passed through unchanged, and the only place the two
// halves - what the caller knew and what the transport saw - are joined.
func Record(ctx context.Context, journal Journal, handle string, r Report) {
	report(ctx, r)
	if journal == nil {
		return
	}
	origin := OriginOf(ctx)
	_ = journal.Append(Entry{
		Session:     origin.Session,
		Handle:      handle,
		Trigger:     origin.Trigger,
		Path:        r.Path,
		Reason:      r.Reason,
		Sender:      r.Sender,
		NameOnWire:  r.NameOnWire,
		NameDropped: r.NameDropped,
		MsgID:       r.MsgID,
		Bytes:       r.Bytes,
		Error:       r.Error,
	})
}

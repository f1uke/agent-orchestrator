package domain

import "time"

// ActivityEventKind names what an activity-feed frame reports.
//
// The tool kinds come from an agent's per-tool hooks: a PreToolUse fires
// tool_start, and the completion fires tool_end or tool_failed (harnesses that
// distinguish failure fire the latter INSTEAD of the former). "activity" carries
// only a coarse level change with no detail. "message" reports that a message
// was just accepted FOR this session (ao send, either direction — the sender is
// already encoded in the text).
type ActivityEventKind string

// The activity-feed event kinds.
const (
	ActivityEventToolStart  ActivityEventKind = "tool_start"
	ActivityEventToolEnd    ActivityEventKind = "tool_end"
	ActivityEventToolFailed ActivityEventKind = "tool_failed"
	ActivityEventActivity   ActivityEventKind = "activity"
	ActivityEventMessage    ActivityEventKind = "message"
)

// ActivityCoarse is the coarse truth an activity detail decays TO: the honest,
// low-resolution answer that survives long after a per-tool line has gone stale.
// It is deliberately smaller than SessionStatus — the feed reports only what a
// hook actually observed, never the status deriver's timeout guesses.
type ActivityCoarse string

// The coarse levels. An empty ActivityCoarse on an event means "this event does
// not change the coarse level" — the consumer keeps whatever it had.
const (
	CoarseWorking ActivityCoarse = "working"
	CoarseWaiting ActivityCoarse = "waiting"
	CoarseIdle    ActivityCoarse = "idle"
	CoarseExited  ActivityCoarse = "exited"
)

// Detail TTLs: how long a fine-grained line may be presented as CURRENTLY TRUE.
//
// These are what stop the bubble lying. A tool_start is normally superseded by
// its own completion within milliseconds-to-seconds, so its TTL only matters
// when the completion never lands (a crashed agent, a dropped hook); it is kept
// short enough that "Running the test suite" cannot outlive the run by much.
// The completion kinds are past tense, so a brief window is truthful by
// construction.
const (
	ToolStartDetailTTL  = 20 * time.Second
	ToolEndDetailTTL    = 8 * time.Second
	ToolFailedDetailTTL = 12 * time.Second
	MessageDetailTTL    = 12 * time.Second
)

// Coarse TTLs mirror the status deriver's own graces
// (service/session/status.go), so the feed stops claiming a level at exactly the
// moment AO itself stops believing it. Past the TTL the consumer is at
// "unknown", which is the truthful answer and a renderable state — NOT a guess
// the feed invented.
const (
	// CoarseWorkingTTL mirrors activeStaleGrace: past it, an unrefreshed
	// "active" means the feed died, not that the agent is busy.
	CoarseWorkingTTL = 10 * time.Minute
	// CoarseIdleTTL mirrors waitingInputGrace: past it AO would promote the
	// idle to needs-input, which is a timeout GUESS the feed must not make.
	CoarseIdleTTL = 45 * time.Second
)

// ActivityDetail is the curated, whitelisted description of one agent action.
// Every field has passed a per-tool whitelist plus sanitisation in the hook
// process (see adapters/agent/toolcurate) BEFORE it is transmitted — raw agent
// payloads never reach the daemon, the store, or a log.
type ActivityDetail struct {
	Kind ActivityEventKind `json:"kind" enum:"tool_start,tool_end,tool_failed,message" description:"What the detail reports."`
	// Tool is the tool name, set only for a tool on the whitelist. An unknown
	// tool (including any MCP tool) contributes nothing at all.
	Tool string `json:"tool,omitempty" description:"Whitelisted tool name; empty for a tool AO does not curate."`
	// Target is the curated noun: a file BASE name, a search pattern, a URL
	// host. Never a full path, never a command, never file content.
	Target string `json:"target,omitempty" description:"Curated target noun (file base name, pattern, URL host)."`
	// Text is a model-authored one-line sentence (a Bash/Task description) or a
	// message's truncated first line.
	Text string `json:"text,omitempty" description:"Curated one-line description of the action."`
}

// IsZero reports whether the detail carries nothing worth transmitting.
func (d ActivityDetail) IsZero() bool {
	return d.Kind == "" && d.Tool == "" && d.Target == "" && d.Text == ""
}

// ActivityEvent is one frame on the activity feed
// (GET /api/v1/activity/stream). It is ephemeral: never stored, never part of
// the CDC pipeline, and droppable under back-pressure.
//
// The truth contract is structural — every event carries BOTH the fine-grained
// detail and the coarse level it decays to, so a consumer cannot render one
// without knowing when to stop:
//
//  1. while now < At+TTLMs        -> the detail (Tool/Target/Text) is current;
//  2. else while the coarse is fresh (CoarseTTLMs == 0 means sticky) -> Coarse;
//  3. else                        -> unknown. Show nothing specific; the
//     session's derived status (GET /sessions/{id}) is the authority for
//     AO's timeout-based readings.
type ActivityEvent struct {
	SessionID SessionID         `json:"sessionId"`
	Kind      ActivityEventKind `json:"kind" enum:"tool_start,tool_end,tool_failed,activity,message"`
	At        time.Time         `json:"at" description:"Daemon clock, UTC. The consumer runs on the same host over loopback."`
	Tool      string            `json:"tool,omitempty"`
	Target    string            `json:"target,omitempty"`
	Text      string            `json:"text,omitempty"`
	// TTLMs bounds how long Tool/Target/Text may be presented as currently
	// true. 0 means the event carries no detail.
	TTLMs int64 `json:"ttlMs" description:"Milliseconds after 'at' that the detail may be shown as currently true. 0 means no detail."`
	// Coarse is the level the detail decays to. Empty means this event does not
	// change the coarse level.
	Coarse ActivityCoarse `json:"coarse,omitempty" enum:"working,waiting,idle,exited"`
	// CoarseTTLMs bounds the coarse level. 0 means sticky: it never decays and
	// is only replaced by a later event.
	CoarseTTLMs int64 `json:"coarseTtlMs" description:"Milliseconds after 'at' that 'coarse' stays true. 0 means sticky (never decays)."`
}

// DetailFreshAt reports whether the event's detail may still be presented as
// what the agent is doing right now.
func (e ActivityEvent) DetailFreshAt(now time.Time) bool {
	if e.TTLMs <= 0 {
		return false
	}
	return now.Before(e.At.Add(time.Duration(e.TTLMs) * time.Millisecond))
}

// CoarseFreshAt reports whether the event's coarse level is still true. A sticky
// level (CoarseTTLMs == 0) is always fresh until a later event replaces it.
func (e ActivityEvent) CoarseFreshAt(now time.Time) bool {
	if e.Coarse == "" {
		return false
	}
	if e.CoarseTTLMs <= 0 {
		return true
	}
	return now.Before(e.At.Add(time.Duration(e.CoarseTTLMs) * time.Millisecond))
}

// DetailTTL is how long a detail of the given kind stays presentable. An
// activity (coarse-only) event has no detail and returns 0.
func DetailTTL(kind ActivityEventKind) time.Duration {
	switch kind {
	case ActivityEventToolStart:
		return ToolStartDetailTTL
	case ActivityEventToolEnd:
		return ToolEndDetailTTL
	case ActivityEventToolFailed:
		return ToolFailedDetailTTL
	case ActivityEventMessage:
		return MessageDetailTTL
	case ActivityEventActivity:
		return 0
	default:
		return 0
	}
}

// CoarseFromActivityState maps a reported activity state onto the coarse level
// and its validity. The sticky states (waiting_input, exited) map to a zero TTL:
// a pending prompt is pending until answered, and an exit is terminal.
func CoarseFromActivityState(s ActivityState) (ActivityCoarse, time.Duration) {
	switch s {
	case ActivityActive:
		return CoarseWorking, CoarseWorkingTTL
	case ActivityIdle:
		return CoarseIdle, CoarseIdleTTL
	case ActivityWaitingInput:
		return CoarseWaiting, 0
	case ActivityExited:
		return CoarseExited, 0
	default:
		return "", 0
	}
}

// DurationMs converts a duration to the whole milliseconds the wire carries.
func DurationMs(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64(d / time.Millisecond)
}

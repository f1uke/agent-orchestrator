package terminal

import (
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

// The wire protocol is a single multiplexed JSON stream tagged by channel
// ("ch"), mirroring the legacy Node mux server so the existing xterm client can
// connect unchanged. One socket carries every logical stream:
//
//	ch "terminal" — per-pane byte stream, keyed by an opaque runtime handle id
//	ch "subscribe" — the client opts into the session-state channel
//	ch "sessions"  — server-pushed session-state messages (CDC-fed)
//	ch "system"    — liveness; ws-level ping/pong also runs underneath
//	ch "chat"      — a chat session's event log, then its host's live stream,
//	                 keyed by session id
//
// Terminal payloads are base64 in the Data field: PTY output is arbitrary bytes
// and need not be valid UTF-8, which a raw JSON string could not carry.
const (
	chTerminal  = "terminal"
	chSubscribe = "subscribe"
	chSessions  = "sessions"
	chSystem    = "system"
	chChat      = "chat"
)

// client message types (ch "terminal" unless noted).
const (
	msgOpen      = "open" // ch "terminal" or "chat"
	msgData      = "data"
	msgResize    = "resize"
	msgClose     = "close"     // ch "terminal" or "chat"
	msgSubscribe = "subscribe" // ch "subscribe"
	msgPing      = "ping"      // ch "system"
)

// server message types.
const (
	msgOpened   = "opened"
	msgExited   = "exited"
	msgError    = "error"
	msgSnapshot = "snapshot" // ch "sessions"
	msgPong     = "pong"     // ch "system"
	msgEntry    = "entry"    // ch "chat": one log entry
	msgDelta    = "delta"    // ch "chat": a partial-message stream event
	msgLive     = "live"     // ch "chat": replay done, the host stream is attached
	msgOffline  = "offline"  // ch "chat": replay done or host stream ended, no host
)

// clientMsg is one inbound frame. Fields are shared across channels; which are
// populated depends on Ch/Type.
type clientMsg struct {
	Ch   string `json:"ch"`
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
	// Data is base64-encoded keystrokes for ch "terminal" / type "data".
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
	// FromSeq is the last log seq the client holds, for ch "chat" / type "open".
	FromSeq uint64 `json:"fromSeq,omitempty"`
	// BlobRefs asks ch "chat" / type "open" to send stored images as ao_blob
	// references, fetched from the chat blob route, instead of inline base64.
	BlobRefs bool `json:"blobRefs,omitempty"`
}

// serverMsg is one outbound frame.
type serverMsg struct {
	Ch   string `json:"ch"`
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
	// Data is base64-encoded PTY output for ch "terminal" / type "data".
	Data    string           `json:"data,omitempty"`
	Error   string           `json:"error,omitempty"`
	Session *sessionUpdate   `json:"session,omitempty"`
	Entry   *chatproto.Entry `json:"entry,omitempty"`
	// Delta is the raw stream_event line for ch "chat" / type "delta".
	Delta json.RawMessage `json:"delta,omitempty"`
}

// sessionUpdate is the ch "sessions" payload: a single CDC change projected to
// the fields a client needs to refresh its view. It deliberately omits the raw
// change_log payload blob; the client refetches detail over the REST surface.
type sessionUpdate struct {
	Seq       int64  `json:"seq"`
	ProjectID string `json:"projectId"`
	SessionID string `json:"sessionId,omitempty"`
	EventType string `json:"eventType"`
}

package domain

import "time"

// QueuedMessageState is where a queued message stands in its one-shot journey
// from "held because the session could not receive it" to "typed into the
// agent's pane".
//
// There is deliberately no `delivered` state: a delivered row is DELETED, so
// "still here" and "not yet delivered" are the same fact and cannot disagree.
type QueuedMessageState string

const (
	// QueuedMessagePending is waiting for the session to come back.
	QueuedMessagePending QueuedMessageState = "pending"
	// QueuedMessageDelivering is claimed by a deliverer and in flight. The claim
	// is a conditional UPDATE, so exactly one deliverer can hold a row.
	QueuedMessageDelivering QueuedMessageState = "delivering"
	// QueuedMessageFailed will never be delivered: it ran out of attempts, or it
	// was in flight when the daemon died. Kept so the drop is visible instead of
	// silent.
	QueuedMessageFailed QueuedMessageState = "failed"
)

// QueuedMessage is one message held for a session that could not receive it
// when it was sent - today, a session the idle sweep suspended (tmux reaped,
// record and worktree kept).
//
// ID is the SQLite rowid and is the ordering key: SQLite hands out max(rowid)+1,
// so insertion order is total and survives a restart. QueuedAt is for the
// reader (and for expiry), never for ordering, so a clock step cannot reorder
// an inbox.
type QueuedMessage struct {
	ID        int64              `json:"id"`
	SessionID SessionID          `json:"sessionId"`
	Body      string             `json:"body"`
	State     QueuedMessageState `json:"state"`
	Attempts  int                `json:"attempts"`
	LastError string             `json:"lastError,omitempty"`
	QueuedAt  time.Time          `json:"queuedAt"`
	ExpiresAt time.Time          `json:"expiresAt"`
	UpdatedAt time.Time          `json:"updatedAt"`
}

// QueuedMessageCounts is how many messages a session is holding, split by what
// the reader can still expect: Pending will be delivered when the session comes
// back, Failed never will.
type QueuedMessageCounts struct {
	Pending int `json:"pending"`
	Failed  int `json:"failed"`
}

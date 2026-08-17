// Package reclaimlog is the durable audit trail for auto-reclaim.
//
// Auto-reclaim deletes without asking. That is only defensible if the user can
// find out afterwards exactly what went, when, why it qualified and how much it
// freed — so this log is not a debugging aid, it is the half of the feature
// that makes silent deletion recoverable. It records refusals too: knowing that
// a worktree was KEPT, and why, is what tells the user the guards are working.
//
// The format is JSON Lines under the data dir: greppable by eye, parseable by
// tools, and append-only so an interrupted write can damage at most the final
// line, which a reader skips.
package reclaimlog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileName is the log's name under the data dir.
const FileName = "reclaim.jsonl"

// Actions recorded in the log.
const (
	// ActionReclaimed: a worktree was removed and its disk freed.
	ActionReclaimed = "reclaimed"
	// ActionSkipped: a candidate was NOT reclaimed, and Reason says why.
	ActionSkipped = "skipped"
	// ActionAborted: a whole sweep stopped early because something looked wrong
	// enough that continuing to delete would have been reckless.
	ActionAborted = "aborted"
)

// Entry is one recorded reclaim decision.
type Entry struct {
	At     time.Time `json:"at"`
	Action string    `json:"action"`
	// SessionID is empty for an orphan worktree (one no session record owns).
	SessionID string `json:"sessionId,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
	// Branch is recorded so the user can get the work back: reclaim never
	// deletes a branch, so this name is the recovery instruction.
	Branch        string `json:"branch,omitempty"`
	WorkspacePath string `json:"workspacePath,omitempty"`
	// Qualified names WHY it was eligible — the session's display status
	// (merged / terminated), or "orphan" for an unowned worktree.
	Qualified string `json:"qualified,omitempty"`
	// AgeMinutes is how long it had been sitting in that state.
	AgeMinutes int64 `json:"ageMinutes,omitempty"`
	// BytesFreed is the measured size of what was removed.
	BytesFreed int64 `json:"bytesFreed,omitempty"`
	// Reason explains a skip or an abort.
	Reason string `json:"reason,omitempty"`
}

// Writer appends entries to the log file.
type Writer struct {
	path string
	mu   sync.Mutex
}

// New builds a Writer over dir/reclaim.jsonl.
func New(dir string) (*Writer, error) {
	if dir == "" {
		return nil, errors.New("reclaimlog: data dir is required")
	}
	return &Writer{path: filepath.Join(dir, FileName)}, nil
}

// Path is where the log lives, so the daemon can tell the user.
func (w *Writer) Path() string { return w.path }

// Append writes one entry.
//
// The open is O_APPEND, so concurrent writers interleave whole lines rather
// than overwriting each other, and a crash mid-sweep leaves the entries already
// written intact. The write is a single call with the newline included: a
// partially written final line is possible on a hard kill but cannot corrupt
// any earlier line.
func (w *Writer) Append(e Entry) error {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("reclaimlog: marshal: %w", err)
	}
	line = append(line, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()
	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("reclaimlog: open: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("reclaimlog: write: %w", err)
	}
	return nil
}

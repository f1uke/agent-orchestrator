package msgdelivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileName is the journal's name under the data dir. JSON Lines: greppable by
// eye, parseable by tools, and append-only so an interrupted write can damage
// at most the final line, which a reader skips.
const FileName = "message-delivery.jsonl"

// RotatedSuffix is appended to the previous generation of the journal.
const RotatedSuffix = ".1"

// maxFileBytes is when the journal rolls over. Messages are far more frequent
// than the events in reclaim.jsonl, so this file cannot be append-forever: at
// the bound the current file becomes <name>.1 (replacing the previous one) and
// a fresh one starts. Two generations is the right trade here - the question
// this answers is "what happened to that message earlier today", and roughly
// 8 MiB of records reaches much further back than that.
const maxFileBytes = 4 << 20

// FileJournal appends delivery records to a rolling file under the data dir.
//
// It is deliberately dumb: one open-append-close per record, guarded by a
// mutex. The write rate is one line per message delivered to an agent, which is
// nowhere near a rate that would justify holding a file handle open across the
// daemon's whole life and having to reason about reopening it after a rotation
// or an external truncate.
type FileJournal struct {
	path string
	mu   sync.Mutex
}

// NewFileJournal builds a journal over dir/message-delivery.jsonl. dir must be
// the daemon's data dir, so the file lands under ~/.ao (or AO_DATA_DIR) like
// every other piece of app state.
func NewFileJournal(dir string) (*FileJournal, error) {
	if dir == "" {
		return nil, errors.New("msgdelivery: data dir is required")
	}
	return &FileJournal{path: filepath.Join(dir, FileName)}, nil
}

// Path is where the journal lives, so the daemon can tell a human where to look.
func (j *FileJournal) Path() string { return j.path }

// Append writes one record as a single line.
//
// The open is O_APPEND and the line is written in one call, so concurrent
// writers interleave whole lines and a hard kill can leave at most a truncated
// final line.
func (j *FileJournal) Append(e Entry) error {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("msgdelivery: marshal: %w", err)
	}
	line = append(line, '\n')

	j.mu.Lock()
	defer j.mu.Unlock()
	j.rotateIfFull(len(line))
	f, err := os.OpenFile(j.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("msgdelivery: open: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("msgdelivery: write: %w", err)
	}
	return nil
}

// rotateIfFull moves the journal aside once it would pass the size bound.
//
// Every failure here is swallowed on purpose: a journal that cannot rotate must
// still record, because the record is the point and an unbounded file is the
// lesser problem of the two. Callers hold j.mu.
func (j *FileJournal) rotateIfFull(incoming int) {
	info, err := os.Stat(j.path)
	if err != nil || info.Size()+int64(incoming) <= maxFileBytes {
		return
	}
	_ = os.Rename(j.path, j.path+RotatedSuffix)
}

// Package daemonlog keeps the daemon's own log on disk, under the data dir.
//
// The daemon logs to stderr, and when the desktop app owns it stderr goes to
// the Electron main process and nowhere else: once that process exits, every
// line is gone. That is how an orchestrator restart that failed with
// INTERNAL_ERROR could not be traced afterwards even though the daemon had
// logged the cause. This package adds a rotating file beside the other app
// state, so a request id from an error envelope can be grepped for later.
package daemonlog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// DirName is the log directory under the daemon's data dir.
const DirName = "logs"

// FileName is the current log file in DirName. Older generations are
// FileName.1 (newest) through FileName.<Generations>.
const FileName = "daemon.log"

const (
	// MaxFileBytes is when the current file rolls over.
	MaxFileBytes = 10 << 20
	// Generations is how many rolled files are kept besides the current one.
	// Six files of 10 MiB reach back days on a busy machine, which is the
	// window "what happened to that request this morning" needs.
	Generations = 5
)

// Path is where the current log file lives for a data dir.
func Path(dataDir string) string { return filepath.Join(dataDir, DirName, FileName) }

// RotatingFile is an io.Writer over a size-bounded log file. Each Write lands
// whole in one file - a line is never split across a rotation - because the
// size check runs before the write.
type RotatingFile struct {
	path        string
	maxBytes    int64
	generations int

	mu   sync.Mutex
	f    *os.File
	size int64
}

// Open opens (creating as needed) the log file for dataDir, appending to what
// an earlier run left there.
func Open(dataDir string) (*RotatingFile, error) {
	if dataDir == "" {
		return nil, errors.New("daemonlog: data dir is required")
	}
	return open(Path(dataDir), MaxFileBytes, Generations)
}

func open(path string, maxBytes int64, generations int) (*RotatingFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("daemonlog: create log dir: %w", err)
	}
	r := &RotatingFile{path: path, maxBytes: maxBytes, generations: generations}
	if err := r.openCurrent(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *RotatingFile) openCurrent() error {
	f, err := os.OpenFile(r.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("daemonlog: open: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("daemonlog: stat: %w", err)
	}
	r.f, r.size = f, info.Size()
	return nil
}

// Path is the current log file.
func (r *RotatingFile) Path() string { return r.path }

// Write appends p, rolling the file over first when p would take it past the
// bound. A failed rotation keeps writing to the current file: the log is the
// point, and an oversized file is the lesser problem.
func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f != nil && r.size > 0 && r.size+int64(len(p)) > r.maxBytes {
		r.rotate()
	}
	if r.f == nil {
		if err := r.openCurrent(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// rotate shifts daemon.log.N-1 -> .N ... daemon.log -> .1; the caller's Write
// then opens a fresh file (or, if a rename failed, reopens the old one). The
// oldest generation is overwritten by the shift. Callers hold r.mu.
func (r *RotatingFile) rotate() {
	_ = r.f.Close()
	r.f, r.size = nil, 0
	for i := r.generations - 1; i >= 1; i-- {
		_ = os.Rename(r.path+"."+strconv.Itoa(i), r.path+"."+strconv.Itoa(i+1))
	}
	_ = os.Rename(r.path, r.path+".1")
}

// Close closes the current file.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// Tee writes every Write to each destination and never lets one destination's
// failure starve another. io.MultiWriter stops at the first error, so an
// app-owned daemon whose stderr pipe broke when the app quit would also stop
// writing its log file - the one place left to read.
func Tee(dsts ...io.Writer) io.Writer { return tee(dsts) }

type tee []io.Writer

func (t tee) Write(p []byte) (int, error) {
	var firstErr error
	for _, w := range t {
		if _, err := w.Write(p); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil && len(t) == 1 {
		return 0, firstErr
	}
	return len(p), nil
}

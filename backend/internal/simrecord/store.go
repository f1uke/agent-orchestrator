package simrecord

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simflow"
)

// ErrNotFound is a flow that is not in this session's artifact directory.
var ErrNotFound = errors.New("simrecord: no such recording")

// ErrInvalidName is a file name that does not name a flow in a session's own
// directory - anything with a path separator in it, anything that is not a
// .yaml file, and the two directory entries that are not files at all.
var ErrInvalidName = errors.New("simrecord: not a recording file name")

// SessionDir is where a session's artifacts live.
//
// Flows and screenshots are separated because they are used differently: a
// screenshot is looked at once and forgotten, a flow is found again later and
// handed to somebody. In one flat directory - which is what this was - a
// session with a morning of screenshots in it buries every flow it recorded,
// and finding "the one that reaches the portfolio screen" means opening files
// one at a time.
//
// ⚠ This is per SESSION, and a recording is meant to outlive the session that
// made it: the point of recording a path by hand is to hand it to a worker
// later. Moving these under the project is the right shape and is deliberately
// NOT done here - it is a data move that needs a migration and a decision
// about the files already on disk.
func SessionDir(dataDir, sessionID string) string { return filepath.Join(dataDir, "sim", sessionID) }

// FlowsDir is where flows recorded from now on are written.
func FlowsDir(dataDir, sessionID string) string {
	return filepath.Join(SessionDir(dataDir, sessionID), "flows")
}

// ShotsDir is where `ao sim shot` writes from now on.
func ShotsDir(dataDir, sessionID string) string {
	return filepath.Join(SessionDir(dataDir, sessionID), "shots")
}

// Flow is one recorded flow on disk, as a list needs to describe it.
//
// There is deliberately nothing here about what the flow CONTAINS beyond its
// counts. A recorded step carries the text that was typed, and a list is the
// wrong place to surface it.
type Flow struct {
	// Name is what a human called it, empty when they have not named it yet.
	// It is read back out of the file name rather than stored beside it: the
	// file name IS the name, so the two can never drift apart and what a
	// person copies is what is actually on disk.
	Name string `json:"name"`
	// FileName is the base name, which is what a human writes in prose.
	FileName string `json:"fileName"`
	// Path is the absolute path, which is what a worker can act on.
	Path string `json:"path"`
	// RecordedAt is when the recording was taken, from the file name; for a
	// file whose name does not carry one, the file's own modification time.
	RecordedAt time.Time `json:"recordedAt"`
	// TimeFromFileName is false when RecordedAt fell back to the modification
	// time, so a caller never presents a guess as a record.
	TimeFromFileName bool `json:"timeFromFileName"`
	// Steps and Review are what the flow says about itself. Known is false for
	// a flow written before flows stated their counts: a list shows that it
	// does not know, rather than showing zero.
	Steps  int   `json:"steps"`
	Review int   `json:"review"`
	Known  bool  `json:"countsKnown"`
	Bytes  int64 `json:"bytes"`
}

// List reports every flow this session has recorded, newest first.
//
// It reads the flows directory AND the session directory itself, because
// flows recorded before the two kinds of artifact were separated are still
// sitting beside the screenshots. Nothing is moved: a path a human already
// pasted into a message to somebody keeps resolving.
func List(dataDir, sessionID string) ([]Flow, error) {
	seen := make(map[string]struct{})
	var flows []Flow
	for _, dir := range []string{FlowsDir(dataDir, sessionID), SessionDir(dataDir, sessionID)} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), flowExt) {
				continue
			}
			if _, dup := seen[entry.Name()]; dup {
				continue
			}
			seen[entry.Name()] = struct{}{}
			flow, err := describe(filepath.Join(dir, entry.Name()))
			if err != nil {
				// One unreadable file must not hide the rest of the list.
				continue
			}
			flows = append(flows, flow)
		}
	}
	sort.Slice(flows, func(i, j int) bool {
		if flows[i].RecordedAt.Equal(flows[j].RecordedAt) {
			return flows[i].FileName > flows[j].FileName
		}
		return flows[i].RecordedAt.After(flows[j].RecordedAt)
	})
	return flows, nil
}

// describe reads one flow file into a Flow.
func describe(path string) (Flow, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Flow{}, err
	}
	base := filepath.Base(path)
	flow := Flow{FileName: base, Path: path, Bytes: info.Size(), RecordedAt: info.ModTime().UTC()}
	if name, at, ok := ParseFileName(base); ok {
		flow.Name, flow.RecordedAt, flow.TimeFromFileName = name, at, true
	}
	// The counts come out of the flow's own header - the numbers the emitter
	// wrote from Choice.NeedsReview - and are never recomputed here. There is
	// one definition of "needs review" and this is not a second one.
	body, err := os.ReadFile(path) //nolint:gosec // path is built from a validated base name inside the session's own directory
	if err != nil {
		return Flow{}, err
	}
	if counts, ok := simflow.ParseCounts(string(body)); ok {
		flow.Steps, flow.Review, flow.Known = counts.Steps, counts.Review, true
	}
	return flow, nil
}

// Resolve turns a base file name into the path it actually occupies, in the
// flows directory or, for an older recording, beside the screenshots.
//
// The validation is the security boundary for delete and rename: a name that
// is not a bare file name is refused outright rather than cleaned up, because
// a caller asking to delete "../../something" is not making a typo.
func Resolve(dataDir, sessionID, fileName string) (string, error) {
	if err := validateFileName(fileName); err != nil {
		return "", err
	}
	for _, dir := range []string{FlowsDir(dataDir, sessionID), SessionDir(dataDir, sessionID)} {
		path := filepath.Join(dir, fileName)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNotFound, fileName)
}

// pathish is every character that could make a name address something other
// than a file inside the directory it is joined to.
//
// The rule is spelled out rather than delegated to filepath.Base, and that is
// deliberate: Base's answer DEPENDS ON THE PLATFORM the daemon happens to be
// running on, so a check built on it is a different check on Windows than on a
// mac - and a name like "C:x.yaml" is a drive-relative path on one and an
// ordinary file name on the other. Naming the characters means the same input
// is refused everywhere, and every clause here is one a test can reach.
//
// ⚠ A mutation check found the previous version of this: it had a
// filepath.Base clause AND a separator clause, and removing the first changed
// nothing any test could see, because on a unix host the two say the same
// thing. That is duplication, not depth.
const pathish = `/\:`

func validateFileName(fileName string) error {
	// There is deliberately no separate clause for "", "." and "..". A
	// mutation check showed one could be removed without any test noticing,
	// and it was right to: none of the three ends in .yaml, so the suffix rule
	// below already refuses all of them. A clause that can never be the reason
	// something is refused is not extra safety, it is a second place to keep
	// correct.
	if strings.ContainsAny(fileName, pathish) {
		return fmt.Errorf("%w: %q addresses a path, not a file in this session's recordings", ErrInvalidName, fileName)
	}
	// ⚠ The extension is a safety rule, not tidiness. Resolve looks in the
	// session directory as well as the flows directory, because that is where
	// recordings made before the two were separated still live - and that
	// directory is also full of `ao sim shot` screenshots. Without this,
	// DELETE .../sim-flows/<a screenshot's name> would delete the screenshot.
	if !strings.HasSuffix(fileName, flowExt) {
		return fmt.Errorf("%w: %q is not a %s file", ErrInvalidName, fileName, flowExt)
	}
	return nil
}

// Write puts an emitted flow in this session's flows directory, under the
// name a human gave the recording.
func Write(dataDir, sessionID, name string, recordedAt time.Time, body string) (Flow, error) {
	dir := FlowsDir(dataDir, sessionID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Flow{}, fmt.Errorf("create flows directory: %w", err)
	}
	path := filepath.Join(dir, FileName(name, recordedAt))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return Flow{}, fmt.Errorf("write flow to %s: %w", path, err)
	}
	return describe(path)
}

// WriteTo puts an emitted flow at an exact path a caller named, for
// `ao sim record stop --out`. The path is resolved to an absolute one by the
// caller that knows which working directory a relative path meant; this side
// refuses to guess.
func WriteTo(path, body string) (Flow, error) {
	if !filepath.IsAbs(path) {
		return Flow{}, fmt.Errorf("%w: %q must be an absolute path", ErrInvalidName, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return Flow{}, fmt.Errorf("create recording directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return Flow{}, fmt.Errorf("write flow to %s: %w", path, err)
	}
	return describe(path)
}

// Delete removes exactly the one flow named, and nothing else.
//
// A recording is a path somebody played through by hand; it cannot be
// regenerated without replaying the whole thing. So this takes one name, never
// a pattern, and it refuses a name that is not a bare file name rather than
// interpreting it.
func Delete(dataDir, sessionID, fileName string) error {
	path, err := Resolve(dataDir, sessionID, fileName)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete %s: %w", fileName, err)
	}
	return nil
}

// Rename gives a recorded flow the name a human decided on afterwards.
//
// Naming is deliberately not asked for while recording: the loop is press
// record, drive the app, press stop, and again - putting a text field in the
// middle of that makes a person stop and compose a name for something they
// have not decided is worth keeping. The name is applied here instead, to the
// one or two recordings that turn out to be the good take.
//
// The RECORDING's timestamp is kept, not the moment of renaming. It is the
// provenance the list sorts by, and re-stamping a file at rename time would
// shuffle it to the top as though it had just been captured.
func Rename(dataDir, sessionID, fileName, newName string) (Flow, error) {
	oldPath, err := Resolve(dataDir, sessionID, fileName)
	if err != nil {
		return Flow{}, err
	}
	var recordedAt time.Time
	if _, at, ok := ParseFileName(fileName); ok {
		recordedAt = at
	} else {
		info, err := os.Stat(oldPath)
		if err != nil {
			return Flow{}, err
		}
		recordedAt = info.ModTime().UTC()
	}

	dir := FlowsDir(dataDir, sessionID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Flow{}, fmt.Errorf("create flows directory: %w", err)
	}
	newPath := filepath.Join(dir, FileName(newName, recordedAt))
	if newPath == oldPath {
		return describe(oldPath)
	}
	if _, err := os.Stat(newPath); err == nil {
		// Only reachable by renaming a flow to the name a DIFFERENT flow
		// recorded in the same millisecond already has, which needs two
		// devices. Refusing is still better than silently replacing a file
		// somebody recorded by hand.
		return Flow{}, fmt.Errorf("a recording called %s already exists", filepath.Base(newPath))
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return Flow{}, fmt.Errorf("rename %s: %w", fileName, err)
	}
	return describe(newPath)
}

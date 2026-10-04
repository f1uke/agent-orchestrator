package apply

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// State is what an applied proposal wrote, as it is on disk now.
type State struct {
	Path    string
	Exists  bool
	Content string
	// IndexPath and IndexLine name the MEMORY.md line the write added ("" when
	// it added none); IndexLinePresent says whether that line is still there.
	IndexPath        string
	IndexLine        string
	IndexLinePresent bool
	// Changed is true when the file (or the index line) is not what AO wrote:
	// edited by hand, by an agent, by another proposal, or removed.
	Changed bool
	// Token names this exact state. An undo or an edit of a changed file goes
	// ahead only when the caller confirms the state it reviewed by its token,
	// so a change made after the review is never overwritten unseen.
	Token string
}

// ChangedError is an undo or edit of a file that is not what AO wrote, without
// a confirmation of the state it is in now.
type ChangedError struct{ State State }

func (e *ChangedError) Error() string {
	return fmt.Sprintf("%s changed after it was written; review the change and confirm to go ahead", e.State.Path)
}

// ErrNothingToRestore is an undo of a changed file whose earlier version AO
// does not have.
var ErrNothingToRestore = errors.New("the version this replaced was not kept, so it cannot be put back")

// Inspect reads what an applied proposal wrote as it is now.
func Inspect(r Roots, p domain.LearnProposal) (State, error) {
	if err := r.allowed(p.TargetPath); err != nil {
		return State{}, err
	}
	st := State{Path: p.TargetPath, IndexLine: p.AppliedIndexLine, IndexLinePresent: true}
	b, err := os.ReadFile(p.TargetPath)
	switch {
	case err == nil:
		st.Exists, st.Content = true, string(b)
	case !errors.Is(err, os.ErrNotExist):
		return State{}, err
	}
	if st.IndexLine != "" {
		st.IndexPath = indexPath(p.TargetPath)
		idx, err := os.ReadFile(st.IndexPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return State{}, err
		}
		st.IndexLinePresent = lineIndex(string(idx), st.IndexLine) >= 0
	}
	st.Changed = !st.Exists || sha(b) != p.AppliedSHA256 || !st.IndexLinePresent
	fileToken := "absent"
	if st.Exists {
		sum := sha256.Sum256(b)
		fileToken = hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\n%t", fileToken, st.IndexLinePresent))
	st.Token = hex.EncodeToString(sum[:16])
	return st, nil
}

// guard reads p's state and refuses to go on when it changed after it was
// written, unless confirm is the token of that state.
func (r Roots) guard(p domain.LearnProposal, confirm string) (State, error) {
	st, err := Inspect(r, p)
	if err != nil {
		return st, err
	}
	if st.Changed && confirm != st.Token {
		return st, &ChangedError{State: st}
	}
	return st, nil
}

// Undo takes back what an applied proposal wrote: a new memory's file and the
// MEMORY.md line it added, or a changed file's earlier version. What it
// replaces or removes is backed up first. It returns the state it undid.
func Undo(r Roots, p domain.LearnProposal, confirm string, now time.Time) (State, error) {
	switch p.Action {
	case domain.LearnProposeCreateMemory:
		st, err := r.guard(p, confirm)
		if err != nil {
			return st, err
		}
		if st.Exists {
			if err := r.backup(p.TargetPath, []byte(st.Content), now); err != nil {
				return st, err
			}
			if err := os.Remove(p.TargetPath); err != nil {
				return st, err
			}
		}
		if st.IndexLine != "" && st.IndexLinePresent {
			if err := r.removeIndexLine(st.IndexPath, st.IndexLine, now); err != nil {
				return st, fmt.Errorf("MEMORY.md: %w", err)
			}
		}
		return st, nil
	case domain.LearnProposeUpdateMemory, domain.LearnProposeUpdateSkill, domain.LearnProposeEditRuleFile:
		before := p.AppliedBefore
		if before == "" {
			var err error
			if before, err = r.backupAt(p.TargetPath, p.DecidedAt); err != nil {
				return State{}, err
			}
		}
		st, err := r.guard(p, confirm)
		if err != nil {
			return st, err
		}
		if st.Exists {
			if err := r.backup(p.TargetPath, []byte(st.Content), now); err != nil {
				return st, err
			}
		}
		return st, writeAtomic(p.TargetPath, []byte(before))
	default:
		return State{}, fmt.Errorf("a %s proposal wrote no file", p.Action)
	}
}

// Rewrite replaces what an applied proposal wrote with the person's edit. It
// returns the hash of the file written.
func Rewrite(r Roots, p domain.LearnProposal, content, confirm string, now time.Time) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "", errors.New("refusing to write an empty file")
	}
	switch p.Action {
	case domain.LearnProposeCreateMemory, domain.LearnProposeUpdateMemory, domain.LearnProposeUpdateSkill, domain.LearnProposeEditRuleFile:
	default:
		return "", fmt.Errorf("a %s proposal wrote no file", p.Action)
	}
	st, err := r.guard(p, confirm)
	if err != nil {
		return "", err
	}
	if st.Exists {
		if err := r.backup(p.TargetPath, []byte(st.Content), now); err != nil {
			return "", err
		}
	} else if err := os.MkdirAll(filepath.Dir(p.TargetPath), 0o750); err != nil {
		return "", err
	}
	if err := writeAtomic(p.TargetPath, []byte(content)); err != nil {
		return "", err
	}
	return sha([]byte(content)), nil
}

// removeIndexLine takes one line out of a MEMORY.md, backing the index up
// first; an index left with nothing in it is removed.
func (r Roots) removeIndexLine(index, line string, now time.Time) error {
	b, err := os.ReadFile(index)
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	i := lineIndex(string(b), line)
	if i < 0 {
		return nil
	}
	if err := r.backup(index, b, now); err != nil {
		return err
	}
	next := strings.Join(append(lines[:i:i], lines[i+1:]...), "\n")
	if strings.TrimSpace(next) == "" {
		return os.Remove(index)
	}
	return writeAtomic(index, []byte(next))
}

func lineIndex(content, line string) int {
	for i, l := range strings.Split(content, "\n") {
		if strings.TrimRight(l, "\r") == line {
			return i
		}
	}
	return -1
}

// backupAt reads the backup Apply made of path when it was approved at at: a
// proposal applied before the replaced content was kept with it.
func (r Roots) backupAt(path string, at time.Time) (string, error) {
	if r.History == "" || at.IsZero() {
		return "", ErrNothingToRestore
	}
	rel, err := filepath.Rel(r.Home, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = filepath.Base(path)
	}
	dir := filepath.Join(r.History, rel)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", ErrNothingToRestore
	}
	type cand struct {
		name string
		off  time.Duration
	}
	var cs []cand
	for _, e := range entries {
		t, err := time.Parse(backupStamp, e.Name())
		if err != nil {
			continue
		}
		// The backup is named by the same clock reading as decided_at; allow
		// for a store that kept the time at a coarser precision.
		if off := at.Sub(t).Abs(); off <= 2*time.Second {
			cs = append(cs, cand{e.Name(), off})
		}
	}
	if len(cs) == 0 {
		return "", ErrNothingToRestore
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].off < cs[j].off })
	b, err := os.ReadFile(filepath.Join(dir, cs[0].name)) //nolint:gosec // G304: a backup AO wrote under its own history dir
	if err != nil {
		return "", err
	}
	return string(b), nil
}

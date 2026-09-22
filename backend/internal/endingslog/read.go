package endingslog

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Cluster is several sessions that stopped as one event: endings nobody ordered,
// falling inside MassEndingWindow of each other.
//
// Three sessions ending 105 milliseconds apart is a different fact from three
// sessions ending, and the only reason that fact took hours to establish is
// that nothing in AO grouped them. This is that grouping, computed from the
// journal rather than from the writer's in-memory window - so a daemon that
// restarted in the middle of an incident still reports it.
type Cluster struct {
	// At is when the first ending in the group was recorded.
	At time.Time
	// Entries are the endings, in the order they were recorded.
	Entries []Entry
}

// Span is how long the whole group took, first ending to last.
func (c Cluster) Span() time.Duration {
	if len(c.Entries) < 2 {
		return 0
	}
	return c.Entries[len(c.Entries)-1].At.Sub(c.Entries[0].At)
}

// IDs are the sessions in the group, in recorded order.
func (c Cluster) IDs() []string {
	ids := make([]string, 0, len(c.Entries))
	for _, e := range c.Entries {
		ids = append(ids, e.SessionID)
	}
	return ids
}

// Read returns every ending recorded in dir's journal, oldest first, across
// both generations.
//
// A line that will not parse is SKIPPED rather than failing the read: the
// journal is append-only and a hard kill can leave a truncated final line, and
// refusing to report anything because the last line is half-written would lose
// exactly the evidence a crash-time investigation wants.
func Read(dir string) ([]Entry, error) {
	if dir == "" {
		return nil, errors.New("endingslog: data dir is required")
	}
	var all []Entry
	// Oldest generation first, so the result is in recorded order without a sort
	// across files.
	for _, name := range []string{FileName + RotatedSuffix, FileName} {
		entries, err := readFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		all = append(all, entries...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.Before(all[j].At) })
	return all, nil
}

func readFile(path string) ([]Entry, error) {
	f, err := os.Open(path) //nolint:gosec // path is rooted in AO's own data dir
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	// The journal is capped at maxFileBytes per generation, but one line is
	// bounded by the record it carries, not by the file; a generous line buffer
	// keeps a long transcript path from truncating the read.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		var e Entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		if e.SessionID == "" {
			continue
		}
		entries = append(entries, e)
	}
	// A scan error (a truncated final line, a read fault) keeps what was read:
	// partial evidence beats none, and this is a diagnostic path.
	return entries, nil
}

// MassEndings groups the journal's unordered endings recorded at or after
// `since` into the ones that happened together, returning only groups of
// MassEndingThreshold or more, oldest first.
//
// AO-ordered endings are excluded for the same reason the live alarm excludes
// them: a crew teardown ends dev and its members together and an auto-reclaim
// sweep walks a batch, so counting those would report a mass ending on an
// ordinary afternoon.
//
// Grouping is chained - each ending joins the group when it is inside
// MassEndingWindow of the one before it - which is what the writer's sliding
// window does live, so the file and the alarm cannot disagree about what
// counted.
func MassEndings(dir string, since time.Time) ([]Cluster, error) {
	all, err := Read(dir)
	if err != nil {
		return nil, err
	}
	var groups []Cluster
	var current []Entry
	flush := func() {
		if len(current) >= MassEndingThreshold {
			groups = append(groups, Cluster{At: current[0].At, Entries: current})
		}
		current = nil
	}
	for _, e := range all {
		if e.At.Before(since) || e.Source == string(domain.TerminationSourceAO) {
			continue
		}
		if len(current) > 0 && e.At.Sub(current[len(current)-1].At) > MassEndingWindow {
			flush()
		}
		current = append(current, e)
	}
	flush()
	return groups, nil
}

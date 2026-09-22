package endingslog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// Describe names a session in a group with what is known about how it stopped,
// e.g. "nter-ios-app-79 (parked, SIGTERM)": whether AO parked it rather than
// terminating it, whether nothing ever reported it, and how its process ended.
// The exit is the part that says what did it - a line of SIGTERMs is something
// signalling the agents, a line of plain exits is the agents deciding to.
func (e Entry) Describe() string {
	var notes []string
	switch {
	case e.Source == "":
		notes = append(notes, "unreported")
	case e.Outcome == "parked":
		notes = append(notes, "parked")
	}
	if e.Exit != nil {
		if e.Exit.Signal != "" {
			notes = append(notes, e.Exit.Signal)
		} else {
			notes = append(notes, fmt.Sprintf("exit %d", e.Exit.Code))
		}
	}
	if len(notes) == 0 {
		return e.SessionID
	}
	return e.SessionID + " (" + strings.Join(notes, ", ") + ")"
}

// Describe is Entry.Describe for every session in the group, in recorded order.
func (c Cluster) Describe() []string {
	out := make([]string, 0, len(c.Entries))
	for _, e := range c.Entries {
		out = append(out, e.Describe())
	}
	return out
}

// Read returns every ending recorded in dir's journal, oldest first, across
// both generations, with each agent's exit status joined onto the ending it
// belongs to (see exitrecord.go). An exit the pane recorded that no ending line
// claims is returned as an entry of its own, with an empty Source.
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
	var exits []sessionExit
	// Oldest generation first, so the result is in recorded order without a sort
	// across files. Both generations are read before joining: an exit line can
	// land just after the journal rolled, leaving its ending in the older file.
	for _, name := range []string{FileName + RotatedSuffix, FileName} {
		entries, fileExits, err := readFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		all = append(all, entries...)
		exits = append(exits, fileExits...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.Before(all[j].At) })
	all = append(all, joinExits(all, exits)...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.Before(all[j].At) })
	return all, nil
}

// line is either kind of journal line: an ending the daemon wrote, or an exit
// status a pane appended (the only kind with agentExit set).
type line struct {
	Entry
	AgentExit *rawExit `json:"agentExit,omitempty"`
}

func readFile(path string) ([]Entry, []sessionExit, error) {
	f, err := os.Open(path) //nolint:gosec // path is rooted in AO's own data dir
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()

	var entries []Entry
	var exits []sessionExit
	scanner := bufio.NewScanner(f)
	// The journal is capped at maxFileBytes per generation, but one line is
	// bounded by the record it carries, not by the file; a generous line buffer
	// keeps a long transcript path from truncating the read.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		var l line
		if err := json.Unmarshal(scanner.Bytes(), &l); err != nil {
			continue
		}
		if l.SessionID == "" {
			continue
		}
		if l.AgentExit != nil {
			if x, ok := l.AgentExit.toAgentExit(); ok {
				exits = append(exits, sessionExit{sessionID: l.SessionID, exit: x})
			}
			continue
		}
		entries = append(entries, l.Entry)
	}
	// A scan error (a truncated final line, a read fault) keeps what was read:
	// partial evidence beats none, and this is a diagnostic path.
	return entries, exits, nil
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
// Parked exits count - the agent stopped, and nobody ordered it - and so does
// an exit the pane recorded that no hook ever reported: a SIGKILL fires no
// SessionEnd, so an event that killed every agent that way would otherwise
// leave nothing to group.
//
// Grouping is chained - each ending joins the group when it is inside
// MassEndingWindow of the one before it - which is what the writer's sliding
// window does live. The live alarm sees only what reached the daemon, so an
// unreported exit is grouped here and not there: the file is the fuller of the
// two accounts.
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

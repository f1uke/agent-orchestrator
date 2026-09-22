// Package endingslog is the durable account of WHY sessions stop.
//
// On 2026-09-22 three sessions across two projects ended within 105
// milliseconds of each other. Every one recorded `termination_source=agent,
// termination_reason=other` - Claude Code's catch-all - and that was the entire
// record, so the cause could not be established: an idle timeout, a signal and
// a harness quitting normally are indistinguishable once reduced to that one
// word. Ruling out an app reinstall, auto-reclaim, memory pressure, a crash, a
// CLI update and a pane collision took hours of manual elimination against
// evidence that lived everywhere except in AO.
//
// So this journal records, for every termination, the context AO actually held
// at that instant and previously threw away: sub-second timing, how long the
// session had been silent, whether its terminal pane outlived it, and which
// other sessions ended alongside it.
//
// TWO CONSTRAINTS SHAPE IT, and they pull in opposite directions.
//
// It must not become an unbounded log of everything a hook ever said, so it
// rolls at a byte cap with one previous generation, the way
// message-delivery.jsonl does. And a session's row is read on every board
// refresh, so none of this may live on the row: it is a file off to the side,
// written when a session ends and read when somebody is investigating, which is
// roughly never. The common path pays nothing for it.
//
// JSON Lines, under the data dir: greppable by eye, parseable by tools, and
// append-only so an interrupted write can damage at most the final line.
package endingslog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// FileName is the journal's name under the data dir.
const FileName = "endings.jsonl"

// RotatedSuffix is appended to the previous generation of the journal.
const RotatedSuffix = ".1"

// maxFileBytes is when the journal rolls over. One line per session ending is a
// far lower rate than message delivery, so a smaller cap still reaches back
// further in wall-clock time than anyone investigating an ending will need; two
// generations is the same trade message-delivery.jsonl makes.
const maxFileBytes = 1 << 20

// MassEndingWindow is how close together endings have to be to count as one
// event. The incident that prompted this spanned 105 milliseconds; five seconds
// is loose enough to also catch a cascade that takes a moment to propagate, and
// still far tighter than any plausible coincidence.
const MassEndingWindow = 5 * time.Second

// MassEndingThreshold is how many UNORDERED endings inside the window make a
// mass ending.
//
// Unordered is the whole of it: AO ends many sessions at once on purpose - the
// shutdown sweep terminates every live session, a crew teardown takes dev and
// qa together - so counting those would cry wolf on every ordinary shutdown and
// teach the reader to ignore it. Only endings AO did not order count, and three
// of those inside five seconds is the shape of the incident. Two is left below
// the bar because two agents finishing seconds apart is ordinary; the peers are
// still recorded on the line either way, so a pair remains findable by grep.
const MassEndingThreshold = 3

// Entry is one recorded session ending, as it appears on disk.
//
// Every field is a fact AO held at the moment the session stopped. Nothing here
// is inferred afterwards, and an unknown is omitted rather than guessed.
type Entry struct {
	// At is when the ending was recorded, at millisecond resolution. The
	// precision is the point: sorting terminated_at by hand is how the incident
	// was found, and seconds would have shown three sessions ending in the same
	// second without showing they ended together.
	At        time.Time `json:"at"`
	SessionID string    `json:"sessionId"`
	ProjectID string    `json:"projectId,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	CrewRole  string    `json:"crewRole,omitempty"`
	Harness   string    `json:"harness,omitempty"`

	// Source is who ended it: agent, ao, or runtime_gone. Reason is the
	// harness's own word for it, or the AO operation that ordered the teardown.
	Source string `json:"source"`
	Reason string `json:"reason,omitempty"`

	// LastState is what the session was doing immediately before it stopped.
	LastState string `json:"lastState,omitempty"`
	// SilentForSeconds is how long it had said nothing before it ended, and it
	// is the field that separates an idle timeout from a signal: a session that
	// has been quiet for three hours ended one way, a session that stopped
	// mid-turn ended another. Absent when the session had never reported.
	SilentForSeconds *int64 `json:"silentForSeconds,omitempty"`

	// TranscriptPath and AgentSessionID are the address of what the agent was
	// actually doing, which is the next question after "what ended it".
	TranscriptPath string `json:"transcriptPath,omitempty"`
	AgentSessionID string `json:"agentSessionId,omitempty"`

	// PaneAlive is whether the session's terminal pane still existed at the
	// moment the ending was recorded. True means the agent died inside a pane
	// that outlived it - a signal to the process, or the harness quitting -
	// while false means whatever happened took the pane too. It has to be
	// probed HERE because it stops being true: the investigator who checked by
	// hand hours later was reading a different fact. Absent when there was no
	// pane to ask about, or the probe could not answer.
	PaneAlive *bool `json:"paneAlive,omitempty"`

	// Together names the other sessions that ended inside MassEndingWindow of
	// this one, so a line says for itself that it was not alone.
	Together []string `json:"together,omitempty"`
	// MassEnding marks a line that is part of MassEndingThreshold or more
	// endings nobody ordered. This is the bit a reader greps for.
	MassEnding bool `json:"massEnding,omitempty"`
}

// PaneProbe answers whether a runtime handle still has a terminal pane. The
// daemon supplies one over its runtime adapter; a Recorder without one simply
// records no answer, which is honest.
type PaneProbe func(ctx context.Context, handleID string) (bool, error)

// Option configures a Recorder.
type Option func(*Recorder)

// WithPaneProbe wires the liveness question asked of the ending session's pane.
func WithPaneProbe(p PaneProbe) Option { return func(r *Recorder) { r.pane = p } }

// WithLogger replaces the logger a mass ending is announced on.
func WithLogger(l *slog.Logger) Option {
	return func(r *Recorder) {
		if l != nil {
			r.log = l
		}
	}
}

// WithClock replaces the clock, so a test can place endings in time.
func WithClock(now func() time.Time) Option {
	return func(r *Recorder) {
		if now != nil {
			r.now = now
		}
	}
}

// Recorder is the ports.SessionEndingSink that writes the journal.
//
// It is deliberately dumb about I/O - one open-append-close per line, guarded
// by a mutex - because a session ending is a rare event and holding a file
// handle open across the daemon's whole life would buy nothing and cost
// reasoning about rotation.
type Recorder struct {
	path string
	pane PaneProbe
	log  *slog.Logger
	now  func() time.Time

	mu sync.Mutex
	// recent is the sliding window used to notice that several sessions ended
	// at once. In memory only: it exists to stamp the line being written and to
	// raise the alarm live. Reading a cluster back later is done from the FILE
	// (see Cluster), so a daemon restart mid-incident loses nothing durable.
	recent []recentEnding
}

type recentEnding struct {
	at      time.Time
	id      string
	ordered bool
}

// New builds a Recorder over dir/endings.jsonl. dir must be the daemon's data
// dir, so the journal lands under ~/.ao (or AO_DATA_DIR) like every other piece
// of app state.
func New(dir string, opts ...Option) (*Recorder, error) {
	if dir == "" {
		return nil, errors.New("endingslog: data dir is required")
	}
	r := &Recorder{path: filepath.Join(dir, FileName), log: slog.Default(), now: time.Now}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

// Path is where the journal lives, so the daemon and `ao doctor` can tell a
// human where to look.
func (r *Recorder) Path() string { return r.path }

// RecordEnding writes one ending.
//
// It returns nothing and swallows every failure. The session has already ended
// by the time this runs; a journal that cannot write must not turn a recorded
// termination into a failed one.
func (r *Recorder) RecordEnding(ctx context.Context, e ports.SessionEnding) {
	at := e.At
	if at.IsZero() {
		at = r.now()
	}
	entry := Entry{
		At:             at.UTC().Truncate(time.Millisecond),
		SessionID:      string(e.SessionID),
		ProjectID:      string(e.ProjectID),
		Kind:           string(e.Kind),
		CrewRole:       string(e.CrewRole),
		Harness:        string(e.Harness),
		Source:         string(e.Source),
		Reason:         e.Reason,
		LastState:      string(e.LastState),
		TranscriptPath: e.TranscriptPath,
		AgentSessionID: e.AgentSessionID,
	}
	if !e.LastActivityAt.IsZero() {
		silent := int64(at.Sub(e.LastActivityAt).Round(time.Second).Seconds())
		if silent < 0 {
			silent = 0
		}
		entry.SilentForSeconds = &silent
	}
	entry.PaneAlive = r.probePane(ctx, e.RuntimeHandleID)

	// AO-ordered endings are excluded from the alarm, not from the journal: the
	// shutdown sweep and a crew teardown end several sessions at once by design,
	// and an alarm that fires on those is one nobody reads.
	ordered := e.Source == domain.TerminationSourceAO
	together, mass := r.cluster(entry.At, entry.SessionID, ordered)
	entry.Together, entry.MassEnding = together, mass

	if mass {
		r.log.Warn("endings: several sessions ended at once and nobody ordered it",
			"session", entry.SessionID, "at", entry.At, "together", together,
			"source", entry.Source, "reason", entry.Reason, "journal", r.path)
	}
	if err := r.append(entry); err != nil {
		r.log.Warn("endings: could not record how a session ended",
			"session", entry.SessionID, "journal", r.path, "error", err)
	}
}

// probePane asks whether the ending session's pane is still there, tolerating
// every way that question can fail to have an answer.
func (r *Recorder) probePane(ctx context.Context, handleID string) *bool {
	if r.pane == nil || handleID == "" {
		return nil
	}
	alive, err := r.pane(ctx, handleID)
	if err != nil {
		// An unanswerable probe is recorded as no answer rather than as "dead":
		// a failed probe is the one thing this journal must never dress up as a
		// fact, because it is exactly what a reader would build a theory on.
		return nil
	}
	return &alive
}

// cluster records this ending in the sliding window and reports who it landed
// beside. Callers must not hold r.mu.
func (r *Recorder) cluster(at time.Time, id string, ordered bool) (together []string, mass bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoff := at.Add(-MassEndingWindow)
	kept := make([]recentEnding, 0, len(r.recent)+1)
	unordered := 0
	if !ordered {
		unordered = 1
	}
	for _, prev := range r.recent {
		if prev.at.Before(cutoff) {
			continue
		}
		kept = append(kept, prev)
		together = append(together, prev.id)
		if !prev.ordered {
			unordered++
		}
	}
	kept = append(kept, recentEnding{at: at, id: id, ordered: ordered})
	r.recent = kept
	return together, unordered >= MassEndingThreshold
}

// append writes one line, rolling the file first if it would pass the cap.
func (r *Recorder) append(e Entry) error {
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("endingslog: marshal: %w", err)
	}
	line = append(line, '\n')

	r.mu.Lock()
	defer r.mu.Unlock()
	r.rotateIfFull(len(line))
	f, err := os.OpenFile(r.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("endingslog: open: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("endingslog: write: %w", err)
	}
	return nil
}

// rotateIfFull moves the journal aside once it would pass the size bound. Every
// failure is swallowed on purpose: a journal that cannot rotate must still
// record, because the record is the point and an oversized file is the lesser
// of the two problems. Callers hold r.mu.
func (r *Recorder) rotateIfFull(incoming int) {
	info, err := os.Stat(r.path)
	if err != nil || info.Size()+int64(incoming) <= maxFileBytes {
		return
	}
	_ = os.Rename(r.path, r.path+RotatedSuffix)
}

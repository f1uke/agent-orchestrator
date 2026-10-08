// Package xcresultstream reads the result stream `xcodebuild -resultStreamPath`
// writes while a build runs: concatenated JSON events, appended as they happen.
package xcresultstream

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Phase is what the build is doing, read from the tasks it starts.
type Phase string

const (
	PhaseResolving Phase = "resolving"
	PhasePlanning  Phase = "planning"
	PhaseCompiling Phase = "compiling"
	PhaseLinking   Phase = "linking"
	PhaseSigning   Phase = "signing"
	PhaseBuilding  Phase = "building"
)

// Counts is the build service's own task count, as `-IDEPostProgressNotifications=YES`
// reports it.
type Counts struct {
	Done     int     `json:"done"`
	Total    int     `json:"total"`
	Fraction float64 `json:"fraction"`
}

// Issue is one compiler or build-system diagnostic. Line and Column are
// one-based, as the compiler prints them; zero means the issue has no location.
type Issue struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
}

// MaxIssues is how many errors, and separately how many warnings, a snapshot
// keeps. A clean nter build emits about 3,300 warnings; the counts carry the rest.
const MaxIssues = 20

// Snapshot is what the stream has said so far.
type Snapshot struct {
	Phase    Phase
	Counts   *Counts
	Errors   int
	Warnings int
	// Issues is the first errors in arrival order, then the first warnings.
	Issues []Issue
}

// Reader decodes a growing stream. Feed it every byte the file gains, in order.
type Reader struct {
	pending  []byte
	broken   bool
	targets  map[string]bool
	phase    Phase
	counts   *Counts
	errors   int
	warnings int
	errs     []Issue
	warns    []Issue
}

// Feed decodes every complete event in p plus what earlier calls left over. A
// half-written event at the end waits for the next call.
func (r *Reader) Feed(p []byte) {
	if r.broken {
		return
	}
	r.pending = append(r.pending, p...)
	dec := json.NewDecoder(bytes.NewReader(r.pending))
	consumed := 0
	for {
		var ev streamedEvent
		err := dec.Decode(&ev)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				r.broken = true
				r.pending = nil
				return
			}
			break
		}
		consumed = int(dec.InputOffset())
		r.apply(ev)
	}
	r.pending = append(r.pending[:0], r.pending[consumed:]...)
}

// Snapshot is the state so far. The returned value shares nothing with the reader.
func (r *Reader) Snapshot() Snapshot {
	snap := Snapshot{Phase: r.phase, Errors: r.errors, Warnings: r.warnings}
	if r.counts != nil {
		c := *r.counts
		snap.Counts = &c
	}
	snap.Issues = append(append([]Issue{}, r.errs...), r.warns...)
	return snap
}

type value struct {
	V string `json:"_value"`
}

type streamedEvent struct {
	Name    value           `json:"name"`
	Payload json.RawMessage `json:"structuredPayload"`
}

func (r *Reader) apply(ev streamedEvent) {
	switch ev.Name.V {
	case "advisoryMessage":
		var p struct {
			Message  value `json:"message"`
			Progress value `json:"progress"`
		}
		if json.Unmarshal(ev.Payload, &p) == nil {
			r.advise(p.Message.V, p.Progress.V)
		}
	case "logSectionCreated":
		var p struct {
			Head struct {
				Title value `json:"title"`
			} `json:"head"`
		}
		if json.Unmarshal(ev.Payload, &p) == nil {
			r.section(p.Head.Title.V)
		}
	case "logMessageEmitted":
		var p struct {
			Message struct {
				Title       value `json:"title"`
				Annotations struct {
					Values []struct {
						Title value `json:"title"`
					} `json:"_values"`
				} `json:"annotations"`
			} `json:"message"`
		}
		if json.Unmarshal(ev.Payload, &p) == nil && strings.HasPrefix(p.Message.Title.V, "Target dependency graph") {
			r.targets = map[string]bool{}
			for _, a := range p.Message.Annotations.Values {
				if name, ok := graphTarget(a.Title.V); ok {
					r.targets[name] = true
				}
			}
		}
	case "issueEmitted":
		var p struct {
			Severity value `json:"severity"`
			Issue    struct {
				Message  value `json:"message"`
				Location struct {
					URL value `json:"url"`
				} `json:"documentLocationInCreatingWorkspace"`
			} `json:"issue"`
		}
		if json.Unmarshal(ev.Payload, &p) == nil {
			r.issue(p.Severity.V, p.Issue.Message.V, p.Issue.Location.URL.V)
		}
	}
}

var targetLine = regexp.MustCompile(`^Target '([^']+)'`)

func graphTarget(line string) (string, bool) {
	m := targetLine.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return m[1], true
}

var countPattern = regexp.MustCompile(`(\d+)[\s\x{2009}\x{202F}\x{00A0}]*/[\s\x{2009}\x{202F}\x{00A0}]*(\d+)`)

// advise takes a progress event only when it names one of THIS build's targets:
// the build service posts these machine-wide, so every xcodebuild with a result
// stream also receives the counts of every other flagged build on the Mac.
func (r *Reader) advise(message, progress string) {
	target, rest, ok := strings.Cut(message, " : ")
	if !ok || !r.targets[strings.TrimSpace(target)] {
		return
	}
	m := countPattern.FindStringSubmatch(rest)
	if m == nil {
		return
	}
	done, errDone := strconv.Atoi(m[1])
	total, errTotal := strconv.Atoi(m[2])
	fraction, errFraction := strconv.ParseFloat(progress, 64)
	if errDone != nil || errTotal != nil || errFraction != nil || total <= 0 {
		return
	}
	if r.counts != nil && (done < r.counts.Done || fraction < r.counts.Fraction) {
		return
	}
	r.counts = &Counts{Done: done, Total: total, Fraction: min(max(fraction, 0), 1)}
}

var phasePrefixes = []struct {
	prefix string
	phase  Phase
}{
	{"Prepare packages", PhaseResolving},
	{"Resolve Package", PhaseResolving},
	{"Resolving", PhaseResolving},
	{"Fetching", PhaseResolving},
	{"Cloning", PhaseResolving},
	{"Checking out", PhaseResolving},
	{"Compute target dependency graph for package", PhaseResolving},
	{"Compute target dependency graph", PhasePlanning},
	{"Create build", PhasePlanning},
	{"Send project description", PhasePlanning},
	{"Gather provisioning inputs", PhasePlanning},
	{"Compil", PhaseCompiling},
	{"Scan dependencies", PhaseCompiling},
	{"Emit", PhaseCompiling},
	{"Planning Swift module", PhaseCompiling},
	{"Merge Objective-C", PhaseCompiling},
	{"Link ", PhaseLinking},
	{"Sign ", PhaseSigning},
}

// section moves the phase only on a task that says what the build is doing;
// the copies and header writes between compiles would make the label flicker.
func (r *Reader) section(title string) {
	for _, p := range phasePrefixes {
		if strings.HasPrefix(title, p.prefix) {
			r.phase = p.phase
			return
		}
	}
	if r.phase == "" {
		r.phase = PhaseBuilding
	}
}

func (r *Reader) issue(severity, message, location string) {
	it := Issue{Severity: severity, Message: message}
	it.File, it.Line, it.Column = parseLocation(location)
	switch severity {
	case "error":
		r.errors++
		if len(r.errs) < MaxIssues {
			r.errs = append(r.errs, it)
		}
	case "warning":
		r.warnings++
		if len(r.warns) < MaxIssues {
			r.warns = append(r.warns, it)
		}
	}
}

// parseLocation reads `file:///a/B.swift#StartingLineNumber=0&StartingColumnNumber=37&...`,
// whose numbers are zero-based.
func parseLocation(raw string) (string, int, int) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" {
		return "", 0, 0
	}
	fragment, _ := url.ParseQuery(u.Fragment)
	line, errLine := strconv.Atoi(fragment.Get("StartingLineNumber"))
	column, errColumn := strconv.Atoi(fragment.Get("StartingColumnNumber"))
	if errLine != nil {
		return u.Path, 0, 0
	}
	if errColumn != nil {
		return u.Path, line + 1, 0
	}
	return u.Path, line + 1, column + 1
}

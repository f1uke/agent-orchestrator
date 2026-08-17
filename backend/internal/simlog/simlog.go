// Package simlog reads what an app running on a simulator writes to the
// unified log.
//
// It exists because `ao sim` could see a simulator's SCREEN and nothing else,
// so an agent that needed an app's own output - the body of a response, not
// what the screen made of it - had to invent a way to get it. The obvious
// invention, `xcrun simctl launch --console-pipe`, wedges the app the moment
// something stops draining the pipe: the buffer fills and a `print` blocks in
// write() on the main thread. The unified log cannot do that to an app, which
// is the entire reason this is the route AO offers.
//
// This package is the parsing and filtering half, deliberately separate from
// running anything: it takes a reader, so every rule in it is tested without
// macOS, Xcode or a simulator.
//
// Filtering happens HERE rather than in the log command's own predicate, and
// that is load-bearing rather than a preference. A `log stream` running inside
// the simulator is a child of the guest's launchd, not of ours: no process
// group reaches it, and the only thing that ends it is a failed write to the
// pipe our process holds. A stream that is filtered at the source and matches
// nothing never writes, never notices that its reader has gone, and stays
// running for days - which is exactly the leak found on this machine, left by
// the incident this work came from. An unfiltered stream always has traffic,
// so it always notices.
package simlog

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Entry is one log entry as `log --style compact` prints it.
type Entry struct {
	// Time is the device's own timestamp, kept verbatim: it is what the same
	// entry looks like in Console.app, so the two can be matched up by eye.
	Time string `json:"time,omitempty"`
	// Type is the level, spelled out (Default, Error, Fault, Info, Debug,
	// Activity). The one-letter codes are unreadable and easy to confuse.
	Type      string `json:"type,omitempty"`
	Process   string `json:"process,omitempty"`
	PID       int    `json:"pid,omitempty"`
	Subsystem string `json:"subsystem,omitempty"`
	Category  string `json:"category,omitempty"`
	// Message includes every continuation line, joined by newlines: a body
	// printed across several lines is one entry, not several.
	Message string `json:"message"`
	// Raw is the whole entry as it arrived, which is what --grep is applied to
	// and what the text output prints.
	Raw string `json:"-"`
}

// Filter is what the caller asked to see. A zero Filter keeps everything.
type Filter struct {
	// Process matches the executable name exactly, ignoring case. It is not a
	// substring match: `--process Nim` quietly matching Nimbus would make the
	// flag mean something other than what it says.
	Process string
	// Grep is applied to the whole entry, so it matches what a `grep` over the
	// same output would.
	Grep *regexp.Regexp
}

// NewFilter builds a filter from what the flags carried. A pattern that will
// not compile is refused rather than treated as literal text.
func NewFilter(process, grep string) (Filter, error) {
	filter := Filter{Process: strings.TrimSpace(process)}
	if pattern := strings.TrimSpace(grep); pattern != "" {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return Filter{}, fmt.Errorf("--grep %q is not a valid regular expression: %w", grep, err)
		}
		filter.Grep = compiled
	}
	return filter, nil
}

// Match reports whether an entry is one the caller asked for.
func (f Filter) Match(e Entry) bool {
	if f.Process != "" && !strings.EqualFold(f.Process, e.Process) {
		return false
	}
	if f.Grep != nil && !f.Grep.MatchString(e.Raw) {
		return false
	}
	return true
}

// ProcessCount is one process and how much it logged in the window that was
// read.
type ProcessCount struct {
	Name    string `json:"name"`
	Entries int    `json:"entries"`
}

// Scan is what the whole read saw, whether or not it matched. It is what turns
// an empty result into an actionable one: "nothing from Nimbus" is a dead end,
// "nothing from Nimbus, but these processes did log" is a next step.
type Scan struct {
	// Entries is every entry read, matched or not.
	Entries int `json:"scanned"`
	// Processes is every process that logged, busiest first.
	Processes []ProcessCount `json:"processes,omitempty"`
}

// maxProcesses caps the did-you-mean list. A device logs from a hundred or so
// processes and a list that long is not a hint, it is another haystack.
const maxProcesses = 12

// Read consumes a finite capture - what `log show` prints for a window that
// has already happened - and delivers every matching entry.
//
// An entry is complete when the next one begins or the capture ends, so a
// message that runs over several lines is one entry and --grep sees all of it.
// An error from onEntry stops the read and comes back unchanged.
func Read(r io.Reader, filter Filter, onEntry func(Entry) error) (Scan, error) {
	return read(r, filter, onEntry, 0)
}

// Follow consumes a live stream, where an entry that waits for the next one is
// an entry an agent watching a running app cannot use.
//
// The difference from Read is the only thing a live stream needs: an entry is
// also complete once nothing more has arrived for a moment. Without that, the
// last thing an app logged before the device went quiet would sit unshown -
// which is precisely the entry somebody watching a live app is waiting for.
func Follow(r io.Reader, filter Filter, onEntry func(Entry) error) (Scan, error) {
	return read(r, filter, onEntry, quietFlush)
}

// quietFlush is how long a live entry waits for a continuation line before it
// is considered finished. Long enough that the rest of one multi-line message
// arrives first (they are written in one burst), short enough to be invisible.
const quietFlush = 50 * time.Millisecond

func read(r io.Reader, filter Filter, onEntry func(Entry) error, quiet time.Duration) (Scan, error) {
	scan := Scan{}
	counts := map[string]int{}
	var current *Entry

	deliver := func() error {
		if current == nil {
			return nil
		}
		entry := *current
		current = nil
		scan.Entries++
		if entry.Process != "" {
			counts[entry.Process]++
		}
		if !filter.Match(entry) {
			return nil
		}
		return onEntry(entry)
	}

	handle := func(line string) error {
		parsed, ok := parse(line)
		if !ok {
			// A line that is not an entry belongs to the one before it: log
			// messages carry payloads across several lines. Before the first
			// entry there is nothing to belong to - that is simctl's own column
			// header and the noise it prints on the way in - so it is dropped.
			if current != nil {
				current.Message += "\n" + line
				current.Raw += "\n" + line
			}
			return nil
		}
		if err := deliver(); err != nil {
			return err
		}
		current = &parsed
		return nil
	}

	lines, done, errs := scanLines(r)
	defer close(done)
	for {
		var timeout <-chan time.Time
		if quiet > 0 && current != nil {
			timeout = time.After(quiet)
		}
		select {
		case line, ok := <-lines:
			if !ok {
				if err := deliver(); err != nil {
					return finish(scan, counts), err
				}
				return finish(scan, counts), <-errs
			}
			if err := handle(line); err != nil {
				return finish(scan, counts), err
			}
		case <-timeout:
			if err := deliver(); err != nil {
				return finish(scan, counts), err
			}
		}
	}
}

// scanLines reads in the background, because a live stream only produces when
// the device does and a read parked on it cannot also watch a clock. done lets
// a caller that has stopped early leave without waiting for another line.
func scanLines(r io.Reader) (<-chan string, chan struct{}, <-chan error) {
	lines := make(chan string, 64)
	errs := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-done:
				errs <- nil
				return
			}
		}
		errs <- scanner.Err()
	}()
	return lines, done, errs
}

// maxLineBytes bounds one line. Log messages can carry a whole response body;
// a megabyte is far above any real entry and stops a runaway line from becoming
// an allocation.
const maxLineBytes = 1 << 20

func finish(scan Scan, counts map[string]int) Scan {
	for name, n := range counts {
		scan.Processes = append(scan.Processes, ProcessCount{Name: name, Entries: n})
	}
	sort.Slice(scan.Processes, func(i, j int) bool {
		if scan.Processes[i].Entries != scan.Processes[j].Entries {
			return scan.Processes[i].Entries > scan.Processes[j].Entries
		}
		return scan.Processes[i].Name < scan.Processes[j].Name
	})
	if len(scan.Processes) > maxProcesses {
		scan.Processes = scan.Processes[:maxProcesses]
	}
	return scan
}

// compactEntry is one line of `log --style compact`:
//
//	2026-08-17 16:31:59.078 Df Nimbus[4242:714e3fa] [com.example.nimbus:net] request failed
//
// The subsystem and category are optional - NSLog sets neither - and so is the
// message.
var compactEntry = regexp.MustCompile(
	`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+) +(\S+) +(.+?)\[(\d+):[0-9a-fA-F]+\] *(?:\[([^:\]]*)(?::([^\]]*))?\])? ?(.*)$`)

// logTypes spells out the one-letter level codes compact prints.
var logTypes = map[string]string{
	"Db": "Debug",
	"Df": "Default",
	"I":  "Info",
	"E":  "Error",
	"F":  "Fault",
	"A":  "Activity",
	"Ts": "TimesyncStart",
	"Te": "TimesyncEnd",
}

func parse(line string) (Entry, bool) {
	match := compactEntry.FindStringSubmatch(line)
	if match == nil {
		return Entry{}, false
	}
	pid, err := strconv.Atoi(match[4])
	if err != nil {
		return Entry{}, false
	}
	kind := match[2]
	if spelled, ok := logTypes[kind]; ok {
		kind = spelled
	}
	return Entry{
		Time:      match[1],
		Type:      kind,
		Process:   match[3],
		PID:       pid,
		Subsystem: match[5],
		Category:  match[6],
		Message:   match[7],
		Raw:       line,
	}, true
}

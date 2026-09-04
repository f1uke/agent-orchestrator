package wiki

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/wikisettings"
)

// The Tasks surface: the unchecked `- [ ]` rows in one configured corner of the
// vault, and the one write that ticks a row off.
//
// Everything about the vault's task convention is CONFIGURATION. This file
// knows the markdown — a checkbox row, a "## " heading, an owner token, a
// `due:` or `created:` field, a "(from: …)" tag — and nothing at all about which
// folder, which section, or which person any particular vault uses. A schema baked in here would make the tab
// work for exactly one person's notes.

// maxTaskNoteBytes bounds one note's contribution to the scan. A task list is
// prose; anything larger is not one, and reading it whole would make opening
// the tab cost as much as indexing the vault.
const maxTaskNoteBytes = 512 << 10

// maxTaskRows bounds the answer. A configured subtree is meant to be small; the
// cap exists so a subtree pointed at the wrong place cannot make the response
// unbounded, and the tab says so rather than quietly listing a prefix.
const maxTaskRows = 5000

// taskRowPattern matches an unchecked task row: optional indentation, a list
// marker, and an empty checkbox. The captured groups are the indent+marker
// prefix and the text after the box, which is all the parser needs to rebuild
// the line byte for byte.
var taskRowPattern = regexp.MustCompile(`^([ \t]*(?:[-*+]|\d+[.)])[ \t]+)\[([ xX])\][ \t]*(.*)$`)

// ownerPattern matches an owner token at the START of a row's text: "[@Name]"
// or a bare "@name". Only the leading position counts — an "@" in the middle of
// a sentence is prose, not an assignment.
//
// The name runs to the closing bracket in the bracketed form, and to
// whitespace in the bare form, so multi-word owners work in the form that can
// express them.
var ownerPattern = regexp.MustCompile(`^(?:\[@([^\]\n]+)\]|@([^\s\]]+))[ \t]*`)

// duePattern matches a `due:YYYY-MM-DD` field anywhere in a row's text. It is
// written as a trailing field by convention, but a row that carries it earlier
// means the same thing and refusing to read it would be a puzzle, not a rule.
var duePattern = regexp.MustCompile(`(?:^|\s)due:(\d{4}-\d{2}-\d{2})(?:\s|$)`)

// createdPattern matches a `created:YYYY-MM-DD` field anywhere in a row's text.
// It is the row's OWN creation date — the thing "how old is this backlog item"
// actually asks — as opposed to `due:`, which is a promise about the future.
var createdPattern = regexp.MustCompile(`(?:^|\s)created:(\d{4}-\d{2}-\d{2})(?:\s|$)`)

// fromTagPattern matches a parenthesised provenance tag: "(from: 2026-05-07
// standup)", "(from: chat 2026-05-07, Mobility HQ)". The capture is the tag's
// free text, and a date is looked for INSIDE it and nowhere else — a date in
// the task's own sentence is a date the task talks about, not the date the task
// was written.
var fromTagPattern = regexp.MustCompile(`(?i)\(\s*from:([^)\n]*)\)`)

// isoDatePattern is a bare YYYY-MM-DD. It is only ever run inside a from-tag.
var isoDatePattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

// Task is one unchecked checkbox row, addressed well enough that ticking it can
// never land on a different line.
type Task struct {
	// ID identifies the row for the client's list. It is derived from the
	// address below, so it changes when the row moves or its text changes —
	// which is the point: a stale id must not look fresh.
	ID string
	// Path is the vault-relative note the row lives in.
	Path string
	// Line is the 1-based line number the row was read from.
	Line int
	// Raw is the line EXACTLY as it appears on disk, byte for byte. It is the
	// real key: a tick is only ever written to a line whose full text equals
	// the text the reader was shown.
	Raw string
	// Text is the row for display: the checkbox, the owner token and the
	// `due:` / `created:` fields removed, so the sentence reads as a sentence.
	// The "(from: …)" tag stays — it is prose the reader wrote.
	Text string
	// Section is the nearest "## " heading above the row, Subsection the
	// nearest "### ". Empty when the row sits above any heading.
	Section    string
	Subsection string
	// Owner is the owner token at the start of the row, without its brackets.
	// Empty means the row names nobody, which is how an unowned row reads.
	Owner string
	// Due is the row's `due:` date as YYYY-MM-DD, empty when it carries none.
	Due string
	// Created is the row's `created:` date, FromDate the date inside its
	// "(from: …)" provenance tag. Both are YYYY-MM-DD, empty when absent, and
	// both describe the ROW — which is why the note's mtime is not here at all.
	// A row that carries neither has no date, and saying so is more honest than
	// borrowing the file's.
	Created  string
	FromDate string
}

// Tasks is the whole answer behind the Tasks tab.
type Tasks struct {
	// Configured is false when no subtree is set. Nothing is scanned in that
	// state and the tab explains what to set, rather than quietly reading the
	// whole vault.
	Configured   bool
	Folders      []string
	Sections     []string
	Cutoff       string
	OwnerAliases []string
	Rows         []Task
	// Owners are the distinct owner tokens seen in the scan, sorted, so the
	// filter can offer real names without this package knowing any.
	Owners       []string
	ScannedNotes int
	// Truncated reports that the subtree holds more rows than the cap.
	Truncated bool
}

// ListTasks reads every unchecked task row in the configured subtree.
//
// The CUTOFF and the mine/others filter are deliberately NOT applied here. Both
// are applied by the renderer, which lets the tab say "N rows are hidden by the
// cutoff — show them" and flip it instantly, with no round trip and no way for
// a filtered list to be mistaken for a destroyed backlog.
func (s *Service) ListTasks(ctx context.Context) (Tasks, error) {
	vault, err := s.requireVault()
	if err != nil {
		return Tasks{}, err
	}
	cfg := s.taskSettings()
	out := Tasks{
		Folders:      cfg.Folders,
		Sections:     cfg.Sections,
		Cutoff:       cfg.Cutoff,
		OwnerAliases: cfg.OwnerAliases,
		Rows:         []Task{},
		Owners:       []string{},
	}
	if len(cfg.Folders) == 0 {
		return out, nil
	}
	out.Configured = true

	wanted := sectionFilter(cfg.Sections)
	owners := map[string]bool{}
	for _, configured := range cfg.Folders {
		// `folder` is the CANONICAL vault-relative form of the subtree, and
		// every row's path is built from it. Deriving a path from `root`
		// instead would be wrong wherever the vault is reached through a
		// symlink (/var -> /private/var on macOS): `root` is symlink-resolved
		// and `vault` is not, so a Rel between them walks out through "..".
		root, folder, ok := confined(vault, configured)
		if !ok {
			// A subtree that is gone (renamed, deleted, never there) is a
			// configuration problem the tab must SAY, not one it can paper
			// over by quietly reading the rest.
			return Tasks{}, apierr.Invalid(
				"WIKI_TASKS_FOLDER_MISSING",
				fmt.Sprintf("The configured tasks folder %q is not in the vault", configured),
				map[string]any{"folder": configured},
			)
		}
		if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
			return Tasks{}, apierr.Invalid(
				"WIKI_TASKS_FOLDER_MISSING",
				fmt.Sprintf("The configured tasks folder %q is not a directory", configured),
				map[string]any{"folder": configured},
			)
		}
		if err := s.scanFolder(ctx, root, folder, wanted, owners, &out); err != nil {
			return Tasks{}, err
		}
		if out.Truncated {
			break
		}
	}

	for owner := range owners {
		out.Owners = append(out.Owners, owner)
	}
	sort.Strings(out.Owners)
	// A settled order the client can rely on: note path, then position in it.
	// Stable across the folder loop, so two subtrees interleave by path rather
	// than by which was configured first.
	sort.SliceStable(out.Rows, func(i, j int) bool {
		if out.Rows[i].Path != out.Rows[j].Path {
			return out.Rows[i].Path < out.Rows[j].Path
		}
		return out.Rows[i].Line < out.Rows[j].Line
	})
	return out, nil
}

// scanFolder walks one configured subtree, appending its rows to out.
func (s *Service) scanFolder(
	ctx context.Context,
	root, folder string,
	wanted map[string]bool,
	owners map[string]bool,
	out *Tasks,
) error {
	walkErr := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return nil //nolint:nilerr // an unreadable entry is skipped, not fatal
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil || info.Size() > maxTaskNoteBytes {
			//nolint:nilerr // a note we cannot stat, or one too large to be prose, is skipped
			return nil
		}
		within, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil //nolint:nilerr // unrelatable path, skip
		}
		rel := path.Join(folder, filepath.ToSlash(within))
		body, readErr := os.ReadFile(p) //nolint:gosec // p came from walking the confined subtree
		if readErr != nil {
			return nil //nolint:nilerr // unreadable note, skip
		}
		out.ScannedNotes++
		for _, row := range parseTasks(rel, string(body), wanted) {
			if row.Owner != "" {
				owners[row.Owner] = true
			}
			out.Rows = append(out.Rows, row)
			if len(out.Rows) >= maxTaskRows {
				out.Truncated = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	if walkErr != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

// sectionFilter turns the configured section names into a lookup, or nil for
// "every section". Matching is case-insensitive and whitespace-trimmed, because
// a heading typed with a stray capital is the same heading to the person who
// wrote it.
func sectionFilter(sections []string) map[string]bool {
	if len(sections) == 0 {
		return nil
	}
	want := make(map[string]bool, len(sections))
	for _, name := range sections {
		if name = strings.TrimSpace(name); name != "" {
			want[strings.ToLower(name)] = true
		}
	}
	if len(want) == 0 {
		return nil
	}
	return want
}

// parseTasks reads one note's unchecked rows, tracking the headings above them.
//
// Only "## " and "### " are tracked. A "# " title names the note, which the
// path already says, and levels below three are rarer than the noise they would
// add to a one-line row.
func parseTasks(notePath, body string, wanted map[string]bool) []Task {
	var out []Task
	section, subsection := "", ""
	inFence := false
	var fence string
	for i, line := range strings.Split(body, "\n") {
		// A checkbox inside a fenced code block is a code sample, not a task.
		if marker := fenceMarker(line); marker != "" {
			switch {
			case !inFence:
				inFence, fence = true, marker
			case strings.HasPrefix(marker, fence):
				inFence, fence = false, ""
			}
			continue
		}
		if inFence {
			continue
		}
		if heading, level := headingOf(line); level > 0 {
			switch level {
			case 2:
				section, subsection = heading, ""
			case 3:
				subsection = heading
			default:
				// A heading at any other level closes the ones below it: a new
				// "# " starts a new part of the note, so the "## " above it no
				// longer describes what follows.
				if level < 2 {
					section, subsection = "", ""
				}
			}
			continue
		}
		m := taskRowPattern.FindStringSubmatch(line)
		if len(m) < 4 || m[2] != " " {
			continue // not a task row, or one already ticked
		}
		if wanted != nil && !wanted[strings.ToLower(section)] {
			continue
		}
		raw := line
		// A note read on Windows, or written by an editor that keeps CRLF, must
		// not carry the "\r" into the row's identity — the renderer would show
		// it and the echo back would never match.
		raw = strings.TrimSuffix(raw, "\r")
		text := strings.TrimSuffix(m[3], "\r")
		owner := ""
		if om := ownerPattern.FindStringSubmatch(text); om != nil {
			owner = strings.TrimSpace(om[1] + om[2])
			text = text[len(om[0]):]
		}
		due := ""
		if dm := duePattern.FindStringSubmatch(text); dm != nil {
			due = validDate(dm[1])
			text = strings.TrimSpace(strings.Replace(text, dm[0], " ", 1))
		}
		created := ""
		if cm := createdPattern.FindStringSubmatch(text); cm != nil {
			created = validDate(cm[1])
			text = strings.TrimSpace(strings.Replace(text, cm[0], " ", 1))
		}
		out = append(out, Task{
			ID:         TaskID(notePath, i+1, raw),
			Path:       notePath,
			Line:       i + 1,
			Raw:        raw,
			Text:       strings.TrimSpace(text),
			Section:    section,
			Subsection: subsection,
			Owner:      owner,
			Due:        due,
			Created:    created,
			FromDate:   fromDate(text),
		})
	}
	return out
}

// fromDate reads a row's provenance date: the first real date inside a
// "(from: …)" tag. Every from-tag on the row is considered, so a row carrying
// both "(from: My active items)" and a dated tag still finds the date.
func fromDate(text string) string {
	for _, tag := range fromTagPattern.FindAllStringSubmatch(text, -1) {
		for _, candidate := range isoDatePattern.FindAllString(tag[1], -1) {
			if at := validDate(candidate); at != "" {
				return at
			}
		}
	}
	return ""
}

// validDate returns the date unchanged when it is a real calendar day, and ""
// when it is not. "2026-13-45" is shaped like a date and is not one: an
// impossible date is treated as an ABSENT field rather than guessed at or
// raised as an error, because a typo in one row must not cost the reader the
// note.
func validDate(day string) string {
	if _, err := time.Parse(dayLayout, day); err != nil {
		return ""
	}
	return day
}

// dayLayout is the only date format any of these fields is written in.
const dayLayout = "2006-01-02"

// headingOf reads an ATX heading, returning its text and level. A "#" with no
// space after it is a tag, not a heading.
func headingOf(line string) (string, int) {
	trimmed := strings.TrimRight(line, " \t\r")
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(trimmed) || (trimmed[level] != ' ' && trimmed[level] != '\t') {
		return "", 0
	}
	// A closing run of "#" is decoration in ATX headings, not part of the name.
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(trimmed[level:]), "#")), level
}

// fenceMarker returns the backtick or tilde run that opens or closes a fenced
// code block, or "" for an ordinary line.
func fenceMarker(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	for _, ch := range []byte{'`', '~'} {
		n := 0
		for n < len(trimmed) && trimmed[n] == ch {
			n++
		}
		if n >= 3 {
			return trimmed[:n]
		}
	}
	return ""
}

// TaskID derives a row's client-facing id from its whole address. It is a hash
// rather than the address itself so the client cannot be tempted to take it
// apart and reassemble an address the server never issued.
func TaskID(notePath string, line int, raw string) string {
	sum := sha256.Sum256([]byte(notePath + "\n" + strconv.Itoa(line) + "\n" + raw))
	return hex.EncodeToString(sum[:])[:32]
}

// taskSettings reads the Tasks configuration, re-applying the store's own
// tidying rules rather than trusting them to have been applied.
//
// The store normalizes on save, so this is belt and braces — but the service
// takes its settings through an INTERFACE, and a folder typed as "/Areas"
// arriving unnormalized would land as an absolute path, be refused by
// `confined`, and surface as "that folder is not in the vault" for a folder
// that plainly is.
func (s *Service) taskSettings() wikisettings.TaskSettings {
	if s.settings == nil {
		return wikisettings.TaskSettings{}
	}
	cfg := s.settings.Tasks()
	cfg.Folders = wikisettings.NormalizeFolders(cfg.Folders)
	cfg.Cutoff = strings.TrimSpace(cfg.Cutoff)
	return cfg
}

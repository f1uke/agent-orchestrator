package wiki

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// CompleteTaskInput is one tick: the row's address, and the row's text exactly
// as the reader saw it.
type CompleteTaskInput struct {
	// Path is the vault-relative note, as ListTasks returned it.
	Path string
	// Line is the 1-based line the row was read from. It is a HINT, not the
	// key — the row may have moved since, and the text below decides.
	Line int
	// Raw is the line byte for byte as it was displayed. It is REQUIRED and it
	// is the real key: no line is ever written unless its full text equals
	// this. There is deliberately no way to spell "tick line N regardless".
	Raw string
}

// CompleteTaskResult says what was actually written.
type CompleteTaskResult struct {
	Path string
	// Line is where the tick LANDED, which is not always where it was asked
	// for — see Moved.
	Line int
	// Raw is the line as written, now ticked.
	Raw string
	// Moved reports that the row was found somewhere other than the line the
	// caller named. It is not an error (the text still matched exactly, so it
	// is provably the same row), but the caller shows it rather than hiding it.
	Moved          bool
	NoteModifiedAt string
}

// CompleteTask ticks one `- [ ]` row off, in the note it lives in.
//
// 🗝 Ticking the WRONG line has to be impossible by construction, because these
// are somebody's personal notes and there is no backup. The rule that makes it
// impossible is simple and has no escape hatch:
//
//	The only line this ever writes to is a line whose FULL TEXT is
//	byte-identical to the text the reader was looking at.
//
// So:
//
//   - The named line is checked first, and used when it still matches.
//   - Otherwise the whole note is searched. Exactly one identical line means
//     the row moved — the text is unchanged, so it is provably the same row,
//     and the result says it moved. Zero means the row changed underneath the
//     reader; two or more means two rows are indistinguishable. Both are
//     REFUSED and explained, never guessed at.
//   - The matched line must still be unchecked. A row already ticked is
//     reported as such rather than rewritten.
//
// A whole-note hash is deliberately NOT the primary key. The vault's own agent
// edits these notes constantly, so a note-level precondition would refuse
// almost every tick because of an unrelated edit elsewhere in the file. It is
// still applied to the WRITE (through WriteNote below), which is where it
// belongs: it guards against a change landing between this read and this write,
// not against every change since the tab last refreshed.
//
// Only the checkbox changes. Every other byte of the line, and every other line
// of the note, is written back exactly as it was read.
func (s *Service) CompleteTask(ctx context.Context, in CompleteTaskInput) (CompleteTaskResult, error) {
	if err := ctx.Err(); err != nil {
		return CompleteTaskResult{}, err
	}
	if strings.TrimSpace(in.Raw) == "" {
		return CompleteTaskResult{}, apierr.Invalid(
			"WIKI_TASK_RAW_REQUIRED",
			"Ticking a task requires the exact row text it was shown with",
			nil,
		)
	}
	note, err := s.ReadNote(ctx, in.Path)
	if err != nil {
		return CompleteTaskResult{}, err
	}

	// Split on "\n" and keep every line's own trailing "\r", so a note with
	// CRLF endings is rejoined exactly as it came in. The comparison strips it,
	// because the row identity the client was given had it stripped too.
	lines := strings.Split(note.Content, "\n")
	target, moved, err := findTaskLine(lines, in.Line, in.Raw)
	if err != nil {
		return CompleteTaskResult{}, err
	}

	ticked, ok := tickCheckbox(strings.TrimSuffix(lines[target], "\r"))
	if !ok {
		// Unreachable through findTaskLine, which only matches unchecked rows.
		// Kept as a refusal rather than a panic: a write that cannot explain
		// itself must not happen at all.
		return CompleteTaskResult{}, apierr.Invalid(
			"WIKI_TASK_NOT_A_TASK",
			"That line is not an unchecked task row",
			map[string]any{"path": note.Path, "line": target + 1},
		)
	}
	if strings.HasSuffix(lines[target], "\r") {
		lines[target] = ticked + "\r"
	} else {
		lines[target] = ticked
	}

	// The precondition is the hash of the bytes just read, so a change landing
	// between that read and this write is a refusal rather than a clobber.
	written, err := s.WriteNote(ctx, WriteNoteInput{
		Path:     note.Path,
		Content:  strings.Join(lines, "\n"),
		BaseHash: note.ContentHash,
	})
	if err != nil {
		return CompleteTaskResult{}, err
	}
	return CompleteTaskResult{
		Path:           written.Path,
		Line:           target + 1,
		Raw:            ticked,
		Moved:          moved,
		NoteModifiedAt: written.ModifiedAt.UTC().Format(rfc3339),
	}, nil
}

// rfc3339 is the stamp format every Wiki response already uses.
const rfc3339 = "2006-01-02T15:04:05Z07:00"

// findTaskLine locates the row whose text is byte-identical to raw, preferring
// the line the caller named. It returns the 0-based index and whether the row
// had moved.
//
// Every failure here is a REFUSAL the caller can explain to the reader. None of
// them falls back to "close enough".
func findTaskLine(lines []string, wantLine int, raw string) (index int, moved bool, err error) {
	want := strings.TrimSuffix(raw, "\r")
	at := wantLine - 1
	if at >= 0 && at < len(lines) && strings.TrimSuffix(lines[at], "\r") == want {
		return at, false, nil
	}

	var matches []int
	for i, line := range lines {
		if strings.TrimSuffix(line, "\r") == want {
			matches = append(matches, i)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], true, nil
	case 0:
		// The row is not there any more, with that text. It was edited, ticked
		// elsewhere, or deleted — and this is exactly the case where guessing
		// would tick the wrong line.
		if alreadyDone(lines, at, want) {
			return 0, false, apierr.Conflict(
				"WIKI_TASK_ALREADY_DONE",
				"That task was already ticked off somewhere else",
				map[string]any{"line": wantLine},
			)
		}
		return 0, false, apierr.Conflict(
			"WIKI_TASK_NOT_FOUND",
			"That row is no longer in the note with the text it was shown with. Nothing was written.",
			map[string]any{"line": wantLine},
		)
	default:
		return 0, false, apierr.Conflict(
			"WIKI_TASK_AMBIGUOUS",
			"The note has more than one row with exactly that text, so there is no way to tell which one you meant. Nothing was written.",
			map[string]any{"line": wantLine, "matches": len(matches)},
		)
	}
}

// alreadyDone reports whether the row the caller meant is present but ticked,
// so "somebody already did this" can be said instead of the blunter "gone".
//
// It only claims so for an EXACT match of the same row ticked — the same text
// with the box filled in — which is what the vault's own agent writes when it
// closes a row out.
func alreadyDone(lines []string, at int, want string) bool {
	done, ok := tickCheckbox(want)
	if !ok {
		return false
	}
	if at >= 0 && at < len(lines) && strings.TrimSuffix(lines[at], "\r") == done {
		return true
	}
	for _, line := range lines {
		if strings.TrimSuffix(line, "\r") == done {
			return true
		}
	}
	return false
}

// tickCheckbox rewrites an unchecked task row as a checked one, changing the
// single byte inside the brackets and NOTHING else — not the indentation, not
// the list marker, not the spacing, not the text. It reports false for a line
// that is not an unchecked task row.
func tickCheckbox(line string) (string, bool) {
	m := taskRowPattern.FindStringSubmatch(line)
	if len(m) < 4 || m[2] != " " {
		return "", false
	}
	// The prefix is captured verbatim, so this is the original line with one
	// byte replaced rather than a re-rendering of what was parsed out of it.
	box := len(m[1]) + 1
	return line[:box] + "x" + line[box+1:], true
}

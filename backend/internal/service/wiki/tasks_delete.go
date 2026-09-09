package wiki

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// DeleteTaskInput is one removal: the row's address, and the row's text exactly
// as the reader saw it.
//
// The shape is deliberately identical to CompleteTaskInput. A delete is the
// same identity question as a tick, asked about a row the reader has decided
// they are never going to do, so it reuses the tick's key rather than inventing
// a second, weaker one.
type DeleteTaskInput struct {
	// Path is the vault-relative note, as ListTasks returned it.
	Path string
	// Line is the 1-based line the row was read from. It is a HINT, not the
	// key — the row may have moved, and the text below decides.
	Line int
	// Raw is the line byte for byte as it was displayed. It is REQUIRED and it
	// is the real key: no line is ever removed unless its full text equals
	// this. There is deliberately no way to spell "delete line N regardless".
	Raw string
}

// DeleteTaskResult says what was actually removed.
type DeleteTaskResult struct {
	Path string
	// Line is where the row was removed FROM, which is not always where it was
	// asked for — see Moved.
	Line int
	// Raw is the line that was removed, as it read on disk.
	Raw string
	// Moved reports that the row was found somewhere other than the line the
	// caller named. Not an error — the text still matched exactly, so it is
	// provably the same row — but the caller shows it rather than hiding it.
	Moved          bool
	NoteModifiedAt string
}

// DeleteTask removes one `- [ ]` row from the note it lives in.
//
// 🗝 This is the most destructive thing this daemon does to somebody's notes.
// A tick on the wrong line is visible and a human can put it back; a delete on
// the wrong line destroys work nobody can see was lost. So the bar is the
// tick's bar, applied harder rather than relaxed:
//
//   - The row is identified by its FULL TEXT, byte for byte, exactly as
//     CompleteTask identifies it — the same findTaskLine, with no second,
//     looser matcher living beside it. Zero matches, two matches, or a row
//     that is already gone are REFUSALS the caller can explain. Nothing is
//     ever deleted on a best guess.
//   - The matched line must still be an UNCHECKED task row. The tab only ever
//     lists those, so a request naming anything else did not come from a row
//     the reader was looking at, and this surface is reachable with no session
//     at all.
//   - EXACTLY ONE line leaves the note. Every other byte — line endings, the
//     trailing whitespace on other rows, the blank-line structure, the note's
//     own trailing newline or lack of one — is written back as it was read.
//     Nothing is renumbered, reflowed, reordered or tidied on the way past.
//   - The write is preconditioned on the hash of the bytes just read, so a
//     change landing between that read and this write is a conflict rather
//     than a clobber.
//
// There is deliberately no undo here and none is offered: the note's previous
// bytes are not kept anywhere. The protection against a slip lives in the tab,
// where the click happens, as a confirm step before this is ever called.
func (s *Service) DeleteTask(ctx context.Context, in DeleteTaskInput) (DeleteTaskResult, error) {
	if err := ctx.Err(); err != nil {
		return DeleteTaskResult{}, err
	}
	if strings.TrimSpace(in.Raw) == "" {
		return DeleteTaskResult{}, apierr.Invalid(
			"WIKI_TASK_RAW_REQUIRED",
			"Deleting a task requires the exact row text it was shown with",
			nil,
		)
	}
	note, err := s.ReadNote(ctx, in.Path)
	if err != nil {
		return DeleteTaskResult{}, err
	}

	// Split on "\n" and keep every line's own trailing "\r", so a note with
	// CRLF endings is rejoined exactly as it came in. A note that ends with a
	// newline produces a final empty element, which is what preserves that
	// newline through the join below — including when the row being removed is
	// the last one in the file.
	lines := strings.Split(note.Content, "\n")
	target, moved, err := findTaskLine(lines, in.Line, in.Raw)
	if err != nil {
		return DeleteTaskResult{}, err
	}

	removed := strings.TrimSuffix(lines[target], "\r")
	if !isOpenTaskRow(removed) {
		// Unreachable through findTaskLine, which only matches lines whose text
		// equals a raw the tab drew, and the tab draws only unchecked rows.
		// Kept as a refusal rather than a panic: a delete that cannot explain
		// itself must not happen at all.
		return DeleteTaskResult{}, apierr.Invalid(
			"WIKI_TASK_NOT_A_TASK",
			"That line is not an unchecked task row",
			map[string]any{"path": note.Path, "line": target + 1},
		)
	}

	// One line leaves, and only one. Joining the remainder with "\n" restores
	// every other byte of the note verbatim, because the split above kept them.
	kept := make([]string, 0, len(lines)-1)
	kept = append(kept, lines[:target]...)
	kept = append(kept, lines[target+1:]...)

	written, err := s.WriteNote(ctx, WriteNoteInput{
		Path:     note.Path,
		Content:  strings.Join(kept, "\n"),
		BaseHash: note.ContentHash,
	})
	if err != nil {
		return DeleteTaskResult{}, err
	}
	return DeleteTaskResult{
		Path:           written.Path,
		Line:           target + 1,
		Raw:            removed,
		Moved:          moved,
		NoteModifiedAt: written.ModifiedAt.UTC().Format(rfc3339),
	}, nil
}

// isOpenTaskRow reports whether a line is an UNCHECKED task row — the only kind
// of line the Tasks tab ever lists, and so the only kind this package will
// remove. It is the same pattern the parser and the tick both read the row
// with, asked as a yes/no question.
func isOpenTaskRow(line string) bool {
	m := taskRowPattern.FindStringSubmatch(line)
	return len(m) >= 3 && m[2] == " "
}

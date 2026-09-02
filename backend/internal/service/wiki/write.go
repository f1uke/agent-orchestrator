package wiki

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/fsatomic"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// WriteNoteInput is one save: the note, its new bytes, and the precondition
// that says which bytes the caller was editing.
type WriteNoteInput struct {
	// Path is vault-relative and slash-separated, as ReadNote returns it.
	Path string
	// Content is written verbatim - no EOL translation, no trailing-newline
	// insertion, no whitespace policy. These are somebody's notes, and a server
	// that silently rewrites bytes cannot show anyone what it did.
	Content string
	// BaseHash is the ContentHash ReadNote handed out. It is REQUIRED: there is
	// deliberately no way to spell "write regardless".
	BaseHash string
}

// WriteNoteResult is what the caller needs to keep editing without a second
// round trip: the token to precondition the next save on.
type WriteNoteResult struct {
	Path        string
	ContentHash string
	Size        int64
	ModifiedAt  time.Time
}

// WriteNote replaces one note's content.
//
// The rules are the workspace editor's rules (see
// service/session/workspace_file_write.go), for the same reasons and with one
// extra one of its own:
//
//   - CONFINED to the vault, symlinks resolved. Writing anywhere on disk
//     through a no-auth loopback daemon is an escalation, and this surface is
//     reachable with no session at all.
//   - EXISTING FILES ONLY. Save-what-you-opened is the whole use case, and
//     "must already exist" removes directory creation and the symlinked-parent
//     hole that cannot be checked for a path that is not there yet.
//   - PRECONDITIONED. The vault's own agent writes these files, so "it changed
//     since you opened it" is the normal case. The caller hands back the hash
//     it read; a mismatch is a conflict, never a clobber.
//
// The precondition is a CHECK, not a lock: the compare and the write are not
// one atomic operation, so two writes landing within a few milliseconds of each
// other can both succeed. What holds at any spacing is that the rename is
// atomic, so no reader ever sees a torn note, and that a write which loses the
// compare is refused rather than merged.
//
// 🗝 These are somebody's personal notes and there is no backup. The caller
// sends the ORIGINAL bytes with one range spliced, never a re-rendering of what
// it drew, and the hash above is what proves those original bytes are still the
// ones on disk.
func (s *Service) WriteNote(ctx context.Context, in WriteNoteInput) (WriteNoteResult, error) {
	if err := ctx.Err(); err != nil {
		return WriteNoteResult{}, err
	}
	vault, err := s.requireVault()
	if err != nil {
		return WriteNoteResult{}, err
	}
	if in.BaseHash == "" {
		return WriteNoteResult{}, apierr.Invalid(
			"WIKI_NOTE_BASE_HASH_REQUIRED",
			"Saving a note requires the contentHash it was read with",
			nil,
		)
	}
	abs, rel, ok := confined(vault, in.Path)
	if !ok {
		return WriteNoteResult{}, apierr.NotFound("WIKI_NOTE_NOT_FOUND", "Note not found")
	}
	info, statErr := os.Stat(abs)
	if statErr != nil || !info.Mode().IsRegular() {
		return WriteNoteResult{}, apierr.NotFound("WIKI_NOTE_NOT_FOUND", "Note not found")
	}
	// Over the cap the read returned no content and no hash at all, so there is
	// no precondition to compare against - and hashing it would pull a blob of
	// any size into memory, which is what the read-side cap exists to prevent.
	if info.Size() > maxNoteBytes {
		return WriteNoteResult{}, apierr.Invalid("WIKI_NOTE_TOO_LARGE", "This note is too large to edit", nil)
	}
	if int64(len(in.Content)) > maxNoteBytes {
		return WriteNoteResult{}, apierr.Invalid("WIKI_NOTE_TOO_LARGE", "This edit would make the note too large to open", nil)
	}
	current, readErr := os.ReadFile(abs) //nolint:gosec // abs is confined to the configured vault above
	if readErr != nil {
		return WriteNoteResult{}, apierr.NotFound("WIKI_NOTE_NOT_FOUND", "Note not found")
	}
	if got := ContentHash(current); got != in.BaseHash {
		return WriteNoteResult{}, apierr.Conflict(
			"WIKI_NOTE_CONFLICT",
			"The note changed on disk since it was read",
			map[string]any{
				"currentHash":       got,
				"currentSize":       len(current),
				"currentModifiedAt": info.ModTime().UTC().Format(time.RFC3339Nano),
			},
		)
	}

	data := []byte(in.Content)
	if err := fsatomic.WriteFile(abs, data, info.Mode().Perm()); err != nil {
		return WriteNoteResult{}, apierr.Internal("WIKI_NOTE_WRITE_FAILED", "Could not write the note")
	}
	written := time.Now().UTC()
	if after, err := os.Stat(abs); err == nil {
		written = after.ModTime().UTC()
	}
	return WriteNoteResult{
		Path:        rel,
		ContentHash: ContentHash(data),
		Size:        int64(len(data)),
		ModifiedAt:  written,
	}, nil
}

// ContentHash identifies a note by its exact bytes. The algorithm is in the
// value, so the token stays self-describing if it ever changes.
func ContentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

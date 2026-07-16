package diffhunk

import "strings"

// ChangeKind classifies a contiguous run of changed lines for a gutter marker.
type ChangeKind string

const (
	// ChangeAdded is a run of lines present only on the new side.
	ChangeAdded ChangeKind = "added"
	// ChangeModified is a run of new-side lines that replaced removed content
	// (a deletion block immediately followed by an addition block).
	ChangeModified ChangeKind = "modified"
	// ChangeRemoved is a zero-height marker where content was deleted with no
	// replacement; Start==End is the new-side line now occupying that position
	// (len(newLines)+1 for a deletion at end-of-file).
	ChangeRemoved ChangeKind = "removed"
)

// LineChange is one gutter marker in NEW-side line coordinates (1-based,
// inclusive). For ChangeRemoved, Start==End marks the boundary line.
type LineChange struct {
	Start int
	End   int
	Kind  ChangeKind
}

// ChangedLines parses the unified diff for a single file (the output of
// `git diff HEAD -- <path>`) and returns every changed run as a new-side gutter
// marker, in order. A block is a maximal run of consecutive +/- lines bounded
// by context; a block with both deletions and additions is Modified, additions
// only is Added, and deletions only is a zero-height Removed marker. Input with
// no hunks (unchanged, or not a diff) yields nil.
func ChangedLines(diff string) []LineChange {
	rows := strings.Split(diff, "\n")
	var out []LineChange
	i := 0
	for i < len(rows) {
		if !strings.HasPrefix(rows[i], "@@") {
			i++
			continue
		}
		_, newCur, ok := parseHunkHeader(rows[i])
		if !ok {
			i++
			continue
		}
		i++
		// Accumulate the current change block.
		var adds, dels int
		var firstAdd int // first new-side line of the block's additions
		var blockStartNew int
		flush := func() {
			if adds == 0 && dels == 0 {
				return
			}
			switch {
			case adds > 0 && dels > 0:
				out = append(out, LineChange{Start: firstAdd, End: firstAdd + adds - 1, Kind: ChangeModified})
			case adds > 0:
				out = append(out, LineChange{Start: firstAdd, End: firstAdd + adds - 1, Kind: ChangeAdded})
			default: // dels only
				out = append(out, LineChange{Start: blockStartNew, End: blockStartNew, Kind: ChangeRemoved})
			}
			adds, dels, firstAdd = 0, 0, 0
		}
		for i < len(rows) {
			r := rows[i]
			if strings.HasPrefix(r, "@@") || strings.HasPrefix(r, "diff ") ||
				strings.HasPrefix(r, "--- ") || strings.HasPrefix(r, "+++ ") ||
				strings.HasPrefix(r, "index ") {
				break // next hunk or next file header
			}
			if r == "" {
				i++
				continue // trailing blank from the split
			}
			switch r[0] {
			case ' ':
				flush()
				newCur++
			case '+':
				if adds == 0 {
					firstAdd = newCur
				}
				adds++
				newCur++
			case '-':
				if adds == 0 && dels == 0 {
					// New-side line the deletion sits before (the line that will
					// occupy this position once the removed content is gone).
					blockStartNew = newCur
				}
				dels++
			case '\\':
				// "\ No newline at end of file" — metadata, ignore.
			default:
				// Unexpected content: stop parsing this hunk defensively.
				i = len(rows)
			}
			i++
		}
		flush()
	}
	return out
}

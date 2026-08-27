package session

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Bounds on one ⌘⇧F search.
//
// The whole tree is always SCANNED — a full pass over the human's 6,940-file
// iOS app measures 123 ms — so the totals reported are honest. What is bounded
// is what travels back over the wire, because "self" in that same repo matches
// 12,847 times across 1.7 MB of line text and a rail cannot use 12,847 rows.
// Every bound that bites is reported (Truncated, SearchFile.Truncated), because
// silent truncation reads as "that's all there is".
const (
	// maxSearchMatches caps the matches returned across every file.
	maxSearchMatches = 2000
	// maxSearchFiles caps how many files are returned.
	maxSearchFiles = 500
	// maxSearchFileMatches caps one file's share of the budget, so a single
	// generated or minified file cannot spend the whole of it.
	maxSearchFileMatches = 100
	// maxSearchFileScan caps how many matches are COUNTED in one file. Beyond it
	// the file's Total is a floor rather than an exact count — the alternative is
	// allocating a slice entry per match in a file that holds hundreds of
	// thousands of them.
	maxSearchFileScan = 10000
	// maxSearchFileBytes skips files too large to be worth reading into memory.
	// Well above any hand-written source file and well below a checked-in blob.
	maxSearchFileBytes = 1 << 20 // 1 MiB
	// maxSearchIndex bounds the path list a search will consider, matching the
	// ⌘⇧O index cap so both surfaces agree on what "the project" is.
	maxSearchIndex = maxWorkspaceFileIndex
	// searchBinarySniff is how much of a file is inspected for a NUL byte before
	// deciding it is binary. Same heuristic git itself uses.
	searchBinarySniff = 8192
	// searchPreview caps how much of a matching line travels back, in UTF-16
	// units. A minified bundle's single 400 KB line must not become the response.
	searchPreview = 400
	// searchDeadline stops a pathological tree from holding a request open. The
	// measured full-tree search is 123 ms; anything approaching this is degraded,
	// and partial results with Truncated set beat a hung request.
	searchDeadline = 10 * time.Second
)

// SearchQuery is one ⌘⇧F request.
type SearchQuery struct {
	// Query is the text to find. Empty means "no search yet" — an available
	// result with nothing in it, not an error: the field starts empty.
	Query string
	// MatchCase, WholeWord and Regex are the three toggles the panel shows. They
	// compose: a whole-word regex is wrapped rather than refused.
	MatchCase bool
	WholeWord bool
	Regex     bool
	// Include and Exclude are comma-separated globs over workspace-relative
	// paths. See globSet for the two forms and what each matches.
	Include string
	Exclude string
}

// SearchMatch is one matching line.
//
// Columns are 1-based **UTF-16** offsets, not bytes, because the only consumer
// is Monaco and that is the unit its Position speaks. A line with an emoji
// before the match would otherwise land the caret in the wrong place.
type SearchMatch struct {
	Line      int
	Column    int
	EndColumn int
	// Preview is the line as the rail should draw it, trimmed around the match
	// when the line is very long.
	Preview string
	// PreviewStart/PreviewEnd are 0-based UTF-16 offsets INTO Preview — the range
	// to highlight. They differ from Column/EndColumn whenever Preview was
	// trimmed, which is exactly why both pairs exist: one addresses the file, the
	// other addresses the string on screen.
	PreviewStart int
	PreviewEnd   int
}

// SearchFile is one file's matches.
type SearchFile struct {
	Path    string
	Matches []SearchMatch
	// Total is how many times the file matched, which is >= len(Matches).
	Total int
	// Truncated reports that Matches is a prefix of Total.
	Truncated bool
}

// SearchResult is the answer to one ⌘⇧F request.
type SearchResult struct {
	// Available is false only when there is nothing to search; Reason says which
	// degraded state it is, mirroring WorkspaceChanges and ListWorkspaceFiles.
	Available bool
	Reason    string
	// Query is echoed so a response that arrives after the reader has typed on
	// can be recognised as stale and dropped, rather than painted.
	Query string
	Files []SearchFile
	// TotalMatches and TotalFiles count the WHOLE tree, not the returned prefix.
	TotalMatches int
	TotalFiles   int
	// FilesSearched is how many files were actually read (after the globs, the
	// size cap and the binary sniff), so the panel can say what it looked at.
	FilesSearched int
	// Truncated reports that Files is a prefix — of the matches, of the files, or
	// of both.
	Truncated bool
	// InvalidRegex carries the compile error for a regex the reader is still
	// typing. A half-written pattern is a normal state of the input box, not a
	// request failure, so it comes back as an available result that says why it
	// found nothing.
	InvalidRegex string
}

// SearchWorkspace searches the CONTENTS of every file in a session's workspace.
//
// The engine is Go, not `git grep` and not ripgrep, decided on measurement over
// the human's real 6,940-file / 62.6 MB iOS app: git grep 114 ms, ripgrep 90 ms,
// this 135 ms over a 60–70 ms `git ls-files`. The 20–90 ms the subprocesses save
// does not buy back what they cost —
//
//   - git grep reports no COLUMN, and this feature must open a hit at its column
//     and highlight the match in the results list. Re-deriving that in the
//     renderer means a second implementation of the matcher.
//   - git grep has nothing to say about a workspace that is not a git repo;
//     walkWorkspaceFiles already answers the index for one, so this serves both
//     with one code path.
//   - the pattern is typed by a human into a text box. Go's regexp is RE2, which
//     is linear-time by construction; `git grep -P` is PCRE, where a pathological
//     pattern pins the daemon.
//   - ripgrep is a Homebrew binary the app cannot assume, a daemon launched from
//     Electron does not reliably inherit /opt/homebrew/bin, and it disagreed with
//     both git grep and this scanner on the same corpus (12,840 vs 12,847 hits
//     for "self"). A result count that changes with what is installed is worse
//     than one that is 45 ms slower.
//
// It runs over the SAME index ⌘⇧O and terminal-ref resolution use, so every
// files surface in the app agrees on what the project contains.
//
// CANCELLATION IS CORRECT HERE, and the store's rule against it is not about
// this. #258 and #259 both concluded "never send $/cancelRequest" — that is
// about LANGUAGE SERVERS, where cancelling discards an in-progress type-check
// the next request must redo. A scan holds no such state. Measured on the same
// repo: a full search burns 792 ms of CPU across ~6.4 cores; cancelled at 10 ms
// it burns 68 ms. Typing a word behind a debounce starts several searches, and
// abandoning them without cancelling would spend CPU-seconds on answers nobody
// will read.
func (s *Service) SearchWorkspace(ctx context.Context, id domain.SessionID, q SearchQuery) (SearchResult, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return SearchResult{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return SearchResult{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	workspace := rec.Metadata.WorkspacePath
	if workspace == "" || !isDir(workspace) {
		return SearchResult{Reason: ChangesNoWorkspace, Query: q.Query, Files: []SearchFile{}}, nil
	}
	return searchWorkspaceTree(ctx, workspace, q), nil
}

// searchWorkspaceTree is SearchWorkspace without the session lookup — the whole
// engine, over a directory. Split out so the tests can drive it against a temp
// tree without a store.
func searchWorkspaceTree(ctx context.Context, workspace string, q SearchQuery) SearchResult {
	out := SearchResult{Available: true, Query: q.Query, Files: []SearchFile{}}
	if strings.TrimSpace(q.Query) == "" {
		return out
	}
	re, err := compileSearch(q)
	if err != nil {
		// A pattern the reader has not finished typing is a state of the input
		// box, not a failed request.
		out.InvalidRegex = err.Error()
		return out
	}

	ctx, cancel := context.WithTimeout(ctx, searchDeadline)
	defer cancel()

	paths := workspaceFileIndex(ctx, workspace)
	if len(paths) > maxSearchIndex {
		paths = paths[:maxSearchIndex]
		out.Truncated = true
	}
	include := globSet(q.Include)
	exclude := globSet(q.Exclude)
	root := absRoot(workspace)

	type fileResult struct {
		file    SearchFile
		scanned bool
	}
	jobs := make(chan string)
	results := make(chan fileResult, 64)

	var wg sync.WaitGroup
	for i := 0; i < searchWorkers(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rel := range jobs {
				if ctx.Err() != nil {
					return
				}
				file, scanned := searchOneFile(root, rel, re)
				if scanned {
					results <- fileResult{file: file, scanned: true}
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, rel := range paths {
			if !include.matchesAll(rel) || exclude.matchesAny(rel) {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case jobs <- rel:
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	var files []SearchFile
	for r := range results {
		out.FilesSearched++
		if r.file.Total == 0 {
			continue
		}
		out.TotalFiles++
		out.TotalMatches += r.file.Total
		files = append(files, r.file)
	}
	// A cancelled or timed-out search reports what it managed rather than
	// pretending its partial totals are the whole tree.
	if ctx.Err() != nil {
		out.Truncated = true
	}

	// Path order, so the same query twice paints the same list. The workers
	// finish in whatever order the filesystem hands them back.
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	out.Files, out.Truncated = capSearchFiles(files, out.Truncated)
	return out
}

// searchWorkers sizes the scanning pool. One per core minus none: the scan is
// I/O plus a linear regexp pass, and the daemon has nothing else on the critical
// path while a search is in flight.
func searchWorkers() int {
	n := runtime.NumCPU()
	if n < 1 {
		return 1
	}
	return n
}

// capSearchFiles trims the sorted result set to the wire budget and reports
// whether anything was dropped.
func capSearchFiles(files []SearchFile, truncated bool) ([]SearchFile, bool) {
	if len(files) > maxSearchFiles {
		files = files[:maxSearchFiles]
		truncated = true
	}
	kept := make([]SearchFile, 0, len(files))
	budget := maxSearchMatches
	for _, f := range files {
		if budget <= 0 {
			truncated = true
			break
		}
		if len(f.Matches) > budget {
			f.Matches = f.Matches[:budget]
			f.Truncated = true
		}
		if f.Truncated {
			truncated = true
		}
		budget -= len(f.Matches)
		kept = append(kept, f)
	}
	return kept, truncated
}

// searchOneFile reads one file and collects its matches. The bool reports
// whether the file was actually SCANNED — a file skipped for being too large,
// unreadable or binary is not, and must not count toward FilesSearched.
func searchOneFile(root, rel string, re *regexp.Regexp) (SearchFile, bool) {
	abs := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSearchFileBytes {
		return SearchFile{}, false
	}
	data, err := os.ReadFile(abs) //nolint:gosec // path comes from the workspace's own index
	if err != nil {
		return SearchFile{}, false
	}
	sniff := data
	if len(sniff) > searchBinarySniff {
		sniff = sniff[:searchBinarySniff]
	}
	if bytes.IndexByte(sniff, 0) >= 0 {
		return SearchFile{}, false
	}

	// ONE pass over the whole file rather than a split-then-scan: the regexp is
	// compiled multiline, so `^`/`$` still mean line edges, and a file with no
	// match — the overwhelming majority — costs exactly one linear scan and no
	// allocation at all.
	locs := re.FindAllIndex(data, maxSearchFileScan)
	if len(locs) == 0 {
		return SearchFile{Path: rel}, true
	}

	file := SearchFile{Path: rel, Truncated: len(locs) >= maxSearchFileScan}
	lineNo := 1
	lineStart := 0
	scanned := 0
	for _, loc := range locs {
		// A pattern that can match nothing (`a*`, `x?`) matches at EVERY position,
		// which would report a file as one zero-width hit per byte. Those are not
		// results; the non-empty matches of the same pattern still are.
		if loc[0] == loc[1] {
			continue
		}
		// Walk forward to the line holding this match. Matches come back in
		// order, so the whole file is walked once across all of them.
		for scanned < loc[0] {
			if data[scanned] == '\n' {
				lineNo++
				lineStart = scanned + 1
			}
			scanned++
		}
		lineEnd := bytes.IndexByte(data[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(data)
		} else {
			lineEnd += lineStart
		}
		end := loc[1]
		// A `(?s)`-flagged pattern can match across a newline; clamp so a match
		// never claims a range outside the line it is reported on.
		if end > lineEnd {
			end = lineEnd
		}
		file.Total++
		if len(file.Matches) < maxSearchFileMatches {
			file.Matches = append(file.Matches, matchOnLine(data[lineStart:lineEnd], loc[0]-lineStart, end-lineStart, lineNo))
		} else {
			file.Truncated = true
		}
	}
	if file.Total == 0 {
		return SearchFile{Path: rel}, true
	}
	return file, true
}

// matchOnLine turns byte offsets within a line into the wire form: 1-based
// UTF-16 columns into the real line, plus a preview trimmed around the match
// with its own offsets.
func matchOnLine(line []byte, startByte, endByte, lineNo int) SearchMatch {
	startU16 := utf16Len(line[:startByte])
	endU16 := startU16 + utf16Len(line[startByte:endByte])

	// The whole line, when it fits. Nothing is trimmed in the common case, so
	// the preview offsets are the column offsets and the two agree — and the
	// UTF-16 re-encoding below, three allocations per match, never happens.
	if utf16Len(line) <= searchPreview {
		return SearchMatch{
			Line:         lineNo,
			Column:       startU16 + 1,
			EndColumn:    endU16 + 1,
			Preview:      string(line),
			PreviewStart: startU16,
			PreviewEnd:   endU16,
		}
	}

	// A long line is windowed around the match rather than head-truncated: a hit
	// 40,000 characters into a minified bundle is invisible in the first 400.
	units := utf16.Encode([]rune(string(line)))
	lead := searchPreview / 4
	from := startU16 - lead
	if from < 0 {
		from = 0
	}
	to := from + searchPreview
	if to > len(units) {
		to = len(units)
		from = to - searchPreview
	}
	previewStart := startU16 - from
	previewEnd := endU16 - from
	if previewEnd > searchPreview {
		previewEnd = searchPreview
	}
	if previewStart > searchPreview {
		// The match itself starts past the window (a very long match). Show the
		// window and highlight nothing rather than an offset off the end.
		previewStart = searchPreview
		previewEnd = searchPreview
	}
	return SearchMatch{
		Line:         lineNo,
		Column:       startU16 + 1,
		EndColumn:    endU16 + 1,
		Preview:      string(utf16.Decode(units[from:to])),
		PreviewStart: previewStart,
		PreviewEnd:   previewEnd,
	}
}

// utf16Len is how many UTF-16 code units the bytes encode to — the unit Monaco
// counts columns in. Invalid bytes count as one unit each (utf8.DecodeRune
// returns RuneError with size 1), which keeps a column on a latin-1 file
// sensible instead of collapsing it.
func utf16Len(b []byte) int {
	n := 0
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
		i += size
	}
	return n
}

// compileSearch turns the three toggles into one multiline regexp.
//
// Everything becomes a regexp, including a plain literal: RE2 recognises a
// literal pattern and searches it with the same memchr-style scan a hand-written
// bytes.Index would, so the second code path would buy nothing and could
// disagree with the first about Unicode case folding.
func compileSearch(q SearchQuery) (*regexp.Regexp, error) {
	pattern := q.Query
	if !q.Regex {
		pattern = regexp.QuoteMeta(pattern)
	} else {
		// A user regex is wrapped so that alternation binds inside it: `a|b` with
		// a whole-word wrapper must be `\b(?:a|b)\b`, never `\ba|b\b`.
		pattern = "(?:" + pattern + ")"
	}
	if q.WholeWord {
		pattern = `\b` + pattern + `\b`
	}
	flags := "(?m)"
	if !q.MatchCase {
		flags += "(?i)"
	}
	return regexp.Compile(flags + pattern)
}

// globs is a parsed include or exclude list.
type globs struct {
	res []*regexp.Regexp
}

// globSet parses a comma-separated glob list into matchers.
//
// Two forms, one rule each, so the behaviour is guessable without documentation:
//
//   - a pattern with NO slash matches any path SEGMENT — `*.swift` matches by
//     basename anywhere, and `Pods` excludes everything under a directory of
//     that name, which is what someone typing a folder name into "exclude"
//     means.
//   - a pattern WITH a slash matches the whole workspace-relative path, `**`
//     crossing separators and `*` stopping at one — and also matches everything
//     beneath it, so `App/Features` scopes to that directory.
func globSet(spec string) globs {
	var g globs
	for _, raw := range strings.Split(spec, ",") {
		p := strings.TrimSpace(raw)
		p = strings.TrimPrefix(p, "./")
		p = strings.TrimSuffix(p, "/")
		if p == "" {
			continue
		}
		var expr string
		if strings.Contains(p, "/") {
			expr = "^" + globExpr(p) + "(?:/.*)?$"
		} else {
			expr = "(?:^|/)" + globExpr(p) + "(?:/|$)"
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			// An unparseable glob narrows nothing rather than excluding
			// everything: the reader is mid-typing, not asking for an empty list.
			continue
		}
		g.res = append(g.res, re)
	}
	return g
}

// matchesAll is the INCLUDE test: an empty list includes everything.
func (g globs) matchesAll(rel string) bool {
	if len(g.res) == 0 {
		return true
	}
	return g.matchesAny(rel)
}

func (g globs) matchesAny(rel string) bool {
	for _, re := range g.res {
		if re.MatchString(rel) {
			return true
		}
	}
	return false
}

// globExpr translates one glob into a regexp fragment. `**` crosses separators,
// `*` and `?` do not, and everything else is literal.
func globExpr(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '*':
			if i+1 < len(p) && p[i+1] == '*' {
				i++
				// `**/` collapses to "zero or more directories", so `**/x` matches
				// a bare `x` as well as `a/b/x`.
				if i+1 < len(p) && p[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
					continue
				}
				b.WriteString(".*")
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(p[i])))
		}
	}
	return b.String()
}

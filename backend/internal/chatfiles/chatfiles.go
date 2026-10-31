// Package chatfiles resolves the file names a chat session's agent writes to
// real files the chat view may show, and opens them under one confinement rule:
// a file is served only when, after symlinks, it is a regular file of an allowed
// type inside the session's worktree or its project's knowledge directory.
package chatfiles

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// Root names one of the directories a session may read.
type Root string

// The two directories a session may read.
const (
	Worktree  Root = "worktree"
	Knowledge Root = "knowledge"
)

var rootOrder = []Root{Worktree, Knowledge}

// Kind tells the chat view how to present a file.
type Kind string

// The kinds in the allowlist.
const (
	KindImage    Kind = "image"
	KindMarkdown Kind = "markdown"
	KindHTML     Kind = "html"
	KindPDF      Kind = "pdf"
	KindJSON     Kind = "json"
	KindText     Kind = "text"
	KindLog      Kind = "log"
	KindDiff     Kind = "diff"
)

const mib = 1 << 20

const maxGlobMatches = 24

type kindSpec struct {
	kind Kind
	mime string
	max  int64
}

// kinds is the type allowlist: extension to kind, Content-Type and size cap.
var kinds = map[string]kindSpec{
	"png":      {KindImage, "image/png", 25 * mib},
	"jpg":      {KindImage, "image/jpeg", 25 * mib},
	"jpeg":     {KindImage, "image/jpeg", 25 * mib},
	"gif":      {KindImage, "image/gif", 25 * mib},
	"webp":     {KindImage, "image/webp", 25 * mib},
	"svg":      {KindImage, "image/svg+xml", 2 * mib},
	"md":       {KindMarkdown, "text/markdown; charset=utf-8", 2 * mib},
	"markdown": {KindMarkdown, "text/markdown; charset=utf-8", 2 * mib},
	"html":     {KindHTML, "text/html; charset=utf-8", 2 * mib},
	"htm":      {KindHTML, "text/html; charset=utf-8", 2 * mib},
	"pdf":      {KindPDF, "application/pdf", 50 * mib},
	"json":     {KindJSON, "application/json", 2 * mib},
	"txt":      {KindText, "text/plain; charset=utf-8", 2 * mib},
	"csv":      {KindText, "text/plain; charset=utf-8", 2 * mib},
	"log":      {KindLog, "text/plain; charset=utf-8", 2 * mib},
	"diff":     {KindDiff, "text/plain; charset=utf-8", 2 * mib},
	"patch":    {KindDiff, "text/plain; charset=utf-8", 2 * mib},
}

var (
	// ErrNotFound covers every refusal that is not a size refusal, so a caller
	// cannot tell a missing file from one it may not read.
	ErrNotFound = errors.New("chatfiles: file not found")
	// ErrTooLarge means the file is allowed but over its type's size cap.
	ErrTooLarge = errors.New("chatfiles: file too large")
)

// File is a servable file, addressed the way Open takes it back.
type File struct {
	Root Root
	// Path is slash-separated and relative to the root's resolved directory.
	Path    string
	Kind    Kind
	MIME    string
	Size    int64
	ModTime time.Time
}

// Roots maps each root to its symlink-resolved absolute directory. A root whose
// directory is missing is absent.
type Roots map[Root]string

// NewRoots resolves both directories once, because macOS temp and home paths are symlinks.
func NewRoots(worktree, knowledge string) Roots {
	roots := Roots{}
	for root, dir := range map[Root]string{Worktree: worktree, Knowledge: knowledge} {
		if dir == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			continue
		}
		if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
			continue
		}
		roots[root] = resolved
	}
	return roots
}

// Open returns the servable file rel names in root; ".." in rel is refused, never clamped.
func (r Roots) Open(root Root, rel string) (File, string, error) {
	base, ok := r[root]
	if !ok || !cleanRel(rel) {
		return File{}, "", ErrNotFound
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(base, filepath.FromSlash(rel)))
	if err != nil {
		return File{}, "", ErrNotFound
	}
	inside, ok := relWithin(base, resolved)
	if !ok {
		return File{}, "", ErrNotFound
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return File{}, "", ErrNotFound
	}
	spec, ok := kinds[strings.ToLower(strings.TrimPrefix(filepath.Ext(resolved), "."))]
	if !ok {
		return File{}, "", ErrNotFound
	}
	if info.Size() > spec.max {
		return File{}, "", ErrTooLarge
	}
	return File{
		Root:    root,
		Path:    filepath.ToSlash(inside),
		Kind:    spec.kind,
		MIME:    spec.mime,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}, resolved, nil
}

// Resolve returns the servable files ref names. It never errors: a ref that
// names nothing, escapes a root, or names a file off the allowlist yields none.
//
//   - "~/x" expands against the home directory. An absolute path must resolve
//     inside a root and is reported relative to it.
//   - A relative ref is tried in the worktree, then in the knowledge root, and
//     the first root that has it wins.
//   - A ref with * ? or [ is a glob, matched inside each root in that order.
//
// A ref with a ".." segment names nothing.
func (r Roots) Resolve(ref string) []File {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.ContainsRune(ref, 0) || hasParentSegment(ref) {
		return nil
	}
	if ref == "~" || strings.HasPrefix(ref, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return nil
		}
		ref = filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(ref, "~"), "/")))
	}
	glob := strings.ContainsAny(ref, "*?[")
	if filepath.IsAbs(ref) {
		if glob {
			return r.globAbsolute(ref)
		}
		return r.openAbsolute(ref)
	}
	rel := path.Clean(filepath.ToSlash(ref))
	if rel == "." {
		return nil
	}
	if glob {
		return r.globRelative(rel)
	}
	for _, root := range rootOrder {
		if file, _, err := r.Open(root, rel); err == nil {
			return []File{file}
		}
	}
	return nil
}

func (r Roots) openAbsolute(abs string) []File {
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil
	}
	for _, root := range rootOrder {
		base, ok := r[root]
		if !ok {
			continue
		}
		if rel, ok := relWithin(base, resolved); ok {
			if file, _, err := r.Open(root, filepath.ToSlash(rel)); err == nil {
				return []File{file}
			}
			return nil
		}
	}
	return nil
}

// globAbsolute expands only below the pattern's literal prefix, and only when
// that prefix is inside a root, so /*/*.png never walks the disk.
func (r Roots) globAbsolute(pattern string) []File {
	segments := strings.Split(filepath.ToSlash(filepath.Clean(pattern)), "/")
	literal := 0
	for literal < len(segments) && !strings.ContainsAny(segments[literal], "*?[") {
		literal++
	}
	prefix, err := filepath.EvalSymlinks(filepath.FromSlash(strings.Join(segments[:literal], "/") + "/"))
	if err != nil {
		return nil
	}
	rest := strings.Join(segments[literal:], "/")
	out := make([]File, 0, maxGlobMatches)
	for _, root := range rootOrder {
		base, ok := r[root]
		if !ok {
			continue
		}
		inside, ok := relWithin(base, prefix)
		if !ok {
			continue
		}
		out = append(out, r.globIn(root, path.Join(filepath.ToSlash(inside), rest), maxGlobMatches-len(out))...)
	}
	return out
}

func (r Roots) globRelative(pattern string) []File {
	out := make([]File, 0, maxGlobMatches)
	for _, root := range rootOrder {
		out = append(out, r.globIn(root, pattern, maxGlobMatches-len(out))...)
	}
	return out
}

func (r Roots) globIn(root Root, pattern string, limit int) []File {
	base, ok := r[root]
	if !ok || limit <= 0 {
		return nil
	}
	matches, err := filepath.Glob(escapeGlob(base) + string(filepath.Separator) + filepath.FromSlash(pattern))
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	out := make([]File, 0, min(len(matches), limit))
	seen := map[string]bool{}
	for _, match := range matches {
		rel, err := filepath.Rel(base, match)
		if err != nil {
			continue
		}
		file, _, err := r.Open(root, filepath.ToSlash(rel))
		if err != nil || seen[file.Path] {
			continue
		}
		seen[file.Path] = true
		out = append(out, file)
		if len(out) == limit {
			break
		}
	}
	return out
}

func escapeGlob(dir string) string {
	var b strings.Builder
	for _, c := range dir {
		if strings.ContainsRune(`*?[\`, c) {
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func cleanRel(rel string) bool {
	if rel == "" || strings.ContainsRune(rel, 0) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) || filepath.IsAbs(rel) {
		return false
	}
	return !hasParentSegment(rel)
}

func hasParentSegment(p string) bool {
	return slices.Contains(strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }), "..")
}

func relWithin(root, abs string) (string, bool) {
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

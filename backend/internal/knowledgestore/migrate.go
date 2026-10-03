package knowledgestore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Before the store root was pinned to HomeRelDir, the teardown safety net
// wrote under <dataDir>/knowledge/<project>/plans (~/.ao/data/knowledge by
// default) - a directory no prompt names, so no agent ever read what landed
// there. MigrateStranded empties it once: copies of committed docs are
// dropped, everything else moves into the real store.

// MigrationAction is what MigrateStranded did, or in a dry run would do, with
// one stranded file.
type MigrationAction string

const (
	// ActionDropCommitted means the file's exact content is committed at the path
	// its name was preserved from, on some branch of the project's repo.
	ActionDropCommitted MigrationAction = "drop-committed"
	// ActionDropDuplicate means the store already holds a byte-identical file.
	ActionDropDuplicate MigrationAction = "drop-duplicate"
	// ActionMove means a genuine rescue, moved into the store's plans directory.
	ActionMove MigrationAction = "move"
	// ActionLeave means it was not classified (an unexpected entry, or git could not
	// answer), so it is left exactly where it is.
	ActionLeave MigrationAction = "leave"
)

// MigrationEntry records the outcome for one stranded path.
type MigrationEntry struct {
	Project string
	Action  MigrationAction
	From    string
	// To is the store file a move wrote, or the identical file a duplicate
	// matched. Empty otherwise.
	To string
	// Reason explains a drop or a leave: the committed path, or the error.
	Reason string
}

// MigrationReport lists every stranded path MigrateStranded looked at.
type MigrationReport struct {
	Entries []MigrationEntry
}

// Count returns how many entries took action a.
func (r MigrationReport) Count(a MigrationAction) int {
	n := 0
	for _, e := range r.Entries {
		if e.Action == a {
			n++
		}
	}
	return n
}

// MigrateOptions configures MigrateStranded.
type MigrateOptions struct {
	// StrandedRoot is the old store root, <dataDir>/knowledge.
	StrandedRoot string
	// StoreRoot is the real store root (see Root).
	StoreRoot string
	// RepoPath returns a project's repository path, or "" when the project is
	// unknown. Archived projects should still resolve: their docs were stranded
	// all the same. An error (the lookup itself failed) leaves the project's
	// files untouched for a later run.
	RepoPath func(projectID string) (string, error)
	// DryRun reports what would happen without touching disk.
	DryRun bool
}

// MigrateStranded moves the docs stranded under StrandedRoot into StoreRoot.
//
// Only regular files directly in <StrandedRoot>/<project>/plans are touched;
// anything else is reported as left. For each file:
//   - its exact content is committed, on some branch of the project's repo, at
//     the path its "<branch>--<path>" name was preserved from: it is dropped;
//   - otherwise it moves to <StoreRoot>/<project>/plans/ under its own name,
//     never replacing a file there: identical content means the stranded copy
//     is a duplicate and is dropped, different content takes the next free
//     "-N" suffix.
//
// A project whose repo is unknown or not a git checkout has nothing to compare
// against, so all its files move (nothing is lost, and nothing could ever
// classify them). A project whose repo git cannot read right now is left
// untouched for a later run. Directories emptied by the migration are removed.
//
// It is idempotent: a second run finds nothing left to do. When StrandedRoot is
// StoreRoot (AO_DATA_DIR pointed at ~/.ao) there is nothing stranded and it is
// a no-op. The returned error joins per-project failures; the report is valid
// either way.
func MigrateStranded(ctx context.Context, opts MigrateOptions) (MigrationReport, error) {
	var report MigrationReport
	if sameDir(opts.StrandedRoot, opts.StoreRoot) {
		return report, nil
	}
	projects, err := os.ReadDir(opts.StrandedRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	var errs []error
	for _, p := range projects {
		from := filepath.Join(opts.StrandedRoot, p.Name())
		if !p.IsDir() {
			report.Entries = append(report.Entries, MigrationEntry{Action: ActionLeave, From: from, Reason: "not a project directory"})
			continue
		}
		entries, projErr := migrateProject(ctx, opts, p.Name())
		report.Entries = append(report.Entries, entries...)
		if projErr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.Name(), projErr))
		}
		if !opts.DryRun {
			removeIfEmpty(filepath.Join(from, "plans"))
			removeIfEmpty(from)
		}
	}
	if !opts.DryRun {
		removeIfEmpty(opts.StrandedRoot)
	}
	return report, errors.Join(errs...)
}

func migrateProject(ctx context.Context, opts MigrateOptions, project string) ([]MigrationEntry, error) {
	projectDir := filepath.Join(opts.StrandedRoot, project)
	plansDir := filepath.Join(projectDir, "plans")
	var out []MigrationEntry
	leave := func(path, reason string) {
		out = append(out, MigrationEntry{Project: project, Action: ActionLeave, From: path, Reason: reason})
	}

	top, err := os.ReadDir(projectDir)
	if err != nil {
		return out, err
	}
	for _, e := range top {
		if e.Name() != "plans" || !e.IsDir() {
			leave(filepath.Join(projectDir, e.Name()), "not under plans/")
		}
	}
	entries, err := os.ReadDir(plansDir)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var files []string
	for _, e := range entries {
		path := filepath.Join(plansDir, e.Name())
		if !e.Type().IsRegular() {
			leave(path, "not a regular file")
			continue
		}
		files = append(files, path)
	}
	if len(files) == 0 {
		return out, nil
	}

	repoPath, err := opts.RepoPath(project)
	var committedAt map[string]string
	if err == nil {
		committedAt, err = classifyCommitted(ctx, repoPath, files)
	}
	if err != nil {
		for _, f := range files {
			leave(f, "could not tell committed copies apart: "+err.Error())
		}
		return out, err
	}

	dest := PlansDir(opts.StoreRoot, project)
	var reserved map[string]bool
	if opts.DryRun {
		reserved = map[string]bool{}
	}
	var errs []error
	for _, f := range files {
		if path, ok := committedAt[f]; ok {
			if !opts.DryRun {
				if err := os.Remove(f); err != nil {
					errs = append(errs, err)
					continue
				}
			}
			out = append(out, MigrationEntry{Project: project, Action: ActionDropCommitted, From: f, Reason: "committed at " + path})
			continue
		}
		entry, err := moveIntoStore(f, dest, reserved)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(f), err))
			leave(f, err.Error())
			continue
		}
		entry.Project = project
		out = append(out, entry)
	}
	return out, errors.Join(errs...)
}

// classifyCommitted maps each stranded file whose exact content is committed,
// at the repo path its preserved name came from, to that path. An empty or
// non-git repoPath classifies nothing as committed, without error.
func classifyCommitted(ctx context.Context, repoPath string, files []string) (map[string]string, error) {
	if repoPath == "" || !isGitRepo(ctx, repoPath) {
		return nil, nil
	}
	blobs, err := committedBlobs(ctx, repoPath, nil)
	if err != nil {
		return nil, err
	}
	// Index the committed docs by the slug their preserved names carry. Two
	// paths can share a slug ("docs/plans/a.md", "docs-plans/a.md"); either
	// one's content counts.
	type doc struct {
		path  string
		blobs map[string]bool
	}
	bySlug := map[string]*doc{}
	for path, ids := range blobs {
		if !isStrayDoc(path) {
			continue
		}
		s := slug(path)
		d := bySlug[s]
		if d == nil {
			d = &doc{path: path, blobs: map[string]bool{}}
			bySlug[s] = d
		}
		for id := range ids {
			d.blobs[id] = true
		}
	}
	ids, err := hashFiles(ctx, repoPath, files)
	if err != nil {
		return nil, err
	}
	committed := map[string]string{}
	for i, f := range files {
		name := filepath.Base(f)
		for s, d := range bySlug {
			if d.blobs[ids[i]] && preservedFrom(name, s) {
				committed[f] = d.path
				break
			}
		}
	}
	return committed, nil
}

// preservedFrom reports whether name is what copyPreserve would have produced
// for a doc whose path slug is relSlug: "<branch>--<relSlug>", or the same with
// a "-N" collision suffix before the extension.
func preservedFrom(name, relSlug string) bool {
	if len(name) > len(relSlug)+2 && strings.HasSuffix(name, "--"+relSlug) {
		return true
	}
	stem, ext := splitExt(relSlug)
	body, ok := strings.CutSuffix(name, ext)
	if !ok {
		return false
	}
	i := strings.LastIndex(body, "-")
	if i < 0 || i == len(body)-1 || strings.Trim(body[i+1:], "0123456789") != "" {
		return false
	}
	rest := body[:i]
	return len(rest) > len(stem)+2 && strings.HasSuffix(rest, "--"+stem)
}

// moveIntoStore moves src into dest without replacing anything there (see
// placeNoClobber), keeping its modification time. With reserved non-nil it is
// a dry run and touches nothing.
func moveIntoStore(src, dest string, reserved map[string]bool) (MigrationEntry, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return MigrationEntry{}, err
	}
	info, err := os.Stat(src)
	if err != nil {
		return MigrationEntry{}, err
	}
	if reserved == nil {
		// Created only for a file that needs it: a project whose strays were
		// all committed copies gets no empty dir in the store.
		if err := os.MkdirAll(dest, 0o750); err != nil {
			return MigrationEntry{}, err
		}
	}
	dst, wrote, err := placeNoClobber(dest, filepath.Base(src), data, reserved)
	if err != nil {
		return MigrationEntry{}, err
	}
	entry := MigrationEntry{Action: ActionMove, From: src, To: dst}
	if !wrote {
		entry.Action = ActionDropDuplicate
		entry.Reason = "identical to " + dst
	}
	if reserved != nil {
		return entry, nil
	}
	if wrote {
		_ = os.Chtimes(dst, info.ModTime(), info.ModTime())
	}
	if err := os.Remove(src); err != nil {
		return MigrationEntry{}, err
	}
	return entry, nil
}

// sameDir reports whether a and b name the same directory, following symlinks
// where they exist.
func sameDir(a, b string) bool {
	resolve := func(p string) string {
		p = filepath.Clean(p)
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	return resolve(a) == resolve(b)
}

func removeIfEmpty(dir string) {
	_ = os.Remove(dir) // fails, harmlessly, on a non-empty or missing dir
}

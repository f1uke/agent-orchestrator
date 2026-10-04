// Package apply writes an approved learning proposal to disk: a new Claude
// Code memory file and its MEMORY.md line, a changed memory file or skill, or
// lines added to the person's CLAUDE.md. It never writes over a file that
// changed after the proposal was made (the proposal goes stale instead), never
// writes outside the places learning may change, backs up what it replaces, and
// writes each file atomically.
package apply

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ErrStale is a target that changed after the proposal was made.
var ErrStale = errors.New("the file changed after the proposal was made")

// Roots are where learning may write.
type Roots struct {
	// Home is the person's home: ~/.claude/projects/*/memory, ~/.claude/skills
	// and ~/.claude/CLAUDE.md are under it.
	Home string
	// History is where replaced versions are backed up.
	History string
}

// Apply writes p, with content in place of its proposed content when the
// person edited it. It returns the hash of the file written.
func Apply(r Roots, p domain.LearnProposal, content string, now time.Time) (string, error) {
	if content == "" {
		content = p.NewContent
	}
	if strings.TrimSpace(content) == "" {
		return "", errors.New("refusing to write an empty file")
	}
	if err := r.allowed(p.TargetPath); err != nil {
		return "", err
	}
	switch p.Action {
	case domain.LearnProposeCreateMemory:
		return r.createMemory(p, content, now)
	case domain.LearnProposeUpdateMemory, domain.LearnProposeUpdateSkill, domain.LearnProposeEditRuleFile:
		old, err := os.ReadFile(p.TargetPath)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", p.TargetPath, err)
		}
		if sha(old) != p.BaseSHA256 {
			return "", ErrStale
		}
		if err := r.backup(p.TargetPath, old, now); err != nil {
			return "", err
		}
		if err := writeAtomic(p.TargetPath, []byte(content)); err != nil {
			return "", err
		}
		return sha([]byte(content)), nil
	default:
		return "", fmt.Errorf("a %s proposal writes no file", p.Action)
	}
}

func (r Roots) createMemory(p domain.LearnProposal, content string, now time.Time) (string, error) {
	if _, err := os.Stat(p.TargetPath); err == nil {
		return "", ErrStale
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	// Everything that can fail before a write is done first, so a failure
	// leaves nothing behind: the index's next content and its backup.
	index := filepath.Join(filepath.Dir(p.TargetPath), "MEMORY.md")
	old, err := os.ReadFile(index)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	writeIndex := p.IndexLine != "" && !strings.Contains(string(old), p.IndexLine)
	next := string(old)
	if writeIndex {
		if len(old) > 0 {
			if err := r.backup(index, old, now); err != nil {
				return "", err
			}
		}
		if next != "" && !strings.HasSuffix(next, "\n") {
			next += "\n"
		}
		next += p.IndexLine + "\n"
	}
	if err := os.MkdirAll(filepath.Dir(p.TargetPath), 0o750); err != nil {
		return "", err
	}
	if err := writeAtomic(p.TargetPath, []byte(content)); err != nil {
		return "", err
	}
	if writeIndex {
		if err := writeAtomic(index, []byte(next)); err != nil {
			// A memory without its index line is half a change: take it back.
			_ = os.Remove(p.TargetPath)
			return "", fmt.Errorf("write MEMORY.md: %w", err)
		}
	}
	return sha([]byte(content)), nil
}

// allowed confines a target, through symlinks, to the places learning may
// change: a project's Claude Code memory, the person's skills, their CLAUDE.md.
func (r Roots) allowed(target string) error {
	if r.Home == "" || !filepath.IsAbs(target) {
		return fmt.Errorf("refusing to write %q", target)
	}
	resolved := resolve(target)
	claude := resolve(filepath.Join(r.Home, ".claude"))
	rel, err := filepath.Rel(claude, resolved)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("refusing to write %s: outside ~/.claude", target)
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	switch {
	case rel == "CLAUDE.md":
		return nil
	case len(parts) == 4 && parts[0] == "projects" && parts[2] == "memory" && strings.HasSuffix(parts[3], ".md") && parts[3] != "MEMORY.md":
		return nil
	case len(parts) == 3 && parts[0] == "skills" && parts[2] == "SKILL.md":
		return nil
	}
	return fmt.Errorf("refusing to write %s: learning only writes memory files, skills and CLAUDE.md", target)
}

// resolve follows symlinks through the deepest part of p that exists, so a
// file - or a whole directory - that does not exist yet still resolves the
// way it will once written.
func resolve(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for dir := p; ; dir = filepath.Dir(dir) {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return p
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}

// backup keeps the version a write replaces under History, mirroring its path
// under Home (every directory name stays short) with a timestamped file.
func (r Roots) backup(path string, content []byte, now time.Time) error {
	if r.History == "" {
		return errors.New("no backup directory")
	}
	rel, err := filepath.Rel(r.Home, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = filepath.Base(path)
	}
	dir := filepath.Join(r.History, rel)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, now.UTC().Format("20060102T150405.000000000Z")), content, 0o600)
}

// writeAtomic writes through a temp file in the same directory, synced, then
// renamed over the target, keeping the target's mode when it exists.
func writeAtomic(path string, content []byte) error {
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".learn-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func sha(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

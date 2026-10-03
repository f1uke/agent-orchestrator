package claudecode

import (
	"path/filepath"
	"strings"
)

// Where Claude Code keeps an AO session's transcripts, for learning capture
// (internal/observe/learncapture). Capture reads transcript CONTENT under its
// own contract (docs/architecture.md, "Learning capture"); the helpers here only
// say where the files are, so the layout knowledge stays in this adapter.

// ProjectsDir is the root Claude Code stores conversations under, honoring
// CLAUDE_CONFIG_DIR the way Claude Code does.
func ProjectsDir() (string, error) {
	return claudeProjectsDir()
}

// WorkspaceTranscriptDir is the directory Claude Code writes the transcripts
// of every conversation started in workspacePath to. The path is symlink
// resolved first, as Claude Code resolves the cwd before deriving the name.
func WorkspaceTranscriptDir(workspacePath string) (string, error) {
	base, err := claudeProjectsDir()
	if err != nil {
		return "", err
	}
	resolved := workspacePath
	if r, err := filepath.EvalSymlinks(workspacePath); err == nil {
		resolved = r
	}
	return filepath.Join(base, claudeProjectDirName(resolved)), nil
}

// PinnedTranscriptPath is the transcript of the conversation AO launched for
// the session with --session-id. It may not exist yet.
func PinnedTranscriptPath(workspacePath, aoSessionID string) (string, error) {
	dir, err := WorkspaceTranscriptDir(workspacePath)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, claudeSessionUUID(aoSessionID)+".jsonl"), nil
}

// IsTranscriptPath reports whether path names a transcript file directly inside
// one of Claude Code's per-project directories. A hook reports the path it was
// handed; this is the check that it points where a transcript lives before AO
// records it, so a malformed or hostile payload cannot steer capture at an
// arbitrary file.
func IsTranscriptPath(path string) bool {
	if !filepath.IsAbs(path) || filepath.Ext(path) != ".jsonl" {
		return false
	}
	base, err := claudeProjectsDir()
	if err != nil {
		return false
	}
	clean := filepath.Clean(path)
	rel, err := filepath.Rel(filepath.Clean(base), clean)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	// <projectsDir>/<project dir>/<id>.jsonl - exactly one directory deep.
	return len(strings.Split(rel, string(filepath.Separator))) == 2
}

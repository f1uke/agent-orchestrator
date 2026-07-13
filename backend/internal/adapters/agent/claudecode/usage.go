package claudecode

// Token-usage reading for the Claude Code adapter.
//
// AO never surfaced the token cost of a session. The Claude Code harness records,
// on every assistant message, a real usage{input, cache_creation, cache_read,
// output}. This file locates a session's transcript(s) and sums that usage so AO
// can persist per-session token totals (see observe/tokenusage).
//
// CORRECTNESS — dedupe by message id: Claude Code writes ONE JSONL line per
// assistant content block (thinking / text / tool_use), and every one of those
// lines repeats the SAME message.usage (usage is for the whole message, not the
// block). Summing every "assistant" line therefore double/triple-counts. The parser
// counts each message.id exactly once. Only aggregate numbers are read; transcript
// content is never retained or sent anywhere.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ReadSessionUsage sums the token usage across a claude-code session's
// transcript(s). It returns ok=false (never an error) when the session is not a
// claude-code session or has no transcript on disk yet, so callers degrade
// gracefully for other agents and freshly-spawned sessions. A read/parse failure
// on an existing transcript is returned as err (non-fatal for the caller to log
// and skip); malformed individual lines are silently skipped so a partial trailing
// write never fails the whole parse.
func ReadSessionUsage(rec domain.SessionRecord) (domain.TokenUsage, bool, error) {
	if rec.Harness != domain.HarnessClaudeCode {
		return domain.TokenUsage{}, false, nil
	}
	paths := TranscriptPaths(rec.Metadata.WorkspacePath, string(rec.ID), rec.Metadata.AgentSessionID)
	if len(paths) == 0 {
		return domain.TokenUsage{}, false, nil
	}
	// Dedup message ids across ALL of the session's transcript files so a message
	// that somehow appears in more than one is still counted once.
	seen := make(map[string]struct{})
	var total domain.TokenUsage
	found := false
	for _, p := range paths {
		u, err := parseTranscriptUsage(p, seen)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// Raced away between the stat in TranscriptPaths and open; skip it.
				continue
			}
			return domain.TokenUsage{}, false, err
		}
		found = true
		total = total.Add(u)
	}
	if !found {
		return domain.TokenUsage{}, false, nil
	}
	return total, true, nil
}

// TranscriptPaths returns the existing Claude Code transcript files for an AO
// session, newest-candidate first. Claude Code stores a conversation at
// <projectsDir>/<projectDirName(workspace)>/<sessionUUID>.jsonl. Two ids can point
// at a session's transcript: the hook-captured native id (Metadata.AgentSessionID)
// and the deterministic id AO pins via --session-id (claudeSessionUUID). They are
// normally identical, but both are probed and de-duplicated so either path resolves.
// Only files that exist on disk are returned; an empty result means "no transcript
// (yet)". The workspace path is symlink-resolved because Claude derives the project
// dir from the resolved cwd (mirrors claudeConversationExists).
func TranscriptPaths(workspacePath, aoSessionID, agentSessionID string) []string {
	base, err := claudeProjectsDir()
	if err != nil || strings.TrimSpace(workspacePath) == "" {
		return nil
	}
	resolved := workspacePath
	if r, err := filepath.EvalSymlinks(workspacePath); err == nil {
		resolved = r
	}
	dir := filepath.Join(base, claudeProjectDirName(resolved))

	ids := make([]string, 0, 2)
	if s := strings.TrimSpace(agentSessionID); s != "" {
		ids = append(ids, s)
	}
	if strings.TrimSpace(aoSessionID) != "" {
		ids = append(ids, claudeSessionUUID(aoSessionID))
	}

	seen := make(map[string]bool, len(ids))
	var paths []string
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		p := filepath.Join(dir, id+".jsonl")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			paths = append(paths, p)
		}
	}
	return paths
}

// transcriptLine is the minimal subset of a Claude Code transcript JSONL record we
// read: the record type, the line's own uuid (a dedup fallback), and the assistant
// message's id + usage buckets.
type transcriptLine struct {
	Type    string `json:"type"`
	UUID    string `json:"uuid"`
	Message struct {
		ID    string `json:"id"`
		Usage *struct {
			InputTokens              int64 `json:"input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// parseTranscriptUsage streams one transcript file and sums usage over its
// assistant messages, skipping any id already in seen (so it dedupes across files).
// It reads line-by-line via a bufio.Reader (not bufio.Scanner) so an arbitrarily
// long line — e.g. a message carrying a large base64 tool payload — never trips a
// max-token-size limit and truncates the parse.
func parseTranscriptUsage(path string, seen map[string]struct{}) (domain.TokenUsage, error) {
	f, err := os.Open(path)
	if err != nil {
		return domain.TokenUsage{}, err
	}
	defer func() { _ = f.Close() }()

	r := bufio.NewReaderSize(f, 1<<20)
	var total domain.TokenUsage
	for {
		line, readErr := r.ReadBytes('\n')
		if len(line) > 0 {
			if u, id, ok := parseAssistantUsage(line); ok {
				if _, dup := seen[id]; !dup {
					seen[id] = struct{}{}
					total = total.Add(u)
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return total, nil
			}
			return domain.TokenUsage{}, readErr
		}
	}
}

// parseAssistantUsage extracts one assistant message's usage and its dedup key from
// a single JSONL line. ok=false for non-assistant lines, lines without a usage
// block, malformed JSON (skipped, not fatal), and the rare line with no id at all
// (skipped rather than risk double-counting). The dedup key is message.id, falling
// back to the line uuid so an id-less-but-uuid'd line is still counted once.
func parseAssistantUsage(line []byte) (domain.TokenUsage, string, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return domain.TokenUsage{}, "", false
	}
	var tl transcriptLine
	if json.Unmarshal(line, &tl) != nil {
		return domain.TokenUsage{}, "", false
	}
	if tl.Type != "assistant" || tl.Message.Usage == nil {
		return domain.TokenUsage{}, "", false
	}
	id := tl.Message.ID
	if id == "" {
		id = tl.UUID
	}
	if id == "" {
		return domain.TokenUsage{}, "", false
	}
	u := domain.TokenUsage{
		Input:         tl.Message.Usage.InputTokens,
		CacheCreation: tl.Message.Usage.CacheCreationInputTokens,
		CacheRead:     tl.Message.Usage.CacheReadInputTokens,
		Output:        tl.Message.Usage.OutputTokens,
		Turns:         1,
	}
	return u, id, true
}

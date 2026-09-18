package iosrun

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// EnvResultFile names the file `ao sim run` writes its verdict to. The daemon
// sets it on the run pane; a human or an agent typing the command has it unset
// and the command writes nothing.
//
// 🗝 Why the COMMAND reports rather than the daemon watching. The daemon starts
// a tmux pane and lets go: nothing of it waits on the process, and the pane's
// keep-alive shell means the pane outlives the build on purpose. So the only
// thing that can tell a failed BUILD from a refused LEASE from a launch that
// worked is the command itself, which already knows - it composed the sentence
// it would have printed to a human.
const EnvResultFile = "AO_IOS_RUN_RESULT_FILE"

// Result is what `ao sim run` writes when it ends. Deliberately small: a state,
// one sentence, and when. The build's own output is thousands of lines and is
// already in the pane the bar points at.
type Result struct {
	State   RunState `json:"state"`
	Summary string   `json:"summary,omitempty"`
	// FinishedAt is when the command ended, by the command's own clock.
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// runDir is where this session's run record lives, or "" when no state dir was
// configured - a service with nowhere to write simply remembers less.
func (s *Service) runDir(id domain.SessionID) string {
	if s.stateDir == "" {
		return ""
	}
	// The session id is a path element, so it is taken apart and rebuilt from
	// its last element: a crafted id must not be able to name a directory
	// outside the state dir.
	return filepath.Join(s.stateDir, "iosrun", filepath.Base(filepath.Clean(string(id))))
}

func (s *Service) resultPath(id domain.SessionID) string {
	dir := s.runDir(id)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "result.json")
}

func (s *Service) recordPath(id domain.SessionID) string {
	dir := s.runDir(id)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "run.json")
}

// record writes down the run that was just started, and clears the verdict of
// the one before it. The order matters: a stale result.json beside a fresh
// run.json would report the previous build's failure against this build.
func (s *Service) record(id domain.SessionID, run Run) {
	dir := s.runDir(id)
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return
	}
	_ = os.Remove(filepath.Join(dir, "result.json"))
	body, err := json.Marshal(run)
	if err != nil {
		return
	}
	// A record that cannot be written is not a reason to refuse a build: the
	// run still happens, and the bar falls back to what it holds in memory.
	_ = os.WriteFile(filepath.Join(dir, "run.json"), body, 0o600)
}

// recalled is the run this session last started, as the last daemon left it.
func (s *Service) recalled(id domain.SessionID) (Run, bool) {
	path := s.recordPath(id)
	if path == "" {
		return Run{}, false
	}
	body, err := os.ReadFile(path) //nolint:gosec // the path is this service's own, under its state dir
	if err != nil {
		return Run{}, false
	}
	var run Run
	if err := json.Unmarshal(body, &run); err != nil || run.HandleID == "" {
		return Run{}, false
	}
	s.mu.Lock()
	s.runs[id] = run
	s.mu.Unlock()
	return run, true
}

// readResult is the verdict the command left, if it has ended.
func (s *Service) readResult(id domain.SessionID) (Result, bool) {
	path := s.resultPath(id)
	if path == "" {
		return Result{}, false
	}
	body, err := os.ReadFile(path) //nolint:gosec // the path is this service's own, under its state dir
	if err != nil {
		return Result{}, false
	}
	var result Result
	if err := json.Unmarshal(body, &result); err != nil {
		return Result{}, false
	}
	switch result.State {
	case RunSucceeded, RunFailed, RunStopped:
		return result, true
	case RunRunning:
		// "Still going" is not a verdict; the liveness probe answers that, and
		// it cannot be fooled by a file nobody updated.
		return Result{}, false
	}
	return Result{}, false
}

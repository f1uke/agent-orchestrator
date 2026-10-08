package iosrun

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/fsatomic"
	"github.com/aoagents/agent-orchestrator/backend/internal/xcresultstream"
)

// EnvRunDir names the directory `ao sim run` reports into: its progress while
// it runs and its verdict when it ends. The daemon sets it on the run pane; a
// human or an agent typing the command has it unset and the command writes
// nothing.
//
// 🗝 Why the COMMAND reports rather than the daemon watching. The daemon starts
// a tmux pane and lets go: nothing of it waits on the process, and the pane's
// keep-alive shell means the pane outlives the build on purpose. So the only
// thing that can tell a failed BUILD from a refused LEASE from a launch that
// worked is the command itself, which already knows - it composed the sentence
// it would have printed to a human.
const EnvRunDir = "AO_IOS_RUN_DIR"

// The files in a run directory. Each has one writer: the daemon writes run.json,
// the command writes the others.
const (
	RunFile      = "run.json"
	ResultFile   = "result.json"
	ProgressFile = "progress.json"
	// LogFile is the build's output as the terminal showed it, plus the failed
	// tasks' output replayed from the result bundle.
	LogFile = "build.log"
)

// Stage is the step of a run the command is on.
type Stage string

const (
	StagePreparing  Stage = "preparing"
	StageBooting    Stage = "booting"
	StageBuilding   Stage = "building"
	StageInstalling Stage = "installing"
	StageLaunching  Stage = "launching"
	// StageAppRunning is a run that launched its app and stays with it until it
	// exits, so the bar can stop it.
	StageAppRunning Stage = "app-running"
)

// BuildProgress is how far the build is, as its result stream says.
type BuildProgress struct {
	Phase xcresultstream.Phase `json:"phase,omitempty" enum:"resolving,planning,compiling,linking,signing,building" description:"What the build is doing, read from the tasks it starts."`
	// Counts are the build service's own, and only ever this build's; see Shared.
	Counts *xcresultstream.Counts `json:"counts,omitempty" description:"Tasks done and planned, and the build service's own fraction. Absent until the build has planned, and while another progress-reporting build runs on this Mac."`
	// Shared is set while another build that reports progress runs on this Mac:
	// the build service posts its counts machine-wide with nothing saying whose
	// they are, so none are trusted until it ends.
	Shared   bool `json:"shared,omitempty" description:"Another progress-reporting build is running on this Mac, so task counts cannot be told apart and are withheld."`
	Errors   int  `json:"errors"`
	Warnings int  `json:"warnings"`
}

// Progress is what `ao sim run` writes while it runs, replaced whole each time.
type Progress struct {
	// PID is the command's own process, which is what a stop signals.
	PID            int            `json:"pid"`
	Stage          Stage          `json:"stage"`
	StageStartedAt time.Time      `json:"stageStartedAt"`
	BuildStartedAt *time.Time     `json:"buildStartedAt,omitempty"`
	Build          *BuildProgress `json:"build,omitempty"`
}

// Result is what `ao sim run` writes when it ends. Deliberately small: a state,
// one sentence, and when. The build's own output is thousands of lines and is
// already in the pane the bar points at.
type Result struct {
	State   RunState `json:"state"`
	Summary string   `json:"summary,omitempty"`
	// Warning is what is wrong with the app a SUCCEEDED run put on the device.
	//
	// 🗝 It is separate from Summary, and from State, because it is the one
	// outcome neither of those can carry: the run worked, the app is on screen,
	// and it is broken. That combination is exactly what shipped an app with no
	// entitlements from the Run button for as long as `ao sim run` passed
	// CODE_SIGNING_ALLOWED=NO - a success the bar had no way to qualify, and a
	// failure that only showed up as a login that would not stick.
	Warning string `json:"warning,omitempty"`
	// FinishedAt is when the command ended, by the command's own clock.
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	// BuildSeconds is how long the build took, set only when it succeeded.
	BuildSeconds float64 `json:"buildSeconds,omitempty"`
	Errors       int     `json:"errors,omitempty"`
	Warnings     int     `json:"warnings,omitempty"`
	// Issues are the build's first errors, then its first warnings.
	Issues []xcresultstream.Issue `json:"issues,omitempty"`
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
	return filepath.Join(dir, ResultFile)
}

func (s *Service) recordPath(id domain.SessionID) string {
	dir := s.runDir(id)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, RunFile)
}

// clearRun removes what the previous run reported, before the next one starts:
// a stale result.json beside a fresh run.json would report the previous build's
// failure against this build, and clearing after the pane starts would race the
// command's first progress write.
func (s *Service) clearRun(id domain.SessionID) {
	dir := s.runDir(id)
	if dir == "" {
		return
	}
	_ = os.Remove(filepath.Join(dir, ResultFile))
	_ = os.Remove(filepath.Join(dir, ProgressFile))
	_ = os.MkdirAll(dir, 0o750)
}

// record writes down the run that was just started.
func (s *Service) record(id domain.SessionID, run Run) {
	dir := s.runDir(id)
	if dir == "" {
		return
	}
	body, err := json.Marshal(run)
	if err != nil {
		return
	}
	// A record that cannot be written is not a reason to refuse a build: the
	// run still happens, and the bar falls back to what it holds in memory.
	_ = os.WriteFile(filepath.Join(dir, RunFile), body, 0o600)
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

// readProgress is what the command last said about the run it is in.
func (s *Service) readProgress(id domain.SessionID) (Progress, bool) {
	dir := s.runDir(id)
	if dir == "" {
		return Progress{}, false
	}
	body, err := os.ReadFile(filepath.Join(dir, ProgressFile)) //nolint:gosec // the path is this service's own, under its state dir
	if err != nil {
		return Progress{}, false
	}
	var progress Progress
	if err := json.Unmarshal(body, &progress); err != nil || progress.Stage == "" {
		return Progress{}, false
	}
	return progress, true
}

// historyEntry is the last successful build of one project, scheme and
// configuration. The device is not part of the key: the build targets the
// Simulator generically, so the device cannot change it.
type historyEntry struct {
	BuildSeconds float64   `json:"buildSeconds"`
	FinishedAt   time.Time `json:"finishedAt"`
}

func historyKey(project domain.ProjectID, scheme, configuration string) string {
	return string(project) + "|" + scheme + "|" + configuration
}

func (s *Service) historyPath() string {
	if s.stateDir == "" {
		return ""
	}
	return filepath.Join(s.stateDir, "iosrun-history.json")
}

func (s *Service) readHistory() map[string]historyEntry {
	history := map[string]historyEntry{}
	path := s.historyPath()
	if path == "" {
		return history
	}
	body, err := os.ReadFile(path) //nolint:gosec // the path is this service's own, under its state dir
	if err != nil {
		return history
	}
	_ = json.Unmarshal(body, &history)
	return history
}

// lastBuild is the estimate a running build is measured against.
func (s *Service) lastBuild(project domain.ProjectID, scheme, configuration string) (historyEntry, bool) {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	entry, ok := s.readHistory()[historyKey(project, scheme, configuration)]
	return entry, ok && entry.BuildSeconds > 0
}

// rememberBuild records a successful build once; reading the same verdict again
// changes nothing.
func (s *Service) rememberBuild(project domain.ProjectID, scheme, configuration string, result Result) {
	path := s.historyPath()
	if path == "" || result.BuildSeconds <= 0 || result.FinishedAt == nil {
		return
	}
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	history := s.readHistory()
	key := historyKey(project, scheme, configuration)
	if !history[key].FinishedAt.Before(*result.FinishedAt) {
		return
	}
	history[key] = historyEntry{BuildSeconds: result.BuildSeconds, FinishedAt: *result.FinishedAt}
	body, err := json.Marshal(history)
	if err != nil {
		return
	}
	_ = fsatomic.WriteFile(path, body, 0o600)
}

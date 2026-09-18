// Package iosrun is the surface behind the run bar above the terminal: what an
// AO session can build and run, and the pane it runs in.
//
// It answers two questions and does one thing.
//
//   - Project: is there an Xcode project at the root of this session's worktree,
//     and what can be built from it - its schemes AND its build configurations,
//     which are two independent axes rather than one. The run bar's whole
//     visibility test is the first half of that answer, so "not an iOS project"
//     is an ordinary result here, not an error.
//   - Start: run `ao sim run` in the session's worktree, in a pane the renderer
//     attaches its terminal to.
//
// 🗝 The run pane is NOT an AO session, the same way the reviewer pane and the
// Wiki pane are not. It has no database row, no worktree of its own, no branch
// and no board card - it is a bare runtime handle created straight against the
// runtime adapter. Nothing that sweeps sessions can see it, which is what makes
// a build that outlives the click safe to leave running.
//
// Nothing here talks to a simulator. The lease, the boot cap and the install
// all live in `ao sim run`, which this package starts and does not reimplement:
// a second implementation of "may I have this device" is the one thing that
// could make the CLI and the button disagree about who holds it.
package iosrun

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/xcodeproj"
)

// listingTTL is how long a project listing is reused. `xcodebuild -list` takes
// seconds on a real project (~13 s cold on nter-ios-app) and the answer changes
// when somebody edits the project file, which is rarely - so the bar opens
// instantly on every visit after the first, and a scheme added this morning
// appears within a minute.
//
// The cache is a floor on staleness, not a ceiling: a human who ran `xcodegen`
// in the terminal and reached straight for the picker gets a fresh answer,
// because opening either dropdown asks with refresh=true, which skips this
// entirely. See Project.
const listingTTL = time.Minute

// Sessions is the slice of the session store this package needs: where a
// session's worktree is. The SQLite store satisfies it.
type Sessions interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// Runtime is the slice of the runtime adapter a run pane needs. The tmux
// runtime satisfies it.
type Runtime interface {
	Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error)
	Destroy(ctx context.Context, handle ports.RuntimeHandle) error
	IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error)
}

// Project is what a session can build, as the run bar needs it.
type Project struct {
	// Name is the container, e.g. "Nter.xcworkspace". Empty is the answer on a
	// worktree with no Xcode project, and the bar does not render at all.
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"`
	// Schemes are what can be built, read live from the project.
	Schemes []string `json:"schemes"`
	// SchemesError says why the list is empty when the project itself was
	// found. A bar that showed an empty picker with no explanation would be
	// indistinguishable from one still loading.
	SchemesError string `json:"schemesError,omitempty"`
	// Configurations are which environment it is built FOR - Debug, Release,
	// Dev, UAT, whatever this project defines. It is a separate axis from the
	// scheme and is never derived from one: a project whose schemes are the app
	// and a library keeps its entire environment here.
	Configurations []string `json:"configurations"`
	// ConfigurationsError says why THAT list is empty. It is its own field
	// rather than sharing SchemesError because either question can fail on its
	// own, and a picker must say which one did.
	ConfigurationsError string `json:"configurationsError,omitempty"`
}

// RunState is what became of a run. The bar shows one of these four and
// nothing else, so each has to be a fact rather than a guess.
type RunState string

const (
	// RunRunning is `ao sim run` still alive as a process in the pane.
	RunRunning RunState = "running"
	// RunSucceeded is a run that built, installed and launched.
	RunSucceeded RunState = "succeeded"
	// RunFailed is a run that ended badly and said why in one line. The
	// compiler's own output is in the pane, which is where the bar sends the
	// human.
	RunFailed RunState = "failed"
	// RunStopped is a run whose process is gone leaving no result - Ctrl-C in
	// the pane, a killed tmux server, a machine that restarted mid-build. It is
	// deliberately not "failed": nothing says the build was going to fail.
	RunStopped RunState = "stopped"
)

// Run is a build the bar started, and what became of it.
type Run struct {
	// HandleID is the runtime handle the renderer attaches its terminal to.
	HandleID string `json:"handleId"`
	Scheme   string `json:"scheme"`
	// Configuration is the environment it was built for. Recorded so the bar can
	// re-select what is already running rather than resetting to a default, and
	// so a failed run says which environment failed.
	Configuration string `json:"configuration"`
	UDID          string `json:"udid"`
	// State is how it is going, or how it went.
	//
	// 🗝 It is NOT read from the pane's liveness. The pane's keep-alive shell
	// outlives the command on purpose - so a failed build stays on screen - so
	// "the pane is there" says nothing about the build. Running is
	// `AgentAlive`, which sees the command itself; succeeded and failed are
	// what `ao sim run` WROTE down as it exited, because only the command knows
	// the difference between a build that failed and a lease that was refused.
	State RunState `json:"state" enum:"running,succeeded,failed,stopped" description:"How the run is going, or how it went. running is the command still alive in the pane; succeeded and failed are what it reported as it exited; stopped is a run that ended without reporting - Ctrl-C, or a tmux server that went away."`
	// Summary is one line saying how it ended, in the command's own words. It
	// never restates compiler errors: those are in the pane, and the bar's job
	// is to say a run failed and point at the output.
	Summary   string    `json:"summary,omitempty" description:"One line saying how it ended, in the command's own words. Never the build log - that is in the pane this run's handleId names."`
	StartedAt time.Time `json:"startedAt"`
	// FinishedAt is when the command reported its result; absent while running,
	// and absent for a run that was stopped without reporting one.
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// Manager is the surface the HTTP controller depends on.
type Manager interface {
	// Project reads what this session can build. refresh skips the cache, which
	// is what opening a picker in the bar does.
	Project(ctx context.Context, id domain.SessionID, refresh bool) (Project, error)
	Start(ctx context.Context, id domain.SessionID, scheme, configuration, udid string) (Run, error)
	Current(ctx context.Context, id domain.SessionID) (Run, bool, error)
}

// Service is the production Manager.
type Service struct {
	sessions Sessions
	runtime  Runtime
	// run executes a command in a directory; injected so the scheme listing is
	// testable without Xcode.
	run xcodeproj.Runner
	// aoBinary is the `ao` a run pane invokes. Pinned to this daemon's own
	// executable rather than left to PATH, so a run bar in one build of AO
	// cannot start the CLI of another - the same pin the session manager and
	// the reviewer launcher apply for the same reason.
	aoBinary string
	// stateDir is where a run's record and its result live, under the app's own
	// data dir. On disk rather than in memory only, so a human who pressed Run,
	// went away and came back - or restarted AO in between - still learns how
	// it went. Empty disables the record entirely, which is what a test that
	// does not care gets.
	stateDir string
	now      func() time.Time

	mu     sync.Mutex
	runs   map[domain.SessionID]Run
	cached map[domain.SessionID]cachedListing
}

type cachedListing struct {
	project Project
	at      time.Time
}

// Option configures a Service.
type Option func(*Service)

// WithRunner replaces the command runner. Tests use it; production passes none.
func WithRunner(run xcodeproj.Runner) Option { return func(s *Service) { s.run = run } }

// WithClock replaces the clock, so a test can age the scheme cache.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithStateDir is where runs are recorded. Production passes the daemon's data
// dir; a test passes t.TempDir() or nothing.
func WithStateDir(dir string) Option { return func(s *Service) { s.stateDir = dir } }

// New builds the service. aoBinary is the absolute path to this daemon's `ao`.
func New(sessions Sessions, runtime Runtime, aoBinary string, opts ...Option) *Service {
	s := &Service{
		sessions: sessions,
		runtime:  runtime,
		run:      commandOutputInDir,
		aoBinary: aoBinary,
		now:      time.Now,
		runs:     map[domain.SessionID]Run{},
		cached:   map[domain.SessionID]cachedListing{},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// liveness answers "is the build still going".
//
// 🗝 `ports.AgentLivenessProber` is what it wants: AgentAlive sees the COMMAND
// under the pane leader, and IsAlive sees only the PANE - which the keep-alive
// shell keeps alive long after the build ended, on purpose, so a failed build
// stays readable. It is an OPTIONAL capability reached by type assertion (the
// tmux runtime has it, conpty does not), so a runtime without it falls back to
// the pane, which is what the bar reported before any of this. In practice the
// fallback is unreachable: an Xcode build is a macOS build, and macOS is tmux.
func (s *Service) live(ctx context.Context, handle string) (bool, error) {
	if prober, ok := s.runtime.(ports.AgentLivenessProber); ok {
		return prober.AgentAlive(ctx, ports.RuntimeHandle{ID: handle})
	}
	return s.runtime.IsAlive(ctx, ports.RuntimeHandle{ID: handle})
}

func commandOutputInDir(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // the command is this package's own xcodebuild invocation
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

// HandleID is the runtime handle a session's run pane lives under. One per
// session and deterministic, so a daemon that restarted finds the pane it left
// and the renderer's terminal keeps pointing at the same place.
func HandleID(id domain.SessionID) string { return "iosrun-" + string(id) }

// Project answers what this session can build. A worktree with no Xcode project
// yields a zero Project and no error: that is the run bar's visibility test, and
// most worktrees answer it that way.
func (s *Service) Project(ctx context.Context, id domain.SessionID, refresh bool) (Project, error) {
	dir, err := s.workspace(ctx, id)
	if err != nil {
		return Project{}, err
	}
	now := s.now()
	if !refresh {
		s.mu.Lock()
		hit, ok := s.cached[id]
		s.mu.Unlock()
		if ok && now.Sub(hit.at) < listingTTL {
			return hit.project, nil
		}
	}

	found, err := xcodeproj.Find(dir)
	if err != nil {
		if errors.Is(err, xcodeproj.ErrNoProject) {
			// Cached like any other answer: a Go worktree is asked this question
			// every time its session is opened, and the answer never changes.
			s.remember(id, Project{}, now)
			return Project{}, nil
		}
		return Project{}, err
	}
	project := Project{Name: found.Name, Path: found.Path, Kind: string(found.Kind)}

	// Both listings are separate `xcodebuild -list` invocations, and each takes
	// seconds on a real project, so they run at the same time: asking them in
	// sequence would double the wait a human sees when a dropdown opens.
	var schemes, configurations []string
	var schemesErr, configurationsErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		schemes, schemesErr = xcodeproj.Schemes(ctx, s.run, dir, found)
	}()
	go func() {
		defer wg.Done()
		configurations, configurationsErr = xcodeproj.Configurations(ctx, s.run, dir, found)
	}()
	wg.Wait()

	switch {
	case errors.Is(schemesErr, xcodeproj.ErrNoSchemes):
		project.SchemesError = "This project has no schemes, so there is nothing to build."
	case schemesErr != nil:
		// The project is still real, and the bar still renders: saying which
		// tool failed beats a picker that is silently empty.
		project.SchemesError = firstLine(schemesErr.Error())
	default:
		project.Schemes = schemes
	}
	switch {
	case errors.Is(configurationsErr, xcodeproj.ErrNoConfigurations):
		// Never "so Debug it is". Every Xcode project defines configurations, so
		// an empty list means the question failed - and the project this feature
		// was built for has no Debug at all, which is precisely the case a
		// fallback would turn into a three-minute build that could not work.
		project.ConfigurationsError = "No build configurations could be read from this project, so there is no safe one to build."
	case configurationsErr != nil:
		project.ConfigurationsError = firstLine(configurationsErr.Error())
	default:
		project.Configurations = configurations
	}
	s.remember(id, project, now)
	return project, nil
}

// DefaultConfiguration is the configuration to build when nobody has said, or
// "" when there is no safe answer and the human has to choose.
//
// 🗝 The whole rule, in one place, so the bar, the daemon and `ao sim run`
// cannot disagree about what Run means:
//
//  1. exactly one configuration - use it, ask nothing (the scheme picker's rule);
//  2. the list contains Debug - use the spelling the project used;
//  3. otherwise - no default. nter-ios-app's configurations are Dev, Mock-api,
//     Mock-local, Production, Release and UAT, and picking any of them for
//     somebody would be picking their app's environment for them.
func DefaultConfiguration(configurations []string) string {
	if len(configurations) == 1 {
		return configurations[0]
	}
	for _, configuration := range configurations {
		if strings.EqualFold(configuration, "Debug") {
			return configuration
		}
	}
	return ""
}

func (s *Service) remember(id domain.SessionID, project Project, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cached[id] = cachedListing{project: project, at: at}
}

// Start runs `ao sim run` in the session's worktree, in a pane of its own.
//
// The command is the CLI, not a reimplementation of it. Everything that makes a
// run safe - the lease taken before the build, the build that names no device,
// the boot cap - lives there and is exercised identically whether a human
// pressed a button or an agent typed the command.
func (s *Service) Start(ctx context.Context, id domain.SessionID, scheme, configuration, udid string) (Run, error) {
	dir, err := s.workspace(ctx, id)
	if err != nil {
		return Run{}, err
	}
	project, err := s.Project(ctx, id, false)
	if err != nil {
		return Run{}, err
	}
	if project.Name == "" {
		return Run{}, apierr.Invalid("IOS_RUN_NO_PROJECT",
			"This session's worktree has no .xcodeproj or .xcworkspace at its root, so there is nothing to run.", nil)
	}
	scheme = strings.TrimSpace(scheme)
	if scheme == "" {
		return Run{}, apierr.Invalid("IOS_RUN_NO_SCHEME", "Choose a scheme to run.", nil)
	}
	// Checked here as well as in the CLI because the bar's list can go stale -
	// somebody renames a scheme in Xcode - and a refusal that names the real
	// schemes beats a pane that opens only to print the same thing.
	if len(project.Schemes) > 0 && !contains(project.Schemes, scheme) {
		return Run{}, apierr.Invalid("IOS_RUN_UNKNOWN_SCHEME",
			fmt.Sprintf("%s has no scheme called %q.", project.Name, scheme),
			map[string]any{"schemes": project.Schemes})
	}
	// The configuration is required, and it is not defaulted here. A build of
	// the wrong environment installs an app pointed at the wrong backend, and
	// `-configuration Debug` against a project that has no Debug fails after
	// minutes with an error about an empty PODS_ROOT - which is the bug this
	// picker exists to end.
	configuration = strings.TrimSpace(configuration)
	if configuration == "" {
		return Run{}, apierr.Invalid("IOS_RUN_NO_CONFIGURATION", "Choose a build configuration to run.",
			map[string]any{"configurations": project.Configurations})
	}
	if len(project.Configurations) > 0 && !contains(project.Configurations, configuration) {
		return Run{}, apierr.Invalid("IOS_RUN_UNKNOWN_CONFIGURATION",
			fmt.Sprintf("%s has no build configuration called %q.", project.Name, configuration),
			map[string]any{"configurations": project.Configurations})
	}

	handle := HandleID(id)
	// Tear down whatever is under the handle first. Destroy is idempotent, so
	// this is equally "no pane yet" and "the last run is still on screen", and
	// it is what keeps tmux's new-session from failing on a keep-alive shell.
	if err := s.runtime.Destroy(ctx, ports.RuntimeHandle{ID: handle}); err != nil {
		return Run{}, fmt.Errorf("ios run: clear the previous pane: %w", err)
	}
	argv := []string{s.aoBinary, "sim", "run", "--scheme", scheme, "--configuration", configuration}
	if trimmed := strings.TrimSpace(udid); trimmed != "" {
		argv = append(argv, "--udid", trimmed)
	}
	// AO_SESSION_ID is what makes the lease this SESSION's rather than an
	// anonymous one, so a run started from the bar and a run an agent typed in
	// the same session contend for the device exactly as two agents would.
	env := map[string]string{"AO_SESSION_ID": string(id)}
	// And this is how the command reports what became of it. Only `ao sim run`
	// can tell a failed build from a refused lease, so it writes the verdict
	// rather than the daemon inferring one from an exit nobody watched.
	if path := s.resultPath(id); path != "" {
		env[EnvResultFile] = path
	}
	if trimmed := strings.TrimSpace(udid); trimmed != "" {
		env["AO_SIM_UDID"] = trimmed
		env["AO_SIM_DESTINATION"] = domain.SimDestination(trimmed)
	}
	if _, err := s.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     domain.SessionID(handle),
		WorkspacePath: dir,
		Argv:          argv,
		Env:           env,
	}); err != nil {
		return Run{}, fmt.Errorf("ios run: start the pane: %w", err)
	}
	run := Run{
		HandleID:      handle,
		Scheme:        scheme,
		Configuration: configuration,
		UDID:          strings.TrimSpace(udid),
		State:         RunRunning,
		StartedAt:     s.now().UTC(),
	}
	s.mu.Lock()
	s.runs[id] = run
	s.mu.Unlock()
	s.record(id, run)
	return run, nil
}

// Current is the session's run, if it has one, and how it is going.
//
// The state is assembled from two facts, in this order, because only the second
// one can be wrong:
//
//  1. the RESULT the command wrote as it exited. That is terminal: a build that
//     failed stays failed however long ago it was, which is what makes a run
//     readable by somebody who pressed Run, went to another session and came
//     back.
//  2. otherwise, whether the COMMAND is still alive (`AgentAlive`, not
//     `IsAlive` - the pane's keep-alive shell outlives the build by design).
//     Alive is running; gone without a result is stopped, not failed: a
//     Ctrl-C'd build is not a broken one.
//
// A failed probe leaves the last known state alone, per the hard rule that an
// unreadable runtime is not proof anything died.
func (s *Service) Current(ctx context.Context, id domain.SessionID) (Run, bool, error) {
	s.mu.Lock()
	run, ok := s.runs[id]
	s.mu.Unlock()
	if !ok {
		// Not in memory is not "never happened": this daemon may have started
		// after the run did, and the human is owed the answer either way.
		run, ok = s.recalled(id)
		if !ok {
			return Run{}, false, nil
		}
	}
	if result, found := s.readResult(id); found {
		run.State, run.Summary, run.FinishedAt = result.State, result.Summary, result.FinishedAt
		return run, true, nil
	}
	alive, err := s.live(ctx, run.HandleID)
	if err != nil {
		return run, true, nil //nolint:nilerr // intentional: an unreadable runtime is not a dead run
	}
	if alive {
		run.State = RunRunning
		return run, true, nil
	}
	run.State = RunStopped
	run.Summary = "The run ended without reporting how it went. Its output is still in the pane."
	return run, true, nil
}

// workspace is the session's worktree, which is where everything here happens.
func (s *Service) workspace(ctx context.Context, id domain.SessionID) (string, error) {
	record, found, err := s.sessions.GetSession(ctx, id)
	if err != nil {
		return "", fmt.Errorf("ios run: read session %s: %w", id, err)
	}
	if !found {
		return "", apierr.NotFound("SESSION_NOT_FOUND", fmt.Sprintf("No session %s", id))
	}
	if strings.TrimSpace(record.Metadata.WorkspacePath) == "" {
		return "", apierr.Invalid("IOS_RUN_NO_WORKSPACE",
			fmt.Sprintf("Session %s has no worktree on disk, so there is nothing to build.", id), nil)
	}
	return record.Metadata.WorkspacePath, nil
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// firstLine keeps an error to the sentence a picker can show. xcodebuild's
// failures run to pages, and the rest of them is in the terminal anyway.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

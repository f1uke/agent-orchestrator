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

// Run is a build the bar started, and what became of it.
type Run struct {
	// HandleID is the runtime handle the renderer attaches its terminal to.
	HandleID string `json:"handleId"`
	Scheme   string `json:"scheme"`
	// Configuration is the environment it was built for. Recorded so the bar can
	// re-select what is already running rather than resetting to a default.
	Configuration string `json:"configuration"`
	UDID          string `json:"udid"`
	// Running is whether the pane is still there. It is not "the build
	// succeeded": the pane's keep-alive shell outlives the command on purpose,
	// so a failed build's output is still on screen to read.
	Running   bool      `json:"running"`
	StartedAt time.Time `json:"startedAt"`
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
		Running:       true,
		StartedAt:     s.now().UTC(),
	}
	s.mu.Lock()
	s.runs[id] = run
	s.mu.Unlock()
	return run, nil
}

// Current is the session's run, if it has one, with liveness read from the
// runtime rather than remembered - a pane the user closed in tmux is gone, and
// a bar that still said "running" would be lying about the only thing it says.
func (s *Service) Current(ctx context.Context, id domain.SessionID) (Run, bool, error) {
	s.mu.Lock()
	run, ok := s.runs[id]
	s.mu.Unlock()
	if !ok {
		return Run{}, false, nil
	}
	alive, err := s.runtime.IsAlive(ctx, ports.RuntimeHandle{ID: run.HandleID})
	if err != nil {
		// A failed probe is not proof the pane is dead (the hard rule), so the
		// last known answer stands.
		return run, true, nil //nolint:nilerr // intentional: an unreadable runtime is not a dead pane
	}
	run.Running = alive
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

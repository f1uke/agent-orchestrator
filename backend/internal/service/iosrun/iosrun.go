// Package iosrun is the surface behind the run bar above the terminal: what an
// AO session can build and run, and the pane it runs in.
//
// It answers two questions and does one thing.
//
//   - Project: is there an Xcode project at the root of this session's worktree,
//     and what can be built from it. The run bar's whole visibility test is the
//     first half of that answer, so "not an iOS project" is an ordinary result
//     here, not an error.
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

// schemeTTL is how long a scheme listing is reused. `xcodebuild -list` takes
// seconds on a real project and the answer changes when somebody edits the
// project file, which is rarely - so the bar opens instantly on every visit
// after the first, and a scheme added this morning appears within a minute.
const schemeTTL = time.Minute

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
	// Schemes are the app environments, read live from the project.
	Schemes []string `json:"schemes"`
	// SchemesError says why the list is empty when the project itself was
	// found. A bar that showed an empty picker with no explanation would be
	// indistinguishable from one still loading.
	SchemesError string `json:"schemesError,omitempty"`
}

// Run is a build the bar started, and what became of it.
type Run struct {
	// HandleID is the runtime handle the renderer attaches its terminal to.
	HandleID string `json:"handleId"`
	Scheme   string `json:"scheme"`
	UDID     string `json:"udid"`
	// Running is whether the pane is still there. It is not "the build
	// succeeded": the pane's keep-alive shell outlives the command on purpose,
	// so a failed build's output is still on screen to read.
	Running   bool      `json:"running"`
	StartedAt time.Time `json:"startedAt"`
}

// Manager is the surface the HTTP controller depends on.
type Manager interface {
	Project(ctx context.Context, id domain.SessionID) (Project, error)
	Start(ctx context.Context, id domain.SessionID, scheme, udid string) (Run, error)
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
	cached map[domain.SessionID]cachedSchemes
}

type cachedSchemes struct {
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
		cached:   map[domain.SessionID]cachedSchemes{},
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
func (s *Service) Project(ctx context.Context, id domain.SessionID) (Project, error) {
	dir, err := s.workspace(ctx, id)
	if err != nil {
		return Project{}, err
	}
	now := s.now()
	s.mu.Lock()
	if hit, ok := s.cached[id]; ok && now.Sub(hit.at) < schemeTTL {
		s.mu.Unlock()
		return hit.project, nil
	}
	s.mu.Unlock()

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
	schemes, err := xcodeproj.Schemes(ctx, s.run, dir, found)
	switch {
	case errors.Is(err, xcodeproj.ErrNoSchemes):
		project.SchemesError = "This project has no schemes, so there is nothing to build."
	case err != nil:
		// The project is still real, and the bar still renders: saying which
		// tool failed beats a picker that is silently empty.
		project.SchemesError = firstLine(err.Error())
	default:
		project.Schemes = schemes
	}
	s.remember(id, project, now)
	return project, nil
}

func (s *Service) remember(id domain.SessionID, project Project, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cached[id] = cachedSchemes{project: project, at: at}
}

// Start runs `ao sim run` in the session's worktree, in a pane of its own.
//
// The command is the CLI, not a reimplementation of it. Everything that makes a
// run safe - the lease taken before the build, the build that names no device,
// the boot cap - lives there and is exercised identically whether a human
// pressed a button or an agent typed the command.
func (s *Service) Start(ctx context.Context, id domain.SessionID, scheme, udid string) (Run, error) {
	dir, err := s.workspace(ctx, id)
	if err != nil {
		return Run{}, err
	}
	project, err := s.Project(ctx, id)
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

	handle := HandleID(id)
	// Tear down whatever is under the handle first. Destroy is idempotent, so
	// this is equally "no pane yet" and "the last run is still on screen", and
	// it is what keeps tmux's new-session from failing on a keep-alive shell.
	if err := s.runtime.Destroy(ctx, ports.RuntimeHandle{ID: handle}); err != nil {
		return Run{}, fmt.Errorf("ios run: clear the previous pane: %w", err)
	}
	argv := []string{s.aoBinary, "sim", "run", "--scheme", scheme}
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
	run := Run{HandleID: handle, Scheme: scheme, UDID: strings.TrimSpace(udid), Running: true, StartedAt: s.now().UTC()}
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

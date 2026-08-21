package session

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/messagetemplates"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

// messageRenderer renders an editable nudge/dispatch template. *messagetemplates.Renderer
// satisfies it; kept as an interface so tests can inject a stub.
type messageRenderer interface {
	Render(name messagetemplates.Name, data any) (string, error)
}

// Store is the read-only persistence surface needed to assemble controller-facing session read models.
type Store interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	RenameSession(ctx context.Context, id domain.SessionID, displayName string, updatedAt time.Time) (bool, error)
	SetSessionPreviewURL(ctx context.Context, id domain.SessionID, previewURL string, updatedAt time.Time) (bool, error)
	SetSessionAutoNudge(ctx context.Context, id domain.SessionID, override *bool, updatedAt time.Time) (bool, error)
	SetSessionAutoResolve(ctx context.Context, id domain.SessionID, override *bool, updatedAt time.Time) (bool, error)
	SetSessionKeepWarmOnMerge(ctx context.Context, id domain.SessionID, enabled bool, updatedAt time.Time) (bool, error)
	// SetSessionPRTarget records the branch this session's PR merges into.
	// When the session has an open PR the caller retargets the forge FIRST and
	// only calls this on success, so AO never stores a target the forge rejected.
	SetSessionPRTarget(ctx context.Context, id domain.SessionID, target string, updatedAt time.Time) (bool, error)
	// SetPRTargetBranch moves a tracked PR row after a successful retarget, so
	// the read model (which ranks an open PR above the stored value) reflects
	// the change immediately instead of waiting for the observer's next poll.
	SetPRTargetBranch(ctx context.Context, prURL, target string, updatedAt time.Time) (bool, error)
	SetSessionIssueBinding(ctx context.Context, id domain.SessionID, issueID, displayName string, updatedAt time.Time) (bool, error)
	GetDisplayPRFactsForSession(ctx context.Context, id domain.SessionID) (domain.PRFacts, bool, error)
	ListPRFactsForSession(ctx context.Context, id domain.SessionID) ([]domain.PRFacts, error)
	SessionQueuedMessageCounts(ctx context.Context, id domain.SessionID) (domain.QueuedMessageCounts, error)
	// The crew conversation's counters. Every one of them reads a table that
	// stays empty unless two members of one task actually message each other.
	InsertCrewMessage(ctx context.Context, msg domain.CrewMessage) error
	CrewMessagesOnSubject(ctx context.Context, crewID domain.SessionID, subject string, from domain.SessionID) (int, error)
	CrewMessagesSince(ctx context.Context, crewID domain.SessionID, since time.Time) (int, error)
	LatestCrewMessageFrom(ctx context.Context, from domain.SessionID) (domain.CrewMessage, bool, error)
	// OpenCrewRunForSession and ConsecutiveCrewRunDiscards are the bracketed-run
	// facts the read model needs: what this member is running RIGHT NOW (which
	// nothing else in the daemon can answer - see domain.Session.CrewRun) and how
	// many runs in a row it has had to throw away. Both are indexed lookups on a
	// table that is empty for every solo session.
	OpenCrewRunForSession(ctx context.Context, id domain.SessionID) (domain.CrewRun, bool, error)
	ConsecutiveCrewRunDiscards(ctx context.Context, id domain.SessionID) (int, error)
	ListPRsBySession(ctx context.Context, sessionID domain.SessionID) ([]domain.PullRequest, error)
	ListChecks(ctx context.Context, prURL string) ([]domain.PullRequestCheck, error)
	ListPRReviews(ctx context.Context, prURL string) ([]domain.PullRequestReview, error)
	ListPRReviewThreads(ctx context.Context, prURL string) ([]domain.PullRequestReviewThread, error)
	ListPRComments(ctx context.Context, prURL string) ([]domain.PullRequestComment, error)
	// ListReviewRunsBySession is AO's OWN review verdicts, as distinct from the
	// provider decisions ListPRReviews returns. The PR read model carries both:
	// they are different actors and neither substitutes for the other.
	ListReviewRunsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReviewRun, error)
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// ListFilter captures API-facing session list query filters.
type ListFilter struct {
	ProjectID        domain.ProjectID
	Active           *bool
	OrchestratorOnly bool
	Fresh            bool
}

// commander is the command-side surface Service delegates to: the
// *sessionmanager.Manager in production, a fake in tests.
type commander interface {
	Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, error)
	// PrepareTodo persists a deferred/TODO session (spec only, nothing
	// materialized); StartTodo materializes it in place; UpdateTodoSpec edits
	// the spec while still queued.
	PrepareTodo(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, error)
	StartTodo(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error)
	UpdateTodoSpec(ctx context.Context, id domain.SessionID, patch ports.TodoSpecPatch) (domain.SessionRecord, error)
	Restore(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error)
	Restart(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error)
	// Wake is the user-open hook: resume a suspended session in place, or reset a
	// live session's idle-close countdown; terminated sessions are left untouched.
	Wake(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error)
	// AttachCrewMember adds a member in `role` to the task dev works on, born
	// SUSPENDED. It is how a task that was spawned solo - every `mechanical` one,
	// and every task older than the crew - gains a qa.
	AttachCrewMember(ctx context.Context, devID domain.SessionID, role domain.CrewRole) (domain.SessionRecord, error)
	// CrewDevOf resolves any session to the DEV of the task it belongs to; a solo
	// session answers with itself.
	CrewDevOf(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error)
	// CrewMember resolves the session filling `role` on this session's task, so a
	// member can address its crewmate by role. It answers for either member, and
	// a role is the only address that cannot go stale: a crew is formed after
	// dev's runtime is launched, so dev's environment can never carry qa's id.
	CrewMember(ctx context.Context, id domain.SessionID, role domain.CrewRole) (domain.SessionRecord, bool, error)
	// WakeCrewMember gives the crew slot to one member of a task, standing the
	// current holder down first. It is the human's (and the orchestrator's) way of
	// saying "qa's turn now" while automatic handover is deliberately not built.
	WakeCrewMember(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error)
	Kill(ctx context.Context, id domain.SessionID) (bool, error)
	// Teardown is Kill reporting the full outcome, including WHY a workspace was
	// preserved. The auto-reclaim loop needs that distinction; Kill's bool loses it.
	Teardown(ctx context.Context, id domain.SessionID, cause string) (sessionmanager.TeardownResult, error)
	RetireForReplacement(ctx context.Context, id domain.SessionID) error
	Send(ctx context.Context, id domain.SessionID, message string) (ports.SendOutcome, error)
	Cleanup(ctx context.Context, project domain.ProjectID) (sessionmanager.CleanupResult, error)
	RollbackSpawn(ctx context.Context, id domain.SessionID) (deleted, killed bool, err error)
	PurgeSession(ctx context.Context, id domain.SessionID, force bool) error
}

// RollbackOutcome reports what happened in a rollback: either the seed row was
// deleted, or the partially-spawned session was killed (runtime+workspace torn
// down, row marked terminated).
type RollbackOutcome struct {
	Deleted bool `json:"deleted"`
	Killed  bool `json:"killed"`
}

// CleanupOutcome reports what session cleanup reclaimed and what it preserved.
type CleanupOutcome struct {
	Cleaned []domain.SessionID `json:"cleaned"`
	Skipped []CleanupSkipped   `json:"skipped"`
}

// CleanupSkipped is one terminal session whose workspace was preserved by
// cleanup (never force-deleted), with the user-facing reason.
type CleanupSkipped struct {
	SessionID domain.SessionID `json:"sessionId"`
	Reason    string           `json:"reason"`
}

// ErrSCMWriteForbidden is returned when the SCM provider rejects a review
// thread write (reply/resolve) because the configured token lacks permission
// for it.
var ErrSCMWriteForbidden = errors.New("scm write forbidden")

type scmProvider interface {
	ParseRepository(remote string) (ports.SCMRepo, bool)
	FetchPullRequests(ctx context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error)
	FetchReviewThreads(ctx context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error)
	ReplyToThread(ctx context.Context, ref ports.SCMPRRef, threadID, body string) (ports.SCMReviewCommentObservation, error)
	ResolveThread(ctx context.Context, ref ports.SCMPRRef, threadID string) error
}

// Service is the controller-facing session service. It delegates command-side
// session operations to the internal sessionmanager.Manager and owns read-model
// assembly, including user-facing display status derivation.
type Service struct {
	manager             commander
	store               Store
	prClaimer           ports.PRClaimer
	scm                 scmProvider
	clock               func() time.Time
	telemetry           ports.EventSink
	orchestratorLocksMu sync.Mutex
	orchestratorLocks   map[domain.ProjectID]*sync.Mutex
	// signalCapable reports whether a harness has a hook pipeline that can
	// deliver activity signals at all. Only capable harnesses are available for
	// the no_signal downgrade: a hook-less harness staying silent forever is
	// normal, not a broken pipeline. nil means "unknown": never downgrade.
	signalCapable func(domain.AgentHarness) bool
	renderer      messageRenderer
	// idleCloseTTL is the inactivity window after which the idle sweep suspends a
	// live session. The read model exposes IdleCloseAt = idleReference + this so
	// the board can count down to suspension. 0 (disabled) omits IdleCloseAt.
	idleCloseTTL time.Duration
	// targetFetch throttles the Changes panel's read-only refresh of a session's
	// target branch. Zero value ready; deliberately a field rather than a package
	// global so two services in one test process cannot share throttle state.
	targetFetch targetFetcher
}

// New wires a controller-facing session service over an internal session Manager.
func New(manager *sessionmanager.Manager, store Store) *Service {
	return NewWithDeps(Deps{Manager: manager, Store: store})
}

// Deps are optional collaborators for the session service. The default New
// path keeps existing tests and callers small; daemon wiring uses NewWithDeps
// to supply SCM observation for PR claiming.
type Deps struct {
	Manager   commander
	Store     Store
	PRClaimer ports.PRClaimer
	SCM       scmProvider
	Clock     func() time.Time
	Telemetry ports.EventSink
	// SignalCapable gates the no_signal status downgrade per harness; daemon
	// wiring passes activitydispatch.SupportsHarness. Left nil, no session is
	// ever downgraded to no_signal.
	SignalCapable func(domain.AgentHarness) bool
	// Renderer renders dispatch templates (send-to-worker). nil makes
	// DispatchCommentToWorker fail safe with an Invalid (400) DISPATCH_UNAVAILABLE
	// error instead of panicking.
	Renderer messageRenderer
	// IdleCloseTTL is the idle-suspend window (config.SessionIdleClose), used to
	// derive the read model's IdleCloseAt countdown. 0 disables it (no countdown).
	IdleCloseTTL time.Duration
}

// NewWithDeps wires a session service with optional PR-claim dependencies.
func NewWithDeps(d Deps) *Service {
	s := &Service{manager: d.Manager, store: d.Store, prClaimer: d.PRClaimer, scm: d.SCM, clock: d.Clock, signalCapable: d.SignalCapable, telemetry: d.Telemetry, renderer: d.Renderer, idleCloseTTL: d.IdleCloseTTL}
	if s.prClaimer == nil {
		if w, ok := d.Store.(ports.PRClaimer); ok {
			s.prClaimer = w
		}
	}
	if s.clock == nil {
		s.clock = time.Now
	}
	return s
}

// Spawn creates a session and returns the API-facing read model.
func (s *Service) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, error) {
	project, err := s.requireProject(ctx, cfg.ProjectID)
	if err != nil {
		return domain.Session{}, err
	}
	start := s.now()
	firstSession, err := s.isFirstSession(ctx)
	if err != nil {
		return domain.Session{}, fmt.Errorf("count sessions: %w", err)
	}
	rec, err := s.manager.Spawn(ctx, cfg)
	if err != nil {
		s.emitSpawnFailed(cfg, err, s.now().Sub(start).Milliseconds())
		return domain.Session{}, toAPIError(err)
	}
	s.emitSpawned(rec, s.now().Sub(start).Milliseconds())
	if firstSession {
		s.emitFirstSessionSpawned(rec, project)
	}
	return s.toSession(ctx, rec)
}

// PrepareTodo persists a deferred/TODO session (the board's TODO lane): the spec
// is saved but no branch/worktree/tmux is created. StartTodo materializes it.
func (s *Service) PrepareTodo(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, error) {
	if _, err := s.requireProject(ctx, cfg.ProjectID); err != nil {
		return domain.Session{}, err
	}
	rec, err := s.manager.PrepareTodo(ctx, cfg)
	if err != nil {
		return domain.Session{}, toAPIError(err)
	}
	return s.toSession(ctx, rec)
}

// StartTodo materializes a prepared TODO in place and hands it to the normal
// session lifecycle. The returned read model is the now-live session.
func (s *Service) StartTodo(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	rec, err := s.manager.StartTodo(ctx, id)
	if err != nil {
		return domain.Session{}, toAPIError(err)
	}
	return s.toSession(ctx, rec)
}

// UpdateTodoSpec persists edits to a prepared TODO's spec before it is started.
func (s *Service) UpdateTodoSpec(ctx context.Context, id domain.SessionID, patch ports.TodoSpecPatch) (domain.Session, error) {
	rec, err := s.manager.UpdateTodoSpec(ctx, id, patch)
	if err != nil {
		return domain.Session{}, toAPIError(err)
	}
	return s.toSession(ctx, rec)
}

// requireProject verifies the project is registered before any spawn write
// touches the session store, so an unknown projectId surfaces as a typed 404
// rather than an opaque 500 with an orphan terminated row left behind.
func (s *Service) requireProject(ctx context.Context, id domain.ProjectID) (domain.ProjectRecord, error) {
	if id == "" {
		return domain.ProjectRecord{}, apierr.Invalid("PROJECT_ID_REQUIRED", "projectId is required", nil)
	}
	if s.store == nil {
		return domain.ProjectRecord{ID: string(id)}, nil
	}
	rec, ok, err := s.store.GetProject(ctx, string(id))
	if err != nil {
		return domain.ProjectRecord{}, fmt.Errorf("get project %s: %w", id, err)
	}
	if !ok {
		return domain.ProjectRecord{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project. Register it with `ao project add`")
	}
	return rec, nil
}

func (s *Service) isFirstSession(ctx context.Context) (bool, error) {
	if s.store == nil {
		return false, nil
	}
	rows, err := s.store.ListAllSessions(ctx)
	if err != nil {
		return false, err
	}
	return len(rows) == 0, nil
}

func (s *Service) emitSpawned(rec domain.SessionRecord, durationMs int64) {
	if s.telemetry == nil {
		return
	}
	projectID := rec.ProjectID
	sessionID := rec.ID
	s.telemetry.Emit(context.Background(), ports.TelemetryEvent{
		Name:       "ao.session.spawned",
		Source:     "session_service",
		OccurredAt: s.now(),
		Level:      ports.TelemetryLevelInfo,
		ProjectID:  &projectID,
		SessionID:  &sessionID,
		Payload: map[string]any{
			"kind":        string(rec.Kind),
			"harness":     string(rec.Harness),
			"duration_ms": durationMs,
		},
	})
}

func (s *Service) emitFirstSessionSpawned(rec domain.SessionRecord, project domain.ProjectRecord) {
	if s.telemetry == nil {
		return
	}
	projectID := rec.ProjectID
	sessionID := rec.ID
	payload := map[string]any{
		"kind":    string(rec.Kind),
		"harness": string(rec.Harness),
	}
	if !project.RegisteredAt.IsZero() {
		payload["since_first_project_ms"] = s.now().Sub(project.RegisteredAt).Milliseconds()
	}
	s.telemetry.Emit(context.Background(), ports.TelemetryEvent{
		Name:       "ao.onboarding.first_session_spawned",
		Source:     "session_service",
		OccurredAt: s.now(),
		Level:      ports.TelemetryLevelInfo,
		ProjectID:  &projectID,
		SessionID:  &sessionID,
		Payload:    payload,
	})
}

func (s *Service) emitSpawnFailed(cfg ports.SpawnConfig, err error, durationMs int64) {
	if s.telemetry == nil {
		return
	}
	projectID := cfg.ProjectID
	apiErr := toAPIError(err)
	errorKind, errorCode := telemetrymeta.ErrorKindAndCode(apiErr)
	payload := map[string]any{
		"component":   "session_service",
		"operation":   "spawn_session",
		"kind":        string(cfg.Kind),
		"harness":     string(cfg.Harness),
		"duration_ms": durationMs,
		"error_kind":  errorKind,
		"fingerprint": telemetrymeta.Fingerprint("session_service", "spawn_session", string(cfg.Kind), string(cfg.Harness), errorKind, errorCode),
	}
	if errorCode != "" {
		payload["error_code"] = errorCode
	}
	s.telemetry.Emit(context.Background(), ports.TelemetryEvent{
		Name:       "ao.session.spawn_failed",
		Source:     "session_service",
		OccurredAt: s.now(),
		Level:      ports.TelemetryLevelError,
		ProjectID:  &projectID,
		Payload:    payload,
	})
}

// SpawnOrchestrator spawns an orchestrator session for a project. When clean is
// true it first tears down any active orchestrator(s) for that project so the new
// one is the only live coordinator. When clean is false it is idempotent: if an
// active orchestrator already exists it is returned as-is. A business rule that
// belongs here, not in the HTTP controller.
func (s *Service) SpawnOrchestrator(ctx context.Context, projectID domain.ProjectID, clean bool) (domain.Session, error) {
	unlock := s.lockOrchestratorProject(projectID)
	defer unlock()

	project, err := s.requireProject(ctx, projectID)
	if err != nil {
		return domain.Session{}, err
	}
	active := true
	if clean {
		existing, err := s.List(ctx, ListFilter{ProjectID: projectID, Active: &active, OrchestratorOnly: true})
		if err != nil {
			return domain.Session{}, err
		}
		for _, orch := range existing {
			_ = s.sendRetireNotice(ctx, orch.ID)
			if err := s.manager.RetireForReplacement(ctx, orch.ID); err != nil {
				return domain.Session{}, toAPIError(err)
			}
		}
	} else {
		// ponytail: check-then-spawn is not atomic; fine for the single-frontend ensure-on-load case. Upgrade path: a partial unique index on (project_id) where kind=orchestrator and not terminated.
		existing, err := s.List(ctx, ListFilter{ProjectID: projectID, Active: &active, OrchestratorOnly: true})
		if err != nil {
			return domain.Session{}, err
		}
		if len(existing) > 0 {
			return newestSession(existing), nil
		}
	}
	sess, err := s.Spawn(ctx, ports.SpawnConfig{ProjectID: projectID, Kind: domain.KindOrchestrator})
	if err != nil {
		return domain.Session{}, err
	}
	if err := verifyOrchestratorReplacement(project, sess); err != nil {
		return domain.Session{}, err
	}
	return sess, nil
}

const orchestratorRetireNotice = "AO is replacing this project orchestrator. Stop coordinating new work now; a fresh orchestrator will take over on the canonical branch."

func (s *Service) sendRetireNotice(ctx context.Context, id domain.SessionID) error {
	if _, err := s.manager.Send(ctx, id, orchestratorRetireNotice); err != nil {
		return fmt.Errorf("send retire notice to %s: %w", id, err)
	}
	return nil
}

func verifyOrchestratorReplacement(project domain.ProjectRecord, sess domain.Session) error {
	if sess.IsTerminated {
		return fmt.Errorf("orchestrator replacement verification failed: new session %s is terminated", sess.ID)
	}
	if sess.Kind != domain.KindOrchestrator {
		return fmt.Errorf("orchestrator replacement verification failed: new session %s has kind %q", sess.ID, sess.Kind)
	}
	if expected := project.Config.Orchestrator.Harness; expected != "" && sess.Harness != expected {
		return fmt.Errorf("orchestrator replacement verification failed: new session %s uses harness %q, want %q", sess.ID, sess.Harness, expected)
	}
	expectedBranch := "ao/" + serviceSessionPrefix(project) + "-orchestrator"
	if sess.Metadata.Branch != "" && sess.Metadata.Branch != expectedBranch {
		return fmt.Errorf("orchestrator replacement verification failed: new session %s uses branch %q, want %q", sess.ID, sess.Metadata.Branch, expectedBranch)
	}
	return nil
}

func serviceSessionPrefix(project domain.ProjectRecord) string {
	if p := strings.TrimSpace(project.Config.SessionPrefix); p != "" {
		return p
	}
	id := project.ID
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func newestSession(sessions []domain.Session) domain.Session {
	newest := sessions[0]
	for _, sess := range sessions[1:] {
		if sessionNewer(sess.SessionRecord, newest.SessionRecord) {
			newest = sess
		}
	}
	return newest
}

func sessionNewer(a, b domain.SessionRecord) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return string(a.ID) > string(b.ID)
}

func (s *Service) lockOrchestratorProject(projectID domain.ProjectID) func() {
	s.orchestratorLocksMu.Lock()
	if s.orchestratorLocks == nil {
		s.orchestratorLocks = make(map[domain.ProjectID]*sync.Mutex)
	}
	mu := s.orchestratorLocks[projectID]
	if mu == nil {
		mu = &sync.Mutex{}
		s.orchestratorLocks[projectID] = mu
	}
	s.orchestratorLocksMu.Unlock()

	mu.Lock()
	return mu.Unlock
}

// Restore relaunches a terminated session and returns the API-facing read model.
func (s *Service) Restore(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	rec, err := s.manager.Restore(ctx, id)
	if err != nil {
		return domain.Session{}, toAPIError(err)
	}
	return s.toSession(ctx, rec)
}

// Wake is the user-open hook: opening/selecting a session in the UI resumes it
// in place if the idle sweep suspended it, or resets its idle-close countdown if
// it is live (a genuine open counts as activity). A terminated session is
// returned unchanged — reviving one is Restore's job, not an idle-timer reset.
func (s *Service) Wake(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	rec, err := s.manager.Wake(ctx, id)
	if err != nil {
		return domain.Session{}, toAPIError(err)
	}
	return s.toSession(ctx, rec)
}

// WakeCrewMember hands the task's one awake slot to `id`: whoever holds it is
// stood down (suspended, tmux reaped) and `id` is resumed in its place. It goes
// through the same exclusion every other wake route does, so it can only ever
// leave one member of a crew running.
//
// A member that already holds the slot is returned unchanged - "qa's turn" when
// it is already qa's turn is a no-op, not an error.
func (s *Service) WakeCrewMember(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	rec, err := s.manager.WakeCrewMember(ctx, id)
	if err != nil {
		return domain.Session{}, toAPIError(err)
	}
	return s.toSession(ctx, rec)
}

// AttachCrewMember adds a member in `role` to the task `id` belongs to.
//
// `id` may name either member: a crew member resolves to its dev, and a solo
// session is its own task - the same equality AO_CREW_ID relies on - so a caller
// holding one id never has to know which kind it has.
//
// The FINISHED refusal lives here rather than in the manager because it needs
// the session's DERIVED status, which is assembled from PR facts at read time
// (the repo does not store a display status). A task whose PR has merged, or
// whose dev has been torn down, is over: attaching to it would create a session
// with a worktree that is about to be reclaimed, a branch nobody will push and
// no turn it could ever be given. Refusing is the honest answer.
//
// A SUSPENDED dev is not finished - it is parked - and attaching is allowed
// there: the new member is born asleep, so nothing wakes and nothing changes
// until a human moves the baton.
func (s *Service) AttachCrewMember(ctx context.Context, id domain.SessionID, role domain.CrewRole) (domain.Session, error) {
	dev, err := s.manager.CrewDevOf(ctx, id)
	if err != nil {
		return domain.Session{}, toAPIError(err)
	}
	sess, err := s.toSession(ctx, dev)
	if err != nil {
		return domain.Session{}, err
	}
	if sess.Status == domain.StatusMerged || sess.Status == domain.StatusTerminated {
		return domain.Session{}, toAPIError(fmt.Errorf("%w: %s is %s", sessionmanager.ErrCrewTaskFinished, dev.ID, sess.Status))
	}
	rec, err := s.manager.AttachCrewMember(ctx, dev.ID, role)
	if err != nil {
		return domain.Session{}, toAPIError(err)
	}
	return s.toSession(ctx, rec)
}

// Restart tears a session down and relaunches it in place (kill-then-restore),
// keeping the same session id and native transcript so the agent resumes its
// conversation with a freshly recomputed system prompt.
func (s *Service) Restart(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	rec, err := s.manager.Restart(ctx, id)
	if err != nil {
		return domain.Session{}, toAPIError(err)
	}
	return s.toSession(ctx, rec)
}

// Kill delegates terminal intent and teardown to the internal manager.
func (s *Service) Kill(ctx context.Context, id domain.SessionID) (bool, error) {
	freed, err := s.manager.Kill(ctx, id)
	return freed, toAPIError(err)
}

// RollbackSpawn deletes a seed-state session row, or falls back to a Kill if
// the session has spawn output. Used by the CLI to undo a `spawn --claim-pr`
// when the claim step fails, avoiding the orphan terminated row that a plain
// Kill would leave behind.
func (s *Service) RollbackSpawn(ctx context.Context, id domain.SessionID) (RollbackOutcome, error) {
	deleted, killed, err := s.manager.RollbackSpawn(ctx, id)
	if err != nil {
		return RollbackOutcome{}, toAPIError(err)
	}
	return RollbackOutcome{Deleted: deleted, Killed: killed}, nil
}

// ListKnownWorkspacePaths returns every workspace path any session record
// claims, live or finished.
//
// The orphan sweep subtracts this set from what is on disk, so it must be the
// WHOLE set — filtering it to finished sessions would make every live session's
// worktree look unowned. Paths are returned verbatim; the caller normalises.
func (s *Service) ListKnownWorkspacePaths(ctx context.Context) ([]string, error) {
	recs, err := s.store.ListAllSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(recs))
	for _, rec := range recs {
		if rec.Metadata.WorkspacePath != "" {
			out = append(out, rec.Metadata.WorkspacePath)
		}
	}
	return out, nil
}

// ReasonAlreadyGone means the workspace directory no longer exists, so there
// was nothing to reclaim. It is a quiet, terminal outcome rather than a
// refusal: the loop stops tracking the session and writes nothing to the audit
// log, because nothing happened.
const ReasonAlreadyGone = "already_gone"

// ReasonNotTerminated means the session was still live when the teardown was
// asked for, so nothing was touched. Eligibility is decided by ListReclaimable,
// but a session can come back between that listing and this call — a restore, a
// resume, a TODO started — and a reclaim loop that tore down a live agent's
// worktree would be the worst failure this feature has. Refusing here means the
// eligibility filter is not the only thing standing in the way.
const ReasonNotTerminated = "not_terminated"

// ReclaimOutcome is the auto-reclaim loop's record of one attempt: enough to
// write a truthful log line and to decide whether the attempt needs retrying.
type ReclaimOutcome struct {
	// Freed is true only when the worktree is actually gone from disk.
	Freed bool
	// Reason names why the workspace survived, when Freed is false.
	Reason string
	// BytesFreed is the measured on-disk size of the worktree, taken BEFORE
	// teardown. Zero when nothing was freed or the size could not be read.
	BytesFreed int64
	// Qualified names the session state that made it a candidate (its display
	// status: merged or terminated), for the log.
	Qualified     string
	ProjectID     string
	Branch        string
	WorkspacePath string
}

// Reclaim tears a finished session down (tmux + worktree) while keeping its
// branch, so it stays restorable. It reuses Kill's teardown; the auto-reclaim
// loop is the caller that distinguishes this from a user-initiated kill.
//
// It reports whether the disk was ACTUALLY freed. The previous version threw
// that answer away, so a worktree preserved for holding uncommitted work was
// indistinguishable from one deleted — and the reclaim loop recorded every such
// refusal as a success, then never retried it.
func (s *Service) Reclaim(ctx context.Context, id domain.SessionID) (ReclaimOutcome, error) {
	out := ReclaimOutcome{}
	// Re-read the record at the moment of teardown. Eligibility was decided by
	// ListReclaimable, but a session can come back between that listing and this
	// call — a restore, a resume, a TODO started — and tearing the worktree out
	// from under a working agent is the worst thing this loop could do.
	//
	// An unreadable record stops the teardown as an ERROR rather than a quiet
	// refusal: it is not proof the session is finished, and the repo's standing
	// rule is that an inconclusive reading is never treated as proof of death.
	// The caller logs it loudly and retries on the next pass — a store that will
	// not answer is an operator problem, not the routine "still running" case.
	rec, ok, err := s.getSessionRecord(ctx, id)
	if err != nil {
		return out, toAPIError(err)
	}
	if !ok || !rec.IsTerminated {
		out.Reason = ReasonNotTerminated
		return out, nil
	}
	// The descriptive fields are for the log only: failing to read them must
	// never stop the teardown, so the status lookup below stays best-effort.
	out.ProjectID = string(rec.ProjectID)
	out.Branch = rec.Metadata.Branch
	out.WorkspacePath = rec.Metadata.WorkspacePath
	if sess, sErr := s.toSession(ctx, rec); sErr == nil {
		out.Qualified = string(sess.Status)
	}
	// A session keeps its WorkspacePath after teardown (Restore needs it), so it
	// stays on the candidate list forever and every daemon restart would tear it
	// down again — succeeding trivially against an absent directory and writing
	// a "reclaimed 0 bytes" line into the audit log each time. Recognise that
	// state instead, so the log records only reclaims that actually happened.
	if out.WorkspacePath != "" {
		if _, statErr := os.Stat(out.WorkspacePath); errors.Is(statErr, fs.ErrNotExist) {
			out.Reason = ReasonAlreadyGone
			return out, nil
		}
	}
	// Measure before teardown: afterwards there is nothing left to measure.
	size := dirSize(out.WorkspacePath)

	res, err := s.manager.Teardown(ctx, id, domain.TerminationCauseAutoReclaim)
	if err != nil {
		return out, toAPIError(err)
	}
	out.Freed = res.Freed
	out.Reason = res.Reason
	if res.WorkspacePath != "" {
		out.WorkspacePath = res.WorkspacePath
	}
	if res.Freed {
		out.BytesFreed = size
	}
	return out, nil
}

// getSessionRecord is a nil-tolerant store read, so a Service assembled without
// a store (as the manager-only unit tests do) still tears down.
func (s *Service) getSessionRecord(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	if s.store == nil {
		return domain.SessionRecord{}, false, nil
	}
	return s.store.GetSession(ctx, id)
}

// dirSize sums the apparent size of the regular files under path. It is a
// best-effort measurement for the reclaim log: an unreadable entry contributes
// zero rather than failing the reclaim, because a wrong number in a log is a
// much smaller problem than a worktree left on disk over it.
func dirSize(path string) int64 {
	if path == "" {
		return 0
	}
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree just contributes 0
		}
		if d.IsDir() {
			return nil
		}
		if info, statErr := d.Info(); statErr == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// ReclaimCandidate is a session the auto-reclaim loop may act on, with the
// facts the loop needs to age it, guard it and log it — so the loop never has
// to infer any of them from a directory name.
type ReclaimCandidate struct {
	ID        domain.SessionID
	ProjectID string
	// Branch is never deleted by reclaim; it is carried for the log so the entry
	// doubles as recovery instructions.
	Branch        string
	WorkspacePath string
	// Status is the display status that qualified it (merged or terminated).
	Status string
	// Since is when the record last changed — the clock the age threshold runs
	// on. Unlike an in-memory first-seen stamp it survives a daemon restart, so
	// a machine that restarts more often than the grace period still reclaims.
	Since time.Time
}

// ListReclaimable returns worker sessions that are FINISHED and still hold a
// worktree.
//
// Eligibility is `IsTerminated` on the owning record, never the display status
// alone and never a name match against a directory or a tmux session. Two
// traps this closes:
//
//   - A live worker whose PR has merged derives the display status "merged"
//     while still running (deriveStatusDetail's anyMerged branch is reached with
//     IsTerminated false). Keying off that status pulls the worktree out from
//     under an agent that is mid-task — including a keep-warm worker that merged
//     one PR and is already building the next.
//   - A sleeping or suspended session holds no tmux and no activity, and is
//     fully alive. IsTerminated is false for it, so it is never a candidate.
//
// Orchestrators are excluded outright: no project's orchestrator may be taken
// down to free disk, whatever its age.
func (s *Service) ListReclaimable(ctx context.Context) ([]ReclaimCandidate, error) {
	recs, err := s.store.ListAllSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ReclaimCandidate, 0, len(recs))
	for _, rec := range recs {
		if rec.Kind == domain.KindOrchestrator {
			continue
		}
		// The record's own terminal flag is the only trustworthy signal.
		if !rec.IsTerminated {
			continue
		}
		// Deliberately NOT also gated on IsSuspended. A suspended session is
		// paused rather than finished, but IsSuspended is orthogonal to
		// IsTerminated, so the check above already excludes every live one — and
		// a session suspended and THEN terminated is genuinely finished, so
		// skipping it would strand its worktree on disk forever.
		if rec.Metadata.WorkspacePath == "" {
			continue // nothing on disk left to reclaim
		}
		sess, err := s.toSession(ctx, rec)
		if err != nil {
			continue // a single unreadable row must not sink the pass
		}
		if sess.Status != domain.StatusMerged && sess.Status != domain.StatusTerminated {
			continue
		}
		out = append(out, ReclaimCandidate{
			ID:            rec.ID,
			ProjectID:     string(rec.ProjectID),
			Branch:        rec.Metadata.Branch,
			WorkspacePath: rec.Metadata.WorkspacePath,
			Status:        string(sess.Status),
			Since:         rec.UpdatedAt,
		})
	}
	return out, nil
}

// Delete permanently removes a finished (merged or terminated) session from AO,
// keeping its git branch. A dirty worktree is refused unless force is set.
func (s *Service) Delete(ctx context.Context, id domain.SessionID, force bool) error {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return toAPIError(err)
	}
	if !ok {
		return toAPIError(sessionmanager.ErrNotFound)
	}
	sess, err := s.toSession(ctx, rec)
	if err != nil {
		return err
	}
	// A prepared TODO can always be deleted (nothing was materialized); a live
	// session must be finished (merged or terminated) first.
	if sess.Status != domain.StatusTodo && sess.Status != domain.StatusMerged && sess.Status != domain.StatusTerminated {
		return toAPIError(sessionmanager.ErrNotTerminal)
	}
	return toAPIError(s.manager.PurgeSession(ctx, id, force))
}

// Send delegates agent messaging to the internal manager, passing back whether
// the message reached the agent or was queued for a session that is asleep.
//
// `talk` names the SENDER when a session sent it. That is what makes the crew
// conversation cappable: a message from one crew member to the other is the one
// runaway class this daemon cannot leave to good behaviour, so it is counted,
// recorded and - at the cap - refused (crew_message.go). Every other message,
// which is nearly all of them, carries an empty CrewTalk and takes exactly the
// path it always has.
func (s *Service) Send(ctx context.Context, id domain.SessionID, message string) (ports.SendOutcome, error) {
	return s.SendFrom(ctx, id, message, CrewTalk{})
}

// SendFrom is Send with the sender named. See Send.
func (s *Service) SendFrom(ctx context.Context, id domain.SessionID, message string, talk CrewTalk) (ports.SendOutcome, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return ports.SendOutcome{}, fmt.Errorf("send %s: %w", id, err)
	}
	if !ok {
		return ports.SendOutcome{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	entry, err := s.crewTalkCheck(ctx, rec, talk)
	if err != nil {
		return ports.SendOutcome{}, err
	}
	if entry != nil {
		// Recorded whatever the answer: a refusal is not a non-event, it is the
		// signal that parks the task at NEEDS YOU.
		if err := s.store.InsertCrewMessage(ctx, *entry); err != nil {
			return ports.SendOutcome{}, fmt.Errorf("record crew message: %w", err)
		}
		if entry.Refused() {
			return ports.SendOutcome{}, apierr.Conflict("CREW_MESSAGE_CAPPED", entry.RefusedReason, nil)
		}
	}
	outcome, err := s.manager.Send(ctx, id, message)
	return outcome, toAPIError(err)
}

// Rename updates the user-facing session display name.
func (s *Service) Rename(ctx context.Context, id domain.SessionID, displayName string) error {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return apierr.Invalid("DISPLAY_NAME_REQUIRED", "Display name is required", nil)
	}
	renamed, err := s.store.RenameSession(ctx, id, displayName, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("rename %s: %w", id, err)
	}
	if !renamed {
		return apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return nil
}

// SetPreview persists the browser preview URL for a session and returns the
// refreshed read model. The URL is taken verbatim from the caller (the
// controller resolves it, either an explicit target or an autodetected entry).
// Persisting it via the store fans out a session_updated CDC event through the
// sessions_cdc_update trigger, mirroring how other session mutations surface on
// the live event stream.
func (s *Service) SetPreview(ctx context.Context, id domain.SessionID, previewURL string) (domain.Session, error) {
	updated, err := s.store.SetSessionPreviewURL(ctx, id, previewURL, time.Now().UTC())
	if err != nil {
		return domain.Session{}, fmt.Errorf("set preview url %s: %w", id, err)
	}
	if !updated {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return s.Get(ctx, id)
}

// SetAutoNudge sets (or clears, when override is nil) the per-session override
// for auto-nudging the worker on unresolved PR review comments, then returns
// the refreshed read model. A nil override means "inherit the global default".
func (s *Service) SetAutoNudge(ctx context.Context, id domain.SessionID, override *bool) (domain.Session, error) {
	updated, err := s.store.SetSessionAutoNudge(ctx, id, override, time.Now().UTC())
	if err != nil {
		return domain.Session{}, fmt.Errorf("set auto-nudge %s: %w", id, err)
	}
	if !updated {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return s.Get(ctx, id)
}

// SetAutoResolve sets (or clears, when override is nil) the per-session gate for
// auto-resolving a review thread once our side replies on it, then returns the
// refreshed read model. A nil override means OFF (there is no global default).
func (s *Service) SetAutoResolve(ctx context.Context, id domain.SessionID, override *bool) (domain.Session, error) {
	updated, err := s.store.SetSessionAutoResolve(ctx, id, override, time.Now().UTC())
	if err != nil {
		return domain.Session{}, fmt.Errorf("set auto-resolve %s: %w", id, err)
	}
	if !updated {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return s.Get(ctx, id)
}

// SetKeepWarmOnMerge toggles whether a worker suspends-in-place on merge (card
// stays on the board, resumable) rather than terminating to Done, then returns
// the refreshed read model (feature/merge-suspend-in-place). Returns 404 when the
// session id is unknown.
func (s *Service) SetKeepWarmOnMerge(ctx context.Context, id domain.SessionID, enabled bool) (domain.Session, error) {
	updated, err := s.store.SetSessionKeepWarmOnMerge(ctx, id, enabled, time.Now().UTC())
	if err != nil {
		return domain.Session{}, fmt.Errorf("set keep-warm-on-merge %s: %w", id, err)
	}
	if !updated {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return s.Get(ctx, id)
}

// SetIssueBinding sets a session's issue_id + display_name together (the Jira
// link/unlink-after-creation path) and returns the refreshed read model. The
// caller (the Jira service) composes the values: on link issue_id is
// "jira:<KEY>" with the issue title as the display name; on unlink issue_id is a
// plain title. Returns 404 when the session id is unknown.
func (s *Service) SetIssueBinding(ctx context.Context, id domain.SessionID, issueID, displayName string) (domain.Session, error) {
	updated, err := s.store.SetSessionIssueBinding(ctx, id, issueID, displayName, time.Now().UTC())
	if err != nil {
		return domain.Session{}, fmt.Errorf("set issue binding %s: %w", id, err)
	}
	if !updated {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return s.Get(ctx, id)
}

// Cleanup delegates terminal workspace cleanup to the internal manager and
// reports both reclaimed and preserved (skipped) workspaces.
func (s *Service) Cleanup(ctx context.Context, project domain.ProjectID) (CleanupOutcome, error) {
	res, err := s.manager.Cleanup(ctx, project)
	if err != nil {
		return CleanupOutcome{}, err
	}
	out := CleanupOutcome{Cleaned: res.Cleaned, Skipped: make([]CleanupSkipped, 0, len(res.Skipped))}
	if out.Cleaned == nil {
		out.Cleaned = []domain.SessionID{}
	}
	for _, skip := range res.Skipped {
		out.Skipped = append(out.Skipped, CleanupSkipped{SessionID: skip.SessionID, Reason: skip.Reason})
	}
	return out, nil
}

// TeardownProject stops every live session in a project, then asks the session
// manager to reclaim terminal workspaces. Dirty worktrees are preserved by Kill
// and Cleanup; callers only see hard teardown failures.
func (s *Service) TeardownProject(ctx context.Context, project domain.ProjectID) error {
	recs, err := s.listRecords(ctx, project)
	if err != nil {
		return err
	}
	for _, rec := range recs {
		if rec.IsTerminated {
			continue
		}
		if _, err := s.Kill(ctx, rec.ID); err != nil {
			return err
		}
	}
	_, err = s.Cleanup(ctx, project)
	return err
}

// List returns sessions as enriched display models after applying API filters.
func (s *Service) List(ctx context.Context, filter ListFilter) ([]domain.Session, error) {
	recs, err := s.listRecords(ctx, filter.ProjectID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Session, 0, len(recs))
	for _, rec := range recs {
		if !matchesSessionFilter(rec, filter) {
			continue
		}
		sess, err := s.toSession(ctx, rec)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, nil
}

func (s *Service) listRecords(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	if project == "" {
		recs, err := s.store.ListAllSessions(ctx)
		if err != nil {
			return nil, fmt.Errorf("list all sessions: %w", err)
		}
		return recs, nil
	}
	recs, err := s.store.ListSessions(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", project, err)
	}
	return recs, nil
}

func matchesSessionFilter(rec domain.SessionRecord, filter ListFilter) bool {
	if filter.Active != nil && rec.IsTerminated == *filter.Active {
		return false
	}
	if filter.OrchestratorOnly && rec.Kind != domain.KindOrchestrator {
		return false
	}
	if filter.Fresh && rec.IsTerminated {
		return false
	}
	return true
}

// Get returns one session as an enriched display model, or an apierr.NotFound
// (SESSION_NOT_FOUND) if it is absent.
func (s *Service) Get(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return domain.Session{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return s.toSession(ctx, rec)
}

// toAPIError maps the session engine's sentinel errors to their REST API
// equivalents; an unrecognized error passes through and surfaces as a 500.
func toAPIError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sessionmanager.ErrNotFound):
		return apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	case errors.Is(err, sessionmanager.ErrNotRestorable):
		return apierr.Conflict("SESSION_NOT_RESTORABLE", "Session is not restorable", nil)
	case errors.Is(err, sessionmanager.ErrTerminated):
		return apierr.Conflict("SESSION_TERMINATED", "Session is terminated", nil)
	case errors.Is(err, sessionmanager.ErrIncompleteHandle):
		return apierr.Conflict("SESSION_INCOMPLETE_HANDLE", "Session is missing runtime or workspace handles", nil)
	case errors.Is(err, sessionmanager.ErrNotResumable):
		return apierr.Conflict("SESSION_NOT_RESUMABLE",
			"This session has no saved agent session or prompt to resume from", nil)
	case errors.Is(err, sessionmanager.ErrProjectNotResolvable):
		return apierr.Invalid("PROJECT_NOT_RESOLVABLE", "Project is not registered or has no repo. Register it with `ao project add`", nil)
	case errors.Is(err, sessionmanager.ErrUnknownHarness):
		return apierr.Invalid("UNKNOWN_HARNESS", err.Error(), nil)
	case errors.Is(err, sessionmanager.ErrMissingHarness):
		return apierr.Invalid("AGENT_REQUIRED", err.Error(), nil)
	case errors.Is(err, ports.ErrWorkspaceBranchCheckedOutElsewhere):
		return apierr.Conflict("BRANCH_CHECKED_OUT_ELSEWHERE", err.Error(), nil)
	case errors.Is(err, ports.ErrWorkspaceBranchNotFetched):
		return apierr.Invalid("BRANCH_NOT_FETCHED", err.Error(), nil)
	case errors.Is(err, ports.ErrWorkspaceBranchInvalid):
		return apierr.Invalid("INVALID_BRANCH", err.Error(), nil)
	case errors.Is(err, ports.ErrAgentBinaryNotFound):
		return apierr.Invalid("AGENT_BINARY_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, ports.ErrRuntimePrerequisite):
		return apierr.Invalid("RUNTIME_PREREQUISITE_MISSING", err.Error(), nil)
	case errors.Is(err, sessionmanager.ErrInvalidCrew):
		return apierr.Invalid("INVALID_CREW", err.Error(), nil)
	case errors.Is(err, sessionmanager.ErrCrewRoleTaken):
		// The task is already the shape it was asked to become - a conflict with
		// what exists, not a malformed request.
		return apierr.Conflict("CREW_ROLE_TAKEN", err.Error(), nil)
	case errors.Is(err, sessionmanager.ErrCrewTaskFinished):
		return apierr.Conflict("CREW_TASK_FINISHED", err.Error(), nil)
	case errors.Is(err, sessionmanager.ErrNotTodo):
		return apierr.Conflict("SESSION_NOT_TODO", "Session is not a prepared TODO (already started or never was one)", nil)
	case errors.Is(err, sessionmanager.ErrNotTerminal):
		return apierr.Conflict("SESSION_NOT_TERMINAL", "Session is not finished (merged or terminated)", nil)
	case errors.Is(err, ports.ErrWorkspaceDirty):
		return apierr.Conflict("SESSION_WORKSPACE_DIRTY", "Session worktree has uncommitted changes; delete with force to discard them", nil)
	default:
		return err
	}
}

func (s *Service) toSession(ctx context.Context, rec domain.SessionRecord) (domain.Session, error) {
	prs, err := s.store.ListPRFactsForSession(ctx, rec.ID)
	if err != nil {
		return domain.Session{}, fmt.Errorf("pr facts %s: %w", rec.ID, err)
	}
	var approvalRule domain.ApprovalRule
	var projectDefaultBranch string
	if project, ok, perr := s.store.GetProject(ctx, string(rec.ProjectID)); perr == nil && ok {
		approvalRule = project.Config.ApprovalRule
		projectDefaultBranch = project.Config.WithDefaults().DefaultBranch
	}
	// Two indexed lookups on a table that stays empty unless somebody brackets a
	// run: what this member is running now, and whether its runs are being thrown
	// away. Both are on the LIST read model because the board draws them.
	openRun, hasOpenRun, err := s.store.OpenCrewRunForSession(ctx, rec.ID)
	if err != nil {
		return domain.Session{}, fmt.Errorf("open crew run %s: %w", rec.ID, err)
	}
	discards, err := s.store.ConsecutiveCrewRunDiscards(ctx, rec.ID)
	if err != nil {
		return domain.Session{}, fmt.Errorf("crew run discards %s: %w", rec.ID, err)
	}
	// One more indexed lookup on a table that stays empty unless two members of
	// one task actually message each other, and it is skipped outright for a solo
	// session.
	talkCapped, err := s.crewTalkRefused(ctx, rec)
	if err != nil {
		return domain.Session{}, fmt.Errorf("crew talk %s: %w", rec.ID, err)
	}
	detail := deriveStatusDetail(rec, prs, s.now(), s.harnessSignals(rec.Harness), approvalRule, crewRunFacts{Discards: discards, TalkCapped: talkCapped})
	// Resolve the target branch from facts already loaded above — no extra query
	// and no subprocess, so this stays affordable on the sessions LIST endpoint.
	// That budget is why the chain stops at the project default here: the
	// origin/HEAD step needs a worktree and belongs to Changes mode alone.
	targetPRs := make([]targetPR, 0, len(prs))
	for _, p := range prs {
		targetPRs = append(targetPRs, targetPR{Branch: p.TargetBranch, Open: !p.Merged && !p.Closed})
	}
	targetBranch, targetSource := resolveTargetChain(targetPRs, rec.PRTarget, rec.BaseBranch, projectDefaultBranch)
	// One indexed lookup: the queue table is empty for almost every session, and
	// the count has to be on the LIST read model or the board could not show that
	// a sleeping session has mail waiting.
	queued, err := s.store.SessionQueuedMessageCounts(ctx, rec.ID)
	if err != nil {
		return domain.Session{}, fmt.Errorf("queued messages %s: %w", rec.ID, err)
	}
	return domain.Session{
		SessionRecord:        rec,
		Status:               detail.Status,
		StatusReason:         detail.Reason,
		NextTransitionAt:     detail.NextTransitionAt,
		NextTransitionTo:     detail.NextTransitionTo,
		IdleCloseAt:          s.idleCloseAt(rec),
		TerminalHandleID:     rec.Metadata.RuntimeHandleID,
		PRs:                  prs,
		TargetBranch:         targetBranch,
		TargetSource:         targetSource,
		QueuedMessages:       queued.Pending,
		QueuedMessagesFailed: queued.Failed,
		CrewRun:              openRunPtr(openRun, hasOpenRun),
		CrewRunDiscards:      discards,
	}, nil
}

// openRunPtr keeps the read model's "not running anything" as a nil rather than
// a zero-valued run: an empty object on the wire would say "there is a run and
// we know nothing about it", which is a different and false statement.
func openRunPtr(run domain.CrewRun, ok bool) *domain.CrewRun {
	if !ok {
		return nil
	}
	return &run
}

// idleCloseAt is when the idle sweep would suspend this session if no further
// activity arrives — idleReference + the configured TTL — for the board/sidebar
// countdown. It is nil when the sweep is disabled (TTL 0) or the session is not
// a live suspend candidate: a terminated session (already gone), a prepared TODO
// (no runtime yet), or an already-suspended one (no countdown while paused).
func (s *Service) idleCloseAt(rec domain.SessionRecord) *time.Time {
	if s.idleCloseTTL <= 0 || rec.IsTerminated || rec.IsTodo || rec.IsSuspended {
		return nil
	}
	at := sessionmanager.IdleReference(rec).Add(s.idleCloseTTL)
	return &at
}

// now tolerates a zero-value Service (tests construct the struct literally
// without going through New, which is where clock gets its default).
func (s *Service) now() time.Time {
	if s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock().UTC()
}

// harnessSignals tolerates a zero-value Service the same way now does. Without
// an injected capability predicate the service cannot tell a broken pipeline
// from a hook-less harness, so it never claims no_signal.
func (s *Service) harnessSignals(h domain.AgentHarness) bool {
	if s.signalCapable == nil {
		return false
	}
	return s.signalCapable(h)
}

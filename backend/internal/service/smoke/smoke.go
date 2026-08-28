// Package smoke is the daemon's HTTP-facing service for worker-authored manual
// smoke-test checklists. It mirrors the Reviews data path in shape (Manager +
// Store interfaces, New + options, sentinel errors mapped to 422/404 by the
// controller) but is plain per-session CRUD plus evidence-blob handling and a
// report-back over the same channel `ao send` uses — it spawns nothing.
package smoke

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/pngmeta"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/preview"
)

// ErrInvalid and ErrNotFound are the service sentinels the HTTP controller maps
// to 422 and 404 respectively.
var (
	ErrInvalid  = errors.New("smoke: invalid request")
	ErrNotFound = errors.New("smoke: not found")
)

// ErrResultsAtRisk refuses an Author call that would delete a case the user has
// already played. A case id is derived from the case NAME when the payload omits
// an explicit "id", so an agent that merely rewords a name produces a different
// id: the old case falls out of the payload and would be dropped together with
// the verdict, note and evidence bytes the user recorded on it. Those are the
// one part of a checklist AO cannot regenerate, so authoring fails loudly
// instead, naming the cases and how to keep them. The controller maps it to 422.
var ErrResultsAtRisk = errors.New("smoke: author would discard recorded results")

// ErrCaseRetired refuses any write to a retired case. Retiring one is how a
// checklist shrinks AUDITABLY: the case stops being something the user is asked
// to play, and the row - its name, its steps, the user's verdict, note and
// evidence, and the reason it went - stays. Frozen is what makes that trace
// worth anything, so a retired case takes no verdict, no reset, no machine
// result and no re-author. Nor can an `ao smoke set` payload revive one by
// naming its id: an agent that re-sends its whole checklist every round must not
// be able to resurrect what it retired last round. A case that genuinely comes
// back comes back under a NEW id, unplayed - which is right, because the old
// results were recorded against the old steps. The controller maps it to 422.
var ErrCaseRetired = errors.New("smoke: case is retired")

// maxChecklistCases bounds one session's checklist. Shared by the whole-list and
// the per-case write paths, so adding cases one at a time cannot walk past the
// ceiling `set` enforces in a single call.
const maxChecklistCases = 50

// Evidence size caps (user decision 2026-07-11): 25 MB image / 200 MB video.
const (
	maxImageBytes int64 = 25 << 20
	maxVideoBytes int64 = 200 << 20
)

// evidenceKinds is the accepted upload allow-list, mapping normalized MIME type
// to the stored kind. Anything else is rejected with ErrInvalid.
var evidenceKinds = map[string]string{
	"image/png":       "image",
	"image/jpeg":      "image",
	"image/gif":       "image",
	"image/webp":      "image",
	"video/mp4":       "video",
	"video/webm":      "video",
	"video/quicktime": "video",
}

// Manager is the smoke surface the HTTP controller depends on.
type Manager interface {
	List(ctx context.Context, sessionID domain.SessionID) (SessionSmoke, error)
	Author(ctx context.Context, from, sessionID domain.SessionID, cases []domain.SmokeAuthoredCase) (SessionSmoke, error)
	AddCases(ctx context.Context, from, sessionID domain.SessionID, cases []domain.SmokeAuthoredCase) (SessionSmoke, error)
	EditCase(ctx context.Context, from, sessionID domain.SessionID, checkID string, patch domain.SmokeCasePatch) (domain.SmokeCheck, error)
	RemoveCase(ctx context.Context, from, sessionID domain.SessionID, checkID string) (SessionSmoke, error)
	StandDown(ctx context.Context, from, sessionID domain.SessionID, reason string) (SessionSmoke, error)
	SetVerdict(ctx context.Context, sessionID domain.SessionID, checkID string, verdict domain.SmokeVerdict, note, agreedRunID string) (domain.SmokeCheck, error)
	RecordAgentResult(ctx context.Context, sessionID domain.SessionID, checkID string, res domain.SmokeAgentResult) (domain.SmokeCheck, error)
	Retire(ctx context.Context, sessionID domain.SessionID, checkID, reason string) (domain.SmokeCheck, error)
	Reset(ctx context.Context, sessionID domain.SessionID, checkID string) (domain.SmokeCheck, error)
	AttachEvidence(ctx context.Context, sessionID domain.SessionID, checkID string, upload EvidenceUpload) (domain.SmokeEvidence, error)
	OpenEvidence(ctx context.Context, sessionID domain.SessionID, checkID, evidenceID string) (EvidenceBlob, error)
	ExportEvidence(ctx context.Context, sessionID domain.SessionID, checkID, evidenceID string) (string, error)
	RemoveEvidence(ctx context.Context, sessionID domain.SessionID, checkID, evidenceID string) (domain.SmokeCheck, error)
	Report(ctx context.Context, sessionID domain.SessionID) (ReportOutcome, error)
	PostToJira(ctx context.Context, sessionID domain.SessionID) (JiraPostOutcome, error)
	PurgeSessionEvidence(ctx context.Context, sessionID domain.SessionID) error
	PurgeEvidenceOlderThan(ctx context.Context, cutoff time.Time) (EvidencePurgeResult, error)
}

// Store is the persistence surface the service owns. The concrete
// *sqlite.Store satisfies it, including the two session-read methods used for
// report-back liveness/orchestrator lookup.
type Store interface {
	ListSmokeChecksBySession(ctx context.Context, id domain.SessionID) ([]domain.SmokeCheck, error)
	GetSmokeCheck(ctx context.Context, id string) (domain.SmokeCheck, bool, error)
	ReplaceSmokeChecks(ctx context.Context, sessionID domain.SessionID, projectID domain.ProjectID, cases []domain.SmokeAuthoredCase, author domain.SmokeAuthor, now time.Time) ([]domain.SmokeCheck, []string, error)
	UpsertSmokeChecks(ctx context.Context, sessionID domain.SessionID, projectID domain.ProjectID, cases []domain.SmokeAuthoredCase, author domain.SmokeAuthor, now time.Time) ([]domain.SmokeCheck, error)
	PatchSmokeCheckAuthored(ctx context.Context, id string, patch domain.SmokeCasePatch, author domain.SmokeAuthor, now time.Time) (bool, error)
	DeleteSmokeCheck(ctx context.Context, id string) (bool, error)
	GetSmokeChecklistStandDown(ctx context.Context, sessionID domain.SessionID) (domain.SmokeStandDown, bool, error)
	SetSmokeChecklistStandDown(ctx context.Context, sessionID domain.SessionID, reason string, author domain.SmokeAuthor, now time.Time) error
	ClearSmokeChecklistStandDown(ctx context.Context, sessionID domain.SessionID) error
	SetSmokeVerdict(ctx context.Context, id string, verdict domain.SmokeVerdict, note, agreedRunID string, decidedAt, now time.Time) (bool, error)
	OpenSmokeRun(ctx context.Context, checkID string, sessionID domain.SessionID, now time.Time) (domain.SmokeRun, bool, error)
	CloseSmokeRun(ctx context.Context, runID string, res domain.SmokeAgentResult, recordedAt, now time.Time) (bool, error)
	RetireSmokeCheck(ctx context.Context, id, reason string, retiredAt, now time.Time) (bool, error)
	ListUserSmokeEvidence(ctx context.Context, checkID string) ([]domain.SmokeEvidence, error)
	ResetSmokeCheck(ctx context.Context, id string, now time.Time) (bool, error)
	MarkSmokeReported(ctx context.Context, id domain.SessionID, reportedAt, now time.Time) (int64, error)
	InsertSmokeEvidence(ctx context.Context, ev domain.SmokeEvidence) error
	GetSmokeEvidence(ctx context.Context, id string) (domain.SmokeEvidence, bool, error)
	DeleteSmokeEvidence(ctx context.Context, id string) (bool, error)
	ListSmokeEvidenceCreatedBefore(ctx context.Context, before time.Time) ([]domain.SmokeEvidence, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	ListSessions(ctx context.Context, projectID domain.ProjectID) ([]domain.SessionRecord, error)
}

// Messenger delivers a report-back message over the same path `ao send` uses
// (session manager Send). *sessionsvc.Service satisfies it.
type Messenger interface {
	Send(ctx context.Context, id domain.SessionID, message string) (ports.SendOutcome, error)
}

// SessionSmoke is the list read model: the worker label (drives the tab
// subtitle), the whole checklist, and when its results were last reported.
type SessionSmoke struct {
	Worker     string              `json:"worker"`
	ReportedAt *time.Time          `json:"reportedAt,omitempty"`
	Checks     []domain.SmokeCheck `json:"checks"`
	// StandDown is set when a member has recorded that this change needs no
	// human verification. It is what stops an EMPTY checklist meaning two
	// opposite things at once - nobody has decided yet, or it was decided and
	// there is nothing worth a person's eyes - which is a distinction the screen
	// could not previously draw.
	StandDown *domain.SmokeStandDown `json:"standDown,omitempty"`
}

// EvidenceUpload is one attach request: the declared MIME + original filename
// and a reader over the bytes. The service streams the reader to disk under a
// per-kind size cap; it never trusts the filename for the on-disk path.
type EvidenceUpload struct {
	Filename string
	Mime     string
	Reader   io.Reader
	// Source is who is attaching it. Empty means the user, which is what every
	// caller before `ao smoke record` meant; only the record path sets "agent".
	Source domain.SmokeEvidenceSource
}

// EvidenceBlob is what the controller needs to serve a stored blob.
type EvidenceBlob struct {
	Path     string
	Mime     string
	Filename string
}

// EvidencePurgeResult summarizes one age-based retention sweep: how many
// evidence items were removed and how many bytes that freed.
type EvidencePurgeResult struct {
	Purged     int   `json:"purged"`
	FreedBytes int64 `json:"freedBytes"`
}

// ReportOutcome describes where a report-back landed.
type ReportOutcome struct {
	Delivered bool   `json:"delivered"`
	Target    string `json:"target"` // "worker" | "orchestrator" | "persisted"
	Summary   string `json:"summary"`
	// Queued marks a report that AO is HOLDING because the target session is
	// asleep: it will be delivered when that session's agent is listening again.
	// Distinct from Delivered on purpose - the human must not read "sent" when
	// the agent has not seen it yet.
	Queued bool `json:"queued,omitempty"`
}

// Service is the API-facing smoke service.
type Service struct {
	store        Store
	messenger    Messenger
	jira         JiraPoster
	evidenceRoot string
	clock        func() time.Time
	// mediaResolveBackoff is the retry schedule for resolving an evidence file's
	// Jira media id (see resolveMediaID); tests shorten it.
	mediaResolveBackoff []time.Duration
}

var _ Manager = (*Service)(nil)

// Option customizes the service.
type Option func(*Service)

// WithClock overrides the service clock for tests.
func WithClock(clock func() time.Time) Option {
	return func(s *Service) { s.clock = clock }
}

// WithJiraPoster wires the Jira write client used by PostToJira (comment +
// attachment upload). Left unset (nil) the button's endpoint reports Jira as
// unconfigured rather than panicking, mirroring the other nil-dependency guards.
func WithJiraPoster(poster JiraPoster) Option {
	return func(s *Service) { s.jira = poster }
}

// New builds the smoke service. dataDir is the resolved AO data dir; evidence
// blobs live under <dataDir>/evidence (all under ~/.ao). messenger may be nil
// (report-back then degrades to persist-only).
func New(store Store, dataDir string, messenger Messenger, opts ...Option) *Service {
	s := &Service{
		store:               store,
		messenger:           messenger,
		evidenceRoot:        filepath.Join(dataDir, "evidence"),
		clock:               func() time.Time { return time.Now().UTC() },
		mediaResolveBackoff: defaultMediaResolveBackoff,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// List returns a session's checklist plus its worker label and report state.
func (s *Service) List(ctx context.Context, sessionID domain.SessionID) (SessionSmoke, error) {
	if sessionID == "" {
		return SessionSmoke{}, fmt.Errorf("%w: session id is required", ErrInvalid)
	}
	checks, err := s.store.ListSmokeChecksBySession(ctx, sessionID)
	if err != nil {
		return SessionSmoke{}, err
	}
	worker := s.workerLabel(ctx, sessionID)
	out := SessionSmoke{Worker: worker, ReportedAt: reportedAt(checks), Checks: checks}
	stood, ok, err := s.store.GetSmokeChecklistStandDown(ctx, sessionID)
	if err != nil {
		return SessionSmoke{}, err
	}
	if ok {
		out.StandDown = &stood
	}
	return out, nil
}

// Author registers or replaces a session's whole checklist. Cases matched by
// stable id keep their verdict/note/evidence (see ReplaceSmokeChecks); an id
// absent from the payload is removed and its evidence blobs deleted - but only
// while nobody has played it. A missing case that carries a verdict, a note or
// evidence fails the whole call with ErrResultsAtRisk rather than being deleted.
//
// Retired cases sit outside all of this. They are not "at risk" from an omission
// (surviving one is what retiring a case means) and ReplaceSmokeChecks leaves
// them alone, so a checklist that has shrunk stays shrunk. Naming a retired id
// in the payload is refused with ErrCaseRetired rather than reviving it.
//
// Author sets the WHOLE list, so it is the one write path on which a second
// author is destructive by construction: whoever calls it last decides what the
// list contains. It stays because authoring an initial checklist in one call is
// still the right shape - but it is no longer the only way to write. AddCases,
// EditCase and RemoveCase touch one case each, and that is what two members
// working at once should use.
func (s *Service) Author(ctx context.Context, from, sessionID domain.SessionID, cases []domain.SmokeAuthoredCase) (SessionSmoke, error) {
	if sessionID == "" {
		return SessionSmoke{}, fmt.Errorf("%w: session id is required", ErrInvalid)
	}
	if len(cases) == 0 {
		return SessionSmoke{}, fmt.Errorf("%w: at least one case is required", ErrInvalid)
	}
	if len(cases) > maxChecklistCases {
		return SessionSmoke{}, fmt.Errorf("%w: a checklist may have at most %d cases", ErrInvalid, maxChecklistCases)
	}
	author, err := s.resolveAuthor(ctx, from)
	if err != nil {
		return SessionSmoke{}, err
	}
	rec, ok, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return SessionSmoke{}, err
	}
	if !ok {
		return SessionSmoke{}, fmt.Errorf("%w: session %q", ErrNotFound, sessionID)
	}
	resolved, err := resolveCases(sessionID, cases, func(id string) (bool, error) {
		existing, ok, err := s.store.GetSmokeCheck(ctx, id)
		if err != nil {
			return false, err
		}
		return ok && existing.SessionID != sessionID, nil
	})
	if err != nil {
		return SessionSmoke{}, err
	}
	// Read the stored checklist and refuse before writing anything. The read is
	// outside the replace transaction, so a verdict recorded in the microseconds
	// between the two would still be replaced; closing that would mean pushing
	// the rule into the store's tx, which is not worth the API for a window this
	// narrow (one agent's re-author racing one human click).
	existing, err := s.store.ListSmokeChecksBySession(ctx, sessionID)
	if err != nil {
		return SessionSmoke{}, err
	}
	if err := checkRetiredInPayload(existing, resolved); err != nil {
		return SessionSmoke{}, err
	}
	if err := checkResultsAtRisk(existing, resolved); err != nil {
		return SessionSmoke{}, err
	}
	_, removed, err := s.store.ReplaceSmokeChecks(ctx, sessionID, rec.ProjectID, resolved, author, s.now())
	if err != nil {
		return SessionSmoke{}, err
	}
	for _, checkID := range removed {
		_ = os.RemoveAll(s.checkDir(sessionID, checkID))
	}
	return s.List(ctx, sessionID)
}

// AddCases writes 1..N cases into the checklist without touching any case the
// payload does not name. It is the write path SHARED AUTHORSHIP rests on.
//
// Both members own this list - dev knows what the change actually touched, qa
// reconstructs it from the outside - and the reason this is a per-case verb
// rather than a permission change on Author is mechanical, not stylistic. Author
// sets the whole list, so lifting the old dev refusal on it alone would have
// made the second author destructive: whoever wrote second would erase the
// other's cases and, with them, the human's verdicts, notes and screenshots.
// Here an author only ever reaches the cases they named, so two members adding
// different cases at the same moment both survive - attribution records who
// changed what, it does not prevent anyone destroying anything.
//
// A named id that already exists is EDITED in place, keeping the user's results.
// A retired id is refused rather than revived, exactly as in Author.
func (s *Service) AddCases(ctx context.Context, from, sessionID domain.SessionID, cases []domain.SmokeAuthoredCase) (SessionSmoke, error) {
	if sessionID == "" {
		return SessionSmoke{}, fmt.Errorf("%w: session id is required", ErrInvalid)
	}
	if len(cases) == 0 {
		return SessionSmoke{}, fmt.Errorf("%w: at least one case is required", ErrInvalid)
	}
	author, err := s.resolveAuthor(ctx, from)
	if err != nil {
		return SessionSmoke{}, err
	}
	rec, ok, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return SessionSmoke{}, err
	}
	if !ok {
		return SessionSmoke{}, fmt.Errorf("%w: session %q", ErrNotFound, sessionID)
	}
	existing, err := s.store.ListSmokeChecksBySession(ctx, sessionID)
	if err != nil {
		return SessionSmoke{}, err
	}
	// An id already on THIS checklist resolves to itself (that is the edit), so
	// only ids other sessions hold force a fresh one.
	resolved, err := resolveCases(sessionID, cases, func(id string) (bool, error) {
		other, ok, err := s.store.GetSmokeCheck(ctx, id)
		if err != nil {
			return false, err
		}
		return ok && other.SessionID != sessionID, nil
	})
	if err != nil {
		return SessionSmoke{}, err
	}
	if len(existing)+len(resolved) > maxChecklistCases {
		return SessionSmoke{}, fmt.Errorf("%w: a checklist may have at most %d cases", ErrInvalid, maxChecklistCases)
	}
	if err := checkRetiredInPayload(existing, resolved); err != nil {
		return SessionSmoke{}, err
	}
	if _, err := s.store.UpsertSmokeChecks(ctx, sessionID, rec.ProjectID, resolved, author, s.now()); err != nil {
		return SessionSmoke{}, err
	}
	return s.List(ctx, sessionID)
}

// EditCase rewrites only the authored fields the patch names on ONE case.
//
// The narrow edit is what keeps two authors out of each other's way. Without it,
// changing a case's fileRef would mean re-sending the whole case, which
// overwrites whatever the other member had improved about its wording in the
// meantime - a silent loss that looks like nothing happened. Nothing here can
// reach the user's verdict, note or evidence: the statement behind it does not
// name those columns.
func (s *Service) EditCase(ctx context.Context, from, sessionID domain.SessionID, checkID string, patch domain.SmokeCasePatch) (domain.SmokeCheck, error) {
	check, err := s.requireActiveCheck(ctx, sessionID, checkID)
	if err != nil {
		return domain.SmokeCheck{}, err
	}
	if patch.Empty() {
		return domain.SmokeCheck{}, fmt.Errorf("%w: name at least one field to change", ErrInvalid)
	}
	normalized, err := normalizePatch(patch)
	if err != nil {
		return domain.SmokeCheck{}, err
	}
	author, err := s.resolveAuthor(ctx, from)
	if err != nil {
		return domain.SmokeCheck{}, err
	}
	ok, err := s.store.PatchSmokeCheckAuthored(ctx, check.ID, normalized, author, s.now())
	if err != nil {
		return domain.SmokeCheck{}, err
	}
	if !ok {
		return domain.SmokeCheck{}, fmt.Errorf("%w: smoke check %q", ErrNotFound, checkID)
	}
	return s.getCheck(ctx, check.ID)
}

// RemoveCase drops ONE case off the checklist, and refuses the moment the user
// has played it.
//
// That refusal is the one guard the human kept when they opened the list to both
// members: a case they have already judged is RETIRED, with a reason, never
// silently deleted. Their verdict, note and evidence are the single part of a
// checklist AO cannot regenerate, and a judgement that vanishes unexplained is
// worse than a list that stays one case too long. A case nobody has touched is
// free to remove outright - that is exactly what was asked for.
//
// `from` is accepted for symmetry with the other write verbs and is not read:
// removal leaves no row to attribute it on, and inventing an audit log for it
// would be a second store for one fact. What the caller is told instead is which
// removals are refused, which is the part that protects anything.
func (s *Service) RemoveCase(ctx context.Context, _, sessionID domain.SessionID, checkID string) (SessionSmoke, error) {
	check, err := s.requireActiveCheck(ctx, sessionID, checkID)
	if err != nil {
		return SessionSmoke{}, err
	}
	if played(check) {
		return SessionSmoke{}, removeAtRiskError(check)
	}
	ok, err := s.store.DeleteSmokeCheck(ctx, check.ID)
	if err != nil {
		return SessionSmoke{}, err
	}
	if ok {
		_ = os.RemoveAll(s.checkDir(sessionID, check.ID))
	}
	return s.List(ctx, sessionID)
}

// StandDown records "I looked, and there is nothing here a human needs to
// check".
//
// It exists because an empty Tests tab said two opposite things at once - nobody
// has decided yet, or it was decided and there is nothing worth your eyes - and
// rendered them identically, so the screen could not be read. Saying it in prose
// was all a prompt could do; this is the surface that lets the screen tell them
// apart.
//
// Refused while ACTIVE cases exist, because the claim and the cases contradict
// each other, and a stand-down sitting above a list of things to play is worse
// than none. An all-retired checklist counts as empty: nothing on it is
// something the user is asked to play.
func (s *Service) StandDown(ctx context.Context, from, sessionID domain.SessionID, reason string) (SessionSmoke, error) {
	if sessionID == "" {
		return SessionSmoke{}, fmt.Errorf("%w: session id is required", ErrInvalid)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return SessionSmoke{}, fmt.Errorf("%w: a reason is required - it is the whole content of standing down", ErrInvalid)
	}
	if _, ok, err := s.store.GetSession(ctx, sessionID); err != nil {
		return SessionSmoke{}, err
	} else if !ok {
		return SessionSmoke{}, fmt.Errorf("%w: session %q", ErrNotFound, sessionID)
	}
	checks, err := s.store.ListSmokeChecksBySession(ctx, sessionID)
	if err != nil {
		return SessionSmoke{}, err
	}
	var active []string
	for _, c := range checks {
		if !c.Retired() {
			active = append(active, c.ID)
		}
	}
	if len(active) > 0 {
		return SessionSmoke{}, fmt.Errorf("%w: this checklist still has %d case(s) to play (%s), and \"nothing here needs a person\" cannot stand beside them. Remove the ones that no longer apply (`ao smoke remove`), or retire the ones the user already played (`ao smoke retire`), then stand down",
			ErrInvalid, len(active), strings.Join(active, ", "))
	}
	author, err := s.resolveAuthor(ctx, from)
	if err != nil {
		return SessionSmoke{}, err
	}
	if err := s.store.SetSmokeChecklistStandDown(ctx, sessionID, reason, author, s.now()); err != nil {
		return SessionSmoke{}, err
	}
	return s.List(ctx, sessionID)
}

// resolveAuthor turns the CALLER's own session id into the attribution stamped
// on the write. It is the caller and never the target: both crew members author
// against the same target, because the checklist belongs to the task and
// $AO_CREW_ID is dev's id, so the target cannot say which of them is writing.
//
// An empty or unknown `from` resolves to no author, and is never an error. The
// desktop app, a direct API call and an older `ao` all send nothing, and a write
// AO cannot attribute is still a legitimate write - it simply carries no name
// rather than a guessed one.
func (s *Service) resolveAuthor(ctx context.Context, from domain.SessionID) (domain.SmokeAuthor, error) {
	if from == "" {
		return domain.SmokeAuthor{}, nil
	}
	rec, ok, err := s.store.GetSession(ctx, from)
	if err != nil {
		return domain.SmokeAuthor{}, err
	}
	if !ok {
		return domain.SmokeAuthor{}, nil
	}
	author := domain.SmokeAuthor{ID: rec.ID}
	if rec.InCrew() && rec.CrewRole.Valid() {
		author.Role = rec.CrewRole
	}
	return author, nil
}

// normalizePatch trims what the caller sent the same way resolveCases trims a
// whole case, so an edited field and an authored one are stored identically. A
// name is the one field that may not be blanked: it is what the user reads.
func normalizePatch(patch domain.SmokeCasePatch) (domain.SmokeCasePatch, error) {
	out := patch
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" {
			return domain.SmokeCasePatch{}, fmt.Errorf("%w: a case cannot be left without a name", ErrInvalid)
		}
		out.Name = &name
	}
	if patch.Why != nil {
		why := strings.TrimSpace(*patch.Why)
		out.Why = &why
	}
	if patch.Expected != nil {
		expected := strings.TrimSpace(*patch.Expected)
		out.Expected = &expected
	}
	if patch.FileRef != nil {
		ref := strings.TrimSpace(*patch.FileRef)
		out.FileRef = &ref
	}
	if patch.Steps != nil {
		steps := trimSteps(*patch.Steps)
		out.Steps = &steps
	}
	return out, nil
}

// SetVerdict records the USER's verdict + note for a case.
//
// agreedRunID is optional and is how the Tests tab's "Agree" button reaches
// here: it names the machine run the user was looking at when they confirmed its
// conclusion instead of deriving their own. Everything else about the write is
// identical to a hand-pressed Pass - same columns, same DecidedAt, no run row
// created, nothing in the machine's lane touched - because that is the whole
// point. Agreement is a fact about HOW the user arrived at their verdict; it can
// never be a way for the machine to author one. A case still counts as played
// only because a person acted, which is what keeps "N of M verified" honest.
func (s *Service) SetVerdict(ctx context.Context, sessionID domain.SessionID, checkID string, verdict domain.SmokeVerdict, note, agreedRunID string) (domain.SmokeCheck, error) {
	if !verdict.Valid() {
		return domain.SmokeCheck{}, fmt.Errorf("%w: verdict must be pass, fail, or skip", ErrInvalid)
	}
	check, err := s.requireActiveCheck(ctx, sessionID, checkID)
	if err != nil {
		return domain.SmokeCheck{}, err
	}
	agreedRunID = strings.TrimSpace(agreedRunID)
	if err := checkAgreement(check, verdict, agreedRunID); err != nil {
		return domain.SmokeCheck{}, err
	}
	now := s.now()
	updated, err := s.store.SetSmokeVerdict(ctx, checkID, verdict, note, agreedRunID, now, now)
	if err != nil {
		return domain.SmokeCheck{}, err
	}
	if !updated {
		return domain.SmokeCheck{}, fmt.Errorf("%w: smoke check %q", ErrNotFound, checkID)
	}
	return s.getCheck(ctx, checkID)
}

// checkAgreement validates a claim that the user's verdict was reached by
// agreeing with a machine run. It refuses everything that would let the claim be
// untrue, because an agreement nobody can trust is worse than no agreement at
// all: the run must be one of THIS case's, it must have concluded, and its
// verdict must be the one being recorded.
//
// It also refuses `skip` outright, and that is a decision rather than an
// oversight. qa's skip means "I could not run this one, nothing was exercised";
// the user's skip means "this check does not apply". Those are different claims,
// so there is nothing to agree with - a one-click agreement here would record
// the user asserting a case is irrelevant when all qa said was that it never
// ran. Evidence-only runs are refused by the same rule that requires a matching
// verdict: a run that deliberately did not judge has no verdict to confirm, and
// letting one through could only ever mean "pass".
func checkAgreement(check domain.SmokeCheck, verdict domain.SmokeVerdict, agreedRunID string) error {
	if agreedRunID == "" {
		return nil
	}
	if verdict == domain.SmokeSkip {
		return fmt.Errorf("%w: qa's skip means it could not run the case; your skip means the case does not apply - they are different claims, so there is nothing to agree with. Record the skip on its own", ErrInvalid)
	}
	for _, run := range check.Runs {
		if run.ID != agreedRunID {
			continue
		}
		if !run.Recorded() {
			return fmt.Errorf("%w: run %q never concluded, so there is no result to agree with", ErrInvalid, agreedRunID)
		}
		if run.Verdict != verdict {
			said := string(run.Verdict)
			if said == "" {
				said = "did not judge it"
			} else {
				said = "said " + said
			}
			return fmt.Errorf("%w: run %q %s, so it cannot be agreed with as %s", ErrInvalid, agreedRunID, said, verdict)
		}
		return nil
	}
	return fmt.Errorf("%w: smoke run %q is not on this case", ErrNotFound, agreedRunID)
}

// RecordAgentResult closes the machine's current RUN on a case with its verdict,
// what it saw and the commit it ran against. Strictly additive - it cannot reach
// a single authored field (name/why/steps/expected/prNum/fileRef), cannot reach
// a single user-runtime field (verdict/note/evidence/decidedAt), cannot remove a
// case, and - since a run is a row - cannot destroy the result of an earlier
// round either. Running a case again adds to its history instead of replacing
// it, which is what makes "this used to fail and now passes" readable.
//
// An empty verdict is allowed and means "ran it, captured what I saw, did not
// judge it" - the right record whenever the capture does not actually answer the
// question the case asks. It is accepted only when THIS RUN carries evidence, so
// an empty record still says something; an earlier round's screenshots do not
// count, because they are not what this one saw.
//
// Refused on a retired case.
func (s *Service) RecordAgentResult(ctx context.Context, sessionID domain.SessionID, checkID string, res domain.SmokeAgentResult) (domain.SmokeCheck, error) {
	check, err := s.requireActiveCheck(ctx, sessionID, checkID)
	if err != nil {
		return domain.SmokeCheck{}, err
	}
	verdict := domain.SmokeVerdict(strings.TrimSpace(string(res.Verdict)))
	if verdict != "" && !verdict.Valid() {
		return domain.SmokeCheck{}, fmt.Errorf("%w: agent verdict must be pass, fail, or skip (or omitted, for an evidence-only run)", ErrInvalid)
	}
	// A machine skip is the ONE verdict that answers nothing about the app: it
	// says "I could not run this one", and unaccompanied it is indistinguishable
	// from the case nobody has got to yet - the very ambiguity a recorded result
	// is supposed to end. So it must carry its reason, and the reason has to come
	// from an attempt: "the agent cannot press and hold" is a finding after
	// trying and a guess before it, and only the words put it in front of a
	// person who can tell the difference.
	if verdict == domain.SmokeSkip && strings.TrimSpace(res.Note) == "" {
		return domain.SmokeCheck{}, fmt.Errorf("%w: a skip must say WHY this machine could not run the case - pass a note, and say what you tried", ErrInvalid)
	}
	now := s.now()
	// The run the machine's captures went into, or a fresh one when it recorded a
	// verdict without capturing anything.
	run, opened, err := s.store.OpenSmokeRun(ctx, checkID, sessionID, now)
	if err != nil {
		return domain.SmokeCheck{}, err
	}
	if !opened {
		return domain.SmokeCheck{}, fmt.Errorf("%w: smoke check %q", ErrNotFound, checkID)
	}
	if verdict == "" && len(check.RunEvidence(run.ID)) == 0 {
		return domain.SmokeCheck{}, fmt.Errorf("%w: a record with no verdict must carry evidence from this run - attach a screenshot or clip, or say pass/fail/skip", ErrInvalid)
	}
	closed, err := s.store.CloseSmokeRun(ctx, run.ID, domain.SmokeAgentResult{
		Verdict: verdict,
		Note:    strings.TrimSpace(res.Note),
		SHA:     strings.TrimSpace(res.SHA),
	}, now, now)
	if err != nil {
		return domain.SmokeCheck{}, err
	}
	if !closed {
		return domain.SmokeCheck{}, fmt.Errorf("%w: smoke run %q", ErrNotFound, run.ID)
	}
	return s.getCheck(ctx, checkID)
}

// Retire freezes a case out of the active checklist, with the reason it went.
// This is the legitimate way to remove a case the user has already played -
// Author refuses to drop one (ErrResultsAtRisk) precisely because the verdict,
// note and evidence bytes are the part of a checklist AO cannot regenerate, and
// retiring destroys none of them. Nothing on the row is cleared: the case keeps
// its name, its steps, the user's result and every evidence file, and gains a
// reason and a date. "Retired 3, now covered by tests" is the artifact; three
// cases quietly vanishing is not.
//
// The reason is required for that reason. A second retire is refused rather
// than overwriting the first one's reason and date.
func (s *Service) Retire(ctx context.Context, sessionID domain.SessionID, checkID, reason string) (domain.SmokeCheck, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return domain.SmokeCheck{}, fmt.Errorf("%w: a reason is required - it is the whole point of retiring a case instead of deleting it (e.g. \"now covered by TestFoo\")", ErrInvalid)
	}
	if _, err := s.requireActiveCheck(ctx, sessionID, checkID); err != nil {
		return domain.SmokeCheck{}, err
	}
	now := s.now()
	retired, err := s.store.RetireSmokeCheck(ctx, checkID, reason, now, now)
	if err != nil {
		return domain.SmokeCheck{}, err
	}
	if !retired {
		return domain.SmokeCheck{}, fmt.Errorf("%w: smoke check %q", ErrNotFound, checkID)
	}
	return s.getCheck(ctx, checkID)
}

// Reset clears the USER's verdict/note and deletes the evidence they attached
// (rows + blobs). It is the user re-playing a case, so it clears the user's
// result only: a machine's recorded result and artifacts survive, because the
// two results answer different questions and wiping one from the other's button
// would merge them by the back door. Refused on a retired case.
func (s *Service) Reset(ctx context.Context, sessionID domain.SessionID, checkID string) (domain.SmokeCheck, error) {
	if _, err := s.requireActiveCheck(ctx, sessionID, checkID); err != nil {
		return domain.SmokeCheck{}, err
	}
	// Remove blobs before clearing rows; either order is safe since the bytes are
	// not in the DB. Per-item rather than a directory sweep: the case dir also
	// holds the machine's artifacts now, and RemoveAll would take those too.
	userEvidence, err := s.store.ListUserSmokeEvidence(ctx, checkID)
	if err != nil {
		return domain.SmokeCheck{}, err
	}
	for _, ev := range userEvidence {
		s.removeEvidenceFiles(ev)
	}
	reset, err := s.store.ResetSmokeCheck(ctx, checkID, s.now())
	if err != nil {
		return domain.SmokeCheck{}, err
	}
	if !reset {
		return domain.SmokeCheck{}, fmt.Errorf("%w: smoke check %q", ErrNotFound, checkID)
	}
	return s.getCheck(ctx, checkID)
}

// AttachEvidence validates and persists one screenshot/clip for a case: bytes
// stream to <dataDir>/evidence/<session>/<check>/<evidenceId> under a per-kind
// size cap, and a metadata row is recorded. The client filename is display-only
// and never used for the on-disk path.
//
// A MACHINE capture also OPENS the case's run if one is not already open, and
// the row records which run it belongs to. That is the whole ordering trick:
// `ao smoke record --evidence` uploads before it posts its result, so the run
// has to be created by the capture rather than adopted afterwards. An adoption
// sweep would have to pick up every unattached machine artifact on the case -
// including captures from before AO kept run history, whose result was
// overwritten - and file them under a verdict they may contradict.
func (s *Service) AttachEvidence(ctx context.Context, sessionID domain.SessionID, checkID string, upload EvidenceUpload) (domain.SmokeEvidence, error) {
	if _, err := s.requireActiveCheck(ctx, sessionID, checkID); err != nil {
		return domain.SmokeEvidence{}, err
	}
	normMime := normalizeMime(upload.Mime)
	kind, ok := evidenceKinds[normMime]
	if !ok {
		return domain.SmokeEvidence{}, fmt.Errorf("%w: unsupported evidence type %q (allowed: png/jpeg/gif/webp images, mp4/webm/mov video)", ErrInvalid, upload.Mime)
	}
	limit := maxImageBytes
	if kind == "video" {
		limit = maxVideoBytes
	}
	now := s.now()
	source := evidenceSource(upload.Source)
	runID := ""
	if source == domain.SmokeEvidenceAgent {
		run, opened, err := s.store.OpenSmokeRun(ctx, checkID, sessionID, now)
		if err != nil {
			return domain.SmokeEvidence{}, err
		}
		if !opened {
			return domain.SmokeEvidence{}, fmt.Errorf("%w: smoke check %q", ErrNotFound, checkID)
		}
		runID = run.ID
	}
	evidenceID := "ev_" + uuid.NewString()
	dir := s.checkDir(sessionID, checkID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return domain.SmokeEvidence{}, fmt.Errorf("create evidence dir: %w", err)
	}
	path := filepath.Join(dir, evidenceID)
	size, err := writeCapped(path, upload.Reader, limit)
	if err != nil {
		_ = os.Remove(path)
		return domain.SmokeEvidence{}, err
	}
	ev := domain.SmokeEvidence{
		ID:        evidenceID,
		CheckID:   checkID,
		SessionID: sessionID,
		Kind:      kind,
		Filename:  sanitizeFilename(upload.Filename),
		Mime:      normMime,
		SizeBytes: size,
		CreatedAt: now,
		Source:    source,
		RunID:     runID,
		// Which build the picture is of, taken from the picture. `ao sim shot`
		// writes it into the PNG, so it is read HERE - the one place every
		// upload passes through - rather than asked for as a flag. Both lanes
		// then carry it: the agent that records a run, and the human who drags
		// a screenshot into the Tests tab having been told nothing at all.
		Build: pngBuildID(path),
	}
	if err := s.store.InsertSmokeEvidence(ctx, ev); err != nil {
		_ = os.Remove(path)
		return domain.SmokeEvidence{}, err
	}
	return ev, nil
}

// pngBuildID reads the build a capture recorded about itself. Anything that is
// not a PNG from `ao sim shot` simply has none, which is a legitimate state and
// never an error: most evidence comes from somewhere else.
func pngBuildID(path string) string {
	value, ok := pngmeta.Get(path, simBuildTextKey)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

// simBuildTextKey is the PNG tEXt keyword `ao sim shot` stores the build id
// under. It is an on-disk contract between the CLI that writes it and this
// service, which reads it back off a file that may have travelled through a
// download, a chat and a drag-and-drop in between.
const simBuildTextKey = "ao-build"

// OpenEvidence resolves a stored blob for serving, verifying it belongs to the
// session + case and confining the path under the evidence root.
func (s *Service) OpenEvidence(ctx context.Context, sessionID domain.SessionID, checkID, evidenceID string) (EvidenceBlob, error) {
	ev, ok, err := s.store.GetSmokeEvidence(ctx, evidenceID)
	if err != nil {
		return EvidenceBlob{}, err
	}
	if !ok || ev.SessionID != sessionID || ev.CheckID != checkID {
		return EvidenceBlob{}, fmt.Errorf("%w: evidence %q", ErrNotFound, evidenceID)
	}
	rel := filepath.Join(string(sessionID), checkID, evidenceID)
	path, ok := preview.ConfinedPath(s.evidenceRoot, rel)
	if !ok {
		return EvidenceBlob{}, fmt.Errorf("%w: evidence %q", ErrNotFound, evidenceID)
	}
	if _, err := os.Stat(path); err != nil {
		return EvidenceBlob{}, fmt.Errorf("%w: evidence %q blob missing", ErrNotFound, evidenceID)
	}
	return EvidenceBlob{Path: path, Mime: ev.Mime, Filename: ev.Filename}, nil
}

// ExportEvidence materializes a human-named, correctly-extensioned copy of a
// stored evidence blob so the desktop app can Reveal-in-Finder / Open it by
// content type. The on-disk blob is an opaque, extensionless ev_<uuid> keyed by
// id (a deliberate storage choice), which Finder cannot open on double-click; the
// export copy — named "<case>-<file>.<ext>" from the record's authoritative MIME
// — lives in an _open/ subdir of the case's evidence dir. That subdir is a
// regenerable cache: existing session-purge / reset / re-author cleanup already
// removes it, and the retention sweep + RemoveEvidence drop each record's copy.
// Returns the copy's absolute path. Idempotent: an up-to-date same-size copy is
// reused rather than rewritten (cheap for repeated reveals of a large clip).
func (s *Service) ExportEvidence(ctx context.Context, sessionID domain.SessionID, checkID, evidenceID string) (string, error) {
	blob, err := s.OpenEvidence(ctx, sessionID, checkID, evidenceID)
	if err != nil {
		return "", err
	}
	dst, ok := s.openExportPath(sessionID, checkID, evidenceID, blob.Filename, blob.Mime)
	if !ok {
		return "", fmt.Errorf("%w: evidence %q", ErrNotFound, evidenceID)
	}
	if err := copyFileIfStale(blob.Path, dst); err != nil {
		return "", fmt.Errorf("export evidence: %w", err)
	}
	return dst, nil
}

// RemoveEvidence deletes one stored evidence item (DB row + on-disk blob) after
// verifying it belongs to the session + case, and returns the case with its
// remaining evidence so the UI reconciles to authoritative state. The user can
// drop a wrong or duplicate screenshot/clip from the case's evidence strip. A
// mismatched or unknown id is ErrNotFound; the blob is removed best-effort after
// the row so a stray file never blocks the delete.
func (s *Service) RemoveEvidence(ctx context.Context, sessionID domain.SessionID, checkID, evidenceID string) (domain.SmokeCheck, error) {
	if _, err := s.requireActiveCheck(ctx, sessionID, checkID); err != nil {
		return domain.SmokeCheck{}, err
	}
	ev, ok, err := s.store.GetSmokeEvidence(ctx, evidenceID)
	if err != nil {
		return domain.SmokeCheck{}, err
	}
	if !ok || ev.SessionID != sessionID || ev.CheckID != checkID {
		return domain.SmokeCheck{}, fmt.Errorf("%w: evidence %q", ErrNotFound, evidenceID)
	}
	if _, err := s.store.DeleteSmokeEvidence(ctx, evidenceID); err != nil {
		return domain.SmokeCheck{}, err
	}
	s.removeEvidenceFiles(ev)
	return s.getCheck(ctx, checkID)
}

// PurgeEvidenceOlderThan deletes every evidence item (DB row + on-disk blob +
// any exported copy) whose created_at predates cutoff, across all sessions. Age
// comes from the DB record's created_at, never the file's mtime, so a touched or
// re-copied file is not spared. It is idempotent and safe to run repeatedly: a
// missing row/file is tolerated, and every blob path is derived from its OWN
// record's session/check, so a sweep can never delete evidence for the wrong
// session or case. Callers pass a cutoff already clamped by the retention policy
// (see evidenceretention.Settings.Cutoff) so a misconfigured tiny TTL cannot nuke
// recent evidence here.
func (s *Service) PurgeEvidenceOlderThan(ctx context.Context, cutoff time.Time) (EvidencePurgeResult, error) {
	var res EvidencePurgeResult
	rows, err := s.store.ListSmokeEvidenceCreatedBefore(ctx, cutoff)
	if err != nil {
		return res, err
	}
	for _, ev := range rows {
		deleted, err := s.store.DeleteSmokeEvidence(ctx, ev.ID)
		if err != nil {
			return res, err
		}
		// Remove the blob + export copy best-effort regardless of whether the row
		// was still present (a concurrent delete may have raced us); only count a
		// row we actually removed so FreedBytes stays honest.
		rel := filepath.Join(string(ev.SessionID), ev.CheckID, ev.ID)
		if path, ok := preview.ConfinedPath(s.evidenceRoot, rel); ok {
			_ = os.Remove(path)
		}
		if dst, ok := s.openExportPath(ev.SessionID, ev.CheckID, ev.ID, ev.Filename, ev.Mime); ok {
			_ = os.Remove(dst)
		}
		if deleted {
			res.Purged++
			res.FreedBytes += ev.SizeBytes
		}
	}
	return res, nil
}

// Report composes a deterministic results summary and delivers it back to the
// worker (live worker → active orchestrator → persist-only), then stamps
// reported_at across the session's checks.
func (s *Service) Report(ctx context.Context, sessionID domain.SessionID) (ReportOutcome, error) {
	rec, ok, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return ReportOutcome{}, err
	}
	if !ok {
		return ReportOutcome{}, fmt.Errorf("%w: session %q", ErrNotFound, sessionID)
	}
	checks, err := s.store.ListSmokeChecksBySession(ctx, sessionID)
	if err != nil {
		return ReportOutcome{}, err
	}
	if len(checks) == 0 {
		return ReportOutcome{}, fmt.Errorf("%w: no checklist to report", ErrInvalid)
	}
	summary := composeSummary(sessionID, checks)
	outcome := s.deliver(ctx, rec, summary)
	now := s.now()
	if _, err := s.store.MarkSmokeReported(ctx, sessionID, now, now); err != nil {
		return ReportOutcome{}, err
	}
	outcome.Summary = summary
	return outcome, nil
}

// deliver picks the report target: a live worker gets it directly; otherwise an
// active orchestrator for the worker's project gets a wrapped copy; otherwise
// the results stay persisted (surfaced by `ao smoke list`).
func (s *Service) deliver(ctx context.Context, worker domain.SessionRecord, summary string) ReportOutcome {
	if s.messenger != nil && !worker.IsTerminated {
		if out, err := s.messenger.Send(ctx, worker.ID, "[smoke results]\n\n"+summary); err == nil {
			return ReportOutcome{Delivered: !out.Queued, Queued: out.Queued, Target: "worker"}
		}
	}
	if s.messenger != nil {
		if orch, ok := s.activeOrchestrator(ctx, worker.ProjectID); ok {
			wrapped := fmt.Sprintf("[smoke results for @%s]\n\n%s", worker.ID, summary)
			if out, err := s.messenger.Send(ctx, orch, wrapped); err == nil {
				return ReportOutcome{Delivered: !out.Queued, Queued: out.Queued, Target: "orchestrator"}
			}
		}
	}
	return ReportOutcome{Delivered: false, Target: "persisted"}
}

func (s *Service) activeOrchestrator(ctx context.Context, projectID domain.ProjectID) (domain.SessionID, bool) {
	recs, err := s.store.ListSessions(ctx, projectID)
	if err != nil {
		return "", false
	}
	for _, rec := range recs {
		if rec.Kind == domain.KindOrchestrator && !rec.IsTerminated {
			return rec.ID, true
		}
	}
	return "", false
}

// PurgeSessionEvidence hard-deletes a session's on-disk evidence tree. Wired
// into the session manager's PurgeSession (like the reviewer reaper) so a
// deleted session leaves no blobs behind; the DB rows cascade separately.
func (s *Service) PurgeSessionEvidence(_ context.Context, sessionID domain.SessionID) error {
	if sessionID == "" {
		return nil
	}
	return os.RemoveAll(s.sessionDir(sessionID))
}

// --- helpers ---------------------------------------------------------------

func (s *Service) now() time.Time { return s.clock() }

func (s *Service) sessionDir(sessionID domain.SessionID) string {
	return filepath.Join(s.evidenceRoot, string(sessionID))
}

func (s *Service) checkDir(sessionID domain.SessionID, checkID string) string {
	return filepath.Join(s.evidenceRoot, string(sessionID), checkID)
}

// openExportSubdir holds the human-named, extensioned copies materialized for
// Reveal/Open. Kept inside the case's evidence dir so os.RemoveAll on
// session-purge / reset / re-author sweeps it away with the rest.
const openExportSubdir = "_open"

// openExportPath is the confined absolute path of an evidence item's export copy
// (<check>/_open/<case>-<file>.<ext>). ok=false when the derived path would
// escape the evidence root.
func (s *Service) openExportPath(sessionID domain.SessionID, checkID, evidenceID, filename, mimeType string) (string, bool) {
	rel := filepath.Join(string(sessionID), checkID, openExportSubdir, exportBaseName(checkID, evidenceID, filename, mimeType))
	return preview.ConfinedPath(s.evidenceRoot, rel)
}

// mimeExtensions maps each accepted evidence MIME (see evidenceKinds) to the
// extension that makes the exported copy open by content type. The MIME is the
// authority — the stored display filename may be wrong or absent.
var mimeExtensions = map[string]string{
	"image/png":       ".png",
	"image/jpeg":      ".jpg",
	"image/gif":       ".gif",
	"image/webp":      ".webp",
	"video/mp4":       ".mp4",
	"video/webm":      ".webm",
	"video/quicktime": ".mov",
}

// extensionForMime returns the file extension (with dot) for an evidence MIME,
// falling back to the stdlib map then ".bin" for anything unexpected.
func extensionForMime(mimeType string) string {
	if ext, ok := mimeExtensions[normalizeMime(mimeType)]; ok {
		return ext
	}
	if exts, err := mime.ExtensionsByType(mimeType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ".bin"
}

// exportBaseName builds a human-readable, filesystem-safe basename for an export
// copy: "<case>-<file-stem-or-id><ext>", the extension derived from the MIME.
func exportBaseName(checkID, evidenceID, filename, mimeType string) string {
	ext := extensionForMime(mimeType)
	stem := ""
	if filename != "" {
		stem = strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	}
	if stem == "" {
		stem = evidenceID
	}
	return sanitizeExportName(checkID + "-" + stem + ext)
}

// sanitizeExportName reduces a name to a single safe path component: no
// separators or control chars, no leading dots, length-capped while keeping the
// extension.
func sanitizeExportName(name string) string {
	name = filepath.Base(filepath.FromSlash(name))
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == filepath.Separator || r < 0x20 {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimLeft(name, ".")
	if name == "" || name == string(filepath.Separator) {
		return "evidence"
	}
	if len(name) > 200 {
		ext := filepath.Ext(name)
		if len(ext) < 200 {
			name = name[:200-len(ext)] + ext
		} else {
			name = name[:200]
		}
	}
	return name
}

// copyFileIfStale writes src to dst unless an up-to-date same-size copy already
// exists, via a temp file + rename so a concurrent reveal never sees a partial
// file. Parent dirs are created as needed.
func copyFileIfStale(src, dst string) error {
	sfi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if dfi, err := os.Stat(dst); err == nil && dfi.Size() == sfi.Size() && !dfi.ModTime().Before(sfi.ModTime()) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// requireActiveCheck resolves a case that belongs to the session and is not
// retired. Every write goes through it, so "a retired case is frozen" is one
// rule in one place rather than a condition each verb has to remember.
func (s *Service) requireActiveCheck(ctx context.Context, sessionID domain.SessionID, checkID string) (domain.SmokeCheck, error) {
	if sessionID == "" || checkID == "" {
		return domain.SmokeCheck{}, fmt.Errorf("%w: session id and check id are required", ErrInvalid)
	}
	check, ok, err := s.store.GetSmokeCheck(ctx, checkID)
	if err != nil {
		return domain.SmokeCheck{}, err
	}
	if !ok || check.SessionID != sessionID {
		return domain.SmokeCheck{}, fmt.Errorf("%w: smoke check %q", ErrNotFound, checkID)
	}
	if check.Retired() {
		return domain.SmokeCheck{}, retiredError(check)
	}
	return check, nil
}

// retiredError names when a case was retired and why, so the caller reads the
// decision rather than a bare refusal.
func retiredError(check domain.SmokeCheck) error {
	when := ""
	if check.RetiredAt != nil {
		when = " on " + check.RetiredAt.Format(time.RFC3339)
	}
	return fmt.Errorf("%w: %q (id %q) was retired%s: %s. A retired case is frozen - its name, steps and the results the user recorded are kept as the trace of why it went. If it needs to come back, add it under a NEW id",
		ErrCaseRetired, check.Name, check.ID, when, check.RetiredReason)
}

// removeEvidenceFiles deletes one evidence item's stored blob and its exported
// copy, best-effort. Both paths are derived from the item's OWN session/check,
// so it can never reach another case's files.
func (s *Service) removeEvidenceFiles(ev domain.SmokeEvidence) {
	rel := filepath.Join(string(ev.SessionID), ev.CheckID, ev.ID)
	if path, ok := preview.ConfinedPath(s.evidenceRoot, rel); ok {
		_ = os.Remove(path)
	}
	if dst, ok := s.openExportPath(ev.SessionID, ev.CheckID, ev.ID, ev.Filename, ev.Mime); ok {
		_ = os.Remove(dst)
	}
}

// evidenceSource normalizes an upload's declared provenance. Anything that is
// not explicitly the agent is the user: the user is the safe default because a
// human artifact mislabelled as a machine's would quietly leave the human's
// evidence list, while the reverse only adds a file to a list a person reads.
func evidenceSource(src domain.SmokeEvidenceSource) domain.SmokeEvidenceSource {
	if src == domain.SmokeEvidenceAgent {
		return domain.SmokeEvidenceAgent
	}
	return domain.SmokeEvidenceUser
}

func (s *Service) getCheck(ctx context.Context, checkID string) (domain.SmokeCheck, error) {
	check, ok, err := s.store.GetSmokeCheck(ctx, checkID)
	if err != nil {
		return domain.SmokeCheck{}, err
	}
	if !ok {
		return domain.SmokeCheck{}, fmt.Errorf("%w: smoke check %q", ErrNotFound, checkID)
	}
	return check, nil
}

func (s *Service) workerLabel(ctx context.Context, sessionID domain.SessionID) string {
	rec, ok, err := s.store.GetSession(ctx, sessionID)
	if err != nil || !ok {
		return string(sessionID)
	}
	if rec.DisplayName != "" {
		return rec.DisplayName
	}
	return string(sessionID)
}

// resolveCases assigns 1-based seq from position and a stable id for each case
// (the worker-supplied id when present, else derived from the name, deduped
// within the payload and against ids other sessions already hold). Every case
// must carry a non-empty name.
func resolveCases(sessionID domain.SessionID, cases []domain.SmokeAuthoredCase, ownedElsewhere func(string) (bool, error)) ([]domain.SmokeAuthoredCase, error) {
	out := make([]domain.SmokeAuthoredCase, 0, len(cases))
	used := make(map[string]struct{}, len(cases))
	for i, c := range cases {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			return nil, fmt.Errorf("%w: case %d is missing a name", ErrInvalid, i+1)
		}
		base := slugify(strings.TrimSpace(c.ID))
		if base == "" {
			base = derivedCaseID(name)
		}
		id, err := resolveID(base, sessionID, used, ownedElsewhere)
		if err != nil {
			return nil, err
		}
		used[id] = struct{}{}
		out = append(out, domain.SmokeAuthoredCase{
			ID:       id,
			Seq:      i + 1,
			Name:     name,
			Why:      strings.TrimSpace(c.Why),
			Steps:    trimSteps(c.Steps),
			Expected: strings.TrimSpace(c.Expected),
			PRNum:    c.PRNum,
			FileRef:  strings.TrimSpace(c.FileRef),
		})
	}
	return out, nil
}

// checkRetiredInPayload refuses a payload that names a retired case's id. It
// runs on the RESOLVED ids, so it also catches the common accident: a case whose
// name still derives the id of one that was retired.
func checkRetiredInPayload(existing []domain.SmokeCheck, resolved []domain.SmokeAuthoredCase) error {
	retired := make(map[string]domain.SmokeCheck, len(existing))
	for _, c := range existing {
		if c.Retired() {
			retired[c.ID] = c
		}
	}
	if len(retired) == 0 {
		return nil
	}
	for _, c := range resolved {
		if was, ok := retired[c.ID]; ok {
			return retiredError(was)
		}
	}
	return nil
}

// checkResultsAtRisk refuses a re-author that would delete played cases. It runs
// on the RESOLVED ids, so it sees exactly what ReplaceSmokeChecks would drop.
// Retired cases are skipped: ReplaceSmokeChecks does not drop them, so their
// absence from the payload destroys nothing - that is precisely what retiring
// one buys, and it is the way OUT of this refusal for a case that must go.
func checkResultsAtRisk(existing []domain.SmokeCheck, resolved []domain.SmokeAuthoredCase) error {
	keep := make(map[string]struct{}, len(resolved))
	for _, c := range resolved {
		keep[c.ID] = struct{}{}
	}
	var atRisk []domain.SmokeCheck
	for _, c := range existing {
		if _, ok := keep[c.ID]; ok {
			continue
		}
		if c.Retired() {
			continue
		}
		if played(c) {
			atRisk = append(atRisk, c)
		}
	}
	if len(atRisk) == 0 {
		return nil
	}
	sort.Slice(atRisk, func(i, j int) bool {
		if atRisk[i].Seq != atRisk[j].Seq {
			return atRisk[i].Seq < atRisk[j].Seq
		}
		return atRisk[i].ID < atRisk[j].ID
	})
	return resultsAtRiskError(atRisk)
}

// played reports whether a case carries work only the user can produce: a
// verdict they recorded, a note they wrote, or evidence they attached.
func played(c domain.SmokeCheck) bool {
	return (c.Verdict != "" && c.Verdict != domain.SmokePending) ||
		strings.TrimSpace(c.Note) != "" ||
		len(c.Evidence) > 0
}

// resultsAtRiskError names every case that would have been destroyed and both
// ways out, so the caller can fix the payload instead of guessing.
func resultsAtRiskError(atRisk []domain.SmokeCheck) error {
	listed := make([]string, 0, len(atRisk))
	for _, c := range atRisk {
		listed = append(listed, fmt.Sprintf("%q (id %q, %s)", c.Name, c.ID, playedSummary(c)))
	}
	subject := fmt.Sprintf("%d cases the user already played are", len(atRisk))
	if len(atRisk) == 1 {
		subject = "1 case the user already played is"
	}
	return fmt.Errorf("%w: %s missing from the payload: %s. A case id is derived from its name, so rewording a name drops the old case: re-send each one under the id it already has (e.g. add \"id\": \"%s\" to the case that replaces it); or retire it, which keeps the results and the reason it went (`ao smoke retire <session> --case %s --reason \"…\"`), and then it may be left out; or ask the user to Reset the case in the Tests tab before dropping it",
		ErrResultsAtRisk, subject, strings.Join(listed, "; "), atRisk[0].ID, atRisk[0].ID)
}

// removeAtRiskError refuses an EXPLICIT removal of a played case and points at
// retire, which keeps what the user recorded and the reason the case went. The
// omission path (checkResultsAtRisk) has its own wording because its fix is
// usually to re-send the case under the id it already has; here the caller meant
// to remove it, so there is exactly one way forward.
func removeAtRiskError(c domain.SmokeCheck) error {
	return fmt.Errorf("%w: the user already played %q (id %q, %s), so it is retired rather than deleted - that keeps their verdict, note and evidence, and records why the case went: `ao smoke retire <session> --case %s --reason \"…\"`. If it should come off the list with nothing kept, ask the user to Reset it in the Tests tab first",
		ErrResultsAtRisk, c.Name, c.ID, playedSummary(c), c.ID)
}

// playedSummary describes what the user recorded on a case, for the refusal.
func playedSummary(c domain.SmokeCheck) string {
	var parts []string
	if c.Verdict != "" && c.Verdict != domain.SmokePending {
		parts = append(parts, "verdict "+string(c.Verdict))
	}
	if strings.TrimSpace(c.Note) != "" {
		parts = append(parts, "a note")
	}
	switch n := len(c.Evidence); {
	case n == 1:
		parts = append(parts, "1 evidence file")
	case n > 1:
		parts = append(parts, fmt.Sprintf("%d evidence files", n))
	}
	return strings.Join(parts, ", ")
}

// resolveID picks an id this checklist has not used and no OTHER session owns.
// smoke_check.id is a global primary key, so an id another session holds would
// fail the insert (that is the crash this guards). The alternative appends a
// hash of the session id rather than a counter, so it stays deterministic: it
// does not drift when the other session's checklist changes, and a re-author
// lands on the same id again.
func resolveID(base string, sessionID domain.SessionID, used map[string]struct{}, ownedElsewhere func(string) (bool, error)) (string, error) {
	for _, candidateBase := range []string{base, withSuffix(base, shortHash(string(sessionID)))} {
		candidate := dedupeID(candidateBase, used)
		owned, err := ownedElsewhere(candidate)
		if err != nil {
			return "", err
		}
		if !owned {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w: could not derive a free id for case %q; supply a distinct \"id\" for it", ErrInvalid, base)
}

func dedupeID(id string, used map[string]struct{}) string {
	if _, ok := used[id]; !ok {
		return id
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", id, n)
		if _, ok := used[candidate]; !ok {
			return candidate
		}
	}
}

// maxCaseIDLen bounds a derived id, matching the slug cap.
const maxCaseIDLen = 64

// derivedCaseID turns a case name into its stable id. A name carrying ASCII
// alphanumerics slugs exactly as it always has, so ids already stored never
// shift. A name that slugs to nothing (Thai, CJK, punctuation-only) falls back
// to a hash of the name: still deterministic, so a re-author reproduces the id
// and the user's verdict, note and evidence stay attached.
func derivedCaseID(name string) string {
	if s := slugify(name); s != "" {
		return s
	}
	return "case-" + shortHash(name)
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}

// withSuffix appends -<suffix>, trimming the base to stay within the id cap.
func withSuffix(base, suffix string) string {
	if len(base)+1+len(suffix) > maxCaseIDLen {
		base = strings.Trim(base[:maxCaseIDLen-1-len(suffix)], "-")
	}
	return base + "-" + suffix
}

var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugNonAlnum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 64 {
		s = strings.Trim(s[:64], "-")
	}
	return s
}

func trimSteps(steps []string) []string {
	out := make([]string, 0, len(steps))
	for _, step := range steps {
		if t := strings.TrimSpace(step); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func reportedAt(checks []domain.SmokeCheck) *time.Time {
	var latest *time.Time
	for i := range checks {
		if checks[i].ReportedAt == nil {
			continue
		}
		if latest == nil || checks[i].ReportedAt.After(*latest) {
			latest = checks[i].ReportedAt
		}
	}
	return latest
}

func normalizeMime(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if mt, _, err := mime.ParseMediaType(raw); err == nil {
		return strings.ToLower(mt)
	}
	return strings.ToLower(strings.TrimSpace(strings.SplitN(raw, ";", 2)[0]))
}

// sanitizeFilename keeps only the base name for display, dropping any path.
func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = filepath.Base(filepath.FromSlash(name))
	if name == "." || name == string(filepath.Separator) {
		return ""
	}
	if len(name) > 200 {
		name = name[:200]
	}
	return name
}

// writeCapped streams r into path, failing with ErrInvalid if more than limit
// bytes arrive. Returns the number of bytes written.
func writeCapped(path string, r io.Reader, limit int64) (int64, error) {
	f, err := os.Create(path)
	if err != nil {
		return 0, fmt.Errorf("create evidence file: %w", err)
	}
	defer func() { _ = f.Close() }()
	limited := io.LimitReader(r, limit+1)
	n, err := io.Copy(f, limited)
	if err != nil {
		return 0, fmt.Errorf("write evidence: %w", err)
	}
	if n > limit {
		return 0, fmt.Errorf("%w: evidence exceeds the %d MB limit", ErrInvalid, limit>>20)
	}
	return n, nil
}

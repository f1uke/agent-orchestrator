// Package smoke is the daemon's HTTP-facing service for worker-authored manual
// smoke-test checklists. It mirrors the Reviews data path in shape (Manager +
// Store interfaces, New + options, sentinel errors mapped to 422/404 by the
// controller) but is plain per-session CRUD plus evidence-blob handling and a
// report-back over the same channel `ao send` uses — it spawns nothing.
package smoke

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/preview"
)

// ErrInvalid and ErrNotFound are the service sentinels the HTTP controller maps
// to 422 and 404 respectively.
var (
	ErrInvalid  = errors.New("smoke: invalid request")
	ErrNotFound = errors.New("smoke: not found")
)

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
	Author(ctx context.Context, sessionID domain.SessionID, cases []domain.SmokeAuthoredCase) (SessionSmoke, error)
	SetVerdict(ctx context.Context, sessionID domain.SessionID, checkID string, verdict domain.SmokeVerdict, note string) (domain.SmokeCheck, error)
	Reset(ctx context.Context, sessionID domain.SessionID, checkID string) (domain.SmokeCheck, error)
	AttachEvidence(ctx context.Context, sessionID domain.SessionID, checkID string, upload EvidenceUpload) (domain.SmokeEvidence, error)
	OpenEvidence(ctx context.Context, sessionID domain.SessionID, checkID, evidenceID string) (EvidenceBlob, error)
	Report(ctx context.Context, sessionID domain.SessionID) (ReportOutcome, error)
	PurgeSessionEvidence(ctx context.Context, sessionID domain.SessionID) error
}

// Store is the persistence surface the service owns. The concrete
// *sqlite.Store satisfies it, including the two session-read methods used for
// report-back liveness/orchestrator lookup.
type Store interface {
	ListSmokeChecksBySession(ctx context.Context, id domain.SessionID) ([]domain.SmokeCheck, error)
	GetSmokeCheck(ctx context.Context, id string) (domain.SmokeCheck, bool, error)
	ReplaceSmokeChecks(ctx context.Context, sessionID domain.SessionID, projectID domain.ProjectID, cases []domain.SmokeAuthoredCase, now time.Time) ([]domain.SmokeCheck, []string, error)
	SetSmokeVerdict(ctx context.Context, id string, verdict domain.SmokeVerdict, note string, decidedAt, now time.Time) (bool, error)
	ResetSmokeCheck(ctx context.Context, id string, now time.Time) (bool, error)
	MarkSmokeReported(ctx context.Context, id domain.SessionID, reportedAt, now time.Time) (int64, error)
	InsertSmokeEvidence(ctx context.Context, ev domain.SmokeEvidence) error
	GetSmokeEvidence(ctx context.Context, id string) (domain.SmokeEvidence, bool, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	ListSessions(ctx context.Context, projectID domain.ProjectID) ([]domain.SessionRecord, error)
}

// Messenger delivers a report-back message over the same path `ao send` uses
// (session manager Send). *sessionsvc.Service satisfies it.
type Messenger interface {
	Send(ctx context.Context, id domain.SessionID, message string) error
}

// SessionSmoke is the list read model: the worker label (drives the tab
// subtitle), the whole checklist, and when its results were last reported.
type SessionSmoke struct {
	Worker     string              `json:"worker"`
	ReportedAt *time.Time          `json:"reportedAt,omitempty"`
	Checks     []domain.SmokeCheck `json:"checks"`
}

// EvidenceUpload is one attach request: the declared MIME + original filename
// and a reader over the bytes. The service streams the reader to disk under a
// per-kind size cap; it never trusts the filename for the on-disk path.
type EvidenceUpload struct {
	Filename string
	Mime     string
	Reader   io.Reader
}

// EvidenceBlob is what the controller needs to serve a stored blob.
type EvidenceBlob struct {
	Path     string
	Mime     string
	Filename string
}

// ReportOutcome describes where a report-back landed.
type ReportOutcome struct {
	Delivered bool   `json:"delivered"`
	Target    string `json:"target"` // "worker" | "orchestrator" | "persisted"
	Summary   string `json:"summary"`
}

// Service is the API-facing smoke service.
type Service struct {
	store        Store
	messenger    Messenger
	evidenceRoot string
	clock        func() time.Time
}

var _ Manager = (*Service)(nil)

// Option customizes the service.
type Option func(*Service)

// WithClock overrides the service clock for tests.
func WithClock(clock func() time.Time) Option {
	return func(s *Service) { s.clock = clock }
}

// New builds the smoke service. dataDir is the resolved AO data dir; evidence
// blobs live under <dataDir>/evidence (all under ~/.ao). messenger may be nil
// (report-back then degrades to persist-only).
func New(store Store, dataDir string, messenger Messenger, opts ...Option) *Service {
	s := &Service{
		store:        store,
		messenger:    messenger,
		evidenceRoot: filepath.Join(dataDir, "evidence"),
		clock:        func() time.Time { return time.Now().UTC() },
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
	return SessionSmoke{Worker: worker, ReportedAt: reportedAt(checks), Checks: checks}, nil
}

// Author registers or replaces a session's whole checklist. Cases matched by
// stable id keep their verdict/note/evidence (see ReplaceSmokeChecks); ids
// absent from the payload are removed and their evidence blobs deleted.
func (s *Service) Author(ctx context.Context, sessionID domain.SessionID, cases []domain.SmokeAuthoredCase) (SessionSmoke, error) {
	if sessionID == "" {
		return SessionSmoke{}, fmt.Errorf("%w: session id is required", ErrInvalid)
	}
	if len(cases) == 0 {
		return SessionSmoke{}, fmt.Errorf("%w: at least one case is required", ErrInvalid)
	}
	if len(cases) > 50 {
		return SessionSmoke{}, fmt.Errorf("%w: a checklist may have at most 50 cases", ErrInvalid)
	}
	rec, ok, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return SessionSmoke{}, err
	}
	if !ok {
		return SessionSmoke{}, fmt.Errorf("%w: session %q", ErrNotFound, sessionID)
	}
	resolved, err := resolveCases(cases)
	if err != nil {
		return SessionSmoke{}, err
	}
	_, removed, err := s.store.ReplaceSmokeChecks(ctx, sessionID, rec.ProjectID, resolved, s.now())
	if err != nil {
		return SessionSmoke{}, err
	}
	for _, checkID := range removed {
		_ = os.RemoveAll(s.checkDir(sessionID, checkID))
	}
	return s.List(ctx, sessionID)
}

// SetVerdict records the user's verdict + note for a case.
func (s *Service) SetVerdict(ctx context.Context, sessionID domain.SessionID, checkID string, verdict domain.SmokeVerdict, note string) (domain.SmokeCheck, error) {
	if !verdict.Valid() {
		return domain.SmokeCheck{}, fmt.Errorf("%w: verdict must be pass, fail, or skip", ErrInvalid)
	}
	if err := s.requireCheck(ctx, sessionID, checkID); err != nil {
		return domain.SmokeCheck{}, err
	}
	now := s.now()
	updated, err := s.store.SetSmokeVerdict(ctx, checkID, verdict, note, now, now)
	if err != nil {
		return domain.SmokeCheck{}, err
	}
	if !updated {
		return domain.SmokeCheck{}, fmt.Errorf("%w: smoke check %q", ErrNotFound, checkID)
	}
	return s.getCheck(ctx, checkID)
}

// Reset clears a case's verdict/note and deletes its evidence (rows + blobs).
func (s *Service) Reset(ctx context.Context, sessionID domain.SessionID, checkID string) (domain.SmokeCheck, error) {
	if err := s.requireCheck(ctx, sessionID, checkID); err != nil {
		return domain.SmokeCheck{}, err
	}
	// Remove blobs before clearing rows; either order is safe since the bytes
	// are not in the DB.
	_ = os.RemoveAll(s.checkDir(sessionID, checkID))
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
func (s *Service) AttachEvidence(ctx context.Context, sessionID domain.SessionID, checkID string, upload EvidenceUpload) (domain.SmokeEvidence, error) {
	if err := s.requireCheck(ctx, sessionID, checkID); err != nil {
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
	}
	if err := s.store.InsertSmokeEvidence(ctx, ev); err != nil {
		_ = os.Remove(path)
		return domain.SmokeEvidence{}, err
	}
	return ev, nil
}

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
		if err := s.messenger.Send(ctx, worker.ID, "[smoke results]\n\n"+summary); err == nil {
			return ReportOutcome{Delivered: true, Target: "worker"}
		}
	}
	if s.messenger != nil {
		if orch, ok := s.activeOrchestrator(ctx, worker.ProjectID); ok {
			wrapped := fmt.Sprintf("[smoke results for @%s]\n\n%s", worker.ID, summary)
			if err := s.messenger.Send(ctx, orch, wrapped); err == nil {
				return ReportOutcome{Delivered: true, Target: "orchestrator"}
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

// requireCheck verifies a case exists and belongs to the session.
func (s *Service) requireCheck(ctx context.Context, sessionID domain.SessionID, checkID string) error {
	if sessionID == "" || checkID == "" {
		return fmt.Errorf("%w: session id and check id are required", ErrInvalid)
	}
	check, ok, err := s.store.GetSmokeCheck(ctx, checkID)
	if err != nil {
		return err
	}
	if !ok || check.SessionID != sessionID {
		return fmt.Errorf("%w: smoke check %q", ErrNotFound, checkID)
	}
	return nil
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
// (the worker-supplied id when present, else a slug of the name, deduped within
// the payload). Every case must carry a non-empty name.
func resolveCases(cases []domain.SmokeAuthoredCase) ([]domain.SmokeAuthoredCase, error) {
	out := make([]domain.SmokeAuthoredCase, 0, len(cases))
	used := make(map[string]struct{}, len(cases))
	for i, c := range cases {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			return nil, fmt.Errorf("%w: case %d is missing a name", ErrInvalid, i+1)
		}
		id := strings.TrimSpace(c.ID)
		if id == "" {
			id = slugify(name)
		} else {
			id = slugify(id)
		}
		if id == "" {
			id = "case"
		}
		id = dedupeID(id, used)
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

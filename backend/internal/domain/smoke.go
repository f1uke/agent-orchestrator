package domain

import "time"

// SmokeVerdict is the outcome the user records while playing a smoke-test case
// live in the app. The default is SmokePending until the user decides.
type SmokeVerdict string

// Smoke verdicts.
const (
	SmokePending SmokeVerdict = "pending"
	SmokePass    SmokeVerdict = "pass"
	SmokeFail    SmokeVerdict = "fail"
	SmokeSkip    SmokeVerdict = "skip"
)

// Valid reports whether v is a verdict the user may set (pending is a stored
// default, not a settable outcome).
func (v SmokeVerdict) Valid() bool {
	return v == SmokePass || v == SmokeFail || v == SmokeSkip
}

// SmokeCheck is one manual verification case a worker authored for a session.
// Author-provided fields (Name..FileRef, plus Seq derived from position) come
// from `ao smoke set`; the user-runtime fields (Verdict/Note/Evidence/DecidedAt)
// are filled while the user plays the case in the Tests tab. One row per case,
// keyed to the worker session (mirrors the per-session Review record).
type SmokeCheck struct {
	ID        string          `json:"id"`
	SessionID SessionID       `json:"sessionId"`
	ProjectID ProjectID       `json:"projectId"`
	Seq       int             `json:"seq"` // 1-based; drives "CHECK N"
	Name      string          `json:"name"`
	Why       string          `json:"why"`
	Steps     []string        `json:"steps"` // stored as a JSON text column
	Expected  string          `json:"expected"`
	PRNum     int             `json:"prNum"`
	FileRef   string          `json:"fileRef"`
	Verdict   SmokeVerdict    `json:"verdict"`
	Note      string          `json:"note"`
	Evidence  []SmokeEvidence `json:"evidence"`
	DecidedAt *time.Time      `json:"decidedAt,omitempty"`
	// ReportedAt marks when this session's checklist results were reported back
	// to the worker (stamped across all of the session's rows on report). nil
	// until the first report.
	ReportedAt *time.Time `json:"reportedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

// SmokeAuthoredCase is the worker-authored subset of a case, supplied by
// `ao smoke set`. Seq is assigned from payload position (1-based) and ID is
// resolved (derived from Name when the worker omits it) before it reaches the
// store; the user-runtime fields (verdict/note/evidence) are never author-set.
type SmokeAuthoredCase struct {
	ID       string
	Seq      int
	Name     string
	Why      string
	Steps    []string
	Expected string
	PRNum    int
	FileRef  string
}

// SmokeEvidence is one screenshot or short clip the user attached to a case
// while playing it. The bytes live on disk under <dataDir>/evidence; this row
// holds only the metadata + reference.
type SmokeEvidence struct {
	ID        string    `json:"id"`
	CheckID   string    `json:"checkId"`
	SessionID SessionID `json:"sessionId"`
	Kind      string    `json:"kind"`     // "image" | "video"
	Filename  string    `json:"filename"` // original name (display only)
	Mime      string    `json:"mime"`
	SizeBytes int64     `json:"sizeBytes"`
	CreatedAt time.Time `json:"createdAt"`
}

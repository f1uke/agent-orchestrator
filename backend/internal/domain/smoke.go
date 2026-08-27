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

// Valid reports whether v is a verdict that may be set (pending is a stored
// default, not a settable outcome). Shared by the user and agent paths: the
// vocabulary is the same, the fields it is written into are not.
func (v SmokeVerdict) Valid() bool {
	return v == SmokePass || v == SmokeFail || v == SmokeSkip
}

// SmokeEvidenceSource says who produced one evidence file. It is the row-level
// half of evidence provenance (the API-level half is the two separate lists on
// SmokeCheck): an evidence list where machine and human artifacts are
// indistinguishable destroys the value of the list, and evidence is exactly what
// you go back to when you distrust a verdict.
type SmokeEvidenceSource string

// Evidence sources.
const (
	SmokeEvidenceUser  SmokeEvidenceSource = "user"
	SmokeEvidenceAgent SmokeEvidenceSource = "agent"
)

// SmokeCheck is one manual verification case a worker authored for a session.
// Author-provided fields (Name..FileRef, plus Seq derived from position) come
// from `ao smoke set`; the user-runtime fields (Verdict/Note/Evidence/DecidedAt)
// are filled while the user plays the case in the Tests tab. One row per case,
// keyed to the worker session (mirrors the per-session Review record).
//
// # Two results, never one
//
// A case carries a SECOND, independent result - the machine's
// (AgentVerdict/AgentNote/AgentEvidence/AgentRanAt/AgentSHA, written only by
// `ao smoke record`). The two are never merged because they answer different
// questions: a machine answers "did the steps run", a human answers "does this
// actually work for a person". Recording latency, dead drag-scroll, keystrokes
// never arriving, a tab pausing when unfocused, control lost after a lease lapse
// - every regression a person has caught by hand lives in the gap between those
// two questions. Merging the fields would declare the questions equivalent and
// would let a card read green with nobody having touched it.
//
// An agent result therefore never substitutes for the human's: it may move a
// label or a tone, and it never opens the merge gate (see readiness.ts).
type SmokeCheck struct {
	ID        string    `json:"id"`
	SessionID SessionID `json:"sessionId"`
	ProjectID ProjectID `json:"projectId"`
	Seq       int       `json:"seq"` // 1-based; drives "CHECK N"
	Name      string    `json:"name"`
	Why       string    `json:"why"`
	Steps     []string  `json:"steps"` // stored as a JSON text column
	Expected  string    `json:"expected"`
	PRNum     int       `json:"prNum"`
	FileRef   string    `json:"fileRef"`
	// Verdict/Note/Evidence/DecidedAt are the USER's, recorded while playing the
	// case in the Tests tab. Never author-set, and never agent-set.
	Verdict   SmokeVerdict    `json:"verdict"`
	Note      string          `json:"note"`
	Evidence  []SmokeEvidence `json:"evidence"`
	DecidedAt *time.Time      `json:"decidedAt,omitempty"`
	// AgentVerdict..AgentSHA are the MACHINE's, written only by `ao smoke record`.
	// AgentVerdict is "" (not "pending") when nothing has judged the case: the
	// user's default means "not decided yet", while "" here also covers "no
	// machine ever will" - a paint/focus/timing/feel case is the human's alone.
	// AgentRanAt set with an empty AgentVerdict is the evidence-only state: the
	// machine drove the app and captured what it saw without judging it.
	AgentVerdict  SmokeVerdict    `json:"agentVerdict,omitempty"`
	AgentNote     string          `json:"agentNote,omitempty"`
	AgentEvidence []SmokeEvidence `json:"agentEvidence"`
	AgentRanAt    *time.Time      `json:"agentRanAt,omitempty"`
	// AgentSHA is the commit the machine ran the case against, so a recorded
	// result can be told apart from one that predates the current head. Compare
	// it with the PR's head SHA to decide whether the result has gone stale.
	AgentSHA string `json:"agentSha,omitempty"`
	// RetiredAt/RetiredReason freeze a case. A retired case stops being one the
	// user is asked to play without pretending it never existed: the row keeps
	// its name, its steps, the user's verdict/note and every evidence byte, and
	// records why it went. That is how a checklist shrinks auditably - "retired
	// 3, now covered by tests" is worth far more than three cases vanishing.
	RetiredAt     *time.Time `json:"retiredAt,omitempty"`
	RetiredReason string     `json:"retiredReason,omitempty"`
	// AuthoredBy/AuthoredByRole/AuthoredAt name the member who last wrote this
	// case's AUTHORED fields, and when. Both crew members author the task's
	// checklist - dev knows what the change touched, qa sees it as a user would -
	// so a shared list has to say which cases came from which of them.
	//
	// It records authorship; it does not police it. Nothing here prevents one
	// member overwriting another's case: what makes two authors safe is that the
	// write path is PER-CASE (add/edit/remove one), so an author only ever
	// touches the case they named. Attribution is what makes the result readable
	// afterwards.
	//
	// A caller AO cannot identify - the desktop app, a direct API call, an older
	// `ao` - leaves all three empty, and an unattributed case renders with no
	// author rather than a guessed one.
	AuthoredBy     SessionID  `json:"authoredBy,omitempty"`
	AuthoredByRole CrewRole   `json:"authoredByRole,omitempty"`
	AuthoredAt     *time.Time `json:"authoredAt,omitempty"`
	// ReportedAt marks when this session's checklist results were reported back
	// to the worker (stamped across all of the session's rows on report). nil
	// until the first report.
	ReportedAt *time.Time `json:"reportedAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
}

// Retired reports whether a case has been retired out of the active checklist.
func (c SmokeCheck) Retired() bool { return c.RetiredAt != nil }

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

// SmokeAgentResult is the machine-produced half of a case's result, supplied by
// `ao smoke record`. It is a separate input type from SmokeAuthoredCase for the
// same reason the columns are separate: the writer of one must never be able to
// reach the fields of the other. Verdict may be empty, which records "ran it,
// captured what I saw, did not judge it" - the evidence-only state.
type SmokeAgentResult struct {
	Verdict SmokeVerdict
	Note    string
	// SHA is the commit the case was run against; empty when the caller could
	// not resolve one. Never parsed, only compared.
	SHA string
}

// SmokeEvidence is one screenshot or short clip attached to a case - by the user
// while playing it, or by a machine while running it, told apart by Source. The
// bytes live on disk under <dataDir>/evidence; this row holds only the metadata
// + reference.
type SmokeEvidence struct {
	ID        string    `json:"id"`
	CheckID   string    `json:"checkId"`
	SessionID SessionID `json:"sessionId"`
	Kind      string    `json:"kind"`     // "image" | "video"
	Filename  string    `json:"filename"` // original name (display only)
	Mime      string    `json:"mime"`
	SizeBytes int64     `json:"sizeBytes"`
	CreatedAt time.Time `json:"createdAt"`
	// Source is who attached it: "user" (the Tests tab) or "agent"
	// (`ao smoke record --evidence`). Rows written before provenance existed
	// read "user", which is true - the Tests tab was the only writer.
	Source SmokeEvidenceSource `json:"source"`
}

// SmokeAuthor is who is making an authoring write: the calling session and the
// crew role it held at that moment. It is resolved from the caller's own session
// id (`ao` sends $AO_SESSION_ID), never from the target - both crew members
// author against the SAME target, because the checklist belongs to the task and
// $AO_CREW_ID is dev's id, so the target cannot say which of them is calling.
//
// The zero value means "AO could not identify the caller", which is a legitimate
// state and never an error: it is what the desktop app, a direct API call and an
// older `ao` all send.
type SmokeAuthor struct {
	ID   SessionID
	Role CrewRole
}

// SmokeCasePatch is a partial edit of ONE case's authored fields: a nil field is
// one the caller did not name and the stored value survives it.
//
// Partial is the point. With two authors, resending a whole case to change its
// fileRef would silently overwrite whatever the other member had improved about
// its prose in the meantime - so the narrow edit exists precisely so that the
// wide one is not the only way to fix one field.
type SmokeCasePatch struct {
	Name     *string
	Why      *string
	Steps    *[]string
	Expected *string
	PRNum    *int
	FileRef  *string
}

// Empty reports whether the patch names no field at all, which is a usage error
// rather than a no-op write: it means the caller asked for an edit and did not
// say what to edit.
func (p SmokeCasePatch) Empty() bool {
	return p.Name == nil && p.Why == nil && p.Steps == nil && p.Expected == nil && p.PRNum == nil && p.FileRef == nil
}

// SmokeStandDown is a member's recorded conclusion that this task's change needs
// NO human verification - "I looked, and there is nothing here for your eyes".
//
// It exists because an empty Tests tab otherwise says two different things at
// once: nobody has decided yet, or it was decided and there is nothing worth
// looking at. Those are opposite answers and they rendered identically, so the
// screen could not be read. A stand-down is stored beside the checklist (not on
// a case - it is a claim about the whole list) and is retracted the moment a
// case is authored, because a case on the list disproves it.
type SmokeStandDown struct {
	SessionID SessionID `json:"sessionId"`
	At        time.Time `json:"at"`
	By        SessionID `json:"by,omitempty"`
	ByRole    CrewRole  `json:"byRole,omitempty"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

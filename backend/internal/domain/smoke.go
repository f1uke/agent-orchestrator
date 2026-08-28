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
// A case carries a SECOND, independent result - the machine's: a run history
// (Runs, one row per `ao smoke record`) whose latest entry is surfaced as
// AgentVerdict/AgentNote/AgentRanAt/AgentSHA. The two are never merged because
// they answer different
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
	// AgreedRunID names the machine run the user CONFIRMED, when they reached
	// this verdict by agreeing with one instead of deriving it themselves. It is
	// a fact about the user's verdict, never a second author of it: the verdict,
	// the note and DecidedAt are written exactly as a hand-pressed Pass writes
	// them, no run row is created and nothing in the machine's lane is touched.
	// A case counts as played because a PERSON acted, agreement or not - which is
	// what keeps "N of M verified" meaning "a person looked".
	//
	// It names a RUN rather than "qa" because a case can have failed at one
	// commit and passed at another (Runs): "agreed with qa" is ambiguous the
	// moment two runs disagree. Empty means the user reached the verdict on their
	// own, which is what every verdict before this field did.
	AgreedRunID string `json:"agreedRunId,omitempty"`
	// Runs is the machine's history on this case, oldest first: one row per
	// `ao smoke record`, each with its own verdict, note, commit and evidence.
	// It is a list because it used to be four columns, and four columns meant
	// every re-run destroyed the result before it - so "this used to fail and now
	// passes" could only ever be reconstructed from prose in a note.
	Runs []SmokeRun `json:"runs"`
	// AgentVerdict..AgentSHA are the MACHINE's, DERIVED from the latest recorded
	// run (LatestRun) rather than stored. They are the answer to "what does the
	// machine say about this case NOW", which is what the gate, the progress
	// counts and the collapsed chip all ask; the history behind that answer is
	// Runs.
	//
	// AgentVerdict is "" (not "pending") when nothing has judged the case: the
	// user's default means "not decided yet", while "" here also covers "no
	// machine ever will" - a case whose evidence cannot answer the question is
	// the human's alone. AgentRanAt set with an empty AgentVerdict is the
	// evidence-only state: the machine drove the app and captured what it saw
	// without concluding.
	AgentVerdict SmokeVerdict `json:"agentVerdict,omitempty"`
	AgentNote    string       `json:"agentNote,omitempty"`
	// AgentEvidence is every machine artifact on the case, across all runs and
	// including any that belongs to no run. It stays FLAT because the report and
	// the Jira post need it flat; which run captured a file is on the file
	// (SmokeEvidence.RunID), so the two views never duplicate a row.
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

// LatestRun returns the most recent RECORDED run - the case's current machine
// result - and ok=false when no machine has concluded anything. A run still open
// (captured, not yet concluded) is deliberately skipped: it is not a result, and
// treating it as one would let a half-finished round replace a real verdict.
func (c SmokeCheck) LatestRun() (SmokeRun, bool) {
	for i := len(c.Runs) - 1; i >= 0; i-- {
		if c.Runs[i].Recorded() {
			return c.Runs[i], true
		}
	}
	return SmokeRun{}, false
}

// MachineDrove reports whether a machine has RECORDED anything against this case:
// a verdict, or an evidence-only round. It is the whole of "driven" - the state a
// case has to be in, or be explicitly excused from, before qa hands the task back.
//
// It reads the run history rather than AgentVerdict because an evidence-only
// record has no verdict and is a complete answer (see SmokeAgentResult).
func (c SmokeCheck) MachineDrove() bool {
	_, ok := c.LatestRun()
	return ok
}

// AwaitsMachine reports whether this case is still qa's to drive: on the active
// list, not yet decided by the person, and carrying nothing from any machine.
//
// A case the HUMAN has already played is deliberately excluded. It has its answer
// from the only judge that outranks the machine, so asking qa to drive it again
// would make every second handback nag about work that is finished.
func (c SmokeCheck) AwaitsMachine() bool {
	return !c.Retired() && c.Verdict != SmokePass && c.Verdict != SmokeFail && c.Verdict != SmokeSkip && !c.MachineDrove()
}

// SmokeHandbackGap names the cases a handback would leave silently undone, oldest
// first: every case still awaiting a machine when qa says its run is over.
//
// It exists because "not driven yet" and "cannot be driven" look IDENTICAL - both
// are a case with nothing in it - so a qa that neglected the most direct part of
// its job is invisible to the human and to itself. Nothing here decides what to DO
// about a gap; it only makes the second state impossible to leave unsaid, the same
// way a stand-down makes "there is nothing to check" impossible to leave unsaid.
//
// Being counted here is not the same as being unanswerable. A case a machine
// cannot drive is declared with `ao smoke record --verdict skip --note "<why>"`,
// which IS a recorded run and so leaves the gap.
func SmokeHandbackGap(checks []SmokeCheck) []string {
	gap := []string{}
	for _, c := range checks {
		if c.AwaitsMachine() {
			gap = append(gap, c.ID)
		}
	}
	return gap
}

// RunEvidence returns the machine artifacts captured during one run.
func (c SmokeCheck) RunEvidence(runID string) []SmokeEvidence {
	out := []SmokeEvidence{}
	for _, ev := range c.AgentEvidence {
		if ev.RunID == runID {
			out = append(out, ev)
		}
	}
	return out
}

// UnknownRunEvidence returns machine artifacts that belong to no run: captures
// taken before AO kept run history, whose result was overwritten and is gone.
// They are grouped apart rather than attributed to the newest run, because a
// stale image read as current evidence is worse than an image with no verdict.
func (c SmokeCheck) UnknownRunEvidence() []SmokeEvidence { return c.RunEvidence("") }

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
// same reason the tables are separate: the writer of one must never be able to
// reach the fields of the other. Verdict may be empty, which records "ran it,
// captured what I saw, did not judge it" - the evidence-only state.
type SmokeAgentResult struct {
	Verdict SmokeVerdict
	Note    string
	// SHA is the commit the case was run against; empty when the caller could
	// not resolve one. Never parsed, only compared.
	SHA string
}

// SmokeRun is ONE round of a machine running a case: what it concluded, what it
// said, and which commit it ran against. One row per `ao smoke record`, so a
// case accumulates its machine history instead of overwriting it.
//
// A run OPENS when the machine attaches its first artifact for the round and
// CLOSES when the result lands. RecordedAt nil is therefore a real state, not
// missing data: the machine captured this and never concluded - which is what a
// crashed or abandoned round leaves behind, and used to look identical to
// evidence pooled under someone else's verdict.
type SmokeRun struct {
	ID        string    `json:"id"`
	CheckID   string    `json:"checkId"`
	SessionID SessionID `json:"sessionId"`
	Seq       int       `json:"seq"` // 1-based per case; drives "RUN N"
	// Verdict is "" for a run that captured evidence without judging it, which
	// is a complete record and not a weaker verdict.
	Verdict SmokeVerdict `json:"verdict,omitempty"`
	Note    string       `json:"note,omitempty"`
	// SHA is the commit this round ran against. It is what makes an old run
	// readable as OLD rather than as wrong when a later run contradicts it.
	SHA string `json:"sha,omitempty"`
	// RecordedAt is when the result landed; nil while the run is still open.
	RecordedAt *time.Time `json:"recordedAt,omitempty"`
	// CreatedAt is when the round opened - its first capture, or the record
	// itself when it captured nothing.
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Recorded reports whether the run has a result, as opposed to being a round
// the machine opened, captured into and never concluded.
func (r SmokeRun) Recorded() bool { return r.RecordedAt != nil }

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
	// RunID is the machine run that captured this file, and "" means the row
	// belongs to no run. There are exactly two ways to be in that state: the user
	// attached it (the user's lane has no runs), or it is machine evidence from
	// before AO kept run history. The second reads as an UNKNOWN run and never as
	// the newest one - the result it belonged to may have been overwritten by a
	// later, contradicting one, and showing it under that verdict would make a
	// stale capture look like current evidence.
	RunID string `json:"runId,omitempty"`
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

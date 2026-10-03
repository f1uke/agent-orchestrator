package domain

import "time"

// Learning capture: the records AO keeps so a later stage can propose skills
// from what the human taught its agents. See internal/learn and the "Learning
// capture" section of docs/architecture.md for the contract (what is read, what
// is kept, where it may go).

// DeliveryAuthor is who wrote a message AO delivered into a session. AO types
// some of what it delivers into the agent's pane, and a typed delivery reaches
// the transcript looking exactly like the human typed it; the author, decided at
// delivery while AO still knows it, is how the two are told apart afterwards.
type DeliveryAuthor string

const (
	// DeliveryAuthorHuman is a person's words that AO carried: the app's send
	// box, `ao send` typed by a person outside any session, the Tests tab's
	// report of the human's verdicts and notes.
	DeliveryAuthorHuman DeliveryAuthor = "human"
	// DeliveryAuthorAgent is another session's `ao send`, which arrives with a
	// "[from @<id>]" prefix. An orchestrator relaying the human's rule to a
	// worker is an agent message: the human's own words live in the
	// orchestrator's transcript, where they were typed.
	DeliveryAuthorAgent DeliveryAuthor = "agent"
	// DeliveryAuthorAO is AO's own text: a spawn brief, a CI or review nudge, a
	// crew notice, a forwarded review comment.
	DeliveryAuthorAO DeliveryAuthor = "ao"
)

// TranscriptRef is one Claude Code transcript file reported for an AO session by
// its hooks. A session normally has one, but `/clear` starts a new conversation
// file under a new native id.
type TranscriptRef struct {
	SessionID       SessionID
	Path            string
	ClaudeSessionID string
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
}

// HookTranscriptRef is what an agent hook reports for learning capture: the
// conversation file the session is writing, and, when the hook fired for a
// submitted prompt, that prompt's fingerprint. The prompt text is reduced to a
// fingerprint inside the hook process and never crosses into the daemon.
type HookTranscriptRef struct {
	ClaudeSessionID string `json:"claudeSessionId,omitempty"`
	TranscriptPath  string `json:"transcriptPath"`
	PromptSHA256    string `json:"promptSha256,omitempty"`
	PromptBytes     int    `json:"promptBytes,omitempty"`
}

// PromptFingerprint is a prompt the agent's submit hook saw, as a hash and a
// length. The text itself is never kept.
type PromptFingerprint struct {
	SessionID       SessionID
	ProjectID       ProjectID
	ClaudeSessionID string
	SHA256          string
	Bytes           int
	SubmittedAt     time.Time
}

// DeliveredFingerprint is a message AO put into a session, as a hash and who
// wrote it.
type DeliveredFingerprint struct {
	SessionID   SessionID
	ProjectID   ProjectID
	SHA256      string
	Bytes       int
	Trigger     string
	Author      DeliveryAuthor
	DeliveredAt time.Time
}

// PromptMatch is one prompt a capture pass read from a transcript, as the
// session and the fingerprint of its text, to be stamped onto the matching
// prompt the hook saw.
type PromptMatch struct {
	SessionID SessionID
	SHA256    string
}

// LearnAttribution says how capture tied a transcript file to an AO session.
type LearnAttribution string

const (
	// LearnAttributionPinned is the file named by the --session-id AO pinned at
	// launch: exact.
	LearnAttributionPinned LearnAttribution = "pinned"
	// LearnAttributionHook is a file a hook of that session reported: exact.
	LearnAttributionHook LearnAttribution = "hook"
	// LearnAttributionWorkspace is a file whose recorded working directory is
	// the session's worktree, with neither of the above. Two crew members share
	// one worktree, so this names the task more surely than the member.
	LearnAttributionWorkspace LearnAttribution = "workspace"
)

// LearnCursor is how far capture has read one transcript file.
type LearnCursor struct {
	TranscriptPath string
	ProjectID      ProjectID
	SessionID      SessionID
	Attribution    LearnAttribution
	ByteOffset     int64
	FileSize       int64
	FileMTime      time.Time
	// PendingCarry is the opaque state a pass hands the next one when a human
	// turn's window is still open (internal/learn/transcript.Carry, encoded).
	PendingCarry string
	HumanTurns   int
	MachineTurns int
	LastError    string
	UpdatedAt    time.Time
}

// LearnSourceClass is how a captured human turn reached the agent.
type LearnSourceClass string

const (
	// LearnSourceTyped is typed into the agent's own prompt.
	LearnSourceTyped LearnSourceClass = "typed"
	// LearnSourceQueued is typed while the agent was busy and submitted later.
	LearnSourceQueued LearnSourceClass = "queued"
	// LearnSourceSuggestionAccepted is a prompt the harness suggested and the
	// human accepted. It is the human agreeing, not the human's words: weak
	// evidence that may support a lesson but never be its only basis.
	LearnSourceSuggestionAccepted LearnSourceClass = "suggestion_accepted"
	// LearnSourceAppSend is the human's message from AO's send box, or `ao send`
	// typed by the human outside any session.
	LearnSourceAppSend LearnSourceClass = "app_send"
	// LearnSourceSmokeReport is the Tests tab reporting the human's verdicts.
	LearnSourceSmokeReport LearnSourceClass = "smoke_report"
)

// LearnWindow is the bounded context on one side of a human turn: the agent's
// own words (clipped in bytes) and a curated list of what it did.
type LearnWindow struct {
	AgentText string   `json:"agentText,omitempty"`
	Actions   []string `json:"actions,omitempty"`
}

// LearnExcerpt is one captured human turn: redacted, with its window.
type LearnExcerpt struct {
	ID             int64
	ProjectID      ProjectID
	SessionID      SessionID
	TranscriptPath string
	TurnUUID       string
	TurnAt         time.Time
	SourceClass    LearnSourceClass
	CWD            string
	GitBranch      string
	Before         LearnWindow
	HumanText      string
	After          LearnWindow
	// Redactions counts what redaction replaced, by kind, so a reader can see
	// that something was taken out without seeing what.
	Redactions map[string]int
	CreatedAt  time.Time
}

// LearnCounts is the project-wide tally behind `ao learn status`: what capture
// stored, and how many prompts the hook saw that capture never found.
type LearnCounts struct {
	Excerpts         int
	BySourceClass    map[LearnSourceClass]int
	Prompts          int
	UnmatchedPrompts int
}

// LearnDraftKind is what a candidate lesson is.
type LearnDraftKind string

const (
	// LearnDraftCorrection is the human correcting a habit of the agent's.
	LearnDraftCorrection LearnDraftKind = "correction"
	// LearnDraftRule is a rule the human stated.
	LearnDraftRule LearnDraftKind = "rule"
	// LearnDraftProcedure is a reusable sequence of steps.
	LearnDraftProcedure LearnDraftKind = "procedure"
	// LearnDraftFact is a non-obvious fact about the environment.
	LearnDraftFact LearnDraftKind = "fact"
	// LearnDraftPreference is how the human wants something done.
	LearnDraftPreference LearnDraftKind = "preference"
)

// Valid reports whether k is one of the kinds a draft may have.
func (k LearnDraftKind) Valid() bool {
	switch k {
	case LearnDraftCorrection, LearnDraftRule, LearnDraftProcedure, LearnDraftFact, LearnDraftPreference:
		return true
	}
	return false
}

// LearnDraftAbout is what a candidate lesson concerns, as the collect model
// tagged it. Only agent_practice is a lesson about how agents work; the other
// tags mark what the human's labels showed to be the usual false lessons, so
// the decide stage can weigh them instead of collect dropping them.
type LearnDraftAbout string

const (
	// LearnAboutAgentPractice is how an agent should work from now on.
	LearnAboutAgentPractice LearnDraftAbout = "agent_practice"
	// LearnAboutProductDecision is a decision about the thing being built.
	LearnAboutProductDecision LearnDraftAbout = "product_decision"
	// LearnAboutOneOff is tied to one task, one time, or decided case by case.
	LearnAboutOneOff LearnDraftAbout = "one_off"
	// LearnAboutQuestion is a question rather than a stated rule.
	LearnAboutQuestion LearnDraftAbout = "question"
)

// Valid reports whether a is one of the tags.
func (a LearnDraftAbout) Valid() bool {
	switch a {
	case LearnAboutAgentPractice, LearnAboutProductDecision, LearnAboutOneOff, LearnAboutQuestion:
		return true
	}
	return false
}

// LearnDraftStatus is where a draft is in its life.
type LearnDraftStatus string

const (
	// LearnDraftOpen is a draft waiting for the decide stage.
	LearnDraftOpen LearnDraftStatus = "open"
	// LearnDraftReversed is a draft a later turn of the same session took back.
	LearnDraftReversed LearnDraftStatus = "reversed"
	// LearnDraftConsumed is a draft the decide stage has used.
	LearnDraftConsumed LearnDraftStatus = "consumed"
	// LearnDraftDropped is a draft the decide stage judged not worth proposing.
	LearnDraftDropped LearnDraftStatus = "dropped"
)

// LearnDraft is one candidate lesson a model extracted from captured turns,
// grounded in the human's own words.
type LearnDraft struct {
	ID        int64
	ProjectID ProjectID
	SessionID SessionID
	// TaskKey groups the drafts of one task for the decide stage: a crew's
	// members share one, an orchestrator gets one per day.
	TaskKey     string
	JobID       int64
	Kind        LearnDraftKind
	Statement   string
	AppliesWhen string
	// ScopeHint is the model's guess at how far the lesson reaches: global,
	// project or repo. Code clamps it when a proposal is made.
	ScopeHint  string
	Confidence float64
	// About is the collect model's tag of what the lesson concerns; empty
	// for drafts collected before tagging existed.
	About LearnDraftAbout
	// Quote is the human's own words, a checked substring of the anchor turn.
	Quote           string
	AnchorExcerptID int64
	// EvidenceExcerptIDs are every captured turn the draft rests on.
	EvidenceExcerptIDs []int64
	AgentBefore        string
	// Weak marks a draft resting only on a suggestion the human accepted.
	Weak         bool
	SupersedesID int64
	Status       LearnDraftStatus
	CreatedAt    time.Time
	// AnchorSourceClass and AnchorTurnAt describe the anchor turn, for display.
	AnchorSourceClass LearnSourceClass
	AnchorTurnAt      time.Time
}

// LearnJobState is the state of one model run.
type LearnJobState string

// The states of a model run: in flight, finished with its drafts stored,
// failed (its turns stay uncollected), or left in flight by a daemon that went
// away (its turns run again).
const (
	LearnJobRunning   LearnJobState = "running"
	LearnJobDone      LearnJobState = "done"
	LearnJobFailed    LearnJobState = "failed"
	LearnJobAbandoned LearnJobState = "abandoned"
)

// LearnJob is one model run over one batch of a session's captured turns.
type LearnJob struct {
	ID        int64
	ProjectID ProjectID
	SessionID SessionID
	// Kind is collect, decide or verify; empty means collect.
	Kind LearnJobKind
	// TaskKey is the task a decide or verify run decided.
	TaskKey      string
	State        LearnJobState
	Model        string
	Turns        int
	Drafts       int
	Rejected     int
	CostUSD      float64
	InputTokens  int64
	OutputTokens int64
	DurationMS   int64
	Error        string
	StderrTail   string
	StartedAt    time.Time
	FinishedAt   time.Time
}

// LearnCollectCandidate is one session with captured turns no model has seen.
type LearnCollectCandidate struct {
	SessionID SessionID
	Turns     int
	OldestAt  time.Time
	NewestAt  time.Time
}

// LearnCollectCounts is what collect has done for one project: turns no model
// has seen yet, and drafts by status.
type LearnCollectCounts struct {
	Uncollected int
	Drafts      map[LearnDraftStatus]int
}

// LearnRuleScope is who a standing rule applies to.
type LearnRuleScope string

const (
	// LearnRuleGlobal applies in every project (the human's own files).
	LearnRuleGlobal LearnRuleScope = "global"
	// LearnRuleProject applies in one project.
	LearnRuleProject LearnRuleScope = "project"
)

// LearnRuleSourceKind is what kind of file or text a standing rule came from.
type LearnRuleSourceKind string

const (
	// LearnRuleSourceClaudeMD is a CLAUDE.md, the human's or a repo's.
	LearnRuleSourceClaudeMD LearnRuleSourceKind = "claude_md"
	// LearnRuleSourceAgentsMD is a repo's AGENTS.md.
	LearnRuleSourceAgentsMD LearnRuleSourceKind = "agents_md"
	// LearnRuleSourceSkill is a skill's SKILL.md.
	LearnRuleSourceSkill LearnRuleSourceKind = "skill"
	// LearnRuleSourceAOPrompt is AO's assembled standing prompt for a project.
	LearnRuleSourceAOPrompt LearnRuleSourceKind = "ao_prompt"
	// LearnRuleSourceKnowledgeIndex is a project's knowledge INDEX.md.
	LearnRuleSourceKnowledgeIndex LearnRuleSourceKind = "knowledge_index"
)

// LearnRuleChunkRef is one chunk of a source, in source order.
type LearnRuleChunkRef struct {
	Hash    string `json:"hash"`
	Heading string `json:"heading,omitempty"`
}

// LearnRuleSource is one file (or AO prompt) the standing-rules corpus is built
// from, with the chunks its current content splits into. Key is stable across
// refreshes: the scope, project, kind and path.
type LearnRuleSource struct {
	Key         string
	Scope       LearnRuleScope
	ProjectID   ProjectID
	Kind        LearnRuleSourceKind
	Label       string
	ContentHash string
	Chunks      []LearnRuleChunkRef
	RefreshedAt time.Time
	// Error is why the last refresh could not atomize every chunk; the chunks
	// that were atomized before still count.
	Error string
}

// LearnRuleAtom is one standing rule as atomized from a chunk.
type LearnRuleAtom struct {
	Text    string   `json:"text"`
	Quote   string   `json:"quote"`
	Tags    []string `json:"tags,omitempty"`
	Heading string   `json:"heading,omitempty"`
}

// LearnRuleChunk is a content-addressed cache entry: the rules one chunk of
// text was split into, and what that cost. Model is empty for a chunk split
// without a model.
type LearnRuleChunk struct {
	Hash         string
	Model        string
	Atoms        []LearnRuleAtom
	Rejected     int
	CostUSD      float64
	InputTokens  int64
	OutputTokens int64
	DurationMS   int64
	CreatedAt    time.Time
}

// LearnRule is one atom of the corpus with where it came from. ID is the
// chunk hash prefix and the atom's position: stable while the chunk is.
type LearnRule struct {
	ID string
	LearnRuleAtom
	SourceKey   string
	SourceLabel string
	SourceKind  LearnRuleSourceKind
	Scope       LearnRuleScope
	ProjectID   ProjectID
}

// LearnProtectedRule is a rule the human pinned: the decide stage always
// checks a proposal against it, and its forbidden patterns block any learned
// skill that matches. ProjectID empty means every project.
type LearnProtectedRule struct {
	ID        int64
	ProjectID ProjectID
	Text      string
	Patterns  []string
	Note      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// LearnJobKind is which stage a model run belongs to.
type LearnJobKind string

const (
	// LearnJobCollect turns captured turns into drafts.
	LearnJobCollect LearnJobKind = "collect"
	// LearnJobDecide turns a finished task's drafts into proposals.
	LearnJobDecide LearnJobKind = "decide"
	// LearnJobVerify checks decide's proposals adversarially.
	LearnJobVerify LearnJobKind = "verify"
)

// LearnOutcome is how the task a proposal came from ended.
type LearnOutcome string

const (
	// LearnOutcomeMerged is a task whose work merged.
	LearnOutcomeMerged LearnOutcome = "merged"
	// LearnOutcomeAbandoned is a task killed, discarded or closed unmerged.
	LearnOutcomeAbandoned LearnOutcome = "abandoned"
	// LearnOutcomeUnknown is a task that ended without saying how.
	LearnOutcomeUnknown LearnOutcome = "unknown"
	// LearnOutcomeOngoing is a session that never ends, decided by daily cut.
	LearnOutcomeOngoing LearnOutcome = "ongoing"
	// LearnOutcomeDay is an orchestrator's day; it has no outcome of its own.
	LearnOutcomeDay LearnOutcome = "day"
)

// Weight is how much a proposal from a task with this outcome is trusted.
func (o LearnOutcome) Weight() float64 {
	switch o {
	case LearnOutcomeMerged, LearnOutcomeDay:
		return 1
	case LearnOutcomeAbandoned:
		return 0.5
	default:
		return 0.7
	}
}

// LearnProposalAction is what a proposal would do.
type LearnProposalAction string

const (
	// LearnProposeCreateSkill writes a new AO-learned skill.
	LearnProposeCreateSkill LearnProposalAction = "create_skill"
	// LearnProposeUpdateSkill changes an existing skill.
	LearnProposeUpdateSkill LearnProposalAction = "update_skill"
	// LearnProposeEditRuleFile changes the human's CLAUDE.md or a knowledge INDEX.
	LearnProposeEditRuleFile LearnProposalAction = "edit_rule_file"
	// LearnProposeConflict shows the human that their new words contradict a
	// standing rule; it writes nothing until the human decides.
	LearnProposeConflict LearnProposalAction = "conflict"
)

// LearnProposalStatus is where a proposal is in its life.
type LearnProposalStatus string

// Proposal statuses.
const (
	LearnProposalPending    LearnProposalStatus = "pending"
	LearnProposalRejected   LearnProposalStatus = "rejected"
	LearnProposalApplied    LearnProposalStatus = "applied"
	LearnProposalStale      LearnProposalStatus = "stale"
	LearnProposalSuperseded LearnProposalStatus = "superseded"
	// LearnProposalDropped is a proposal the verifier or a gate refused; it is
	// kept with DropReason so the refusals can be reviewed.
	LearnProposalDropped LearnProposalStatus = "dropped"
)

// LearnRuleVerdict is how a proposal relates to one standing rule.
type LearnRuleVerdict struct {
	RuleID  string `json:"ruleId"`
	Verdict string `json:"verdict"`
	Note    string `json:"note,omitempty"`
}

// LearnVerifierResult is the adversarial check of one proposal.
type LearnVerifierResult struct {
	ContradictsRule bool   `json:"contradictsRule"`
	Grounded        bool   `json:"grounded"`
	SensitiveData   bool   `json:"sensitiveData"`
	Notes           string `json:"notes,omitempty"`
}

// LearnProposal is a change learning would make, for the human to decide.
type LearnProposal struct {
	ID           int64
	ProjectID    ProjectID
	TaskKey      string
	Action       LearnProposalAction
	TargetPath   string
	Scope        string
	Title        string
	Rationale    string
	BaseSHA256   string
	NewContent   string
	Diff         string
	Confidence   float64
	Outcome      LearnOutcome
	RuleVerdicts []LearnRuleVerdict
	Verifier     LearnVerifierResult
	Status       LearnProposalStatus
	DropReason   string
	EvidenceIDs  []int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// LearnDecidedTask is a task decide has run on.
type LearnDecidedTask struct {
	TaskKey   string
	ProjectID ProjectID
	Outcome   LearnOutcome
	Proposals int
	DecidedAt time.Time
}

// LearnDecideResult is everything one decided task changes, written at once.
type LearnDecideResult struct {
	TaskKey   string
	ProjectID ProjectID
	Outcome   LearnOutcome
	// Proposals are new rows (ID 0) or amendments of a pending one (ID set).
	Proposals []LearnProposal
	// Consumed are drafts a kept proposal rests on; Dropped are the task's
	// other open drafts.
	Consumed []int64
	Dropped  []int64
}

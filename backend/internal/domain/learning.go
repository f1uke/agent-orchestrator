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

package releasetrain

import "time"

// JiraVersionFact is a Jira project version with its approvers.
type JiraVersionFact struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Released    bool       `json:"released"`
	ReleaseDate *Day       `json:"releaseDate,omitempty"`
	Approvers   []Approver `json:"approvers"`
	URL         string     `json:"url"`
}

// Approver is one approver on a Jira version.
type Approver struct {
	AccountID string `json:"accountId"`
	Name      string `json:"name"`
	Status    string `json:"status"`
}

// ApproverApproved is the approver status Jira reports once approved.
const ApproverApproved = "APPROVED"

// RelatedWorkFact holds every related-work link of one Jira version; its absence means it was not fetched.
type RelatedWorkFact struct {
	VersionID string        `json:"versionId"`
	Links     []RelatedWork `json:"links"`
}

// RelatedWork is one related-work link.
type RelatedWork struct {
	Category string `json:"category"`
	Title    string `json:"title"`
	URL      string `json:"url"`
}

// Related-work categories the release process attaches.
const (
	CategoryScanPage    = "Development"
	CategoryReleaseNote = "Communication"
)

// IssueFact is one Jira issue with a fixVersion; the source marks the version's release ticket.
type IssueFact struct {
	Key            string   `json:"key"`
	Summary        string   `json:"summary"`
	Status         string   `json:"status"`
	StatusCategory string   `json:"statusCategory"`
	Subtask        bool     `json:"subtask"`
	ReleaseTicket  bool     `json:"releaseTicket"`
	FixVersions    []string `json:"fixVersions"`
	Assignee       *Person  `json:"assignee,omitempty"`
	IssueType      string   `json:"issueType,omitempty"`
	Parent         string   `json:"parent,omitempty"`
	ParentType     string   `json:"parentType,omitempty"`
	URL            string   `json:"url"`
	MovedTo        string   `json:"movedTo,omitempty"`
}

// IssueTypeEpic is the Jira issue type that groups a feature's cards.
const IssueTypeEpic = "Epic"

// Person is a Jira user.
type Person struct {
	AccountID string `json:"accountId"`
	Name      string `json:"name"`
}

// StatusCategoryDone is Jira's status category key for finished work.
const StatusCategoryDone = "done"

// ForceUpdateFact is a force-update ticket; the source parses its platform and version from the summary.
type ForceUpdateFact struct {
	Key            string   `json:"key"`
	Status         string   `json:"status"`
	StatusCategory string   `json:"statusCategory"`
	Platform       Platform `json:"platform"`
	Version        string   `json:"version"`
	Due            *Day     `json:"due,omitempty"`
	CreatedOn      Day      `json:"createdOn"`
	URL            string   `json:"url"`
}

// Jira fact kinds.
var (
	KindJiraVersions = newKind[JiraVersionFact]("jira.versions", "Jira releases", Snapshotted, 5*time.Minute, global)
	KindRelatedWork  = newKind[RelatedWorkFact]("jira.relatedwork", "related work", Snapshotted, 15*time.Minute, perEntity)
	KindCards        = newKind[IssueFact]("jira.cards", "fixVersion cards", Snapshotted, 5*time.Minute, perEntity)
	KindTechReq      = newKind[ForceUpdateFact]("jira.techreq", "force-update tickets", Snapshotted, 30*time.Minute, global)
	KindLLBCards     = newKind[IssueFact]("jira.llb_cards", "long-lived branch cards", Snapshotted, 10*time.Minute, global)
	KindCardSubtasks = newKind[IssueFact]("jira.card_subtasks", "card subtasks", Snapshotted, 5*time.Minute, global)
)

// LLBCardsScope is the scope of the Jira read of every long-lived-branch card.
const LLBCardsScope Scope = "llb"

// CardSubtasksScope is the scope of the Jira read of every subtask under the cards the Board watches.
const CardSubtasksScope Scope = "watched"

// ItemKey keys a JiraVersionFact within its resource.
func (f JiraVersionFact) ItemKey() string { return f.ID }

// ItemKey keys a RelatedWorkFact within its resource.
func (f RelatedWorkFact) ItemKey() string { return f.VersionID }

// ItemKey keys an IssueFact within its resource.
func (f IssueFact) ItemKey() string { return f.Key }

// ItemKey keys a ForceUpdateFact within its resource.
func (f ForceUpdateFact) ItemKey() string { return f.Key }

// VersionScope is the scope of a per-Jira-version resource.
func VersionScope(id string) Scope { return Scope("v:" + id) }

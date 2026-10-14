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

// IssueFact is one Jira issue with a fixVersion.
type IssueFact struct {
	Key            string   `json:"key"`
	Summary        string   `json:"summary"`
	Status         string   `json:"status"`
	StatusCategory string   `json:"statusCategory"`
	Subtask        bool     `json:"subtask"`
	FixVersions    []string `json:"fixVersions"`
	Assignee       *Person  `json:"assignee,omitempty"`
	URL            string   `json:"url"`
}

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
	KindJiraVersions = newKind[JiraVersionFact]("jira.versions", Snapshotted, 5*time.Minute, global)
	KindRelatedWork  = newKind[RelatedWorkFact]("jira.relatedwork", Snapshotted, 15*time.Minute, perEntity)
	KindCards        = newKind[IssueFact]("jira.cards", Snapshotted, 5*time.Minute, perEntity)
	KindTechReq      = newKind[ForceUpdateFact]("jira.techreq", Snapshotted, 30*time.Minute, global)
)

// VersionScope is the scope of a per-Jira-version resource.
func VersionScope(id string) Scope { return Scope("v:" + id) }

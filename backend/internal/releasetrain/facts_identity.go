package releasetrain

import (
	"strconv"
	"time"
)

// JiraUserFact is a Jira user's display name and avatar, cached per account.
type JiraUserFact struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	AvatarURL   string `json:"avatarUrl,omitempty"`
}

// JiraMyselfFact is the Jira account the monitor's token belongs to.
type JiraMyselfFact struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
}

// GitLabUserFact is the GitLab account the monitor's token belongs to.
type GitLabUserFact struct {
	Username string `json:"username"`
	Name     string `json:"name"`
}

// Identity and people kinds.
var (
	KindJiraUsers  = newKind[JiraUserFact]("jira.users", Snapshotted, 24*time.Hour, global)
	KindJiraMyself = newKind[JiraMyselfFact]("jira.myself", Snapshotted, 24*time.Hour, global)
	KindGitLabUser = newKind[GitLabUserFact]("gitlab.user", Snapshotted, 24*time.Hour, global)
)

// UserScope is the scope of one Jira user's profile.
func UserScope(accountID string) Scope { return Scope("u:" + accountID) }

// MyselfScope is the scope of a token's own account.
const MyselfScope Scope = "me"

// ItemKey implements Keyed.
func (f JiraUserFact) ItemKey() string { return f.AccountID }

// ItemKey implements Keyed.
func (f JiraMyselfFact) ItemKey() string { return f.AccountID }

// ItemKey implements Keyed.
func (f GitLabUserFact) ItemKey() string { return f.Username }

// ItemKey implements Keyed.
func (f MilestoneFact) ItemKey() string { return strconv.Itoa(f.ID) }

// ItemKey implements Keyed.
func (f MRFact) ItemKey() string { return strconv.Itoa(f.IID) }

// ItemKey implements Keyed.
func (f MRDetailFact) ItemKey() string { return strconv.Itoa(f.IID) }

// ItemKey implements Keyed.
func (f JobsFact) ItemKey() string { return strconv.Itoa(f.Pipeline) }

// ItemKey implements Keyed.
func (f TagFact) ItemKey() string { return f.Name }

// ItemKey implements Keyed.
func (f CompareFact) ItemKey() string { return f.From + ".." + f.To }

// ItemKey implements Keyed.
func (f JiraVersionFact) ItemKey() string { return f.ID }

// ItemKey implements Keyed.
func (f RelatedWorkFact) ItemKey() string { return f.VersionID }

// ItemKey implements Keyed.
func (f IssueFact) ItemKey() string { return f.Key }

// ItemKey implements Keyed.
func (f ForceUpdateFact) ItemKey() string { return f.Key }

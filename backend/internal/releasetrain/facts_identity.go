package releasetrain

import "time"

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

// ItemKey keys a JiraUserFact within its resource.
func (f JiraUserFact) ItemKey() string { return f.AccountID }

// ItemKey keys a JiraMyselfFact within its resource.
func (f JiraMyselfFact) ItemKey() string { return f.AccountID }

// ItemKey keys a GitLabUserFact within its resource.
func (f GitLabUserFact) ItemKey() string { return f.Username }

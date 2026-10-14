package releasetrain

// IdentityState is who "me" is: known once both accounts are read.
type IdentityState interface{ identityState() }

// IdentityKnown is the Jira account and GitLab user the monitor's tokens belong to.
type IdentityKnown struct {
	JiraAccountID  string `json:"jiraAccountId"`
	GitLabUsername string `json:"gitlabUsername"`
	DisplayName    string `json:"displayName"`
}

// IdentityUnknown says why "me" is not known; every Mine is then empty.
type IdentityUnknown struct {
	Reason string `json:"reason"`
}

func (IdentityKnown) identityState()   {}
func (IdentityUnknown) identityState() {}

func identityOf(snap Snapshot) IdentityState {
	jira, gitlab := AllItems(snap, KindJiraMyself), AllItems(snap, KindGitLabUser)
	switch {
	case len(jira) == 0 && len(gitlab) == 0:
		return IdentityUnknown{Reason: "neither the Jira account (jira.myself) nor the GitLab user (gitlab.user) has been read"}
	case len(jira) == 0:
		return IdentityUnknown{Reason: "the Jira account (jira.myself) has not been read"}
	case len(gitlab) == 0:
		return IdentityUnknown{Reason: "the GitLab user (gitlab.user) has not been read"}
	}
	return IdentityKnown{JiraAccountID: jira[0].AccountID, GitLabUsername: gitlab[0].Username, DisplayName: jira[0].DisplayName}
}

package releasetrain

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// EffectKind names what one fix step changes, and in which system.
type EffectKind string

// Effect kinds. Only EffectAOPlanTarget writes anything today; the rest are dry runs.
const (
	EffectJiraVersionUpdate  EffectKind = "jira_version_update"
	EffectJiraTransition     EffectKind = "jira_transition"
	EffectJiraApprovers      EffectKind = "jira_approvers"
	EffectJiraRelatedWork    EffectKind = "jira_related_work"
	EffectGitLabRetarget     EffectKind = "gitlab_retarget"
	EffectGitLabJobRetry     EffectKind = "gitlab_job_retry"
	EffectGitLabMilestoneDue EffectKind = "gitlab_milestone_due"
	EffectCopyText           EffectKind = "copy_text"
	EffectAOPlanTarget       EffectKind = "ao_plan_target"
)

// ShownRequest is the exact request a step would send: method, URL with ids
// and body. It never carries credentials; the executor adds those.
type ShownRequest struct {
	Method string          `json:"method"`
	URL    string          `json:"url"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// Effect is one change a fix makes; sealed, JSON {"kind": ...}.
type Effect interface {
	Variant
	Kind() EffectKind
	Request() ShownRequest
}

// VersionFields is the part of a Jira version a fix may change.
type VersionFields struct {
	Released    bool `json:"released"`
	ReleaseDate *Day `json:"releaseDate,omitempty"`
}

// EffJiraVersionUpdate marks a Jira version released or moves its date (E1, A5).
type EffJiraVersionUpdate struct {
	Site      string        `json:"site"`
	VersionID string        `json:"versionId"`
	Before    VersionFields `json:"before"`
	After     VersionFields `json:"after"`
}

// EffJiraTransition moves a card to another status (G1).
type EffJiraTransition struct {
	Site string `json:"site"`
	Key  string `json:"key"`
	From string `json:"from"`
	To   string `json:"to"`
}

// EffJiraApprovers sets a Jira version's approvers (D1).
type EffJiraApprovers struct {
	Site      string   `json:"site"`
	VersionID string   `json:"versionId"`
	After     []Person `json:"after"`
}

// EffJiraRelatedWork attaches a related-work link to a Jira version (D2).
type EffJiraRelatedWork struct {
	Site      string `json:"site"`
	VersionID string `json:"versionId"`
	Category  string `json:"category"`
	Title     string `json:"title"`
}

// EffGitLabRetarget points an open MR at another branch (L1, L8).
type EffGitLabRetarget struct {
	Host    string `json:"host"`
	App     AppID  `json:"app"`
	Project int    `json:"project"`
	IID     int    `json:"iid"`
	From    string `json:"from"`
	To      string `json:"to"`
}

// EffGitLabJobRetry retries one failed pipeline job (C1, C6).
type EffGitLabJobRetry struct {
	Host    string `json:"host"`
	App     AppID  `json:"app"`
	Project int    `json:"project"`
	JobID   int    `json:"jobId"`
	Name    string `json:"name"`
}

// EffGitLabMilestoneDue moves a milestone's due date (A5's other choice).
type EffGitLabMilestoneDue struct {
	Host        string `json:"host"`
	App         AppID  `json:"app"`
	Project     int    `json:"project"`
	MilestoneID int    `json:"milestoneId"`
	Before      Day    `json:"before"`
	After       Day    `json:"after"`
}

// EffCopyText is text for a person to paste where people edit by hand (G5).
type EffCopyText struct {
	Target string `json:"target"`
	Text   string `json:"text"`
}

// LLBTargetInput is a plan's target as the plan form writes it.
type LLBTargetInput struct {
	Kind  TargetKind `json:"targetKind"`
	Train *TrainID   `json:"targetTrain,omitempty"`
}

// EffAOPlanTarget sets AO's own long-lived-branch plan target (L3); the write
// is refused when the plan is no longer at PlanVersion.
type EffAOPlanTarget struct {
	Plan        PlanID         `json:"plan"`
	Before      LLBTargetInput `json:"before"`
	After       LLBTargetInput `json:"after"`
	PlanVersion int            `json:"planVersion"`
}

// Kind is EffectJiraVersionUpdate.
func (EffJiraVersionUpdate) Kind() EffectKind { return EffectJiraVersionUpdate }

// Kind is EffectJiraTransition.
func (EffJiraTransition) Kind() EffectKind { return EffectJiraTransition }

// Kind is EffectJiraApprovers.
func (EffJiraApprovers) Kind() EffectKind { return EffectJiraApprovers }

// Kind is EffectJiraRelatedWork.
func (EffJiraRelatedWork) Kind() EffectKind { return EffectJiraRelatedWork }

// Kind is EffectGitLabRetarget.
func (EffGitLabRetarget) Kind() EffectKind { return EffectGitLabRetarget }

// Kind is EffectGitLabJobRetry.
func (EffGitLabJobRetry) Kind() EffectKind { return EffectGitLabJobRetry }

// Kind is EffectGitLabMilestoneDue.
func (EffGitLabMilestoneDue) Kind() EffectKind { return EffectGitLabMilestoneDue }

// Kind is EffectCopyText.
func (EffCopyText) Kind() EffectKind { return EffectCopyText }

// Kind is EffectAOPlanTarget.
func (EffAOPlanTarget) Kind() EffectKind { return EffectAOPlanTarget }

// Variant is the effect kind.
func (e EffJiraVersionUpdate) Variant() string { return string(e.Kind()) }

// Variant is the effect kind.
func (e EffJiraTransition) Variant() string { return string(e.Kind()) }

// Variant is the effect kind.
func (e EffJiraApprovers) Variant() string { return string(e.Kind()) }

// Variant is the effect kind.
func (e EffJiraRelatedWork) Variant() string { return string(e.Kind()) }

// Variant is the effect kind.
func (e EffGitLabRetarget) Variant() string { return string(e.Kind()) }

// Variant is the effect kind.
func (e EffGitLabJobRetry) Variant() string { return string(e.Kind()) }

// Variant is the effect kind.
func (e EffGitLabMilestoneDue) Variant() string { return string(e.Kind()) }

// Variant is the effect kind.
func (e EffCopyText) Variant() string { return string(e.Kind()) }

// Variant is the effect kind.
func (e EffAOPlanTarget) Variant() string { return string(e.Kind()) }

// MarshalJSON writes the effect with its kind.
func (e EffJiraVersionUpdate) MarshalJSON() ([]byte, error) {
	type plain EffJiraVersionUpdate
	return tagged(e, plain(e))
}

// MarshalJSON writes the effect with its kind.
func (e EffJiraTransition) MarshalJSON() ([]byte, error) {
	type plain EffJiraTransition
	return tagged(e, plain(e))
}

// MarshalJSON writes the effect with its kind.
func (e EffJiraApprovers) MarshalJSON() ([]byte, error) {
	type plain EffJiraApprovers
	return tagged(e, plain(e))
}

// MarshalJSON writes the effect with its kind.
func (e EffJiraRelatedWork) MarshalJSON() ([]byte, error) {
	type plain EffJiraRelatedWork
	return tagged(e, plain(e))
}

// MarshalJSON writes the effect with its kind.
func (e EffGitLabRetarget) MarshalJSON() ([]byte, error) {
	type plain EffGitLabRetarget
	return tagged(e, plain(e))
}

// MarshalJSON writes the effect with its kind.
func (e EffGitLabJobRetry) MarshalJSON() ([]byte, error) {
	type plain EffGitLabJobRetry
	return tagged(e, plain(e))
}

// MarshalJSON writes the effect with its kind.
func (e EffGitLabMilestoneDue) MarshalJSON() ([]byte, error) {
	type plain EffGitLabMilestoneDue
	return tagged(e, plain(e))
}

// MarshalJSON writes the effect with its kind.
func (e EffCopyText) MarshalJSON() ([]byte, error) {
	type plain EffCopyText
	return tagged(e, plain(e))
}

// MarshalJSON writes the effect with its kind.
func (e EffAOPlanTarget) MarshalJSON() ([]byte, error) {
	type plain EffAOPlanTarget
	return tagged(e, plain(e))
}

func jsonBody(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

func jiraAPI(site, path string) string { return site + "/rest/api/3/" + path }

func gitlabAPI(host string, project int, path string) string {
	return fmt.Sprintf("https://%s/api/v4/projects/%d/%s", host, project, path)
}

// Request is PUT /version/{id} with only the fields that change.
func (e EffJiraVersionUpdate) Request() ShownRequest {
	var body struct {
		Released    *bool `json:"released,omitempty"`
		ReleaseDate *Day  `json:"releaseDate,omitempty"`
	}
	if e.Before.Released != e.After.Released {
		body.Released = &e.After.Released
	}
	if !sameDay(e.Before.ReleaseDate, e.After.ReleaseDate) {
		body.ReleaseDate = e.After.ReleaseDate
	}
	return ShownRequest{Method: "PUT", URL: jiraAPI(e.Site, "version/"+url.PathEscape(e.VersionID)), Body: jsonBody(body)}
}

// Request names the target status; the executor looks up its transition id first.
func (e EffJiraTransition) Request() ShownRequest {
	body := map[string]map[string]string{"transition": {"name": e.To}}
	return ShownRequest{Method: "POST", URL: jiraAPI(e.Site, "issue/"+url.PathEscape(e.Key)+"/transitions"), Body: jsonBody(body)}
}

// Request is PUT /version/{id} with the approvers' account ids.
func (e EffJiraApprovers) Request() ShownRequest {
	type approver struct {
		AccountID string `json:"accountId"`
	}
	list := make([]approver, len(e.After))
	for i, p := range e.After {
		list[i] = approver{p.AccountID}
	}
	return ShownRequest{Method: "PUT", URL: jiraAPI(e.Site, "version/"+url.PathEscape(e.VersionID)), Body: jsonBody(map[string]any{"approvers": list})}
}

// Request is POST /version/{id}/relatedwork; the page URL is found at run time.
func (e EffJiraRelatedWork) Request() ShownRequest {
	body := struct {
		Category string `json:"category"`
		Title    string `json:"title"`
	}{e.Category, e.Title}
	return ShownRequest{Method: "POST", URL: jiraAPI(e.Site, "version/"+url.PathEscape(e.VersionID)+"/relatedwork"), Body: jsonBody(body)}
}

// Request is PUT merge_requests/{iid} with the new target branch.
func (e EffGitLabRetarget) Request() ShownRequest {
	return ShownRequest{Method: "PUT", URL: gitlabAPI(e.Host, e.Project, "merge_requests/"+strconv.Itoa(e.IID)),
		Body: jsonBody(map[string]string{"target_branch": e.To})}
}

// Request is POST jobs/{id}/retry, with no body.
func (e EffGitLabJobRetry) Request() ShownRequest {
	return ShownRequest{Method: "POST", URL: gitlabAPI(e.Host, e.Project, "jobs/"+strconv.Itoa(e.JobID)+"/retry")}
}

// Request is PUT milestones/{id} with the new due date.
func (e EffGitLabMilestoneDue) Request() ShownRequest {
	return ShownRequest{Method: "PUT", URL: gitlabAPI(e.Host, e.Project, "milestones/"+strconv.Itoa(e.MilestoneID)),
		Body: jsonBody(map[string]string{"due_date": e.After.String()})}
}

// Request is no network call: COPY names where the text is pasted.
func (e EffCopyText) Request() ShownRequest {
	return ShownRequest{Method: "COPY", URL: e.Target, Body: jsonBody(e.Text)}
}

// Request is AO's own plan route, guarded by the plan version.
func (e EffAOPlanTarget) Request() ShownRequest {
	body := struct {
		LLBTargetInput
		IfVersion int `json:"ifVersion"`
	}{e.After, e.PlanVersion}
	return ShownRequest{Method: "PUT", URL: "/api/v1/release-trains/llb-plans/" + url.PathEscape(string(e.Plan)), Body: jsonBody(body)}
}

func sameDay(a, b *Day) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// DecodeEffect reads an effect back from its JSON.
func DecodeEffect(b []byte) (Effect, error) {
	var head struct {
		Kind EffectKind `json:"kind"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return nil, fmt.Errorf("effect: %w", err)
	}
	switch head.Kind {
	case EffectJiraVersionUpdate:
		return decodeAs[EffJiraVersionUpdate](b)
	case EffectJiraTransition:
		return decodeAs[EffJiraTransition](b)
	case EffectJiraApprovers:
		return decodeAs[EffJiraApprovers](b)
	case EffectJiraRelatedWork:
		return decodeAs[EffJiraRelatedWork](b)
	case EffectGitLabRetarget:
		return decodeAs[EffGitLabRetarget](b)
	case EffectGitLabJobRetry:
		return decodeAs[EffGitLabJobRetry](b)
	case EffectGitLabMilestoneDue:
		return decodeAs[EffGitLabMilestoneDue](b)
	case EffectCopyText:
		return decodeAs[EffCopyText](b)
	case EffectAOPlanTarget:
		return decodeAs[EffAOPlanTarget](b)
	}
	return nil, fmt.Errorf("effect: unknown kind %q", head.Kind)
}

func decodeAs[T Effect](b []byte) (Effect, error) {
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("effect %s: %w", v.Kind(), err)
	}
	return v, nil
}

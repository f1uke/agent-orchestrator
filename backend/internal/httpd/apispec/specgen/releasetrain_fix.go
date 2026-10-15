package specgen

import (
	"encoding/json"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

// Twins of the core's sealed Effect union, which marshals with a "kind".
type (
	rtEffect               struct{}
	rtEffJiraVersionUpdate struct {
		Kind      string           `json:"kind" const:"jira_version_update"`
		Site      string           `json:"site"`
		VersionID string           `json:"versionId"`
		Before    rt.VersionFields `json:"before"`
		After     rt.VersionFields `json:"after"`
	}
	rtEffJiraTransition struct {
		Kind string `json:"kind" const:"jira_transition"`
		Site string `json:"site"`
		Key  string `json:"key"`
		From string `json:"from"`
		To   string `json:"to"`
	}
	rtEffJiraApprovers struct {
		Kind      string      `json:"kind" const:"jira_approvers"`
		Site      string      `json:"site"`
		VersionID string      `json:"versionId"`
		After     []rt.Person `json:"after"`
	}
	rtEffJiraRelatedWork struct {
		Kind      string `json:"kind" const:"jira_related_work"`
		Site      string `json:"site"`
		VersionID string `json:"versionId"`
		Category  string `json:"category"`
		Title     string `json:"title"`
	}
	rtEffGitLabRetarget struct {
		Kind    string   `json:"kind" const:"gitlab_retarget"`
		Host    string   `json:"host"`
		App     rt.AppID `json:"app"`
		Project int      `json:"project"`
		IID     int      `json:"iid"`
		From    string   `json:"from"`
		To      string   `json:"to"`
	}
	rtEffGitLabJobRetry struct {
		Kind    string   `json:"kind" const:"gitlab_job_retry"`
		Host    string   `json:"host"`
		App     rt.AppID `json:"app"`
		Project int      `json:"project"`
		JobID   int      `json:"jobId"`
		Name    string   `json:"name"`
	}
	rtEffGitLabMilestoneDue struct {
		Kind        string   `json:"kind" const:"gitlab_milestone_due"`
		Host        string   `json:"host"`
		App         rt.AppID `json:"app"`
		Project     int      `json:"project"`
		MilestoneID int      `json:"milestoneId"`
		Before      rt.Day   `json:"before"`
		After       rt.Day   `json:"after"`
	}
	rtEffCopyText struct {
		Kind   string `json:"kind" const:"copy_text"`
		Target string `json:"target"`
		Text   string `json:"text"`
	}
	rtEffAOPlanTarget struct {
		Kind        string            `json:"kind" const:"ao_plan_target"`
		Plan        rt.PlanID         `json:"plan"`
		Before      rt.LLBTargetInput `json:"before"`
		After       rt.LLBTargetInput `json:"after"`
		PlanVersion int               `json:"planVersion"`
	}
	rtShownRequest struct {
		Method string          `json:"method" description:"An HTTP method, or COPY for text to paste by hand."`
		URL    string          `json:"url"`
		Body   json.RawMessage `json:"body,omitempty" description:"The JSON body as sent; never credentials."`
	}
)

// JSONSchemaOneOf lists the effect variants.
func (rtEffect) JSONSchemaOneOf() []interface{} {
	return []interface{}{rtEffJiraVersionUpdate{}, rtEffJiraTransition{}, rtEffJiraApprovers{}, rtEffJiraRelatedWork{},
		rtEffGitLabRetarget{}, rtEffGitLabJobRetry{}, rtEffGitLabMilestoneDue{}, rtEffCopyText{}, rtEffAOPlanTarget{}}
}

func init() {
	for name, clean := range map[string]string{
		"ReleasetrainFixPlan":                      "ReleaseTrainFixPlan",
		"ReleasetrainFixStep":                      "ReleaseTrainFixStep",
		"ReleasetrainFixChoice":                    "ReleaseTrainFixChoice",
		"ReleasetrainFixNeed":                      "ReleaseTrainFixNeed",
		"ReleasetrainFixMode":                      "ReleaseTrainFixMode",
		"ReleasetrainFixKind":                      "ReleaseTrainFixKind",
		"ReleasetrainFixRef":                       "ReleaseTrainFixRef",
		"ReleasetrainFixState":                     "ReleaseTrainFixState",
		"ReleasetrainFix":                          "ReleaseTrainFix",
		"ReleasetrainFixEvent":                     "ReleaseTrainFixEvent",
		"ReleasetrainFixEventKind":                 "ReleaseTrainFixEventKind",
		"ReleasetrainVersionFields":                "ReleaseTrainVersionFields",
		"ReleasetrainLLBTargetInput":               "ReleaseTrainLLBTargetInput",
		"SpecgenRtEffect":                          "ReleaseTrainEffect",
		"SpecgenRtEffJiraVersionUpdate":            "ReleaseTrainEffJiraVersionUpdate",
		"SpecgenRtEffJiraTransition":               "ReleaseTrainEffJiraTransition",
		"SpecgenRtEffJiraApprovers":                "ReleaseTrainEffJiraApprovers",
		"SpecgenRtEffJiraRelatedWork":              "ReleaseTrainEffJiraRelatedWork",
		"SpecgenRtEffGitLabRetarget":               "ReleaseTrainEffGitLabRetarget",
		"SpecgenRtEffGitLabJobRetry":               "ReleaseTrainEffGitLabJobRetry",
		"SpecgenRtEffGitLabMilestoneDue":           "ReleaseTrainEffGitLabMilestoneDue",
		"SpecgenRtEffCopyText":                     "ReleaseTrainEffCopyText",
		"SpecgenRtEffAOPlanTarget":                 "ReleaseTrainEffAOPlanTarget",
		"SpecgenRtShownRequest":                    "ReleaseTrainShownRequest",
		"ControllersReleaseTrainFixPlanRequest":    "ReleaseTrainFixPlanRequest",
		"ControllersReleaseTrainFixPlanResponse":   "ReleaseTrainFixPlanResponse",
		"ControllersReleaseTrainFixConfirmRequest": "ReleaseTrainFixConfirmRequest",
		"ControllersReleaseTrainFixUndoRequest":    "ReleaseTrainFixUndoRequest",
		"ControllersReleaseTrainFixResponse":       "ReleaseTrainFixResponse",
		"ControllersReleaseTrainFixesResponse":     "ReleaseTrainFixesResponse",
	} {
		schemaNames[name] = clean
	}
}

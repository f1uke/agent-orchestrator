package specgen

import (
	"time"

	jsonschema "github.com/swaggest/jsonschema-go"
	"github.com/swaggest/openapi-go/openapi31"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

// The release-train core marshals its sealed unions and value types by hand
// (it imports only the standard library); these twins describe that wire shape.
type (
	rtOutcome      struct{}
	rtOutcomeClean struct {
		Kind      string    `json:"kind" const:"clean"`
		CheckedAt time.Time `json:"checkedAt"`
	}
	rtOutcomeFired struct {
		Kind     string              `json:"kind" const:"fired"`
		Hits     []string            `json:"hits" description:"Hit ids; the alert list carries the hits."`
		AsOf     time.Time           `json:"asOf"`
		Degraded []rt.ResourceStatus `json:"degraded,omitempty"`
	}
	rtOutcomeUnknown struct {
		Kind    string              `json:"kind" const:"unknown"`
		Missing []rt.ResourceStatus `json:"missing"`
	}

	rtCodeState  struct{}
	rtCodeMerged struct {
		Kind string `json:"kind" const:"merged"`
		MRs  []int  `json:"mrs"`
		Via  string `json:"via,omitempty" description:"The long-lived branch that carried the MRs into the train."`
	}
	rtCodeParked struct {
		Kind   string `json:"kind" const:"parked"`
		Branch string `json:"branch"`
		MRs    []int  `json:"mrs"`
	}

	rtIdentity      struct{}
	rtIdentityKnown struct {
		Kind           string `json:"kind" const:"known"`
		JiraAccountID  string `json:"jiraAccountId"`
		GitLabUsername string `json:"gitlabUsername"`
		DisplayName    string `json:"displayName"`
	}
	rtIdentityUnknown struct {
		Kind   string `json:"kind" const:"unknown"`
		Reason string `json:"reason"`
	}

	rtLLBTarget   struct{}
	rtLLBLandedIn struct {
		Kind  string     `json:"kind" const:"landed"`
		Train rt.TrainID `json:"train"`
	}
	rtLLBPlanned struct {
		Kind  string     `json:"kind" const:"planned"`
		Train rt.TrainID `json:"train"`
	}
	rtLLBNoTarget struct {
		Kind string `json:"kind" const:"none"`
	}
	rtLLBDropped struct {
		Kind string `json:"kind" const:"dropped"`
	}
	rtCodeOpen struct {
		Kind string `json:"kind" const:"open"`
		MRs  []int  `json:"mrs"`
	}
	rtCodeElsewhere struct {
		Kind   string `json:"kind" const:"elsewhere"`
		Branch string `json:"branch"`
		On     rt.Day `json:"on"`
	}
	rtCodeNone struct {
		Kind string `json:"kind" const:"none"`
	}
)

// JSONSchemaOneOf lists the outcome variants.
func (rtOutcome) JSONSchemaOneOf() []interface{} {
	return []interface{}{rtOutcomeClean{}, rtOutcomeFired{}, rtOutcomeUnknown{}}
}

// JSONSchemaOneOf lists the code state variants.
func (rtCodeState) JSONSchemaOneOf() []interface{} {
	return []interface{}{rtCodeMerged{}, rtCodeOpen{}, rtCodeParked{}, rtCodeElsewhere{}, rtCodeNone{}}
}

// JSONSchemaOneOf lists the identity variants.
func (rtIdentity) JSONSchemaOneOf() []interface{} {
	return []interface{}{rtIdentityKnown{}, rtIdentityUnknown{}}
}

// JSONSchemaOneOf lists the long-lived branch target variants.
func (rtLLBTarget) JSONSchemaOneOf() []interface{} {
	return []interface{}{rtLLBLandedIn{}, rtLLBPlanned{}, rtLLBNoTarget{}, rtLLBDropped{}}
}

func init() {
	for name, clean := range map[string]string{
		"ReleasetrainAlertHit":                       "ReleaseTrainAlertHit",
		"ReleasetrainAlerts":                         "ReleaseTrainAlerts",
		"ReleasetrainAppVersion":                     "ReleaseTrainAppVersion",
		"ReleasetrainApprover":                       "ReleaseTrainApprover",
		"ReleasetrainBadge":                          "ReleaseTrainBadge",
		"ReleasetrainBoard":                          "ReleaseTrainBoard",
		"ReleasetrainBoardApp":                       "ReleaseTrainBoardApp",
		"ReleasetrainBoardResponse":                  "ReleaseTrainBoardResponse",
		"ReleasetrainCardUnit":                       "ReleaseTrainCardUnit",
		"ReleasetrainCommitFact":                     "ReleaseTrainCommitFact",
		"ReleasetrainConfigReport":                   "ReleaseTrainConfigReport",
		"ReleasetrainDay":                            "ReleaseTrainDay",
		"ReleasetrainEvidence":                       "ReleaseTrainEvidence",
		"ReleasetrainForceUpdateFact":                "ReleaseTrainForceUpdateFact",
		"ReleasetrainHotfix":                         "ReleaseTrainHotfix",
		"ReleasetrainIssueFact":                      "ReleaseTrainIssueFact",
		"ReleasetrainJiraVersionFact":                "ReleaseTrainJiraVersionFact",
		"ReleasetrainJobFact":                        "ReleaseTrainJobFact",
		"ReleasetrainJobsFact":                       "ReleaseTrainJobsFact",
		"ReleasetrainLink":                           "ReleaseTrainLink",
		"ReleasetrainMRDetailFact":                   "ReleaseTrainMRDetailFact",
		"ReleasetrainMRFact":                         "ReleaseTrainMRFact",
		"ReleasetrainMilestoneFact":                  "ReleaseTrainMilestoneFact",
		"ReleasetrainMismatch":                       "ReleaseTrainMismatch",
		"ReleasetrainPerson":                         "ReleaseTrainPerson",
		"ReleasetrainPageFact":                       "ReleaseTrainPageFact",
		"ReleasetrainCardWhy":                        "ReleaseTrainCardWhy",
		"ReleasetrainPipelineFact":                   "ReleaseTrainPipelineFact",
		"ReleasetrainRelatedWork":                    "ReleaseTrainRelatedWork",
		"ReleasetrainRelatedWorkFact":                "ReleaseTrainRelatedWorkFact",
		"ReleasetrainReleaseMR":                      "ReleaseTrainReleaseMR",
		"ReleasetrainResourceStatus":                 "ReleaseTrainResourceStatus",
		"ReleasetrainRuleOutcome":                    "ReleaseTrainRuleOutcome",
		"ReleasetrainRuleInfo":                       "ReleaseTrainRuleInfo",
		"ReleasetrainStaleHide":                      "ReleaseTrainStaleHide",
		"ReleasetrainSeverity":                       "ReleaseTrainSeverity",
		"ReleasetrainBucket":                         "ReleaseTrainBucket",
		"ReleasetrainErrorClass":                     "ReleaseTrainErrorClass",
		"ReleasetrainMismatchKind":                   "ReleaseTrainMismatchKind",
		"ReleasetrainPlatform":                       "ReleaseTrainPlatform",
		"ReleasetrainStageKey":                       "ReleaseTrainStageKey",
		"ReleasetrainStageStatus":                    "ReleaseTrainStageStatus",
		"ReleasetrainTrainPhase":                     "ReleaseTrainTrainPhase",
		"ReleasetrainSourceFreshness":                "ReleaseTrainSourceFreshness",
		"ReleasetrainStage":                          "ReleaseTrainStage",
		"ReleasetrainTag":                            "ReleaseTrainTag",
		"ReleasetrainTrain":                          "ReleaseTrainTrain",
		"SpecgenRtCodeElsewhere":                     "ReleaseTrainCodeElsewhere",
		"SpecgenRtCodeMerged":                        "ReleaseTrainCodeMerged",
		"SpecgenRtCodeNone":                          "ReleaseTrainCodeNone",
		"SpecgenRtCodeParked":                        "ReleaseTrainCodeParked",
		"SpecgenRtLLBTarget":                         "ReleaseTrainLLBTarget",
		"SpecgenRtIdentity":                          "ReleaseTrainIdentity",
		"SpecgenRtIdentityKnown":                     "ReleaseTrainIdentityKnown",
		"SpecgenRtIdentityUnknown":                   "ReleaseTrainIdentityUnknown",
		"ReleasetrainMineReason":                     "ReleaseTrainMineReason",
		"ReleasetrainMineWhy":                        "ReleaseTrainMineWhy",
		"SpecgenRtLLBLandedIn":                       "ReleaseTrainLLBLandedIn",
		"SpecgenRtLLBPlanned":                        "ReleaseTrainLLBPlanned",
		"SpecgenRtLLBNoTarget":                       "ReleaseTrainLLBNoTarget",
		"SpecgenRtLLBDropped":                        "ReleaseTrainLLBDropped",
		"ReleasetrainLLBGroup":                       "ReleaseTrainLLBGroup",
		"ReleasetrainLLBCard":                        "ReleaseTrainLLBCard",
		"ReleasetrainLLBState":                       "ReleaseTrainLLBState",
		"ReleasetrainLongLivedBranch":                "ReleaseTrainLongLivedBranch",
		"SpecgenRtCodeOpen":                          "ReleaseTrainCodeOpen",
		"SpecgenRtCodeState":                         "ReleaseTrainCodeState",
		"SpecgenRtOutcome":                           "ReleaseTrainOutcome",
		"SpecgenRtOutcomeClean":                      "ReleaseTrainOutcomeClean",
		"SpecgenRtOutcomeFired":                      "ReleaseTrainOutcomeFired",
		"SpecgenRtOutcomeUnknown":                    "ReleaseTrainOutcomeUnknown",
		"ControllersReleaseTrainRefreshRequest":      "ReleaseTrainRefreshRequest",
		"ControllersReleaseTrainRefreshResponse":     "ReleaseTrainRefreshResponse",
		"ControllersReleaseTrainRuleSettingRequest":  "ReleaseTrainRuleSettingRequest",
		"ControllersReleaseTrainRuleSettingResponse": "ReleaseTrainRuleSettingResponse",
		"ControllersReleaseTrainHideRequest":         "ReleaseTrainHideRequest",
		"ControllersReleaseTrainHideResponse":        "ReleaseTrainHideResponse",
		"ControllersReleaseTrainUnhideStaleResponse": "ReleaseTrainUnhideStaleResponse",
		"ControllersReleaseTrainLlbPlansResponse":    "ReleaseTrainLlbPlansResponse",
		"ControllersReleaseTrainLlbPlanInput":        "ReleaseTrainLlbPlanInput",
		"ControllersReleaseTrainLlbPlanResponse":     "ReleaseTrainLlbPlanResponse",
		"ReleasetrainLlbPlan":                        "ReleaseTrainLlbPlan",
		"ReleasetrainPlanBranch":                     "ReleaseTrainPlanBranch",
		"ReleasetrainPlanAccepted":                   "ReleaseTrainPlanAccepted",
		"ReleasetrainTargetKind":                     "ReleaseTrainTargetKind",
	} {
		schemaNames[name] = clean
	}
}

func mapReleaseTrainTypes(r *openapi31.Reflector) {
	js := r.JSONSchemaReflector()
	js.AddTypeMapping((*rt.Outcome)(nil), rtOutcome{})
	js.AddTypeMapping((*rt.CodeState)(nil), rtCodeState{})
	js.AddTypeMapping((*rt.LLBTarget)(nil), rtLLBTarget{})
	js.AddTypeMapping((*rt.IdentityState)(nil), rtIdentity{})
	day := jsonschema.Schema{}
	day.WithType(jsonschema.String.Type()).WithFormat("date")
	js.AddTypeMapping(rt.Day{}, day)
	train := jsonschema.Schema{}
	train.WithType(jsonschema.String.Type()).WithDescription(`Train key, "21-2026" or "20-1-2026".`)
	js.AddTypeMapping(rt.TrainID{}, train)
	sev := jsonschema.Schema{}
	sev.WithType(jsonschema.String.Type()).WithEnum("block", "data", "risk", "hygiene", "info")
	js.AddTypeMapping(rt.Severity(0), sev)
}

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
	return []interface{}{rtCodeMerged{}, rtCodeOpen{}, rtCodeElsewhere{}, rtCodeNone{}}
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

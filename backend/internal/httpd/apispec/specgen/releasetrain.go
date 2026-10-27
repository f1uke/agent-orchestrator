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

	rtViewer       struct{}
	rtViewerPerson struct {
		Kind   string         `json:"kind" const:"person"`
		Person rt.BoardPerson `json:"person"`
	}
	rtViewerUnknown struct {
		Kind   string `json:"kind" const:"unknown"`
		ID     string `json:"id,omitempty" description:"The person id the request named; absent when the monitor's own identity is unknown."`
		Reason string `json:"reason"`
	}

	rtLLBTarget   struct{}
	rtLLBLandedIn struct {
		Kind   string     `json:"kind" const:"landed"`
		Train  rt.TrainID `json:"train"`
		App    string     `json:"app" description:"The app of the landing that chose the train."`
		Branch string     `json:"branch" description:"The long-lived branch whose landing chose the train."`
	}
	rtLLBPlanned struct {
		Kind  string     `json:"kind" const:"planned"`
		Train rt.TrainID `json:"train"`
	}
	rtLLBFromEpic struct {
		Kind    string       `json:"kind" const:"epic"`
		Train   rt.TrainID   `json:"train"`
		Epic    string       `json:"epic" description:"The epic a train page lists that holds most of the group's cards."`
		Covered int          `json:"covered" description:"Distinct cards of the group under the epic."`
		Total   int          `json:"total" description:"Distinct cards of the group."`
		Also    []rt.TrainID `json:"also,omitempty" description:"Later unpublished trains whose pages list the same epic."`
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
	rtCodeNoMR struct {
		Kind     string    `json:"kind" const:"no_mr"`
		Note     string    `json:"note,omitempty"`
		MarkedBy string    `json:"markedBy" description:"Who marked it: app, cli or api, with the AO session when one was set."`
		MarkedAt time.Time `json:"markedAt"`
	}

	rtDest        struct{}
	rtDestToTrain struct {
		Kind  string     `json:"kind" const:"train"`
		Train rt.TrainID `json:"train"`
	}
	rtDestToLLB struct {
		Kind   string      `json:"kind" const:"llb"`
		Group  rt.PlanID   `json:"group"`
		Branch string      `json:"branch"`
		Via    *rt.TrainID `json:"via,omitempty" description:"The train the long-lived group landed in or targets."`
	}
	rtDestBefore struct {
		Kind  string     `json:"kind" const:"before"`
		Train rt.TrainID `json:"train" description:"The app's first train on the Board; the code shipped before it."`
	}
	rtDestOff struct {
		Kind string `json:"kind" const:"off"`
	}
	rtDestNone struct {
		Kind string `json:"kind" const:"none"`
	}
	rtDestNoMR struct {
		Kind string `json:"kind" const:"nomr"`
	}
)

// JSONSchemaOneOf lists the sprint destination variants.
func (rtDest) JSONSchemaOneOf() []interface{} {
	return []interface{}{rtDestToTrain{}, rtDestToLLB{}, rtDestBefore{}, rtDestOff{}, rtDestNone{}, rtDestNoMR{}}
}

// JSONSchemaOneOf lists the outcome variants.
func (rtOutcome) JSONSchemaOneOf() []interface{} {
	return []interface{}{rtOutcomeClean{}, rtOutcomeFired{}, rtOutcomeUnknown{}}
}

// JSONSchemaOneOf lists the code state variants.
func (rtCodeState) JSONSchemaOneOf() []interface{} {
	return []interface{}{rtCodeMerged{}, rtCodeOpen{}, rtCodeParked{}, rtCodeElsewhere{}, rtCodeNone{}, rtCodeNoMR{}}
}

// JSONSchemaOneOf lists the identity variants.
func (rtIdentity) JSONSchemaOneOf() []interface{} {
	return []interface{}{rtIdentityKnown{}, rtIdentityUnknown{}}
}

// JSONSchemaOneOf lists the viewer variants.
func (rtViewer) JSONSchemaOneOf() []interface{} {
	return []interface{}{rtViewerPerson{}, rtViewerUnknown{}}
}

// JSONSchemaOneOf lists the long-lived branch target variants.
func (rtLLBTarget) JSONSchemaOneOf() []interface{} {
	return []interface{}{rtLLBLandedIn{}, rtLLBPlanned{}, rtLLBFromEpic{}, rtLLBNoTarget{}, rtLLBDropped{}}
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
		"ReleasetrainPeopleReport":                   "ReleaseTrainPeopleReport",
		"ReleasetrainCardUnit":                       "ReleaseTrainCardUnit",
		"ReleasetrainCardEvidence":                   "ReleaseTrainCardEvidence",
		"ReleasetrainFixVersionEvidence":             "ReleaseTrainFixVersionEvidence",
		"ReleasetrainPageEvidence":                   "ReleaseTrainPageEvidence",
		"ReleasetrainSprintMREvidence":               "ReleaseTrainSprintMREvidence",
		"ReleasetrainCommitFact":                     "ReleaseTrainCommitFact",
		"ReleasetrainConfigReport":                   "ReleaseTrainConfigReport",
		"ReleasetrainDay":                            "ReleaseTrainDay",
		"ReleasetrainEvidence":                       "ReleaseTrainEvidence",
		"ReleasetrainForceUpdateFact":                "ReleaseTrainForceUpdateFact",
		"ReleasetrainForecast":                       "ReleaseTrainForecast",
		"ReleasetrainForecastStage":                  "ReleaseTrainForecastStage",
		"ReleasetrainForecastVersion":                "ReleaseTrainForecastVersion",
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
		"ReleasetrainKindFreshness":                  "ReleaseTrainKindFreshness",
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
		"SpecgenRtViewer":                            "ReleaseTrainViewer",
		"SpecgenRtViewerPerson":                      "ReleaseTrainViewerPerson",
		"SpecgenRtViewerUnknown":                     "ReleaseTrainViewerUnknown",
		"ReleasetrainBoardPerson":                    "ReleaseTrainBoardPerson",
		"ReleasetrainPersonMatch":                    "ReleaseTrainPersonMatch",
		"ReleasetrainMineReason":                     "ReleaseTrainMineReason",
		"ReleasetrainMineWhy":                        "ReleaseTrainMineWhy",
		"ReleasetrainMinePolicy":                     "ReleaseTrainMinePolicy",
		"SpecgenRtLLBLandedIn":                       "ReleaseTrainLLBLandedIn",
		"SpecgenRtLLBPlanned":                        "ReleaseTrainLLBPlanned",
		"SpecgenRtLLBFromEpic":                       "ReleaseTrainLLBFromEpic",
		"SpecgenRtLLBNoTarget":                       "ReleaseTrainLLBNoTarget",
		"SpecgenRtLLBDropped":                        "ReleaseTrainLLBDropped",
		"ReleasetrainLLBGroup":                       "ReleaseTrainLLBGroup",
		"ReleasetrainLLBCard":                        "ReleaseTrainLLBCard",
		"ReleasetrainMovedKey":                       "ReleaseTrainMovedKey",
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
		"ControllersReleaseTrainNoMRRequest":         "ReleaseTrainNoMRRequest",
		"ControllersReleaseTrainNoMRResponse":        "ReleaseTrainNoMRResponse",
		"ControllersReleaseTrainNoMRMarksResponse":   "ReleaseTrainNoMRMarksResponse",
		"ControllersReleaseTrainLlbPlanInput":        "ReleaseTrainLlbPlanInput",
		"ControllersReleaseTrainLlbPlanResponse":     "ReleaseTrainLlbPlanResponse",
		"ReleasetrainLlbPlan":                        "ReleaseTrainLlbPlan",
		"ReleasetrainPlanBranch":                     "ReleaseTrainPlanBranch",
		"ReleasetrainPlanAccepted":                   "ReleaseTrainPlanAccepted",
		"ReleasetrainTargetKind":                     "ReleaseTrainTargetKind",
		"ReleasetrainSprintRow":                      "ReleaseTrainSprintRow",
		"ReleasetrainSprintFact":                     "ReleaseTrainSprintFact",
		"ReleasetrainSprintState":                    "ReleaseTrainSprintState",
		"ReleasetrainSprintCounts":                   "ReleaseTrainSprintCounts",
		"ReleasetrainSprintDest":                     "ReleaseTrainSprintDest",
		"ReleasetrainSprintCard":                     "ReleaseTrainSprintCard",
		"ReleasetrainSprintSubtask":                  "ReleaseTrainSprintSubtask",
		"ReleasetrainSprintMR":                       "ReleaseTrainSprintMR",
		"ReleasetrainRoute":                          "ReleaseTrainRoute",
		"ReleasetrainDestKind":                       "ReleaseTrainDestKind",
		"ReleasetrainActiveWhy":                      "ReleaseTrainActiveWhy",
		"ReleasetrainBoardConfig":                    "ReleaseTrainBoardConfig",
		"SpecgenRtDest":                              "ReleaseTrainDest",
		"SpecgenRtDestToTrain":                       "ReleaseTrainDestToTrain",
		"SpecgenRtDestToLLB":                         "ReleaseTrainDestToLLB",
		"SpecgenRtDestBefore":                        "ReleaseTrainDestBefore",
		"SpecgenRtDestOff":                           "ReleaseTrainDestOff",
		"SpecgenRtDestNone":                          "ReleaseTrainDestNone",
		"SpecgenRtDestNoMR":                          "ReleaseTrainDestNoMR",
		"SpecgenRtCodeNoMR":                          "ReleaseTrainCodeNoMR",
		"ReleasetrainNoMRMark":                       "ReleaseTrainNoMRMark",
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
	js.AddTypeMapping((*rt.ViewerState)(nil), rtViewer{})
	js.AddTypeMapping((*rt.Dest)(nil), rtDest{})
	js.AddTypeMapping((*rt.Subject)(nil), rtSubject{})
	js.AddTypeMapping((*rt.Effect)(nil), rtEffect{})
	js.AddTypeMapping(rt.ShownRequest{}, rtShownRequest{})
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

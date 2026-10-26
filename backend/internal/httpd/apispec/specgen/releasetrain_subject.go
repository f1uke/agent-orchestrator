package specgen

import rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"

type (
	rtSubject        struct{}
	rtSubjectVersion struct {
		Kind  string     `json:"kind" const:"version"`
		Train rt.TrainID `json:"train"`
		App   string     `json:"app"`
	}
	rtSubjectVersionDate struct {
		Kind  string       `json:"kind" const:"versionDate"`
		Train rt.TrainID   `json:"train"`
		App   string       `json:"app"`
		Field rt.DateField `json:"field" enum:"freeze,publish,page"`
	}
	rtSubjectJiraVersion struct {
		Kind string `json:"kind" const:"jiraVersion"`
		ID   string `json:"id"`
		App  string `json:"app"`
		Name string `json:"name"`
	}
	rtSubjectMilestone struct {
		Kind  string `json:"kind" const:"milestone"`
		App   string `json:"app"`
		ID    int    `json:"id"`
		Title string `json:"title"`
	}
	rtSubjectHotfix struct {
		Kind    string `json:"kind" const:"hotfix"`
		App     string `json:"app"`
		Version string `json:"version"`
	}
	rtSubjectTrainPair struct {
		Kind string     `json:"kind" const:"trainPair"`
		A    rt.TrainID `json:"a"`
		B    rt.TrainID `json:"b"`
	}
	rtSubjectHotfixTrain struct {
		Kind    string     `json:"kind" const:"hotfixTrain"`
		App     string     `json:"app"`
		Version string     `json:"version"`
		Train   rt.TrainID `json:"train"`
	}
	rtSubjectCardOnTrain struct {
		Kind  string     `json:"kind" const:"cardOnTrain"`
		Train rt.TrainID `json:"train"`
		App   string     `json:"app"`
		Key   string     `json:"key"`
	}
	rtSubjectMR struct {
		Kind   string `json:"kind" const:"mr"`
		App    string `json:"app"`
		IID    int    `json:"iid"`
		Source string `json:"source" description:"The MR's source branch."`
		Target string `json:"target" description:"The MR's target branch."`
	}
	rtSubjectCard struct {
		Kind  string `json:"kind" const:"card"`
		Key   string `json:"key"`
		Board string `json:"board" description:"The board whose sprint row lists the card."`
	}
	rtSubjectPageFamily struct {
		Kind   string     `json:"kind" const:"pageFamily"`
		Train  rt.TrainID `json:"train"`
		Family string     `json:"family"`
		Bucket string     `json:"bucket" enum:"merged,open"`
		Keys   []string   `json:"keys"`
	}
	rtSubjectLLBBranch struct {
		Kind   string     `json:"kind" const:"llbBranch"`
		Train  rt.TrainID `json:"train"`
		App    string     `json:"app"`
		Branch string     `json:"branch"`
		Group  string     `json:"group" description:"The long-lived branch group whose plan sends the branch to the train."`
	}
)

// JSONSchemaOneOf lists the hit subject variants.
func (rtSubject) JSONSchemaOneOf() []interface{} {
	return []interface{}{
		rtSubjectVersion{}, rtSubjectVersionDate{}, rtSubjectJiraVersion{}, rtSubjectMilestone{}, rtSubjectHotfix{},
		rtSubjectTrainPair{}, rtSubjectHotfixTrain{}, rtSubjectCardOnTrain{}, rtSubjectMR{}, rtSubjectCard{},
		rtSubjectPageFamily{}, rtSubjectLLBBranch{},
	}
}

func init() {
	for name, clean := range map[string]string{
		"SpecgenRtSubject":            "ReleaseTrainSubject",
		"SpecgenRtSubjectVersion":     "ReleaseTrainSubjectVersion",
		"SpecgenRtSubjectVersionDate": "ReleaseTrainSubjectVersionDate",
		"SpecgenRtSubjectJiraVersion": "ReleaseTrainSubjectJiraVersion",
		"SpecgenRtSubjectMilestone":   "ReleaseTrainSubjectMilestone",
		"SpecgenRtSubjectHotfix":      "ReleaseTrainSubjectHotfix",
		"SpecgenRtSubjectTrainPair":   "ReleaseTrainSubjectTrainPair",
		"SpecgenRtSubjectHotfixTrain": "ReleaseTrainSubjectHotfixTrain",
		"SpecgenRtSubjectCardOnTrain": "ReleaseTrainSubjectCardOnTrain",
		"SpecgenRtSubjectMR":          "ReleaseTrainSubjectMR",
		"SpecgenRtSubjectCard":        "ReleaseTrainSubjectCard",
		"SpecgenRtSubjectPageFamily":  "ReleaseTrainSubjectPageFamily",
		"SpecgenRtSubjectLLBBranch":   "ReleaseTrainSubjectLLBBranch",
		"ReleasetrainSubjectKind":     "ReleaseTrainSubjectKind",
		"ReleasetrainDateField":       "ReleaseTrainDateField",
	} {
		schemaNames[name] = clean
	}
}

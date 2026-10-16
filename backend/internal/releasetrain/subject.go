package releasetrain

import "fmt"

// Subject is what a hit is about; its key, not its text, identifies the hit.
type Subject interface {
	Variant
	subjectKey() string
}

// KindOf is the subject's kind, the "kind" it carries on the wire.
func KindOf(s Subject) SubjectKind { return SubjectKind(s.Variant()) }

// SubjectKind names a Subject member on the wire; a rule declares the kinds its hits carry.
type SubjectKind string

// Subject kinds, one per Subject member.
const (
	SubjectVersion     SubjectKind = "version"
	SubjectVersionDate SubjectKind = "versionDate"
	SubjectJiraVersion SubjectKind = "jiraVersion"
	SubjectMilestone   SubjectKind = "milestone"
	SubjectHotfix      SubjectKind = "hotfix"
	SubjectTrainPair   SubjectKind = "trainPair"
	SubjectHotfixTrain SubjectKind = "hotfixTrain"
	SubjectCardOnTrain SubjectKind = "cardOnTrain"
	SubjectMR          SubjectKind = "mr"
	SubjectCard        SubjectKind = "card"
	SubjectPageFamily  SubjectKind = "pageFamily"
)

// SubjectKinds lists every subject kind, for the API schema.
var SubjectKinds = []SubjectKind{
	SubjectVersion, SubjectVersionDate, SubjectJiraVersion, SubjectMilestone, SubjectHotfix, SubjectTrainPair,
	SubjectHotfixTrain, SubjectCardOnTrain, SubjectMR, SubjectCard, SubjectPageFamily,
}

// Enum lists the subject kinds for the API schema.
func (SubjectKind) Enum() []any {
	out := make([]any, len(SubjectKinds))
	for i, k := range SubjectKinds {
		out[i] = k
	}
	return out
}

// SubjVersion is one app's version on a train.
type SubjVersion struct {
	Train TrainID `json:"train"`
	App   AppID   `json:"app"`
}

// SubjVersionDate is one date of an app's version that its train page disagrees with.
type SubjVersionDate struct {
	Train TrainID   `json:"train"`
	App   AppID     `json:"app"`
	Field DateField `json:"field"`
}

// DateField names which date a SubjVersionDate is about; DatePage is the whole page, unreadable.
type DateField string

// Date fields.
const (
	DateFreeze  DateField = "freeze"
	DatePublish DateField = "publish"
	DatePage    DateField = "page"
)

// SubjJiraVersion is a Jira version that joined no train or hotfix; App and Name do not take part in the hit's identity.
type SubjJiraVersion struct {
	ID   string `json:"id"`
	App  AppID  `json:"app"`
	Name string `json:"name"`
}

// SubjMilestone is a labelled milestone with no Jira version; Title does not take part in the hit's identity.
type SubjMilestone struct {
	App   AppID  `json:"app"`
	ID    int    `json:"id"`
	Title string `json:"title"`
}

// SubjHotfix is one app's hotfix.
type SubjHotfix struct {
	App     AppID  `json:"app"`
	Version string `json:"version"`
}

// SubjTrainPair is two overlapping trains.
type SubjTrainPair struct {
	A TrainID `json:"a"`
	B TrainID `json:"b"`
}

// SubjHotfixTrain is a hotfix inside a train window.
type SubjHotfixTrain struct {
	App     AppID   `json:"app"`
	Version string  `json:"version"`
	Train   TrainID `json:"train"`
}

// SubjCardOnTrain is one card on one app's version of a train.
type SubjCardOnTrain struct {
	Train TrainID `json:"train"`
	App   AppID   `json:"app"`
	Key   string  `json:"key"`
}

// SubjMR is one merge request; its branches do not take part in the hit's identity.
type SubjMR struct {
	App    AppID  `json:"app"`
	IID    int    `json:"iid"`
	Source string `json:"source"`
	Target string `json:"target"`
}

// SubjCard is one sprint card; Board, the board whose sprint row lists it, does not take part in the hit's identity.
type SubjCard struct {
	Key   string `json:"key"`
	Board string `json:"board"`
}

// SubjPageFamily is one family's train page and the cards missing from it,
// bucketed by whether their code merged; Keys does not take part in the hit's identity.
type SubjPageFamily struct {
	Train  TrainID  `json:"train"`
	Family FamilyID `json:"family"`
	Bucket string   `json:"bucket"`
	Keys   []string `json:"keys"`
}

func (s SubjVersion) subjectKey() string { return fmt.Sprintf("v/%s/%s", s.Train.Key(), s.App) }
func (s SubjVersionDate) subjectKey() string {
	return fmt.Sprintf("vd/%s/%s/%s", s.Train.Key(), s.App, s.Field)
}
func (s SubjJiraVersion) subjectKey() string { return "jv/" + s.ID }
func (s SubjMilestone) subjectKey() string   { return fmt.Sprintf("m/%s/%d", s.App, s.ID) }
func (s SubjHotfix) subjectKey() string      { return fmt.Sprintf("h/%s/%s", s.App, s.Version) }
func (s SubjTrainPair) subjectKey() string   { return fmt.Sprintf("tt/%s/%s", s.A.Key(), s.B.Key()) }
func (s SubjHotfixTrain) subjectKey() string {
	return fmt.Sprintf("ht/%s/%s/%s", s.App, s.Version, s.Train.Key())
}
func (s SubjCardOnTrain) subjectKey() string {
	return fmt.Sprintf("ct/%s/%s/%s", s.Train.Key(), s.App, s.Key)
}
func (s SubjMR) subjectKey() string   { return fmt.Sprintf("mr/%s/%d", s.App, s.IID) }
func (s SubjCard) subjectKey() string { return "c/" + s.Key }
func (s SubjPageFamily) subjectKey() string {
	return fmt.Sprintf("pf/%s/%s/%s", s.Train.Key(), s.Family, s.Bucket)
}

// Variant is "version".
func (SubjVersion) Variant() string { return string(SubjectVersion) }

// Variant is "versionDate".
func (SubjVersionDate) Variant() string { return string(SubjectVersionDate) }

// Variant is "jiraVersion".
func (SubjJiraVersion) Variant() string { return string(SubjectJiraVersion) }

// Variant is "milestone".
func (SubjMilestone) Variant() string { return string(SubjectMilestone) }

// Variant is "hotfix".
func (SubjHotfix) Variant() string { return string(SubjectHotfix) }

// Variant is "trainPair".
func (SubjTrainPair) Variant() string { return string(SubjectTrainPair) }

// Variant is "hotfixTrain".
func (SubjHotfixTrain) Variant() string { return string(SubjectHotfixTrain) }

// Variant is "cardOnTrain".
func (SubjCardOnTrain) Variant() string { return string(SubjectCardOnTrain) }

// Variant is "mr".
func (SubjMR) Variant() string { return string(SubjectMR) }

// Variant is "card".
func (SubjCard) Variant() string { return string(SubjectCard) }

// Variant is "pageFamily".
func (SubjPageFamily) Variant() string { return string(SubjectPageFamily) }

// MarshalJSON writes the subject with its "kind".
func (s SubjVersion) MarshalJSON() ([]byte, error) {
	type plain SubjVersion
	return tagged(s, plain(s))
}

// MarshalJSON writes the subject with its "kind".
func (s SubjVersionDate) MarshalJSON() ([]byte, error) {
	type plain SubjVersionDate
	return tagged(s, plain(s))
}

// MarshalJSON writes the subject with its "kind".
func (s SubjJiraVersion) MarshalJSON() ([]byte, error) {
	type plain SubjJiraVersion
	return tagged(s, plain(s))
}

// MarshalJSON writes the subject with its "kind".
func (s SubjMilestone) MarshalJSON() ([]byte, error) {
	type plain SubjMilestone
	return tagged(s, plain(s))
}

// MarshalJSON writes the subject with its "kind".
func (s SubjHotfix) MarshalJSON() ([]byte, error) { type plain SubjHotfix; return tagged(s, plain(s)) }

// MarshalJSON writes the subject with its "kind".
func (s SubjTrainPair) MarshalJSON() ([]byte, error) {
	type plain SubjTrainPair
	return tagged(s, plain(s))
}

// MarshalJSON writes the subject with its "kind".
func (s SubjHotfixTrain) MarshalJSON() ([]byte, error) {
	type plain SubjHotfixTrain
	return tagged(s, plain(s))
}

// MarshalJSON writes the subject with its "kind".
func (s SubjCardOnTrain) MarshalJSON() ([]byte, error) {
	type plain SubjCardOnTrain
	return tagged(s, plain(s))
}

// MarshalJSON writes the subject with its "kind".
func (s SubjMR) MarshalJSON() ([]byte, error) { type plain SubjMR; return tagged(s, plain(s)) }

// MarshalJSON writes the subject with its "kind".
func (s SubjCard) MarshalJSON() ([]byte, error) { type plain SubjCard; return tagged(s, plain(s)) }

// MarshalJSON writes the subject with its "kind".
func (s SubjPageFamily) MarshalJSON() ([]byte, error) {
	type plain SubjPageFamily
	return tagged(s, plain(s))
}

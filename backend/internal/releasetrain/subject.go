package releasetrain

import "fmt"

// Subject is what a hit is about; its key, not its text, identifies the hit.
type Subject interface{ subjectKey() string }

// SubjVersion is one app's version on a train.
type SubjVersion struct {
	Train TrainID
	App   AppID
}

// SubjVersionDate is one date of an app's version that its train page disagrees with.
type SubjVersionDate struct {
	Train TrainID
	App   AppID
	Field DateField
}

// DateField names which date a SubjVersionDate is about; DatePage is the whole page, unreadable.
type DateField string

// Date fields.
const (
	DateFreeze  DateField = "freeze"
	DatePublish DateField = "publish"
	DatePage    DateField = "page"
)

// SubjJiraVersion is a Jira version that joined no train or hotfix.
type SubjJiraVersion struct{ ID string }

// SubjMilestone is a labelled milestone with no Jira version.
type SubjMilestone struct {
	App AppID
	ID  int
}

// SubjHotfix is one app's hotfix.
type SubjHotfix struct {
	App     AppID
	Version string
}

// SubjTrainPair is two overlapping trains.
type SubjTrainPair struct{ A, B TrainID }

// SubjHotfixTrain is a hotfix inside a train window.
type SubjHotfixTrain struct {
	App     AppID
	Version string
	Train   TrainID
}

// SubjCardOnTrain is one card on one app's version of a train.
type SubjCardOnTrain struct {
	Train TrainID
	App   AppID
	Key   string
}

// SubjMR is one merge request.
type SubjMR struct {
	App AppID
	IID int
}

func (s SubjCardOnTrain) subjectKey() string {
	return fmt.Sprintf("ct/%s/%s/%s", s.Train.Key(), s.App, s.Key)
}
func (s SubjMR) subjectKey() string { return fmt.Sprintf("mr/%s/%d", s.App, s.IID) }

func (s SubjVersion) subjectKey() string     { return fmt.Sprintf("v/%s/%s", s.Train.Key(), s.App) }
func (s SubjJiraVersion) subjectKey() string { return "jv/" + s.ID }
func (s SubjVersionDate) subjectKey() string {
	return fmt.Sprintf("vd/%s/%s/%s", s.Train.Key(), s.App, s.Field)
}
func (s SubjMilestone) subjectKey() string { return fmt.Sprintf("m/%s/%d", s.App, s.ID) }
func (s SubjHotfix) subjectKey() string    { return fmt.Sprintf("h/%s/%s", s.App, s.Version) }
func (s SubjTrainPair) subjectKey() string { return fmt.Sprintf("tt/%s/%s", s.A.Key(), s.B.Key()) }
func (s SubjHotfixTrain) subjectKey() string {
	return fmt.Sprintf("ht/%s/%s/%s", s.App, s.Version, s.Train.Key())
}

// SubjCard is one sprint card.
type SubjCard struct{ Key string }

// SubjPageFamily is one family's train page and the cards missing from it,
// bucketed by whether their code merged; Keys does not take part in the hit's identity.
type SubjPageFamily struct {
	Train  TrainID
	Family FamilyID
	Bucket string
	Keys   []string
}

func (s SubjCard) subjectKey() string { return "c/" + s.Key }
func (s SubjPageFamily) subjectKey() string {
	return fmt.Sprintf("pf/%s/%s/%s", s.Train.Key(), s.Family, s.Bucket)
}

package releasetrain

import "fmt"

// Subject is what a hit is about; its key, not its text, identifies the hit.
type Subject interface{ subjectKey() string }

// SubjVersion is one app's version on a train.
type SubjVersion struct {
	Train TrainID
	App   AppID
}

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

func (s SubjVersion) subjectKey() string     { return fmt.Sprintf("v/%s/%s", s.Train.Key(), s.App) }
func (s SubjJiraVersion) subjectKey() string { return "jv/" + s.ID }
func (s SubjMilestone) subjectKey() string   { return fmt.Sprintf("m/%s/%d", s.App, s.ID) }
func (s SubjHotfix) subjectKey() string      { return fmt.Sprintf("h/%s/%s", s.App, s.Version) }
func (s SubjTrainPair) subjectKey() string   { return fmt.Sprintf("tt/%s/%s", s.A.Key(), s.B.Key()) }
func (s SubjHotfixTrain) subjectKey() string {
	return fmt.Sprintf("ht/%s/%s/%s", s.App, s.Version, s.Train.Key())
}

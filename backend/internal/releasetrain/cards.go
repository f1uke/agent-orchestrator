package releasetrain

// CodeState is where a card's code stands in one app's repo.
type CodeState interface{ codeState() }

// CodeMerged means the card's MRs reached the branch the train ships before the release cut.
type CodeMerged struct {
	MRs []int `json:"mrs"`
}

// CodeOpen means the card has an open MR toward the train.
type CodeOpen struct {
	MRs []int `json:"mrs"`
}

// CodeElsewhere means the card's code merged into a branch the train does not take.
type CodeElsewhere struct {
	Branch string `json:"branch"`
	On     Day    `json:"on"`
}

// CodeNone means the card has no MR in this repo.
type CodeNone struct{}

func (CodeMerged) codeState()    {}
func (CodeOpen) codeState()      {}
func (CodeElsewhere) codeState() {}
func (CodeNone) codeState()      {}

// CardUnit is one card on one app's version.
type CardUnit struct {
	Key            string    `json:"key"`
	App            AppID     `json:"app"`
	Status         string    `json:"status"`
	StatusCategory string    `json:"statusCategory"`
	Assignee       *Person   `json:"assignee,omitempty"`
	Code           CodeState `json:"code"`
}

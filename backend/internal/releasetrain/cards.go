package releasetrain

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

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

var (
	issueKeyRE     = regexp.MustCompile(`\b([A-Z][A-Z0-9]+-\d+)\b`)
	sprintBranchRE = regexp.MustCompile(`sprint-(?:(\d+)-(\d{4})|(\d{4})-(\d+))$`)
)

func mrKeys(mr MRFact) []string {
	if keys := issueKeyRE.FindAllString(mr.Title, -1); len(keys) > 0 {
		return keys
	}
	return issueKeyRE.FindAllString(mr.SourceBranch, -1)
}

func sprintOf(branch string) (int, bool) {
	m := sprintBranchRE.FindStringSubmatch(branch)
	if m == nil {
		return 0, false
	}
	n := m[1]
	if n == "" {
		n = m[4]
	}
	v, _ := strconv.Atoi(n)
	return v, true
}

func cardUnits(t *Train, v *AppVersion, cfg Config, snap Snapshot) []CardUnit {
	app, _ := cfg.app(v.App)
	byKey := map[string][]MRFact{}
	for _, mr := range Items(snap, KindMRs, ProjectScope(app.GitLabProject)) {
		for _, k := range mrKeys(mr) {
			byKey[k] = append(byKey[k], mr)
		}
	}
	var cut *Day
	if v.ReleaseMR != nil {
		c := v.ReleaseMR.MR.CreatedOn
		cut = &c
	}
	source, hasSource := 0, t.ID.Extra == 0
	if hasSource {
		source = t.ID.Number - 1
	}
	units := make([]CardUnit, 0, len(v.Issues))
	for _, is := range v.Issues {
		units = append(units, CardUnit{
			Key: is.Key, App: v.App, Status: is.Status, StatusCategory: is.StatusCategory, Assignee: is.Assignee,
			Code: codeOf(byKey[is.Key], cut, source, hasSource),
		})
	}
	return units
}

func codeOf(all []MRFact, cut *Day, source int, hasSource bool) CodeState {
	var mrs []MRFact
	for _, mr := range all {
		sprint, isSprint := sprintOf(mr.TargetBranch)
		towardTrain := mr.TargetBranch == "develop" || isSprint || strings.HasPrefix(mr.TargetBranch, "feature/")
		if towardTrain && (!isSprint || (hasSource && sprint == source)) {
			mrs = append(mrs, mr)
		}
	}
	sort.Slice(mrs, func(i, j int) bool {
		if mrs[i].CreatedOn != mrs[j].CreatedOn {
			return mrs[i].CreatedOn.Before(mrs[j].CreatedOn)
		}
		return mrs[i].IID < mrs[j].IID
	})
	var shipped, open []int
	var lastMerged *MRFact
	for i := range mrs {
		mr := mrs[i]
		if mr.MergedOn != nil {
			lastMerged = &mrs[i]
			_, isSprint := sprintOf(mr.TargetBranch)
			if (mr.TargetBranch == "develop" || isSprint) && (cut == nil || !mr.MergedOn.After(*cut)) {
				shipped = append(shipped, mr.IID)
			}
		}
		if mr.State == MROpened {
			open = append(open, mr.IID)
		}
	}
	switch {
	case len(shipped) > 0:
		return CodeMerged{MRs: shipped}
	case len(open) > 0:
		return CodeOpen{MRs: open}
	case lastMerged != nil:
		return CodeElsewhere{Branch: lastMerged.TargetBranch, On: *lastMerged.MergedOn}
	}
	return CodeNone{}
}

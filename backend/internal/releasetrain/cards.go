package releasetrain

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// CodeState is where a card's code stands in one app's repo.
type CodeState interface{ codeState() }

// CodeMerged means the card's MRs reached the branch the train ships before
// the release cut; Via names the long-lived branch that carried them there.
type CodeMerged struct {
	MRs []int  `json:"mrs"`
	Via string `json:"via,omitempty"`
}

// CodeParked means the card's MRs merged into a long-lived branch that has not landed.
type CodeParked struct {
	Branch string `json:"branch"`
	MRs    []int  `json:"mrs"`
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

// CodeNoMR means the card has no MR and a person marked it as needing none.
type CodeNoMR NoMRMark

func (CodeMerged) codeState()    {}
func (CodeOpen) codeState()      {}
func (CodeElsewhere) codeState() {}
func (CodeParked) codeState()    {}
func (CodeNone) codeState()      {}
func (CodeNoMR) codeState()      {}

// CardUnit is one card on one app's version.
type CardUnit struct {
	Key            string    `json:"key"`
	URL            string    `json:"url"`
	Summary        string    `json:"summary"`
	App            AppID     `json:"app"`
	Status         string    `json:"status"`
	StatusCategory string    `json:"statusCategory"`
	Assignee       *Person   `json:"assignee,omitempty"`
	Why            []CardWhy `json:"why"`
	// Evidence names the record behind each reason in Why, one key per reason.
	Evidence CardEvidence `json:"evidence"`
	// FactsFrom is the Jira read that gave the status, summary and assignee.
	FactsFrom KindName     `json:"factsFrom"`
	Code      CodeState    `json:"code"`
	Mine      []MineReason `json:"mine,omitempty"`
}

// CardEvidence is what matched for each reason a card is on a version.
type CardEvidence struct {
	FixVersion *FixVersionEvidence `json:"fixVersion,omitempty"`
	Page       *PageEvidence       `json:"page,omitempty"`
	SprintMR   *SprintMREvidence   `json:"sprintMR,omitempty"`
}

// FixVersionEvidence is the Jira release in the card's fixVersions.
type FixVersionEvidence struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// PageEvidence is the release train page that lists the card.
type PageEvidence struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// SprintMREvidence is the card's MRs into the sprint branch the train ships.
type SprintMREvidence struct {
	Branch string `json:"branch"`
	MRs    []int  `json:"mrs"`
}

// CardWhy is why a card is on a version.
type CardWhy string

// Reasons a card is on a version.
const (
	WhyFixVersion   CardWhy = "fixVersion"
	WhyPage         CardWhy = "release train page"
	WhySprintBranch CardWhy = "sprint branch"
)

// Enum lists the reasons for the API schema.
func (CardWhy) Enum() []any { return []any{WhyFixVersion, WhyPage, WhySprintBranch} }

type mrIndex map[AppID]map[string][]MRFact

func indexMRs(cfg Config, snap Snapshot) mrIndex {
	out := mrIndex{}
	for _, app := range cfg.Apps {
		byKey := map[string][]MRFact{}
		for _, mr := range Items(snap, KindMRs, ProjectScope(app.GitLabProject)) {
			for _, k := range mrKeys(mr) {
				byKey[k] = append(byKey[k], mr)
			}
		}
		out[app.ID] = byKey
	}
	return out
}

// pageMembers puts each card a train's pages list on the train's apps of the
// families whose page lists it: those whose Jira version is in the card's
// fixVersions, else those where the card has an MR, else all of them.
func pageMembers(t *Train, cfg Config, snap Snapshot, mrs mrIndex) map[AppID][]IssueFact {
	cards := map[string]IssueFact{}
	families := map[string][]FamilyID{}
	for _, p := range t.Pages {
		for _, is := range Items(snap, KindPageCards, PageScope(p.ID)) {
			if is.Subtask || is.ReleaseTicket {
				continue
			}
			cards[is.Key] = is
			families[is.Key] = append(families[is.Key], p.Family)
		}
	}
	out := map[AppID][]IssueFact{}
	for key, is := range cards {
		var apps []*AppVersion
		for _, v := range t.Versions {
			if slices.Contains(families[key], cfg.familyOf(v.App).ID) {
				apps = append(apps, v)
			}
		}
		for _, v := range pinned(apps, is, mrs) {
			out[v.App] = append(out[v.App], is)
		}
	}
	return out
}

func pinned(apps []*AppVersion, is IssueFact, mrs mrIndex) []*AppVersion {
	var byVersion, byMR []*AppVersion
	for _, v := range apps {
		if v.Jira != nil && slices.Contains(is.FixVersions, v.Jira.ID) {
			byVersion = append(byVersion, v)
		}
		if len(mrs[v.App][is.Key]) > 0 {
			byMR = append(byMR, v)
		}
	}
	switch {
	case len(byVersion) > 0:
		return byVersion
	case len(byMR) > 0:
		return byMR
	}
	return apps
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

func movedKeys(snap Snapshot) map[string]string {
	out := map[string]string{}
	for _, k := range []Kind[IssueFact]{KindLLBCards, KindBranchCards} {
		for _, is := range AllItems(snap, k) {
			if is.MovedTo != "" {
				out[is.Key] = is.MovedTo
			}
		}
	}
	return out
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

type branchMember struct {
	issue IssueFact
	mrs   SprintMREvidence
}

// branchMembers is the cards with an MR in v's app into the branch of the
// sprint before t; their Jira facts come from jira.branch_cards.
func branchMembers(t *Train, v *AppVersion, mrs mrIndex, jira map[string]IssueFact) []branchMember {
	if t.ID.Extra != 0 {
		return nil
	}
	var out []branchMember
	for key, list := range mrs[v.App] {
		is, known := jira[key]
		if !known || is.Subtask || is.ReleaseTicket {
			continue
		}
		var in []MRFact
		for _, mr := range list {
			if n, ok := sprintOf(mr.TargetBranch); ok && n == t.ID.Number-1 {
				in = append(in, mr)
			}
		}
		if len(in) == 0 {
			continue
		}
		sort.Slice(in, func(i, j int) bool { return in[i].IID < in[j].IID })
		ev := SprintMREvidence{Branch: in[0].TargetBranch, MRs: make([]int, len(in))}
		for i, mr := range in {
			ev.MRs[i] = mr.IID
		}
		out = append(out, branchMember{issue: is, mrs: ev})
	}
	return out
}

var whyKind = map[CardWhy]KindName{
	WhyFixVersion:   KindCards.Name,
	WhyPage:         KindPageCards.Name,
	WhySprintBranch: KindBranchCards.Name,
}

func cardUnits(t *Train, v *AppVersion, mrs mrIndex, fromPage []IssueFact, fromBranch []branchMember, llbs map[string]*LongLivedBranch, noMR map[string]NoMRMark) []CardUnit {
	byKey := mrs[v.App]
	var cut *Day
	if v.ReleaseMR != nil {
		c := v.ReleaseMR.MR.CreatedOn
		cut = &c
	}
	source, hasSource := 0, t.ID.Extra == 0
	if hasSource {
		source = t.ID.Number - 1
	}
	units := make([]CardUnit, 0, len(v.Issues)+len(fromPage))
	at := map[string]int{}
	add := func(is IssueFact, why CardWhy, mark func(*CardEvidence)) {
		i, ok := at[is.Key]
		if !ok {
			i = len(units)
			at[is.Key] = i
			code := codeOf(byKey[is.Key], cut, source, hasSource, llbs)
			if m, marked := noMR[is.Key]; marked && code == (CodeNone{}) {
				code = CodeNoMR(m)
			}
			units = append(units, CardUnit{
				Key: is.Key, URL: is.URL, Summary: is.Summary, App: v.App, Status: is.Status, StatusCategory: is.StatusCategory, Assignee: is.Assignee,
				FactsFrom: whyKind[why], Code: code,
			})
		}
		if !slices.Contains(units[i].Why, why) {
			units[i].Why = append(units[i].Why, why)
			mark(&units[i].Evidence)
		}
	}
	for _, is := range v.Issues {
		add(is, WhyFixVersion, func(e *CardEvidence) {
			e.FixVersion = &FixVersionEvidence{ID: v.Jira.ID, Name: v.Jira.Name, URL: v.Jira.URL}
		})
	}
	for _, is := range fromPage {
		add(is, WhyPage, func(e *CardEvidence) { e.Page = &PageEvidence{ID: v.page.ID, Title: v.page.Title, URL: v.page.URL} })
	}
	for _, m := range fromBranch {
		add(m.issue, WhySprintBranch, func(e *CardEvidence) { e.SprintMR = &m.mrs })
	}
	sort.Slice(units, func(i, j int) bool { return units[i].Key < units[j].Key })
	return units
}

func codeOf(all []MRFact, cut *Day, source int, hasSource bool, llbs map[string]*LongLivedBranch) CodeState {
	shipsIn := func(target string, merged Day) bool {
		sprint, isSprint := sprintOf(target)
		return (target == "develop" || (isSprint && hasSource && sprint == source)) && (cut == nil || !merged.After(*cut))
	}
	var mrs []MRFact
	for _, mr := range all {
		sprint, isSprint := sprintOf(mr.TargetBranch)
		towardTrain := mr.TargetBranch == "develop" || isSprint || strings.HasPrefix(mr.TargetBranch, "feature/") || llbs[mr.TargetBranch] != nil
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
			if shipsIn(mr.TargetBranch, *mr.MergedOn) {
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
		if b := llbs[lastMerged.TargetBranch]; b != nil {
			return parkedOrCarried(b, mrs, shipsIn, *lastMerged)
		}
		return CodeElsewhere{Branch: lastMerged.TargetBranch, On: *lastMerged.MergedOn}
	}
	return CodeNone{}
}

// A card merged into the branch after its landing missed that landing, so it stays parked.
func parkedOrCarried(b *LongLivedBranch, mrs []MRFact, shipsIn func(string, Day) bool, last MRFact) CodeState {
	var in []int
	for _, mr := range mrs {
		if mr.TargetBranch == b.Branch && mr.MergedOn != nil {
			in = append(in, mr.IID)
		}
	}
	l := b.Landing
	switch {
	case l == nil || l.MergedOn == nil || last.MergedOn.After(*l.MergedOn):
		return CodeParked{Branch: b.Branch, MRs: in}
	case shipsIn(l.TargetBranch, *l.MergedOn):
		return CodeMerged{MRs: in, Via: b.Branch}
	}
	return CodeElsewhere{Branch: b.Branch, On: *last.MergedOn}
}

package releasetrain

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// LLBState is where a long-lived branch stands.
type LLBState string

// Long-lived branch states. A landing into a sprint branch is right; into
// anything else it is right only before the app's cutover.
const (
	LLBOpen         LLBState = "open"
	LLBLanding      LLBState = "landing"
	LLBLandingWrong LLBState = "landing_wrong"
	LLBLanded       LLBState = "landed"
	LLBLandedWrong  LLBState = "landed_wrong"
	LLBStranded     LLBState = "stranded"
)

// Enum lists the states for the API schema.
func (LLBState) Enum() []any {
	return []any{LLBOpen, LLBLanding, LLBLandingWrong, LLBLanded, LLBLandedWrong, LLBStranded}
}

// LLBCard is one card an MR carried into a long-lived branch, with its Jira status.
type LLBCard struct {
	Key            string       `json:"key"`
	Summary        string       `json:"summary,omitempty"`
	Status         string       `json:"status,omitempty"`
	StatusCategory string       `json:"statusCategory,omitempty"`
	Assignee       *Person      `json:"assignee,omitempty"`
	Epic           string       `json:"epic,omitempty"`
	MR             MRFact       `json:"mr"`
	Mine           []MineReason `json:"mine,omitempty"`
}

// MovedKey is a card key an MR into the branch names that Jira moved to another project: the key is no card of the branch.
type MovedKey struct {
	Key     string `json:"key"`
	MovedTo string `json:"movedTo"`
	MR      MRFact `json:"mr"`
}

// LongLivedBranch is a branch card MRs merge into before it lands in a sprint branch.
type LongLivedBranch struct {
	App      AppID      `json:"app"`
	Branch   string     `json:"branch"`
	OpenedOn Day        `json:"openedOn"`
	LastIn   Day        `json:"lastIn"`
	Cards    []LLBCard  `json:"cards"`
	Moved    []MovedKey `json:"moved,omitempty"`
	Landing  *MRFact    `json:"landing,omitempty"`
	Ahead    *int       `json:"ahead,omitempty"`
	// Gone means GitLab no longer has the branch: it was deleted without landing.
	Gone  bool     `json:"gone,omitempty"`
	State LLBState `json:"state"`
	// Train is the train a merged landing rides.
	Train *TrainID `json:"train,omitempty"`
	// Auto means the plan does not name this branch: it joined as a twin of the branches the plan names.
	Auto bool `json:"auto,omitempty"`
	outs []MRFact
}

// LLBTarget is the train a long-lived branch group landed in or is meant for.
type LLBTarget interface{ llbTarget() }

// LLBLandedIn means every branch of the group landed; Train is where the first landing rides.
type LLBLandedIn struct {
	Train TrainID `json:"train"`
	// App and Branch name the landing that chose Train.
	App    AppID  `json:"app"`
	Branch string `json:"branch"`
}

// LLBPlanned means a plan names the train.
type LLBPlanned struct {
	Train TrainID `json:"train"`
}

// LLBFromEpic means no plan names a train, but an epic a train page lists
// holds more than half of the group's cards. Also names later unpublished
// trains whose pages list the same epic.
type LLBFromEpic struct {
	Train   TrainID   `json:"train"`
	Epic    string    `json:"epic"`
	Covered int       `json:"covered"`
	Total   int       `json:"total"`
	Also    []TrainID `json:"also,omitempty"`
}

// LLBNoTarget means nobody has said where the group goes.
type LLBNoTarget struct{}

// LLBDropped means a plan says the group will never ship.
type LLBDropped struct{}

func (LLBLandedIn) llbTarget() {}
func (LLBPlanned) llbTarget()  {}
func (LLBFromEpic) llbTarget() {}
func (LLBNoTarget) llbTarget() {}
func (LLBDropped) llbTarget()  {}

// LLBGroup is one feature's long-lived branches across apps, named by its plan when one exists.
type LLBGroup struct {
	ID       PlanID            `json:"id"`
	Name     string            `json:"name"`
	Plan     *LlbPlan          `json:"plan,omitempty"`
	Target   LLBTarget         `json:"target"`
	Branches []LongLivedBranch `json:"branches"`
	Mine     []MineReason      `json:"mine,omitempty"`
}

var (
	roleRE     = regexp.MustCompile(`^(develop|main|master|release/.*|hotfix/.*|.*sprint-(\d+-\d{4}|\d{4}-\d+))$`)
	stemRE     = regexp.MustCompile(`-?\d+(\.\d+)*$`)
	planUnsafe = regexp.MustCompile(`[^a-z0-9._-]+`)
)

// A branch named after a card is that card's own branch until a second MR
// goes in or one comes out.
func detectLLBs(cfg Config, snap Snapshot) ([]LongLivedBranch, map[AppID]Day) {
	jira := map[string]IssueFact{}
	for _, is := range AllItems(snap, KindLLBCards) {
		jira[is.Key] = is
	}
	var out []LongLivedBranch
	cutover := map[AppID]Day{}
	for _, app := range cfg.Apps {
		mrs := Items(snap, KindMRs, ProjectScope(app.GitLabProject))
		sortMRs(mrs, func(m MRFact) Day { return m.CreatedOn })
		targets := map[string]bool{}
		for _, m := range mrs {
			if _, sprint := sprintOf(m.TargetBranch); sprint && m.MergedOn != nil {
				if c, ok := cutover[app.ID]; !ok || m.MergedOn.Before(c) {
					cutover[app.ID] = *m.MergedOn
				}
			}
			if m.State != MRClosed && !roleRE.MatchString(m.TargetBranch) {
				targets[m.TargetBranch] = true
			}
		}
		names := make([]string, 0, len(targets))
		for n := range targets {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			var ins, outs []MRFact
			for _, m := range mrs {
				switch {
				case m.State == MRClosed:
				case m.TargetBranch == name:
					ins = append(ins, m)
				case m.SourceBranch == name && roleRE.MatchString(m.TargetBranch):
					outs = append(outs, m)
				}
			}
			if !longLived(name, ins, outs) {
				continue
			}
			out = append(out, llbOf(app, name, ins, outs, snap, jira, cutover))
		}
	}
	return out, cutover
}

func (br LongLivedBranch) cardKeys() []string {
	out := make([]string, 0, len(br.Cards))
	for _, c := range br.Cards {
		if !slices.Contains(out, c.Key) {
			out = append(out, c.Key)
		}
	}
	return out
}

func longLived(name string, ins, outs []MRFact) bool {
	own := issueKeyRE.FindAllString(name, -1)
	if len(own) == 0 {
		return len(ins) > 0 || len(outs) > 0
	}
	others := map[string]bool{}
	for _, m := range ins {
		for _, k := range mrKeys(m) {
			if !slices.Contains(own, k) {
				others[k] = true
			}
		}
	}
	return len(others) >= 2
}

func sortMRs(mrs []MRFact, at func(MRFact) Day) {
	sort.SliceStable(mrs, func(i, j int) bool {
		if a, b := at(mrs[i]), at(mrs[j]); a != b {
			return a.Before(b)
		}
		return mrs[i].IID < mrs[j].IID
	})
}

func lastTouch(m MRFact) Day {
	if m.MergedOn != nil {
		return *m.MergedOn
	}
	return m.CreatedOn
}

func llbOf(app App, name string, ins, outs []MRFact, snap Snapshot, jira map[string]IssueFact, cutover map[AppID]Day) LongLivedBranch {
	b := LongLivedBranch{App: app.ID, Branch: name, OpenedOn: ins[0].CreatedOn, Cards: []LLBCard{}, outs: outs}
	sortMRs(ins, lastTouch)
	b.LastIn = lastTouch(ins[len(ins)-1])
	for _, m := range ins {
		for _, k := range mrKeys(m) {
			if to := jira[k].MovedTo; to != "" {
				b.Moved = append(b.Moved, MovedKey{Key: k, MovedTo: to, MR: m})
				continue
			}
			c := LLBCard{Key: k, MR: m}
			if is, ok := jira[k]; ok {
				c.Summary, c.Status, c.StatusCategory, c.Assignee = is.Summary, is.Status, is.StatusCategory, is.Assignee
				if is.ParentType == IssueTypeEpic {
					c.Epic = is.Parent
				}
			}
			b.Cards = append(b.Cards, c)
		}
	}
	if len(outs) > 0 {
		sorted := append([]MRFact{}, outs...)
		sortMRs(sorted, lastTouch)
		landing := sorted[len(sorted)-1]
		b.Landing = &landing
	}
	if b.Landing == nil || b.Landing.MergedOn == nil {
		scope := CompareScope(app.GitLabProject, "develop", name)
		for _, c := range Items(snap, KindCompare, scope) {
			n := len(c.Commits)
			b.Ahead = &n
		}
		st, ok := snap.Status(KindCompare.Name, scope)
		b.Gone = ok && st.ErrorClass == ErrNotFound
	}
	b.State = llbState(b, ins, cutover[app.ID], hasDay(cutover, app.ID))
	return b
}

func hasDay(m map[AppID]Day, app AppID) bool { _, ok := m[app]; return ok }

func llbState(b LongLivedBranch, ins []MRFact, cutover Day, hasCutover bool) LLBState {
	l := b.Landing
	if l == nil {
		return LLBOpen
	}
	_, intoSprint := sprintOf(l.TargetBranch)
	if l.MergedOn == nil {
		if intoSprint {
			return LLBLanding
		}
		return LLBLandingWrong
	}
	for _, m := range ins {
		if m.MergedOn != nil && m.MergedOn.After(*l.MergedOn) {
			return LLBStranded
		}
	}
	if intoSprint || !hasCutover || l.MergedOn.Before(cutover) {
		return LLBLanded
	}
	return LLBLandedWrong
}

// A plan naming no branches takes the derived group with its id, so
// `ao train plan set <id> --target dropped` needs no --branch.
func groupLLBs(branches []LongLivedBranch, plans []LlbPlan, trains []Train, epics pageEpics, cfg Config) []LLBGroup {
	claimed := map[PlanBranch]bool{}
	used := map[string]bool{}
	for _, p := range plans {
		for _, pb := range p.Branches {
			claimed[pb] = true
		}
		if len(p.Branches) > 0 {
			used[string(p.ID)] = true
		}
	}
	twinOf := claimPlanTwins(branches, plans, claimed)
	type draft struct {
		id       string
		branches []LongLivedBranch
	}
	var drafts []*draft
	for _, b := range branches {
		if claimed[PlanBranch{App: b.App, Branch: b.Branch}] {
			continue
		}
		var into *draft
		for _, d := range drafts {
			if twins(d.branches, b) {
				into = d
				break
			}
		}
		if into == nil {
			into = &draft{}
			drafts = append(drafts, into)
		}
		into.branches = append(into.branches, b)
	}
	for _, d := range drafts {
		d.id = uniqueID(d.branches[0], used)
	}

	groups := make([]LLBGroup, 0, len(plans)+len(drafts))
	for i := range plans {
		p := plans[i]
		g := LLBGroup{ID: p.ID, Name: p.Name, Plan: &p}
		if len(p.Branches) == 0 {
			for j, d := range drafts {
				if d.id == string(p.ID) {
					g.Branches = d.branches
					drafts = append(drafts[:j], drafts[j+1:]...)
					break
				}
			}
		}
		for _, b := range branches {
			if inPlan(p, b, twinOf) {
				b.Auto = !containsBranch(p.Branches, b)
				g.Branches = append(g.Branches, b)
			}
		}
		groups = append(groups, g)
	}
	for _, d := range drafts {
		groups = append(groups, LLBGroup{ID: PlanID(d.id), Name: d.id, Branches: d.branches})
	}
	order := map[AppID]int{}
	for i, a := range cfg.Apps {
		order[a.ID] = i
	}
	for i := range groups {
		g := &groups[i]
		if g.Branches == nil {
			g.Branches = []LongLivedBranch{}
		}
		for bi := range g.Branches {
			if b := &g.Branches[bi]; b.Landing != nil && b.Landing.MergedOn != nil {
				b.Train = trainForLanding(*b, trains)
			}
		}
		sort.SliceStable(g.Branches, func(a, b int) bool {
			if x, y := order[g.Branches[a].App], order[g.Branches[b].App]; x != y {
				return x < y
			}
			return g.Branches[a].Branch < g.Branches[b].Branch
		})
		g.Target = llbTarget(*g, trains, epics)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	return groups
}

func claimPlanTwins(branches []LongLivedBranch, plans []LlbPlan, claimed map[PlanBranch]bool) map[PlanBranch]PlanID {
	twinOf := map[PlanBranch]PlanID{}
	for _, b := range branches {
		pb := PlanBranch{App: b.App, Branch: b.Branch}
		if claimed[pb] {
			continue
		}
		for _, p := range plans {
			var members []LongLivedBranch
			for _, x := range branches {
				if inPlan(p, x, twinOf) {
					members = append(members, x)
				}
			}
			if twins(members, b) {
				twinOf[pb], claimed[pb] = p.ID, true
				break
			}
		}
	}
	return twinOf
}

func inPlan(p LlbPlan, b LongLivedBranch, twinOf map[PlanBranch]PlanID) bool {
	id, ok := twinOf[PlanBranch{App: b.App, Branch: b.Branch}]
	return containsBranch(p.Branches, b) || ok && id == p.ID
}

func containsBranch(pbs []PlanBranch, b LongLivedBranch) bool {
	for _, pb := range pbs {
		if pb.App == b.App && pb.Branch == b.Branch {
			return true
		}
	}
	return false
}

func twins(group []LongLivedBranch, b LongLivedBranch) bool {
	keys := map[string]bool{}
	for _, c := range b.Cards {
		keys[c.Key] = true
	}
	shared := false
	for _, x := range group {
		if x.App == b.App {
			return false
		}
		if stem(x.Branch) == stem(b.Branch) {
			shared = true
		}
		for _, c := range x.Cards {
			if keys[c.Key] {
				shared = true
			}
		}
	}
	return shared
}

func stem(branch string) string {
	name := strings.TrimPrefix(branch, "feature/")
	if s := stemRE.ReplaceAllString(name, ""); s != "" {
		return s
	}
	return name
}

func uniqueID(b LongLivedBranch, used map[string]bool) string {
	for _, c := range []string{planSafe(stem(b.Branch)), planSafe(strings.TrimPrefix(b.Branch, "feature/")), planSafe(strings.TrimPrefix(b.Branch, "feature/") + "-" + string(b.App))} {
		if !used[c] {
			used[c] = true
			return c
		}
	}
	base := planSafe(strings.TrimPrefix(b.Branch, "feature/") + "-" + string(b.App))
	for n := 2; ; n++ {
		if c := base + "-" + strconv.Itoa(n); !used[c] {
			used[c] = true
			return c
		}
	}
}

func planSafe(s string) string {
	s = strings.TrimLeft(planUnsafe.ReplaceAllString(strings.ToLower(s), "-"), "._-")
	if len(s) > 60 {
		s = s[:60]
	}
	if s == "" {
		return "llb"
	}
	return s
}

// A plan naming a train or dropped beats the epic; an unplanned plan does not.
func llbTarget(g LLBGroup, trains []Train, epics pageEpics) LLBTarget {
	var landedIn *LLBLandedIn
	allMerged := len(g.Branches) > 0
	for _, b := range g.Branches {
		if t := trainForLanding(b, trains); t != nil && landedIn == nil {
			landedIn = &LLBLandedIn{Train: *t, App: b.App, Branch: b.Branch}
		}
		if b.Landing == nil || b.Landing.MergedOn == nil {
			allMerged = false
		}
	}
	if landedIn != nil && allMerged {
		return *landedIn
	}
	if g.Plan != nil {
		switch g.Plan.TargetKind {
		case TargetDropped:
			return LLBDropped{}
		case TargetTrain:
			if g.Plan.TargetTrain != nil {
				return LLBPlanned{Train: *g.Plan.TargetTrain}
			}
		}
	}
	if t, ok := epicTarget(g, epics); ok {
		return t
	}
	return LLBNoTarget{}
}

type pageEpics map[string][]TrainID

func pageEpicsOf(trains []Train, snap Snapshot, today Day) pageEpics {
	out := pageEpics{}
	for _, t := range trains {
		if t.Publish != nil && t.Publish.Before(today) {
			continue
		}
		for _, p := range t.Pages {
			for _, is := range Items(snap, KindPageCards, PageScope(p.ID)) {
				if is.IssueType == IssueTypeEpic && !slices.Contains(out[is.Key], t.ID) {
					out[is.Key] = append(out[is.Key], t.ID)
				}
			}
		}
	}
	return out
}

func epicTarget(g LLBGroup, epics pageEpics) (LLBFromEpic, bool) {
	epicOf := map[string]string{}
	for _, b := range g.Branches {
		for _, c := range b.Cards {
			if epicOf[c.Key] == "" {
				epicOf[c.Key] = c.Epic
			}
		}
	}
	covered := map[string]int{}
	for _, e := range epicOf {
		if e != "" {
			covered[e]++
		}
	}
	for e, n := range covered {
		trains := epics[e]
		if 2*n <= len(epicOf) || len(trains) == 0 {
			continue
		}
		t := LLBFromEpic{Train: trains[0], Epic: e, Covered: n, Total: len(epicOf)}
		if len(trains) > 1 {
			t.Also = slices.Clone(trains[1:])
		}
		return t, true
	}
	return LLBFromEpic{}, false
}

// Sprint N's branch ships in train N+1; any other target ships in the
// app's first train that freezes on or after the landing.
func trainForLanding(b LongLivedBranch, trains []Train) *TrainID {
	l := b.Landing
	if l == nil {
		return nil
	}
	if id, ok := sprintTrain(l.TargetBranch); ok {
		for i := range trains {
			if trains[i].ID == id {
				return &trains[i].ID
			}
		}
		return nil
	}
	when := lastTouch(*l)
	var best *Train
	for i := range trains {
		t := &trains[i]
		if t.version(b.App) == nil || t.Freeze == nil || t.Freeze.Before(when) {
			continue
		}
		if best == nil || t.Freeze.Before(*best.Freeze) {
			best = t
		}
	}
	if best == nil {
		return nil
	}
	return &best.ID
}

func sprintTrain(branch string) (TrainID, bool) {
	m := sprintBranchRE.FindStringSubmatch(branch)
	if m == nil {
		return TrainID{}, false
	}
	n, year := m[1], m[2]
	if n == "" {
		n, year = m[4], m[3]
	}
	num, _ := strconv.Atoi(n)
	y, _ := strconv.Atoi(year)
	return TrainID{Number: num + 1, Year: y}, true
}

func groupsByBranch(groups []LLBGroup) map[AppID]map[string]*LLBGroup {
	out := map[AppID]map[string]*LLBGroup{}
	for i := range groups {
		for _, br := range groups[i].Branches {
			if out[br.App] == nil {
				out[br.App] = map[string]*LLBGroup{}
			}
			out[br.App][br.Branch] = &groups[i]
		}
	}
	return out
}

func llbsByApp(groups []LLBGroup) map[AppID]map[string]*LongLivedBranch {
	out := map[AppID]map[string]*LongLivedBranch{}
	for gi := range groups {
		for bi := range groups[gi].Branches {
			b := &groups[gi].Branches[bi]
			if out[b.App] == nil {
				out[b.App] = map[string]*LongLivedBranch{}
			}
			out[b.App][b.Branch] = b
		}
	}
	return out
}

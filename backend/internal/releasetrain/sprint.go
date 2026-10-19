package releasetrain

import (
	"regexp"
	"sort"
	"strings"
)

// ActiveWhy is why a sprint card counts as work of this sprint.
type ActiveWhy string

// Reasons a sprint card is active; a card with none is carried over.
const (
	ActiveNew         ActiveWhy = "new this sprint"
	ActiveMR          ActiveWhy = "MR this sprint"
	ActiveSubtaskOpen ActiveWhy = "mobile subtask open"
)

// Enum lists the reasons for the API schema.
func (ActiveWhy) Enum() []any { return []any{ActiveNew, ActiveMR, ActiveSubtaskOpen} }

// StatusAbandoned is the Jira status of a card nobody will finish; sprint counts and G2 skip it.
const StatusAbandoned = "Abandoned"

// SprintRow is one configured board's active sprint and its cards that touch the mobile apps.
type SprintRow struct {
	Board  BoardConfig  `json:"board"`
	Sprint SprintFact   `json:"sprint"`
	Number int          `json:"number"`
	Total  int          `json:"total"`
	Cards  []SprintCard `json:"cards"`
	Counts SprintCounts `json:"counts"`
	Dests  []SprintDest `json:"dests"`
}

// SprintCounts is the board label's second line. Abandoned cards are not counted.
type SprintCounts struct {
	New            int `json:"new"`
	CarriedWithMRs int `json:"carriedWithMrs"`
	CarriedDone    int `json:"carriedDone"`
}

// SprintDest is how many active cards go to one destination; a card whose apps split counts in each.
type SprintDest struct {
	To    Dest     `json:"to"`
	Count int      `json:"count"`
	Keys  []string `json:"keys"`
}

// SprintCard is one card of a sprint row with its MRs and where each app's code goes.
type SprintCard struct {
	Key            string          `json:"key"`
	URL            string          `json:"url"`
	Summary        string          `json:"summary"`
	Status         string          `json:"status"`
	StatusCategory string          `json:"statusCategory"`
	Assignee       *Person         `json:"assignee,omitempty"`
	Subtasks       []SprintSubtask `json:"subtasks"`
	MRs            []SprintMR      `json:"mrs"`
	Routes         []Route         `json:"routes"`
	OnBoard        bool            `json:"onBoard"`
	Active         bool            `json:"active"`
	ActiveWhy      []ActiveWhy     `json:"activeWhy"`
	Mine           []MineReason    `json:"mine,omitempty"`
	NoMR           *NoMRMark       `json:"noMr,omitempty"`
}

// SprintSubtask is a subtask of a sprint card; Mobile when its summary matches the configured pattern.
type SprintSubtask struct {
	Key            string  `json:"key"`
	Summary        string  `json:"summary"`
	Status         string  `json:"status"`
	StatusCategory string  `json:"statusCategory"`
	Assignee       *Person `json:"assignee,omitempty"`
	Mobile         bool    `json:"mobile"`
}

func (c SprintCard) abandoned() bool { return c.Status == StatusAbandoned }

type sprintIssues struct {
	sprint   SprintFact
	number   int
	year     int
	cards    []IssueFact
	subtasks map[string][]IssueFact
	parentOf map[string]string
	inSprint map[string]bool
	newHere  map[string]bool
}

func readSprint(snap Snapshot, s SprintFact) sprintIssues {
	si := sprintIssues{sprint: s, subtasks: map[string][]IssueFact{}, parentOf: map[string]string{}, inSprint: map[string]bool{}, newHere: map[string]bool{}}
	si.number, si.year, _ = sprintNumber(s)
	for _, is := range Items(snap, KindSprintCards, SprintScope(s.ID)) {
		if is.Subtask {
			si.subtasks[is.Parent] = append(si.subtasks[is.Parent], is)
			si.parentOf[is.Key] = is.Parent
			continue
		}
		si.cards = append(si.cards, is)
		si.inSprint[is.Key] = true
	}
	for _, m := range Items(snap, KindThisSprint, SprintScope(s.ID)) {
		si.newHere[m.Key] = true
	}
	return si
}

// sprintMRs indexes every non-closed MR by the cards it names in its title or
// source branch, a subtask's key standing for its parent.
func sprintMRs(cfg Config, snap Snapshot, parentOf map[string]string) map[string][]SprintMR {
	out := map[string][]SprintMR{}
	for _, app := range cfg.Apps {
		for _, mr := range Items(snap, KindMRs, ProjectScope(app.GitLabProject)) {
			if mr.State == MRClosed {
				continue
			}
			keys := map[string]bool{}
			for _, k := range append(mrKeys(mr), issueKeyRE.FindAllString(mr.SourceBranch, -1)...) {
				if p, ok := parentOf[k]; ok {
					k = p
				}
				keys[k] = true
			}
			for k := range keys {
				out[k] = append(out[k], SprintMR{
					App: app.ID, IID: mr.IID, URL: mr.URL, Title: mr.Title, State: mr.State, Source: mr.SourceBranch,
					Target: mr.TargetBranch, Author: mr.Author, Created: mr.CreatedOn, Merged: mr.MergedOn, username: mr.AuthorUsername,
				})
			}
		}
	}
	order := map[AppID]int{}
	for i, a := range cfg.Apps {
		order[a.ID] = i
	}
	for _, list := range out {
		sort.Slice(list, func(i, j int) bool {
			if list[i].App != list[j].App {
				return order[list[i].App] < order[list[j].App]
			}
			return list[i].IID < list[j].IID
		})
	}
	return out
}

func (si sprintIssues) offBoardKeys(mrs map[string][]SprintMR) []string {
	var out []string
	for k, list := range mrs {
		if si.inSprint[k] {
			continue
		}
		for _, m := range list {
			if n, ok := sprintOf(m.Target); ok && n == si.number {
				out = append(out, k)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// sprintBranchKeys is the owning board's off-board cards plus the cards with
// an MR into the sprint N-1 branch of each live train N.
func sprintBranchKeys(cfg Config, snap Snapshot, liveTrains []TrainID) []string {
	keys := map[string]bool{}
	for _, b := range cfg.Boards {
		s, ok := activeSprint(snap, b.ID)
		if !b.OwnsSprintBranch || !ok {
			continue
		}
		si := readSprint(snap, s)
		for _, k := range si.offBoardKeys(sprintMRs(cfg, snap, si.parentOf)) {
			keys[k] = true
		}
	}
	sources := map[int]bool{}
	for _, t := range liveTrains {
		if t.Extra == 0 {
			sources[t.Number-1] = true
		}
	}
	for _, app := range cfg.Apps {
		for _, mr := range Items(snap, KindMRs, ProjectScope(app.GitLabProject)) {
			if n, ok := sprintOf(mr.TargetBranch); ok && sources[n] {
				for _, k := range mrKeys(mr) {
					keys[k] = true
				}
			}
		}
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func branchCards(snap Snapshot) map[string]IssueFact {
	out := map[string]IssueFact{}
	for _, is := range AllItems(snap, KindBranchCards) {
		out[is.Key] = is
	}
	return out
}

// sprintRows builds one row per configured board with an active sprint; a card
// that is on one board's sprint shows only there.
func sprintRows(cfg Config, snap Snapshot, base routing) []SprintRow {
	mobileSub := regexp.MustCompile(cfg.Mobile.SubtaskPattern)
	mobileProject := map[string]bool{}
	for _, p := range cfg.Mobile.Projects {
		mobileProject[p] = true
	}
	extra := branchCards(snap)
	owner, owned := branchOwner(cfg, snap)
	var rows []SprintRow
	onBoard := map[string]int{}
	for _, b := range cfg.Boards {
		s, ok := activeSprint(snap, b.ID)
		if !ok {
			continue
		}
		si := readSprint(snap, s)
		mrs := sprintMRs(cfg, snap, si.parentOf)
		r := base
		r.sprint, r.year = si.number, si.year
		if owned {
			r.sprint, r.year = owner.number, owner.year
		}
		if s.Start != nil {
			r.start = *s.Start
		}
		issues := append([]IssueFact{}, si.cards...)
		if b.OwnsSprintBranch {
			for _, k := range si.offBoardKeys(mrs) {
				if is, ok := extra[k]; ok && !is.Subtask {
					issues = append(issues, is)
				}
			}
		}
		row := SprintRow{Board: b, Sprint: s, Number: si.number, Total: len(si.cards), Cards: []SprintCard{}}
		for _, is := range issues {
			c, ok := sprintCard(is, si, mrs[is.Key], mobileSub, mobileProject, r, cfg)
			if !ok {
				continue
			}
			if c.OnBoard {
				onBoard[c.Key]++
			}
			row.Cards = append(row.Cards, c)
		}
		rows = append(rows, row)
	}
	for i := range rows {
		kept := rows[i].Cards[:0]
		for _, c := range rows[i].Cards {
			if c.OnBoard || onBoard[c.Key] == 0 {
				kept = append(kept, c)
			}
		}
		rows[i].Cards = kept
		sort.Slice(rows[i].Cards, func(a, b int) bool { return keyLess(rows[i].Cards[a].Key, rows[i].Cards[b].Key) })
		rows[i].Counts, rows[i].Dests = rowTallies(rows[i].Cards)
	}
	if rows == nil {
		rows = []SprintRow{}
	}
	return rows
}

// branchOwner is the active sprint of the board that owns the sprint branch:
// its number decides where every board's sprint-branch MRs go.
func branchOwner(cfg Config, snap Snapshot) (sprintIssues, bool) {
	for _, b := range cfg.Boards {
		if s, ok := activeSprint(snap, b.ID); ok && b.OwnsSprintBranch {
			return readSprint(snap, s), true
		}
	}
	return sprintIssues{}, false
}

func sprintCard(is IssueFact, si sprintIssues, mrs []SprintMR, mobileSub *regexp.Regexp, mobileProject map[string]bool, r routing, cfg Config) (SprintCard, bool) {
	c := SprintCard{
		Key: is.Key, URL: is.URL, Summary: is.Summary, Status: is.Status, StatusCategory: is.StatusCategory, Assignee: is.Assignee,
		Subtasks: []SprintSubtask{}, MRs: append([]SprintMR{}, mrs...), OnBoard: si.inSprint[is.Key], ActiveWhy: []ActiveWhy{},
	}
	var mobile []SprintSubtask
	for _, st := range si.subtasks[is.Key] {
		sub := SprintSubtask{Key: st.Key, Summary: st.Summary, Status: st.Status, StatusCategory: st.StatusCategory, Assignee: st.Assignee, Mobile: mobileSub.MatchString(st.Summary)}
		c.Subtasks = append(c.Subtasks, sub)
		if sub.Mobile {
			mobile = append(mobile, sub)
		}
	}
	project, _, _ := strings.Cut(is.Key, "-")
	if !mobileProject[project] && len(mrs) == 0 && len(mobile) == 0 {
		return SprintCard{}, false
	}
	isNew, openSub, mrNow := si.newHere[is.Key], false, false
	for _, st := range mobile {
		isNew = isNew || si.newHere[st.Key]
		openSub = openSub || st.StatusCategory != StatusCategoryDone
	}
	for _, m := range mrs {
		mrNow = mrNow || m.State == MROpened || !m.Created.Before(r.start) || (m.Merged != nil && !m.Merged.Before(r.start))
	}
	for _, w := range []struct {
		on  bool
		why ActiveWhy
	}{{isNew, ActiveNew}, {mrNow, ActiveMR}, {openSub, ActiveSubtaskOpen}} {
		if w.on {
			c.ActiveWhy = append(c.ActiveWhy, w.why)
		}
	}
	c.Active = len(c.ActiveWhy) > 0
	if m, ok := r.noMR[is.Key]; ok {
		c.NoMR = &m
	}
	c.Routes = []Route{}
	for _, app := range cfg.Apps {
		route := r.RouteOf(is.Key, app.ID, c.MRs)
		if route.Kind == DestNone {
			continue
		}
		c.Routes = append(c.Routes, route)
		for i := range c.MRs {
			if c.MRs[i].App == app.ID {
				c.MRs[i].Dest = route.Kind
			}
		}
	}
	return c, true
}

func rowTallies(cards []SprintCard) (SprintCounts, []SprintDest) {
	var counts SprintCounts
	byKey := map[string]*SprintDest{}
	var order []string
	add := func(to Dest, key string) {
		d := byKey[to.destKey()]
		if d == nil {
			d = &SprintDest{To: to}
			byKey[to.destKey()] = d
			order = append(order, to.destKey())
		}
		if len(d.Keys) == 0 || d.Keys[len(d.Keys)-1] != key {
			d.Keys = append(d.Keys, key)
			d.Count++
		}
	}
	for _, c := range cards {
		switch {
		case c.abandoned():
			continue
		case !c.Active:
			counts.CarriedDone++
			continue
		case c.ActiveWhy[0] == ActiveNew:
			counts.New++
		default:
			counts.CarriedWithMRs++
		}
		switch {
		case len(c.Routes) > 0:
		case c.NoMR != nil:
			add(NoMRNeeded{}, c.Key)
		default:
			add(NotRouted{}, c.Key)
		}
		for _, r := range c.Routes {
			add(r.To, c.Key)
		}
	}
	dests := make([]SprintDest, 0, len(order))
	for _, k := range order {
		dests = append(dests, *byKey[k])
	}
	sort.SliceStable(dests, func(i, j int) bool {
		return destRank(dests[i].To) < destRank(dests[j].To) || destRank(dests[i].To) == destRank(dests[j].To) && destLess(dests[i].To, dests[j].To)
	})
	return counts, dests
}

func destRank(d Dest) int {
	switch d.(type) {
	case BeforeFirstTrain:
		return 0
	case ToTrain:
		return 1
	case ToLLB:
		return 2
	case OffFlow:
		return 3
	case NotRouted:
		return 4
	}
	return 5
}

func destLess(a, b Dest) bool {
	x, okA := a.(ToTrain)
	y, okB := b.(ToTrain)
	if okA && okB {
		return x.Train.less(y.Train)
	}
	return a.destKey() < b.destKey()
}

func keyLess(a, b string) bool {
	pa, na, _ := strings.Cut(a, "-")
	pb, nb, _ := strings.Cut(b, "-")
	if pa != pb {
		return pa < pb
	}
	if len(na) != len(nb) {
		return len(na) < len(nb)
	}
	return na < nb
}

// HasCard says whether key is a sprint card or a card on a train.
func (b *Board) HasCard(key string) bool {
	if b.sprintCard(key) != nil {
		return true
	}
	for i := range b.Trains {
		for _, v := range b.Trains[i].Versions {
			for _, c := range v.Cards {
				if c.Key == key {
					return true
				}
			}
		}
	}
	return false
}

func (b *Board) sprintCard(key string) *SprintCard {
	for i := range b.Sprints {
		for j := range b.Sprints[i].Cards {
			if b.Sprints[i].Cards[j].Key == key {
				return &b.Sprints[i].Cards[j]
			}
		}
	}
	return nil
}

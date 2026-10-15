package releasetrain

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// DestKind is where one app's MRs of a sprint card went, as the sprint row colours it.
type DestKind string

// Destination kinds, from d4's DEST table.
const (
	DestSprint      DestKind = "sprint"
	DestSprintOpen  DestKind = "sprint_open"
	DestLLB         DestKind = "llb"
	DestLLBOpen     DestKind = "llb_open"
	DestPrev        DestKind = "prev"
	DestBack        DestKind = "back"
	DestBefore      DestKind = "before"
	DestDevelop     DestKind = "develop"
	DestDevelopOpen DestKind = "develop_open"
	DestRelease     DestKind = "release"
	DestOther       DestKind = "other"
	DestNone        DestKind = "none"
)

// Enum lists the destination kinds for the API schema.
func (DestKind) Enum() []any {
	return []any{DestSprint, DestSprintOpen, DestLLB, DestLLBOpen, DestPrev, DestBack, DestBefore, DestDevelop, DestDevelopOpen, DestRelease, DestOther, DestNone}
}

// Dest is the train or branch a card's code goes to.
type Dest interface {
	dest()
	destKey() string
}

// ToTrain means the code rides Train.
type ToTrain struct {
	Train TrainID `json:"train"`
}

// ToLLB means the code sits in a long-lived branch; Via is the train its group landed in or targets.
type ToLLB struct {
	Group  PlanID   `json:"group"`
	Branch string   `json:"branch"`
	Via    *TrainID `json:"via,omitempty"`
}

// BeforeFirstTrain means the code shipped before Train, the app's first train on the Board.
type BeforeFirstTrain struct {
	Train TrainID `json:"train"`
}

// OffFlow means the code went somewhere no train takes: develop after cutover, or another branch.
type OffFlow struct{}

// NotRouted means the card has no MR in this app.
type NotRouted struct{}

func (ToTrain) dest()          {}
func (ToLLB) dest()            {}
func (BeforeFirstTrain) dest() {}
func (OffFlow) dest()          {}
func (NotRouted) dest()        {}

func (d ToTrain) destKey() string          { return "t:" + d.Train.Key() }
func (d ToLLB) destKey() string            { return "l:" + string(d.Group) }
func (d BeforeFirstTrain) destKey() string { return "early" }
func (OffFlow) destKey() string            { return "off" }
func (NotRouted) destKey() string          { return "none" }

// Route is where one app's code of one sprint card goes.
type Route struct {
	App  AppID    `json:"app"`
	Kind DestKind `json:"kind"`
	To   Dest     `json:"to"`
	// MR is the app's first MR into the branch the destination came from.
	MR int `json:"mr"`
	// ViaLLB is the landed long-lived branch that carried the code into the train.
	ViaLLB string `json:"viaLlb,omitempty"`
	// Plan is the train whose page or fixVersion lists the card.
	Plan     *TrainID `json:"plan,omitempty"`
	Mismatch bool     `json:"mismatch,omitempty"`
}

// SprintMR is one MR of a sprint card, with its app's destination kind.
type SprintMR struct {
	App      AppID    `json:"app"`
	IID      int      `json:"iid"`
	URL      string   `json:"url"`
	Title    string   `json:"title"`
	State    string   `json:"state"`
	Source   string   `json:"source"`
	Target   string   `json:"target"`
	Author   string   `json:"author"`
	Created  Day      `json:"created"`
	Merged   *Day     `json:"merged,omitempty"`
	Dest     DestKind `json:"dest"`
	username string
}

type routing struct {
	sprint  int
	year    int
	start   Day
	cutover map[AppID]Day
	llbs    map[AppID]map[string]*LongLivedBranch
	groups  map[AppID]map[string]*LLBGroup
	trains  []Train
	plans   map[string]TrainID
}

var (
	trunkTargetRE  = regexp.MustCompile(`^(develop|master)$`)
	releaseRE      = regexp.MustCompile(`^(main|release/|hotfix/)`)
	releaseVerRE   = regexp.MustCompile(`^release/(?:release-)?v?(\d+\.\d+\.\d+)$`)
	sprintNameRE   = regexp.MustCompile(`(?:(\d{4})-(\d+)|(\d+)-(\d{4})|(\d+))$`)
	earlyThreshold = 14
)

// sprintNumber reads a sprint's number and year from its name ("DEMO 2026-7",
// "DEMO 7-2026" or "DEMO 7"); a bare number takes the year it started.
func sprintNumber(s SprintFact) (int, int, bool) {
	m := sprintNameRE.FindStringSubmatch(strings.TrimSpace(s.Name))
	if m == nil {
		return 0, 0, false
	}
	atoi := func(x string) int { n, _ := strconv.Atoi(x); return n }
	switch {
	case m[1] != "":
		return atoi(m[2]), atoi(m[1]), true
	case m[3] != "":
		return atoi(m[3]), atoi(m[4]), true
	}
	year := 0
	if s.Start != nil {
		year = s.Start.Y
	}
	return atoi(m[5]), year, true
}

// destOf ports d4's destOf: the best-ranked merged MR wins, else the best-ranked open one.
func (r routing) destOf(app AppID, mrs []SprintMR) (DestKind, *SprintMR) {
	cut, ok := r.cutover[app]
	if !ok {
		cut = r.start
	}
	type scored struct {
		mr   *SprintMR
		kind DestKind
		rank int
	}
	rank := func(m *SprintMR) (DestKind, int) {
		open := m.State == MROpened
		pick := func(merged, opened DestKind) DestKind {
			if open {
				return opened
			}
			return merged
		}
		n, isSprint := sprintOf(m.Target)
		lb := r.llbs[app][m.Target]
		_, fromSprint := sprintOf(m.Source)
		switch {
		case lb != nil && (lb.Landing == nil || lb.Landing.MergedOn == nil):
			return pick(DestLLB, DestLLBOpen), 2
		case !open && m.Merged != nil && m.Merged.Before(r.start):
			return DestBefore, 7
		case fromSprint:
			return DestBack, 3
		case !open && trunkTargetRE.MatchString(m.Target) && m.Merged != nil && m.Merged.Before(cut):
			return DestBefore, 7
		case isSprint && n == r.sprint:
			return pick(DestSprint, DestSprintOpen), 1
		case lb != nil:
			return pick(DestLLB, DestLLBOpen), 2
		case isSprint && n < r.sprint:
			return DestPrev, 3
		case trunkTargetRE.MatchString(m.Target):
			return pick(DestDevelop, DestDevelopOpen), 4
		case releaseRE.MatchString(m.Target):
			return DestRelease, 5
		}
		return DestOther, 6
	}
	var all []scored
	for i := range mrs {
		if mrs[i].App != app {
			continue
		}
		k, n := rank(&mrs[i])
		all = append(all, scored{&mrs[i], k, n})
	}
	if len(all) == 0 {
		return DestNone, nil
	}
	sort.SliceStable(all, func(i, j int) bool {
		mi, mj := all[i].mr.State == MRMerged, all[j].mr.State == MRMerged
		if mi != mj {
			return mj
		}
		return all[i].rank < all[j].rank
	})
	best := all[0]
	for _, s := range all {
		if s.mr.State == MRMerged {
			best = s
			break
		}
	}
	return best.kind, best.mr
}

// RouteOf ports d4's routeOf: where app's code of the card goes, and whether
// that disagrees with the train the card's page or fixVersion names.
func (r routing) RouteOf(key string, app AppID, mrs []SprintMR) Route {
	kind, best := r.destOf(app, mrs)
	if best == nil {
		return Route{App: app, Kind: DestNone, To: NotRouted{}}
	}
	main := best
	for i := range mrs {
		if mrs[i].App == app && mrs[i].Target == best.Target {
			main = &mrs[i]
			break
		}
	}
	at := main.Created
	if main.Merged != nil {
		at = *main.Merged
	}
	route := Route{App: app, Kind: kind, To: OffFlow{}, MR: main.IID}
	if p, ok := r.plans[planKey(key, app)]; ok {
		route.Plan = &p
	}
	first := r.firstTrain(app)
	shipAt := at
	landed := r.llbs[app][best.Target]
	if landed != nil && (landed.Landing == nil || landed.Landing.MergedOn == nil) {
		landed = nil
	}
	if landed != nil {
		shipAt = *landed.Landing.MergedOn
	}
	var early Dest
	if first != nil && first.Freeze != nil && shipAt.Before(first.Freeze.Add(-earlyThreshold)) {
		early = BeforeFirstTrain{Train: first.ID}
	}
	orEarly := func(to Dest) Dest {
		if early != nil {
			return early
		}
		return to
	}
	switch {
	case landed != nil:
		route.ViaLLB = landed.Branch
		route.To = orEarly(OffFlow{})
		if t := trainForLanding(*landed, r.trains); t != nil && early == nil {
			route.To = ToTrain{Train: *t}
		}
	case kind == DestSprint || kind == DestSprintOpen:
		route.To = ToTrain{Train: r.train(r.sprint + 1)}
	case kind == DestPrev || kind == DestBack:
		route.To = ToTrain{Train: r.train(r.sprint)}
	case kind == DestLLB || kind == DestLLBOpen:
		g := r.groups[app][best.Target]
		to := ToLLB{Branch: best.Target}
		if g != nil {
			to.Group = g.ID
			switch t := g.Target.(type) {
			case LLBPlanned:
				to.Via = &t.Train
			case LLBLandedIn:
				to.Via = &t.Train
			}
		}
		route.To = to
	case kind == DestRelease:
		route.To = r.releaseRoute(app, mrs, route.Plan, at, early)
	case kind == DestBefore:
		switch {
		case route.Plan != nil:
			route.To = ToTrain{Train: *route.Plan}
		case early != nil:
			route.To = early
		default:
			route.To = OffFlow{}
			if t := r.trainAfter(app, at); t != nil {
				route.To = ToTrain{Train: t.ID}
			}
		}
	}
	if route.Plan != nil {
		switch to := route.To.(type) {
		case ToTrain:
			route.Mismatch = to.Train != *route.Plan
		case ToLLB:
			route.Mismatch = to.Via == nil || *to.Via != *route.Plan
		}
	}
	return route
}

func (r routing) releaseRoute(app AppID, mrs []SprintMR, plan *TrainID, at Day, early Dest) Dest {
	version := ""
	for _, m := range mrs {
		if v := releaseVerRE.FindStringSubmatch(m.Source); m.App == app && v != nil {
			version = v[1]
			break
		}
	}
	for i := range r.trains {
		if v := r.trains[i].version(app); version != "" && v != nil && v.Version == version {
			return ToTrain{Train: r.trains[i].ID}
		}
	}
	if plan != nil {
		return ToTrain{Train: *plan}
	}
	for i := range r.trains {
		t := &r.trains[i]
		if t.version(app) != nil && t.Freeze != nil && t.Publish != nil && !t.Freeze.After(at) && !at.After(*t.Publish) {
			return ToTrain{Train: t.ID}
		}
	}
	if early != nil {
		return early
	}
	if t := r.trainAfter(app, at); t != nil {
		return ToTrain{Train: t.ID}
	}
	return OffFlow{}
}

func (r routing) firstTrain(app AppID) *Train {
	var best *Train
	for i := range r.trains {
		t := &r.trains[i]
		if t.version(app) != nil && t.Freeze != nil && (best == nil || t.Freeze.Before(*best.Freeze)) {
			best = t
		}
	}
	return best
}

func (r routing) trainAfter(app AppID, d Day) *Train {
	var best *Train
	for i := range r.trains {
		t := &r.trains[i]
		if t.version(app) != nil && t.Freeze != nil && !t.Freeze.Before(d) && (best == nil || t.Freeze.Before(*best.Freeze)) {
			best = t
		}
	}
	return best
}

func (r routing) train(n int) TrainID {
	for i := range r.trains {
		if id := r.trains[i].ID; id.Number == n && id.Extra == 0 && id.Year == r.year {
			return id
		}
	}
	return TrainID{Number: n, Year: r.year}
}

func planKey(key string, app AppID) string { return string(app) + "/" + key }

// planTrains is, per card and app, the first train not yet published that
// carries the card: by its page, its fixVersion or an MR into its sprint branch.
func planTrains(trains []Train, today Day) map[string]TrainID {
	out := map[string]TrainID{}
	for i := range trains {
		t := &trains[i]
		if t.Publish != nil && t.Publish.Before(today) {
			continue
		}
		for _, v := range t.Versions {
			for _, c := range v.Cards {
				if _, seen := out[planKey(c.Key, v.App)]; !seen {
					out[planKey(c.Key, v.App)] = t.ID
				}
			}
		}
	}
	return out
}

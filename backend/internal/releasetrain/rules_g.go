package releasetrain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	readsSprint      = []KindName{KindBoardSprints.Name, KindSprintCards.Name, KindThisSprint.Name, KindMRs.Name, KindBranchCards.Name}
	readsSprintRoute = append(append([]KindName{}, readsSprint...),
		KindMilestones.Name, KindJiraVersions.Name, KindCards.Name, KindPages.Name, KindPageCards.Name, KindLLBCards.Name)
)

func forEachSprintCard(b *Board, fn func(row *SprintRow, c *SprintCard) []Hit) []Hit {
	var out []Hit
	for i := range b.Sprints {
		row := &b.Sprints[i]
		if row.Next {
			continue
		}
		for j := range row.Cards {
			out = append(out, fn(row, &row.Cards[j])...)
		}
	}
	return out
}

func cardHit(row *SprintRow, c *SprintCard, say Copy, ev Evidence, mrs ...SprintMR) Hit {
	ev["board"], ev["sprint"] = row.Board.Key, row.Number
	links := make([]Link, 0, 1+len(mrs))
	links = append(links, Link{Label: "Open card", URL: c.URL})
	for _, m := range mrs {
		links = append(links, Link{Label: fmt.Sprintf("Open !%d", m.IID), URL: m.URL})
	}
	return Hit{Subject: SubjCard{Key: c.Key, Board: row.Board.Key}, Evidence: ev, Copy: say, Links: links, Cards: []string{c.Key}}
}

func (b *Board) appName(id AppID) string {
	for _, a := range b.Apps {
		if a.ID == id {
			return a.Name
		}
	}
	return string(id)
}

var ruleG1 = rule{
	ID: "G1", Group: "G", Severity: SevHygiene, DefaultOn: true, Scope: scopeGlobal,
	Subjects: []SubjectKind{SubjectCard},
	Mine:     MineListedCards,
	Reads:    readsSprint,
	Title:    "Jira card still in development while its code is merged",
	Hint:     "Every MR of the card is merged, but Jira still says Ready to Dev, In Progress or Pull Request.",
	Check: func(b *Board) []Hit {
		return forEachSprintCard(b, func(row *SprintRow, c *SprintCard) []Hit {
			if len(c.MRs) == 0 || !devStatuses[c.Status] {
				return nil
			}
			for _, m := range c.MRs {
				if m.State != MRMerged {
					return nil
				}
			}
			h := cardHit(row, c, Copy{
				What: fmt.Sprintf("%s is merged, but Jira still says %s", c.Key, c.Status),
				Do:   "Move the card to Ready for QA.",
			}, Evidence{"status": c.Status, "mrs": len(c.MRs)}, c.MRs...)
			return []Hit{*h.fix("Move to Ready for QA", h.Links...)}
		})
	},
}

// workDaysLeft counts the Monday-to-Friday days after today up to and including end.
func workDaysLeft(today, end Day) int {
	n := 0
	for d := today.Add(1); !d.After(end); d = d.Add(1) {
		if wd := d.utc().Weekday(); wd != time.Saturday && wd != time.Sunday {
			n++
		}
	}
	return n
}

var ruleG2 = rule{
	ID: "G2", Group: "G", Severity: SevRisk, DefaultOn: true, Scope: scopeGlobal,
	Subjects: []SubjectKind{SubjectCard},
	Mine:     MineListedCards,
	Reads:    readsSprint,
	Title:    "Sprint card has no MR and the sprint ends within 3 working days",
	Hint:     "Active cards only (new this sprint, or with open mobile work). A card marked No MR needed is skipped.",
	Check: func(b *Board) []Hit {
		return forEachSprintCard(b, func(row *SprintRow, c *SprintCard) []Hit {
			if row.Sprint.End == nil || !c.Active || c.abandoned() || len(c.MRs) > 0 || c.NoMR != nil {
				return nil
			}
			left := workDaysLeft(b.Today, *row.Sprint.End)
			if left > 3 {
				return nil
			}
			h := cardHit(row, c, Copy{
				What: c.Key + " has no MR yet",
				Do:   fmt.Sprintf("Open one before sprint %d ends %s, or mark it No MR needed.", row.Number, row.Sprint.End.label()),
			}, Evidence{"end": *row.Sprint.End, "status": c.Status})
			return []Hit{*h.open(h.Links...).dueAt(b.Today, row.Sprint.End, DueSprintEnd)}
		})
	},
}

var ruleG3 = rule{
	ID: "G3", Group: "G", Severity: SevRisk, DefaultOn: true, Scope: scopeGlobal,
	Subjects: []SubjectKind{SubjectCard},
	Mine:     MineListedCards,
	Reads:    readsSprintRoute,
	Title:    "Card's code goes to a different train than its page or fixVersion says",
	Check: func(b *Board) []Hit {
		return forEachSprintCard(b, func(row *SprintRow, c *SprintCard) []Hit {
			if !c.Active {
				return nil
			}
			for _, r := range c.Routes {
				if !r.Mismatch {
					continue
				}
				var by []SprintMR
				if m := routeMR(c.MRs, r); m != nil {
					by = append(by, *m)
				}
				h := cardHit(row, c, Copy{
					What: fmt.Sprintf("%s: its %s code is %s, but its page or fixVersion says train %s", c.Key, b.appName(r.App), routePhrase(r), r.Plan.short()),
					Do:   fmt.Sprintf("Move the code to the train %s sprint branch, or update the page and the fixVersion.", r.Plan.short()),
				}, Evidence{"app": r.App, "plan": *r.Plan, "to": r.To.destKey()}, by...)
				return []Hit{*h.promote(h.Links, len(h.Links)-1)}
			}
			return nil
		})
	},
}

var ruleG4 = rule{
	ID: "G4", Group: "G", Severity: SevInfo, DefaultOn: true, Scope: scopeGlobal,
	Subjects: []SubjectKind{SubjectCard},
	Mine:     MineListedCards,
	Reads:    readsSprintRoute,
	Title:    "Card's apps go to different trains",
	Check: func(b *Board) []Hit {
		return forEachSprintCard(b, func(row *SprintRow, c *SprintCard) []Hit {
			if !c.Active {
				return nil
			}
			tos := map[string]bool{}
			parts := make([]string, 0, len(c.Routes))
			allTrains := true
			for _, r := range c.Routes {
				tos[r.To.destKey()] = true
				parts = append(parts, fmt.Sprintf("%s %s", b.appName(r.App), routePhrase(r)))
				if _, ok := r.To.(ToTrain); !ok {
					allTrains = false
				}
			}
			if len(tos) < 2 {
				return nil
			}
			keys := make([]string, 0, len(tos))
			for k := range tos {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			what := fmt.Sprintf("%s goes to different places: %s", c.Key, strings.Join(parts, ", "))
			if allTrains && len(tos) == 2 {
				what = fmt.Sprintf("%s ships in two trains: %s", c.Key, strings.Join(parts, ", "))
			}
			h := cardHit(row, c, Copy{
				What: what,
				Do:   "Check that the split is planned.",
			}, Evidence{"to": keys})
			return []Hit{*h.open(h.Links...)}
		})
	},
}

var ruleG5 = rule{
	ID: "G5", Group: "G", Severity: SevRisk, DefaultOn: true, Scope: scopeGlobal,
	Subjects: []SubjectKind{SubjectPageFamily},
	Mine:     MineListedCards,
	Reads:    []KindName{KindMilestones.Name, KindJiraVersions.Name, KindCards.Name, KindMRs.Name, KindPages.Name, KindPageCards.Name, KindBranchCards.Name, KindLLBCards.Name},
	Title:    "Card ships with a train but is not on its release train page",
	Hint:     "Code merged into the sprint branch: risk now. MR still open: info until 3 days before freeze. Only when that family's page exists.",
	Check: func(b *Board) []Hit {
		var out []Hit
		for i := range b.Trains {
			t := &b.Trains[i]
			if t.Publish == nil || t.Publish.Before(b.Today) {
				continue
			}
			for _, pg := range t.Pages {
				merged, open := map[string]bool{}, map[string]bool{}
				for _, v := range t.Versions {
					if b.familyOf(v.App) != pg.Family {
						continue
					}
					for _, c := range v.Cards {
						switch c.Code.(type) {
						case CodeNone, CodeNoMR:
							continue
						}
						if containsWhy(c.Why, WhyPage) {
							continue
						}
						if _, ok := c.Code.(CodeMerged); ok {
							merged[c.Key] = true
						} else {
							open[c.Key] = true
						}
					}
				}
				for k := range merged {
					delete(open, k)
				}
				soon := t.Freeze != nil && t.Freeze.Sub(b.Today) <= 3
				for _, bucket := range []struct {
					name string
					keys map[string]bool
					sev  Severity
				}{{"merged", merged, SevRisk}, {"open", open, map[bool]Severity{true: SevRisk, false: SevInfo}[soon]}} {
					if len(bucket.keys) == 0 {
						continue
					}
					keys := sortedKeys(bucket.keys)
					sort.Slice(keys, func(a, c int) bool { return keyLess(keys[a], keys[c]) })
					train := t.ID
					what := fmt.Sprintf("%s in train %s %s not on its %s page", plural(len(keys), "merged card"), t.ID.short(), isAre(len(keys)), b.familyLabel(pg.Family))
					if bucket.name == "open" {
						what = fmt.Sprintf("%s with an open MR for train %s %s not on its %s page", plural(len(keys), "card"), t.ID.short(), isAre(len(keys)), b.familyLabel(pg.Family))
					}
					h := Hit{
						Subject: SubjPageFamily{Train: t.ID, Family: pg.Family, Bucket: bucket.name, Keys: keys}, Train: &train,
						Severity: bucket.sev,
						Evidence: Evidence{"page": pg.ID, "cards": keys, "bucket": bucket.name},
						Copy: Copy{
							What: what,
							Do:   fmt.Sprintf("Add %s to the page %s.", itThem(len(keys)), before("freeze", t.Freeze)),
						},
						Cards: keys,
					}
					out = append(out, *h.fix("Copy rows", Link{Label: "Open page", URL: pg.URL}).dueAt(b.Today, t.Freeze, DueFreeze))
				}
			}
		}
		return out
	},
}

func containsWhy(list []CardWhy, w CardWhy) bool {
	for _, x := range list {
		if x == w {
			return true
		}
	}
	return false
}

var ruleL8 = rule{
	ID: "L8", Group: "G", Severity: SevRisk, DefaultOn: true, Scope: scopeGlobal,
	Subjects: []SubjectKind{SubjectMR},
	Mine:     MineMR,
	Reads:    []KindName{KindBoardSprints.Name, KindSprintCards.Name, KindMRs.Name, KindBranchCards.Name},
	Title:    "Card MR into develop after the sprint-branch cutover",
	Hint:     "The same cards the sprint row puts in \"off the sprint flow\". An open MR is caught before it merges.",
	Check: func(b *Board) []Hit {
		return forEachSprintCard(b, func(row *SprintRow, c *SprintCard) []Hit {
			var out []Hit
			for _, r := range c.Routes {
				if r.Kind != DestDevelop && r.Kind != DestDevelopOpen {
					continue
				}
				m := routeMR(c.MRs, r)
				if m == nil {
					continue
				}
				h := Hit{
					Subject: SubjMR{App: m.App, IID: m.IID, Source: m.Source, Target: m.Target}, App: m.App,
					Evidence: Evidence{"card": c.Key, "target": m.Target, "state": m.State, "board": row.Board.Key},
					Cards:    []string{c.Key},
				}
				link := Link{Label: "Open MR", URL: m.URL}
				if m.State == MRMerged {
					merged := "was merged into develop"
					if m.Merged != nil {
						merged += " on " + m.Merged.label()
					}
					h.Copy = Copy{What: fmt.Sprintf("MR !%d for %s %s", m.IID, c.Key, merged), Do: "Bring the change into the sprint branch too."}
					h.open(link)
				} else {
					h.Copy = Copy{What: fmt.Sprintf("MR !%d for %s goes into develop", m.IID, c.Key), Do: "Retarget it to the sprint branch."}
					h.fix("Retarget MR", link)
				}
				out = append(out, h)
			}
			return out
		})
	},
}

func routeMR(mrs []SprintMR, r Route) *SprintMR {
	for i := range mrs {
		if mrs[i].App == r.App && mrs[i].IID == r.MR {
			return &mrs[i]
		}
	}
	return nil
}

// routePhrase says where a route's code is, to follow "is" or an app name.
func routePhrase(r Route) string {
	switch to := r.To.(type) {
	case ToLLB:
		return "in " + to.Branch
	case ToTrain:
		return "in train " + to.Train.short()
	case BeforeFirstTrain:
		return "before train " + to.Train.short()
	}
	return "off the sprint flow"
}

var ruleG6 = rule{
	ID: "G6", Group: "G", Severity: SevHygiene, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectMR},
	Mine:     MineMR,
	Reads:    []KindName{KindMRs.Name, KindLLBCards.Name, KindBranchCards.Name},
	Title:    "MR names a card key that Jira moved to another project",
	Hint:     "Jira now opens the old key as a different card, so the Board leaves that key out. Put the card's current key in the MR title: AO reads the title before the branch name.",
	Check: func(b *Board) []Hit {
		var out []Hit
		for _, app := range b.Apps {
			for _, mr := range b.mrs[app.ID] {
				if mr.State == MRClosed {
					continue
				}
				var moves, from, to []string
				for _, k := range mrKeys(mr) {
					if next := b.moved[k]; next != "" {
						moves, from, to = append(moves, k+" → "+next), append(from, k), append(to, next)
					}
				}
				if len(moves) == 0 {
					continue
				}
				h := Hit{
					Subject: SubjMR{App: app.ID, IID: mr.IID, Source: mr.SourceBranch, Target: mr.TargetBranch}, App: app.ID,
					Evidence: Evidence{"mr": mr.IID, "moved": moves},
					Copy: Copy{
						What: fmt.Sprintf("MR !%d names %s, which Jira moved to %s", mr.IID, strings.Join(from, " and "), strings.Join(to, " and ")),
						Do:   fmt.Sprintf("Put %s in the MR title.", strings.Join(to, " and ")),
					},
				}
				out = append(out, *h.open(Link{Label: "Open MR", URL: mr.URL}))
			}
		}
		return out
	},
}

type milestoneOwner struct {
	train *TrainID
	name  string
}

func (b *Board) milestoneOwners() map[AppID]map[int]milestoneOwner {
	out := map[AppID]map[int]milestoneOwner{}
	put := func(app AppID, id int, o milestoneOwner) {
		if out[app] == nil {
			out[app] = map[int]milestoneOwner{}
		}
		out[app][id] = o
	}
	for i := range b.Trains {
		t := &b.Trains[i]
		for _, v := range t.Versions {
			put(v.App, v.Milestone.ID, milestoneOwner{train: &t.ID, name: "train " + t.ID.short()})
		}
	}
	for _, h := range b.Hotfixes {
		put(h.App, h.Milestone.ID, milestoneOwner{name: h.Milestone.Title})
	}
	return out
}

func (b *Board) ridesOn(feeds TrainID, app AppID) (*Train, *AppVersion) {
	var best *Train
	for i := range b.Trains {
		t := &b.Trains[i]
		if t.ID.Number != feeds.Number || t.ID.Year != feeds.Year {
			continue
		}
		if v := t.version(app); v != nil {
			return t, v
		}
		if best == nil || t.ID.regular() {
			best = t
		}
	}
	return best, nil
}

func (b *Board) beforeEveryTrain(id TrainID) bool {
	for _, t := range b.Trains {
		if !id.less(t.ID) {
			return false
		}
	}
	return len(b.Trains) > 0
}

func cardSource(branch string) bool {
	_, sprint := sprintOf(branch)
	return !sprint && !trunkTargetRE.MatchString(branch) && !releaseRE.MatchString(branch)
}

func releasedOn(t *Train, v *AppVersion) bool {
	return t.Phase == PhasePast || v != nil && (v.Tag != nil || v.Jira != nil && v.Jira.Released)
}

var ruleG9 = rule{
	ID: "G9", Group: "G", Severity: SevHygiene, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectMR},
	Mine:     MineMR,
	Reads:    []KindName{KindMilestones.Name, KindMRs.Name, KindJiraVersions.Name, KindTags.Name},
	Title:    "Card MR into a sprint branch has no milestone, or another train's",
	Hint:     "The milestone should be the train the sprint branch feeds. MRs into long-lived branches, develop and release branches are skipped. Risk from 3 working days before the app's freeze; quiet once the version is released.",
	Check: func(b *Board) []Hit {
		owners := b.milestoneOwners()
		var out []Hit
		for _, app := range b.Apps {
			for _, mr := range b.mrs[app.ID] {
				feeds, ok := sprintTrain(mr.TargetBranch)
				if !ok || mr.State == MRClosed || !cardSource(mr.SourceBranch) {
					continue
				}
				t, v := b.ridesOn(feeds, app.ID)
				if t == nil && b.beforeEveryTrain(feeds) || t != nil && releasedOn(t, v) {
					continue
				}
				rides := feeds
				if t != nil {
					rides = t.ID
				}
				var what string
				if mr.MilestoneID == nil {
					what = fmt.Sprintf("MR !%d has no milestone", mr.IID)
				} else {
					o, known := owners[app.ID][*mr.MilestoneID]
					if !known || o.train != nil && o.train.Number == feeds.Number && o.train.Year == feeds.Year {
						continue
					}
					what = fmt.Sprintf("MR !%d has the %s milestone, but ships in train %s", mr.IID, o.name, rides.short())
				}
				links := []Link{{Label: "Open MR", URL: mr.URL}}
				do := fmt.Sprintf("Set the train %s milestone once it exists.", rides.short())
				if v != nil {
					do = fmt.Sprintf("Set milestone %s. It ships in train %s.", v.Milestone.Title, rides.short())
					links = append(links, Link{Label: "Open milestone", URL: v.Milestone.URL})
				}
				h := Hit{
					Subject: SubjMR{App: app.ID, IID: mr.IID, Source: mr.SourceBranch, Target: mr.TargetBranch}, App: app.ID, Train: &rides,
					Evidence: Evidence{"mr": mr.IID, "target": mr.TargetBranch, "milestone": mr.MilestoneID},
					Copy:     Copy{What: what, Do: do},
				}
				h.open(links...)
				if keys := mrKeys(mr); len(keys) > 0 {
					h.Evidence["card"], h.Cards = keys[0], keys[:1]
				}
				var freeze *Day
				if t != nil {
					freeze = t.Freeze
					if v != nil {
						freeze = freezeOf(t, v)
					}
				}
				if freeze != nil && workDaysLeft(b.Today, *freeze) <= nearFreezeWorkDays {
					h.Severity = SevRisk
					h.dueAt(b.Today, freeze, DueFreeze)
				}
				out = append(out, h)
			}
		}
		return out
	},
}

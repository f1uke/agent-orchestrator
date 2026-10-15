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
		for j := range row.Cards {
			out = append(out, fn(row, &row.Cards[j])...)
		}
	}
	return out
}

func cardHit(row *SprintRow, c *SprintCard, text string, ev Evidence, mrs ...SprintMR) Hit {
	ev["board"], ev["sprint"] = row.Board.Key, row.Number
	links := []Link{{Label: "Open card", URL: c.URL}}
	for _, m := range mrs {
		links = append(links, Link{Label: fmt.Sprintf("Open !%d", m.IID), URL: m.URL})
	}
	return Hit{Subject: SubjCard{Key: c.Key}, Evidence: ev, Text: text, Links: links}
}

func mrList(mrs []SprintMR, short func(AppID) string) string {
	parts := make([]string, len(mrs))
	for i, m := range mrs {
		parts[i] = fmt.Sprintf("%s !%d", short(m.App), m.IID)
	}
	return strings.Join(parts, ", ")
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
	Reads: readsSprint,
	Title: "Jira card still in development while its code is merged",
	Hint:  "Every MR of the card is merged, but Jira still says Ready to Dev, In Progress or Pull Request.",
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
			return []Hit{cardHit(row, c, fmt.Sprintf("Jira says %s, all %d MR(s) merged (%s)", c.Status, len(c.MRs), mrList(c.MRs, b.appName)),
				Evidence{"status": c.Status, "mrs": len(c.MRs)}, c.MRs...)}
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
	Reads: readsSprint,
	Title: "Sprint card has no MR and the sprint ends within 3 working days",
	Hint:  "Active cards only (new this sprint, or with open mobile work).",
	Check: func(b *Board) []Hit {
		return forEachSprintCard(b, func(row *SprintRow, c *SprintCard) []Hit {
			if row.Sprint.End == nil || !c.Active || c.abandoned() || len(c.MRs) > 0 {
				return nil
			}
			left := workDaysLeft(b.Today, *row.Sprint.End)
			if left > 3 {
				return nil
			}
			plural := "s"
			if left == 1 {
				plural = ""
			}
			return []Hit{cardHit(row, c, fmt.Sprintf("No MR yet, %s %d ends %s (%d working day%s left) · Jira %s",
				row.Board.Key, row.Number, row.Sprint.End.label(), left, plural, c.Status), Evidence{"end": *row.Sprint.End, "status": c.Status})}
		})
	},
}

var ruleG3 = rule{
	ID: "G3", Group: "G", Severity: SevRisk, DefaultOn: true, Scope: scopeGlobal,
	Reads: readsSprintRoute,
	Title: "Card's code goes to a different train than its page or fixVersion says",
	Check: func(b *Board) []Hit {
		return forEachSprintCard(b, func(row *SprintRow, c *SprintCard) []Hit {
			if !c.Active {
				return nil
			}
			for _, r := range c.Routes {
				if !r.Mismatch {
					continue
				}
				goes := routeName(r)
				if to, ok := r.To.(ToLLB); ok {
					goes = to.Branch
				}
				var by []SprintMR
				if m := routeMR(c.MRs, r); m != nil {
					by = append(by, *m)
				}
				return []Hit{cardHit(row, c, fmt.Sprintf("Page or fixVersion says %s, code goes to %s (%s)", r.Plan, goes, b.appName(r.App)),
					Evidence{"app": r.App, "plan": *r.Plan, "to": r.To.destKey()}, by...)}
			}
			return nil
		})
	},
}

var ruleG4 = rule{
	ID: "G4", Group: "G", Severity: SevInfo, DefaultOn: true, Scope: scopeGlobal,
	Reads: readsSprintRoute,
	Title: "Card's apps go to different trains",
	Check: func(b *Board) []Hit {
		return forEachSprintCard(b, func(row *SprintRow, c *SprintCard) []Hit {
			if !c.Active {
				return nil
			}
			tos := map[string]bool{}
			parts := make([]string, 0, len(c.Routes))
			for _, r := range c.Routes {
				tos[r.To.destKey()] = true
				parts = append(parts, fmt.Sprintf("%s → %s", b.appName(r.App), routeName(r)))
			}
			if len(tos) < 2 {
				return nil
			}
			keys := make([]string, 0, len(tos))
			for k := range tos {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			return []Hit{cardHit(row, c, strings.Join(parts, ", "), Evidence{"to": keys})}
		})
	},
}

var ruleG5 = rule{
	ID: "G5", Group: "G", Severity: SevRisk, DefaultOn: true, Scope: scopeGlobal,
	Reads: []KindName{KindMilestones.Name, KindJiraVersions.Name, KindCards.Name, KindMRs.Name, KindPages.Name, KindPageCards.Name, KindBranchCards.Name, KindLLBCards.Name},
	Title: "Card ships with a train but is not on its release train page",
	Hint:  "Code merged into the sprint branch: risk now. MR still open: info until 3 days before freeze. Only when that family's page exists.",
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
						case CodeNone:
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
					out = append(out, Hit{
						Subject: SubjPageFamily{Train: t.ID, Family: pg.Family, Bucket: bucket.name, Keys: keys}, Train: &train,
						Severity: bucket.sev,
						Evidence: Evidence{"page": pg.ID, "cards": keys, "bucket": bucket.name},
						Text:     fmt.Sprintf("%d card(s) not on %q: %s", len(keys), pg.Title, joinNames(keys)),
						Links:    []Link{{Label: "Open page", URL: pg.URL}},
					})
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
	Reads: []KindName{KindBoardSprints.Name, KindSprintCards.Name, KindMRs.Name, KindBranchCards.Name},
	Title: "Card MR into develop after the sprint-branch cutover",
	Hint:  "The same cards the sprint row puts in \"off the sprint flow\". An open MR is caught before it merges.",
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
				what := "is open into develop"
				if m.State == MRMerged && m.Merged != nil {
					what = "merged into develop on " + m.Merged.label()
				}
				out = append(out, Hit{
					Subject: SubjMR{App: m.App, IID: m.IID}, App: m.App,
					Evidence: Evidence{"card": c.Key, "target": m.Target, "state": m.State, "board": row.Board.Key},
					Text:     fmt.Sprintf("%s !%d %s; it should go to the sprint branch", b.appName(m.App), m.IID, what),
					Links:    []Link{{Label: "Open MR", URL: m.URL}},
				})
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

func routeName(r Route) string {
	switch to := r.To.(type) {
	case ToLLB:
		return strings.TrimPrefix(to.Branch, "feature/")
	case ToTrain:
		return to.Train.String()
	case BeforeFirstTrain:
		return "<" + to.Train.String()
	}
	return "off flow"
}

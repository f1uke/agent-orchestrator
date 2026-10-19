package releasetrain

import "strconv"

// MineWhy is one reason something concerns me.
type MineWhy string

// Reasons something concerns me.
const (
	MineAssignee      MineWhy = "assignee"
	MineSubtask       MineWhy = "subtask"
	MineMRAuthor      MineWhy = "mr_author"
	MineReleaseMR     MineWhy = "release_mr_author"
	MineReleaseTicket MineWhy = "release_ticket_assignee"
	MineApprover      MineWhy = "approver"
	MineCard          MineWhy = "card"
	MineLanding       MineWhy = "landing_author"
	MineNoMR          MineWhy = "no_mr"
)

// Enum lists the reasons for the API schema.
func (MineWhy) Enum() []any {
	return []any{MineAssignee, MineSubtask, MineMRAuthor, MineReleaseMR, MineReleaseTicket, MineApprover, MineCard, MineLanding, MineNoMR}
}

// MineReason is why something concerns me; Ref names the card, subtask or MR behind it.
type MineReason struct {
	Why MineWhy `json:"why"`
	Ref string  `json:"ref,omitempty"`
}

type me struct {
	id       IdentityKnown
	subtasks map[string][]string
}

func meOf(identity IdentityState, snap Snapshot) *me {
	known, ok := identity.(IdentityKnown)
	if !ok {
		return nil
	}
	m := &me{id: known, subtasks: map[string][]string{}}
	for _, st := range AllItems(snap, KindMySubtasks) {
		if st.Parent != "" && st.Assignee != nil && st.Assignee.AccountID == known.JiraAccountID {
			m.subtasks[st.Parent] = append(m.subtasks[st.Parent], st.Key)
		}
	}
	return m
}

func (m *me) authored(mr MRFact) bool {
	return mr.AuthorUsername != "" && mr.AuthorUsername == m.id.GitLabUsername
}

func (m *me) assigned(p *Person) bool { return p != nil && p.AccountID == m.id.JiraAccountID }

func mrRef(mr MRFact) string { return "!" + strconv.Itoa(mr.IID) }

func (m *me) card(key string, assignee *Person, mrs []MRFact) []MineReason {
	var out []MineReason
	if m.assigned(assignee) {
		out = append(out, MineReason{Why: MineAssignee})
	}
	for _, st := range m.subtasks[key] {
		out = append(out, MineReason{Why: MineSubtask, Ref: st})
	}
	seen := map[int]bool{}
	for _, mr := range mrs {
		if m.authored(mr) && !seen[mr.IID] {
			seen[mr.IID] = true
			out = append(out, MineReason{Why: MineMRAuthor, Ref: mrRef(mr)})
		}
	}
	return out
}

func mine(b *Board, m *me, mrs mrIndex, snap Snapshot, cfg Config) {
	if m == nil {
		return
	}
	for i := range b.Trains {
		for _, v := range b.Trains[i].Versions {
			var cards []MineReason
			for ci := range v.Cards {
				c := &v.Cards[ci]
				c.Mine = m.card(c.Key, c.Assignee, mrs[v.App][c.Key])
				if len(c.Mine) > 0 {
					cards = append(cards, MineReason{Why: MineCard, Ref: c.Key})
				}
				if _, marked := c.Code.(CodeNoMR); marked {
					c.Mine = append(c.Mine, MineReason{Why: MineNoMR})
				}
			}
			v.Mine = append(m.version(v), cards...)
		}
	}
	for i := range b.Hotfixes {
		h := &b.Hotfixes[i]
		for _, mr := range Items(snap, KindMRs, ProjectScope(cfg.app(h.App).GitLabProject)) {
			if mr.MilestoneID != nil && *mr.MilestoneID == h.Milestone.ID && m.authored(mr) {
				h.Mine = append(h.Mine, MineReason{Why: MineMRAuthor, Ref: mrRef(mr)})
			}
		}
	}
	for i := range b.Sprints {
		for j := range b.Sprints[i].Cards {
			c := &b.Sprints[i].Cards[j]
			c.Mine = m.sprintCard(c)
		}
	}
	for i := range b.LLBs {
		g := &b.LLBs[i]
		for bi := range g.Branches {
			br := &g.Branches[bi]
			for ci := range br.Cards {
				c := &br.Cards[ci]
				c.Mine = m.card(c.Key, c.Assignee, []MRFact{c.MR})
				if len(c.Mine) > 0 {
					g.Mine = append(g.Mine, MineReason{Why: MineCard, Ref: c.Key})
				}
			}
			if br.Landing != nil && m.authored(*br.Landing) {
				g.Mine = append(g.Mine, MineReason{Why: MineLanding, Ref: mrRef(*br.Landing)})
			}
		}
	}
}

func (m *me) sprintCard(c *SprintCard) []MineReason {
	var out []MineReason
	if m.assigned(c.Assignee) {
		out = append(out, MineReason{Why: MineAssignee})
	}
	for _, st := range c.Subtasks {
		if m.assigned(st.Assignee) {
			out = append(out, MineReason{Why: MineSubtask, Ref: st.Key})
		}
	}
	for _, mr := range c.MRs {
		if mr.username != "" && mr.username == m.id.GitLabUsername {
			out = append(out, MineReason{Why: MineMRAuthor, Ref: mrRef(MRFact{IID: mr.IID})})
		}
	}
	if c.NoMR != nil {
		out = append(out, MineReason{Why: MineNoMR})
	}
	return out
}

func (m *me) version(v *AppVersion) []MineReason {
	var out []MineReason
	if v.ReleaseMR != nil && m.authored(v.ReleaseMR.MR) {
		out = append(out, MineReason{Why: MineReleaseMR, Ref: mrRef(v.ReleaseMR.MR)})
	}
	if v.Ticket != nil && m.assigned(v.Ticket.Assignee) {
		out = append(out, MineReason{Why: MineReleaseTicket, Ref: v.Ticket.Key})
	}
	if v.Jira != nil {
		for _, a := range v.Jira.Approvers {
			if a.AccountID == m.id.JiraAccountID {
				out = append(out, MineReason{Why: MineApprover})
			}
		}
	}
	return out
}

func mineOfSubject(b *Board, s Subject) []MineReason {
	versionMine := func(train TrainID, app AppID) []MineReason {
		if t := b.train(train); t != nil {
			if v := t.version(app); v != nil {
				return v.Mine
			}
		}
		return nil
	}
	hotfixMine := func(app AppID, version string) []MineReason {
		for _, h := range b.Hotfixes {
			if h.App == app && h.Version == version {
				return h.Mine
			}
		}
		return nil
	}
	switch s := s.(type) {
	case SubjVersion:
		return versionMine(s.Train, s.App)
	case SubjVersionDate:
		return versionMine(s.Train, s.App)
	case SubjHotfix:
		return hotfixMine(s.App, s.Version)
	case SubjHotfixTrain:
		return hotfixMine(s.App, s.Version)
	case SubjMilestone:
		return b.mineWhere(func(v *AppVersion) bool { return v.App == s.App && v.Milestone.ID == s.ID })
	case SubjJiraVersion:
		return b.mineWhere(func(v *AppVersion) bool { return v.Jira != nil && v.Jira.ID == s.ID })
	case SubjCardOnTrain:
		if t := b.train(s.Train); t != nil {
			if v := t.version(s.App); v != nil {
				for _, c := range v.Cards {
					if c.Key == s.Key {
						return c.Mine
					}
				}
			}
		}
	case SubjCard:
		if c := b.sprintCard(s.Key); c != nil {
			return c.Mine
		}
	case SubjPageFamily:
		var out []MineReason
		if t := b.train(s.Train); t != nil {
			for _, v := range t.Versions {
				if b.familyOf(v.App) != s.Family {
					continue
				}
				for _, c := range v.Cards {
					if len(c.Mine) > 0 && containsString(s.Keys, c.Key) {
						out = append(out, MineReason{Why: MineCard, Ref: c.Key})
					}
				}
			}
		}
		return out
	case SubjMR:
		for _, row := range b.Sprints {
			for _, c := range row.Cards {
				for _, mr := range c.MRs {
					if mr.App == s.App && mr.IID == s.IID {
						return c.Mine
					}
				}
			}
		}
		for _, g := range b.LLBs {
			for _, br := range g.Branches {
				for _, out := range br.outs {
					if br.App == s.App && out.IID == s.IID {
						return g.Mine
					}
				}
			}
		}
		return b.authoredByMe(s)
	}
	return nil
}

func (b *Board) authoredByMe(s SubjMR) []MineReason {
	id, ok := b.Identity.(IdentityKnown)
	if !ok {
		return nil
	}
	for _, mr := range b.mrs[s.App] {
		if mr.IID == s.IID && mr.AuthorUsername != "" && mr.AuthorUsername == id.GitLabUsername {
			return []MineReason{{Why: MineMRAuthor, Ref: mrRef(mr)}}
		}
	}
	return nil
}

func (b *Board) mineWhere(match func(*AppVersion) bool) []MineReason {
	for i := range b.Trains {
		for _, v := range b.Trains[i].Versions {
			if match(v) {
				return v.Mine
			}
		}
	}
	return nil
}

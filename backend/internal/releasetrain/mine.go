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
			if c := &b.Sprints[i].Cards[j]; c.Mobile {
				c.Mine = m.sprintCard(c)
			}
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

package releasetrain

import (
	"fmt"
	"sort"
	"strings"
)

// PersonLink is one human-edited line of the people file: the same person in Jira and GitLab.
type PersonLink struct {
	JiraAccountID  string `json:"jiraAccountId"`
	GitLabUsername string `json:"gitlabUsername"`
	Name           string `json:"name,omitempty"`
}

// PersonMatch says how a BoardPerson's Jira account and GitLab user were joined.
type PersonMatch string

// Ways a person's two accounts are joined, or not.
const (
	MatchLinked     PersonMatch = "linked"
	MatchSelf       PersonMatch = "self"
	MatchName       PersonMatch = "name"
	MatchJiraOnly   PersonMatch = "jira_only"
	MatchGitLabOnly PersonMatch = "gitlab_only"
)

// Enum lists the matches for the API schema.
func (PersonMatch) Enum() []any {
	return []any{MatchLinked, MatchSelf, MatchName, MatchJiraOnly, MatchGitLabOnly}
}

// BoardPerson is one human on the Board, with whichever of his two accounts are known.
type BoardPerson struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	JiraAccountID  string      `json:"jiraAccountId,omitempty"`
	GitLabUsername string      `json:"gitlabUsername,omitempty"`
	Match          PersonMatch `json:"match"`
	Self           bool        `json:"self,omitempty"`
}

// ViewerState is whose Mine the Board carries.
type ViewerState interface{ viewerState() }

// ViewerPerson is the person every Mine on the Board is about.
type ViewerPerson struct {
	Person BoardPerson `json:"person"`
}

// ViewerUnknown says why no person is the viewer; every Mine is then empty.
type ViewerUnknown struct {
	ID     string `json:"id,omitempty"`
	Reason string `json:"reason"`
}

func (ViewerPerson) viewerState()  {}
func (ViewerUnknown) viewerState() {}

const jiraOnlyIDPrefix = "jira:"

func personID(jira, gitlab string) string {
	if gitlab != "" {
		return gitlab
	}
	return jiraOnlyIDPrefix + jira
}

func (p BoardPerson) is(id string) bool {
	return id != "" && (p.ID == id || p.GitLabUsername == id || (p.JiraAccountID != "" && id == jiraOnlyIDPrefix+p.JiraAccountID))
}

func normalizeName(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

type peopleSeen struct {
	jira   map[string]string
	gitlab map[string]string
}

func (s *peopleSeen) assignee(p *Person) {
	if p != nil {
		s.jiraUser(p.AccountID, p.Name)
	}
}

func (s *peopleSeen) jiraUser(account, name string) {
	if account != "" && s.jira[account] == "" {
		s.jira[account] = name
	}
}

func (s *peopleSeen) gitlabUser(username, name string) {
	if username != "" && s.gitlab[username] == "" {
		s.gitlab[username] = name
	}
}

func (s *peopleSeen) author(mr MRFact) { s.gitlabUser(mr.AuthorUsername, mr.Author) }

func seenOnBoard(b *Board, mrs mrIndex, snap Snapshot, cfg Config) peopleSeen {
	s := peopleSeen{jira: map[string]string{}, gitlab: map[string]string{}}
	parents := map[string]bool{}
	for i := range b.Trains {
		for _, v := range b.Trains[i].Versions {
			for _, c := range v.Cards {
				parents[c.Key] = true
				s.assignee(c.Assignee)
				for _, mr := range mrs[v.App][c.Key] {
					s.author(mr)
				}
			}
			if v.ReleaseMR != nil {
				s.author(v.ReleaseMR.MR)
			}
			if v.Ticket != nil {
				s.assignee(v.Ticket.Assignee)
			}
			if v.Jira != nil {
				for _, a := range v.Jira.Approvers {
					s.jiraUser(a.AccountID, a.Name)
				}
			}
		}
	}
	for i := range b.Hotfixes {
		h := &b.Hotfixes[i]
		for _, mr := range Items(snap, KindMRs, ProjectScope(cfg.app(h.App).GitLabProject)) {
			if mr.MilestoneID != nil && *mr.MilestoneID == h.Milestone.ID {
				s.author(mr)
			}
		}
	}
	for _, row := range b.Sprints {
		for _, c := range row.Cards {
			if !c.Mobile {
				continue
			}
			s.assignee(c.Assignee)
			for _, st := range c.Subtasks {
				s.assignee(st.Assignee)
			}
			for _, mr := range c.MRs {
				s.gitlabUser(mr.username, mr.Author)
			}
		}
	}
	for _, g := range b.LLBs {
		for _, br := range g.Branches {
			for _, c := range br.Cards {
				parents[c.Key] = true
				s.assignee(c.Assignee)
				s.author(c.MR)
			}
			if br.Landing != nil {
				s.author(*br.Landing)
			}
		}
	}
	for _, st := range AllItems(snap, KindCardSubtasks) {
		if parents[st.Parent] {
			s.assignee(st.Assignee)
		}
	}
	return s
}

type accountPair struct {
	jira, gitlab string
	name         string
	match        PersonMatch
}

func buildPeople(seen peopleSeen, links []PersonLink, identity IdentityState, snap Snapshot) []BoardPerson {
	self, known := identity.(IdentityKnown)
	if known {
		seen.jiraUser(self.JiraAccountID, self.DisplayName)
		for _, u := range AllItems(snap, KindGitLabUser) {
			seen.gitlabUser(u.Username, u.Name)
		}
		seen.gitlabUser(self.GitLabUsername, self.DisplayName)
	}
	for id, name := range seen.jira {
		if name == "" {
			for _, u := range Items(snap, KindJiraUsers, UserScope(id)) {
				name = u.DisplayName
			}
			seen.jira[id] = name
		}
	}

	claimedJ, claimedG := map[string]bool{}, map[string]bool{}
	var pairs []accountPair
	claim := func(p accountPair) {
		pairs = append(pairs, p)
		claimedJ[p.jira], claimedG[p.gitlab] = true, true
	}
	for _, l := range links {
		_, onJira := seen.jira[l.JiraAccountID]
		_, onGitLab := seen.gitlab[l.GitLabUsername]
		if l.JiraAccountID == "" || l.GitLabUsername == "" || claimedJ[l.JiraAccountID] || claimedG[l.GitLabUsername] || (!onJira && !onGitLab) {
			continue
		}
		claim(accountPair{jira: l.JiraAccountID, gitlab: l.GitLabUsername, name: l.Name, match: MatchLinked})
	}
	if known && self.JiraAccountID != "" && self.GitLabUsername != "" && !claimedJ[self.JiraAccountID] && !claimedG[self.GitLabUsername] {
		claim(accountPair{jira: self.JiraAccountID, gitlab: self.GitLabUsername, name: self.DisplayName, match: MatchSelf})
	}
	byName := func(accounts map[string]string, claimed map[string]bool) map[string][]string {
		out := map[string][]string{}
		for id, name := range accounts {
			if n := normalizeName(name); n != "" && !claimed[id] {
				out[n] = append(out[n], id)
			}
		}
		return out
	}
	jiraNames, gitlabNames := byName(seen.jira, claimedJ), byName(seen.gitlab, claimedG)
	for n, js := range jiraNames {
		if gs := gitlabNames[n]; len(js) == 1 && len(gs) == 1 {
			claim(accountPair{jira: js[0], gitlab: gs[0], match: MatchName})
		}
	}
	for id := range seen.jira {
		if !claimedJ[id] {
			pairs = append(pairs, accountPair{jira: id, match: MatchJiraOnly})
		}
	}
	for id := range seen.gitlab {
		if !claimedG[id] {
			pairs = append(pairs, accountPair{gitlab: id, match: MatchGitLabOnly})
		}
	}

	people := make([]BoardPerson, 0, len(pairs))
	for _, p := range pairs {
		bp := BoardPerson{ID: personID(p.jira, p.gitlab), Name: p.name, JiraAccountID: p.jira, GitLabUsername: p.gitlab, Match: p.match}
		for _, n := range []string{seen.jira[p.jira], seen.gitlab[p.gitlab], bp.ID} {
			if bp.Name == "" {
				bp.Name = n
			}
		}
		people = append(people, bp)
	}
	if known {
		for i := range people {
			if people[i].JiraAccountID == self.JiraAccountID {
				people[i].Self = true
				break
			}
		}
	}
	sort.Slice(people, func(i, j int) bool {
		a, b := strings.ToLower(people[i].Name), strings.ToLower(people[j].Name)
		if a != b {
			return a < b
		}
		return people[i].ID < people[j].ID
	})
	return people
}

// ViewerOf is the person id names; an empty id names the monitor's own person.
func ViewerOf(people []BoardPerson, identity IdentityState, id string) ViewerState {
	for _, p := range people {
		if (id == "" && p.Self) || p.is(id) {
			return ViewerPerson{Person: p}
		}
	}
	if id == "" {
		if u, ok := identity.(IdentityUnknown); ok {
			return ViewerUnknown{Reason: u.Reason}
		}
		return ViewerUnknown{Reason: "the monitor's own accounts are on no card of the Board"}
	}
	return ViewerUnknown{ID: id, Reason: fmt.Sprintf("no person %q on the Board", id)}
}

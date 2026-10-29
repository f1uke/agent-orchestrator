package releasetrain

// MinePolicy is when a rule's hit is mine: I own it or I must act on it.
type MinePolicy string

// Mine policies, one per rule.
const (
	MineNever           MinePolicy = "never"
	MineOwner           MinePolicy = "owner"
	MinePendingApprover MinePolicy = "pending_approver"
	MineListedCards     MinePolicy = "listed_cards"
	MineMR              MinePolicy = "mr"
)

// Enum lists the policies for the API schema.
func (MinePolicy) Enum() []any {
	return []any{MineNever, MineOwner, MinePendingApprover, MineListedCards, MineMR}
}

func (r rule) mineOf(b *Board, myCards map[string]bool, h Hit) []MineReason {
	switch r.Mine {
	case MineOwner:
		return b.ownerMine(h.Subject)
	case MinePendingApprover:
		return b.pendingApproverMine(h.Subject)
	case MineListedCards:
		return cardsMine(myCards, h.Cards)
	case MineMR:
		if s, ok := h.Subject.(SubjMR); ok {
			if why := b.authoredMine(s); why != nil {
				return why
			}
		}
		return cardsMine(myCards, h.Cards)
	}
	return nil
}

func (b *Board) versionOf(train TrainID, app AppID) *AppVersion {
	if t := b.train(train); t != nil {
		return t.version(app)
	}
	return nil
}

func (b *Board) hotfixOf(app AppID, version string) *Hotfix {
	for i := range b.Hotfixes {
		if b.Hotfixes[i].App == app && b.Hotfixes[i].Version == version {
			return &b.Hotfixes[i]
		}
	}
	return nil
}

func (b *Board) ownerMine(s Subject) []MineReason {
	switch s := s.(type) {
	case SubjVersion:
		v := b.versionOf(s.Train, s.App)
		if v == nil {
			return nil
		}
		var out []MineReason
		for _, m := range v.Mine {
			if m.Why == MineReleaseMR || m.Why == MineReleaseTicket {
				out = append(out, m)
			}
		}
		return out
	case SubjHotfix:
		if h := b.hotfixOf(s.App, s.Version); h != nil {
			return h.Mine
		}
	case SubjHotfixTrain:
		if h := b.hotfixOf(s.App, s.Version); h != nil {
			return h.Mine
		}
	}
	return nil
}

func (b *Board) pendingApproverMine(s Subject) []MineReason {
	m := meOf(b.Viewer, Snapshot{})
	sv, ok := s.(SubjVersion)
	if m == nil || !ok {
		return nil
	}
	if v := b.versionOf(sv.Train, sv.App); v != nil && v.Jira != nil {
		for _, a := range v.Jira.Approvers {
			if m.isJira(a.AccountID) && a.Status != ApproverApproved {
				return []MineReason{{Why: MineApprover}}
			}
		}
	}
	return nil
}

func ownsCard(mine []MineReason) bool {
	for _, m := range mine {
		if m.Why != MineNoMR {
			return true
		}
	}
	return false
}

func (b *Board) myCards() map[string]bool {
	out := map[string]bool{}
	for i := range b.Trains {
		for _, v := range b.Trains[i].Versions {
			for _, c := range v.Cards {
				if ownsCard(c.Mine) {
					out[c.Key] = true
				}
			}
		}
	}
	for _, row := range b.Sprints {
		for _, c := range row.Cards {
			if ownsCard(c.Mine) {
				out[c.Key] = true
			}
		}
	}
	for _, g := range b.LLBs {
		for _, br := range g.Branches {
			for _, c := range br.Cards {
				if ownsCard(c.Mine) {
					out[c.Key] = true
				}
			}
		}
	}
	return out
}

func cardsMine(mine map[string]bool, keys []string) []MineReason {
	var out []MineReason
	seen := map[string]bool{}
	for _, k := range keys {
		if mine[k] && !seen[k] {
			seen[k] = true
			out = append(out, MineReason{Why: MineCard, Ref: k})
		}
	}
	return out
}

func (b *Board) authoredMine(s SubjMR) []MineReason {
	m := meOf(b.Viewer, Snapshot{})
	if m == nil {
		return nil
	}
	for _, mr := range b.mrs[s.App] {
		if mr.IID == s.IID && m.authored(mr) {
			return []MineReason{{Why: MineMRAuthor, Ref: mrRef(mr)}}
		}
	}
	return nil
}

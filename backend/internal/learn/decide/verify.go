package decide

import (
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/rules"
)

type verifyProposal struct {
	ID        string   `json:"id"`
	Action    string   `json:"action"`
	Target    string   `json:"target"`
	Title     string   `json:"title"`
	Rationale string   `json:"rationale"`
	Content   string   `json:"content"`
	Evidence  []string `json:"evidence"`
}

// VerifierInput shapes the kept candidates for the verifier, with their
// evidence and a wider set of rules than decide saw (top 50 plus every
// protected rule). It returns "" when nothing is left to verify.
func VerifierInput(env Env, corpus []domain.LearnRule, cands []Candidate) (string, error) {
	var in struct {
		Proposals []verifyProposal `json:"proposals"`
		Evidence  []draftIn        `json:"evidence"`
		Rules     []ruleIn         `json:"standing_rules"`
	}
	seen := map[int64]bool{}
	query := ""
	for _, c := range cands {
		if c.Drop != "" {
			continue
		}
		vp := verifyProposal{ID: c.Ref, Action: string(c.Proposal.Action), Target: c.Proposal.TargetPath,
			Title: c.Proposal.Title, Rationale: c.Proposal.Rationale, Content: c.Added}
		if c.Proposal.Action == domain.LearnProposeCreateSkill {
			vp.Content = c.Proposal.NewContent
		}
		for _, id := range c.Proposal.EvidenceIDs {
			vp.Evidence = append(vp.Evidence, DraftID(id))
			if d, ok := env.Shaped.Drafts[id]; ok && !seen[id] {
				seen[id] = true
				in.Evidence = append(in.Evidence, toDraftIn(d))
			}
		}
		in.Proposals = append(in.Proposals, vp)
		query += c.Proposal.Title + " " + c.Added + "\n"
	}
	if len(in.Proposals) == 0 {
		return "", nil
	}
	for _, r := range rules.Search(corpus, query, verifierRules) {
		in.Rules = append(in.Rules, ruleIn{ID: r.ID, Text: r.Text, Source: r.SourceLabel, Heading: r.Heading})
		// A contradiction the verifier finds may name any rule it was shown.
		if env.Shaped.Rules != nil {
			env.Shaped.Rules[r.ID] = r.LearnRule
		}
	}
	in.Rules = append(in.Rules, protectedFor(Context{ProjectID: env.ProjectID, Protected: env.Protected})...)
	b, err := json.Marshal(in)
	return string(b), err
}

package rules

import (
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Corpus assembles the rules that apply in project - every global source and
// the project's own - in source and chunk order. A project of "" gets the
// global rules only. A chunk shared by two sources contributes its rules once;
// a chunk not atomized yet contributes nothing.
func Corpus(sources []domain.LearnRuleSource, chunks map[string]domain.LearnRuleChunk, project domain.ProjectID) []domain.LearnRule {
	var out []domain.LearnRule
	seen := map[string]bool{}
	for _, s := range sources {
		if s.Scope != domain.LearnRuleGlobal && (project == "" || s.ProjectID != project) {
			continue
		}
		for _, ref := range s.Chunks {
			c, ok := chunks[ref.Hash]
			if !ok || seen[ref.Hash] {
				continue
			}
			seen[ref.Hash] = true
			for i, a := range c.Atoms {
				out = append(out, domain.LearnRule{
					ID: RuleID(ref.Hash, i), LearnRuleAtom: a, SourceKey: s.Key, SourceLabel: s.Label,
					SourceKind: s.Kind, Scope: s.Scope, ProjectID: s.ProjectID,
				})
			}
		}
	}
	return out
}

// RuleID names a rule by its chunk and position: stable while the chunk's
// text is.
func RuleID(chunkHash string, i int) string {
	if len(chunkHash) > 12 {
		chunkHash = chunkHash[:12]
	}
	return chunkHash + "-" + strconv.Itoa(i)
}

// Scored is a rule with its search score.
type Scored struct {
	domain.LearnRule
	Score float64
}

// Search ranks corpus against query by BM25 over each rule's text, heading
// and tags.
func Search(corpus []domain.LearnRule, query string, limit int) []Scored {
	docs := make([]Doc, len(corpus))
	byID := make(map[string]domain.LearnRule, len(corpus))
	for i, r := range corpus {
		docs[i] = Doc{ID: r.ID, Text: r.Text + "\n" + r.Heading + "\n" + strings.Join(r.Tags, " ")}
		byID[r.ID] = r
	}
	hits := NewIndex(docs).Search(query, limit)
	out := make([]Scored, 0, len(hits))
	for _, h := range hits {
		out = append(out, Scored{LearnRule: byID[h.ID], Score: h.Score})
	}
	return out
}

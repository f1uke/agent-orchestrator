package learning

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/rules"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/learnrules"
)

// RulesStore is the persistence the standing-rules corpus needs.
type RulesStore interface {
	ListLearnRuleSources(ctx context.Context) ([]domain.LearnRuleSource, error)
	ListLearnRuleChunks(ctx context.Context) (map[string]domain.LearnRuleChunk, error)
	ListLearnProtectedRules(ctx context.Context) ([]domain.LearnProtectedRule, error)
	InsertLearnProtectedRule(ctx context.Context, r domain.LearnProtectedRule) (int64, error)
	DeleteLearnProtectedRule(ctx context.Context, id int64) (bool, error)
}

// RulesRefresher is the refresh loop, as the service drives it.
type RulesRefresher interface {
	RunNow(ctx context.Context, budgetUSD float64) error
	Progress() learnrules.Progress
}

// ErrRulesUnavailable is a rules request on a daemon without the corpus.
var ErrRulesUnavailable = errors.New("the rules corpus is not available")

// ErrUnknownRule is a rule id the corpus does not have.
var ErrUnknownRule = errors.New("unknown rule")

// ErrInvalidBudget is a manual run budget out of range.
var ErrInvalidBudget = errors.New("invalid budget")

// ErrInvalidProtectedRule is a protected rule that cannot be saved.
var ErrInvalidProtectedRule = errors.New("invalid protected rule")

// WithRules wires the standing-rules corpus in. runCtx is the daemon's
// context; a manual refresh continues on it. A nil refresher leaves the corpus
// readable but not refreshable.
func (s *Service) WithRules(runCtx context.Context, st RulesStore, r RulesRefresher) *Service {
	s.runCtx, s.rulesStore, s.rules = runCtx, st, r
	return s
}

// RuleHit is a rule of the corpus, with its score when it answers a search.
type RuleHit struct {
	domain.LearnRule
	Score float64
}

// Rules lists the rules that apply in project (global ones and the project's
// own), or, with a query, the best matches for it.
func (s *Service) Rules(ctx context.Context, project domain.ProjectID, query string, limit int) ([]RuleHit, error) {
	corpus, err := s.corpus(ctx, project)
	if err != nil {
		return nil, err
	}
	var out []RuleHit
	if strings.TrimSpace(query) != "" {
		for _, h := range rules.Search(corpus, query, limit) {
			out = append(out, RuleHit{LearnRule: h.LearnRule, Score: h.Score})
		}
		return out, nil
	}
	for _, r := range corpus {
		if limit > 0 && len(out) == limit {
			break
		}
		out = append(out, RuleHit{LearnRule: r})
	}
	return out, nil
}

func (s *Service) corpus(ctx context.Context, project domain.ProjectID) ([]domain.LearnRule, error) {
	if s.rulesStore == nil {
		return nil, ErrRulesUnavailable
	}
	if project != "" {
		if err := s.requireProject(ctx, project); err != nil {
			return nil, err
		}
	}
	sources, err := s.rulesStore.ListLearnRuleSources(ctx)
	if err != nil {
		return nil, err
	}
	chunks, err := s.rulesStore.ListLearnRuleChunks(ctx)
	if err != nil {
		return nil, err
	}
	return rules.Corpus(sources, chunks, project), nil
}

// RuleSource is a source of the corpus with how many rules it holds now.
type RuleSource struct {
	domain.LearnRuleSource
	Rules int
}

// RulesStatus is the corpus's sources and the refresh loop's last pass.
type RulesStatus struct {
	Sources  []RuleSource
	Progress learnrules.Progress
}

// RuleSources lists the sources that apply in project ("" = every source).
func (s *Service) RuleSources(ctx context.Context, project domain.ProjectID) (RulesStatus, error) {
	if s.rulesStore == nil {
		return RulesStatus{}, ErrRulesUnavailable
	}
	if project != "" {
		if err := s.requireProject(ctx, project); err != nil {
			return RulesStatus{}, err
		}
	}
	sources, err := s.rulesStore.ListLearnRuleSources(ctx)
	if err != nil {
		return RulesStatus{}, err
	}
	chunks, err := s.rulesStore.ListLearnRuleChunks(ctx)
	if err != nil {
		return RulesStatus{}, err
	}
	out := RulesStatus{}
	if s.rules != nil {
		out.Progress = s.rules.Progress()
	}
	for _, src := range sources {
		if project != "" && src.Scope != domain.LearnRuleGlobal && src.ProjectID != project {
			continue
		}
		n := 0
		for _, c := range src.Chunks {
			n += len(chunks[c.Hash].Atoms)
		}
		out.Sources = append(out.Sources, RuleSource{LearnRuleSource: src, Rules: n})
	}
	return out, nil
}

// RefreshRules starts a refresh of the corpus at once under its own budget.
func (s *Service) RefreshRules(_ context.Context, budgetUSD float64) error {
	if s.rules == nil {
		return ErrRulesUnavailable
	}
	if budgetUSD <= 0 || budgetUSD > MaxManualBudgetUSD {
		return fmt.Errorf("%w: budget must be above 0 and at most %.0f", ErrInvalidBudget, MaxManualBudgetUSD)
	}
	return s.rules.RunNow(s.runCtx, budgetUSD)
}

// ProtectedRules lists the pinned rules that apply in project (global ones and
// the project's own; "" = all).
func (s *Service) ProtectedRules(ctx context.Context, project domain.ProjectID) ([]domain.LearnProtectedRule, error) {
	if s.rulesStore == nil {
		return nil, ErrRulesUnavailable
	}
	all, err := s.rulesStore.ListLearnProtectedRules(ctx)
	if err != nil {
		return nil, err
	}
	var out []domain.LearnProtectedRule
	for _, r := range all {
		if project == "" || r.ProjectID == "" || r.ProjectID == project {
			out = append(out, r)
		}
	}
	return out, nil
}

// ProtectRequest pins a rule: either its own text, or the text of a corpus
// rule named by From.
type ProtectRequest struct {
	ProjectID domain.ProjectID
	Text      string
	From      string
	Patterns  []string
	Note      string
}

// Protect pins a rule.
func (s *Service) Protect(ctx context.Context, req ProtectRequest) (domain.LearnProtectedRule, error) {
	if s.rulesStore == nil {
		return domain.LearnProtectedRule{}, ErrRulesUnavailable
	}
	if req.ProjectID != "" {
		if err := s.requireProject(ctx, req.ProjectID); err != nil {
			return domain.LearnProtectedRule{}, err
		}
	}
	text := strings.TrimSpace(req.Text)
	if req.From != "" {
		if text != "" {
			return domain.LearnProtectedRule{}, fmt.Errorf("%w: give the text or a rule to copy it from, not both", ErrInvalidProtectedRule)
		}
		corpus, err := s.corpus(ctx, req.ProjectID)
		if err != nil {
			return domain.LearnProtectedRule{}, err
		}
		for _, r := range corpus {
			if r.ID == req.From {
				text = r.Text
				break
			}
		}
		if text == "" {
			return domain.LearnProtectedRule{}, fmt.Errorf("%w: %s", ErrUnknownRule, req.From)
		}
	}
	if text == "" {
		return domain.LearnProtectedRule{}, fmt.Errorf("%w: the rule text is required", ErrInvalidProtectedRule)
	}
	if _, err := rules.CompilePatterns(req.Patterns); err != nil {
		return domain.LearnProtectedRule{}, fmt.Errorf("%w: %v", ErrInvalidProtectedRule, err)
	}
	now := s.clock()
	r := domain.LearnProtectedRule{ProjectID: req.ProjectID, Text: text, Patterns: req.Patterns, Note: strings.TrimSpace(req.Note), CreatedAt: now, UpdatedAt: now}
	id, err := s.rulesStore.InsertLearnProtectedRule(ctx, r)
	if err != nil {
		return domain.LearnProtectedRule{}, err
	}
	r.ID = id
	return r, nil
}

// Unprotect removes a pinned rule.
func (s *Service) Unprotect(ctx context.Context, id int64) error {
	if s.rulesStore == nil {
		return ErrRulesUnavailable
	}
	ok, err := s.rulesStore.DeleteLearnProtectedRule(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: %d", ErrUnknownRule, id)
	}
	return nil
}

// CheckForbidden reports every forbidden pattern that text matches in project.
func (s *Service) CheckForbidden(ctx context.Context, project domain.ProjectID, text string) ([]rules.ForbiddenHit, error) {
	if s.rulesStore == nil {
		return nil, ErrRulesUnavailable
	}
	if project != "" {
		if err := s.requireProject(ctx, project); err != nil {
			return nil, err
		}
	}
	protected, err := s.rulesStore.ListLearnProtectedRules(ctx)
	if err != nil {
		return nil, err
	}
	return rules.Forbidden(text, project, protected), nil
}

func (s *Service) requireProject(ctx context.Context, project domain.ProjectID) error {
	_, ok, err := s.store.GetProject(ctx, string(project))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownProject, project)
	}
	return nil
}

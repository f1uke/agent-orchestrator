package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ListLearnRuleSources returns every source of the standing-rules corpus.
func (s *Store) ListLearnRuleSources(ctx context.Context) ([]domain.LearnRuleSource, error) {
	rows, err := s.qr.ListLearnRuleSources(ctx)
	if err != nil {
		return nil, fmt.Errorf("list learn rule sources: %w", err)
	}
	out := make([]domain.LearnRuleSource, 0, len(rows))
	for _, r := range rows {
		src := domain.LearnRuleSource{
			Key: r.Key, Scope: domain.LearnRuleScope(r.Scope), ProjectID: domain.ProjectID(r.ProjectID.String),
			Kind: domain.LearnRuleSourceKind(r.Kind), Label: r.Label, ContentHash: r.ContentHash,
			RefreshedAt: r.RefreshedAt, Error: r.Error,
		}
		if err := json.Unmarshal([]byte(r.ChunksJson), &src.Chunks); err != nil {
			return nil, fmt.Errorf("learn rule source %s: chunks: %w", r.Key, err)
		}
		out = append(out, src)
	}
	return out, nil
}

// UpsertLearnRuleSource records a source as of its latest refresh.
func (s *Store) UpsertLearnRuleSource(ctx context.Context, src domain.LearnRuleSource) error {
	chunks := src.Chunks
	if chunks == nil {
		chunks = []domain.LearnRuleChunkRef{}
	}
	b, err := json.Marshal(chunks)
	if err != nil {
		return fmt.Errorf("marshal learn rule chunks: %w", err)
	}
	var project sql.NullString
	if src.ProjectID != "" {
		project = sql.NullString{String: string(src.ProjectID), Valid: true}
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpsertLearnRuleSource(ctx, gen.UpsertLearnRuleSourceParams{
		Key: src.Key, Scope: string(src.Scope), ProjectID: project, Kind: string(src.Kind), Label: src.Label,
		ContentHash: src.ContentHash, ChunksJson: string(b), RefreshedAt: src.RefreshedAt.UTC(), Error: src.Error,
	}); err != nil {
		return fmt.Errorf("upsert learn rule source %s: %w", src.Key, err)
	}
	return nil
}

// DeleteLearnRuleSource forgets a source that no longer exists.
func (s *Store) DeleteLearnRuleSource(ctx context.Context, key string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.DeleteLearnRuleSource(ctx, key); err != nil {
		return fmt.Errorf("delete learn rule source %s: %w", key, err)
	}
	return nil
}

// LearnRuleChunkHashes returns the hash of every cached chunk, with when it
// was cached.
func (s *Store) LearnRuleChunkHashes(ctx context.Context) (map[string]time.Time, error) {
	rows, err := s.qr.ListLearnRuleChunkHashes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list learn rule chunk hashes: %w", err)
	}
	out := make(map[string]time.Time, len(rows))
	for _, r := range rows {
		out[r.Hash] = r.CreatedAt
	}
	return out, nil
}

// ListLearnRuleChunks returns every cached chunk, keyed by hash.
func (s *Store) ListLearnRuleChunks(ctx context.Context) (map[string]domain.LearnRuleChunk, error) {
	rows, err := s.qr.ListLearnRuleChunks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list learn rule chunks: %w", err)
	}
	out := make(map[string]domain.LearnRuleChunk, len(rows))
	for _, r := range rows {
		c := domain.LearnRuleChunk{
			Hash: r.Hash, Model: r.Model, Rejected: int(r.Rejected), CostUSD: r.CostUsd,
			InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, DurationMS: r.DurationMs, CreatedAt: r.CreatedAt,
		}
		if err := json.Unmarshal([]byte(r.AtomsJson), &c.Atoms); err != nil {
			return nil, fmt.Errorf("learn rule chunk %s: atoms: %w", r.Hash, err)
		}
		out[r.Hash] = c
	}
	return out, nil
}

// InsertLearnRuleChunk caches a chunk's atoms. A chunk already cached is left
// as it is: the same hash is the same text.
func (s *Store) InsertLearnRuleChunk(ctx context.Context, c domain.LearnRuleChunk) error {
	atoms := c.Atoms
	if atoms == nil {
		atoms = []domain.LearnRuleAtom{}
	}
	b, err := json.Marshal(atoms)
	if err != nil {
		return fmt.Errorf("marshal learn rule atoms: %w", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.InsertLearnRuleChunk(ctx, gen.InsertLearnRuleChunkParams{
		Hash: c.Hash, Model: c.Model, AtomsJson: string(b), Atoms: int64(len(atoms)), Rejected: int64(c.Rejected),
		CostUsd: c.CostUSD, InputTokens: c.InputTokens, OutputTokens: c.OutputTokens, DurationMs: c.DurationMS,
		CreatedAt: c.CreatedAt.UTC(),
	}); err != nil {
		return fmt.Errorf("insert learn rule chunk: %w", err)
	}
	return nil
}

// DeleteLearnRuleChunk drops a cached chunk no source refers to any more.
func (s *Store) DeleteLearnRuleChunk(ctx context.Context, hash string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.DeleteLearnRuleChunk(ctx, hash); err != nil {
		return fmt.Errorf("delete learn rule chunk: %w", err)
	}
	return nil
}

// InsertLearnProtectedRule pins a rule.
func (s *Store) InsertLearnProtectedRule(ctx context.Context, r domain.LearnProtectedRule) (int64, error) {
	patterns := r.Patterns
	if patterns == nil {
		patterns = []string{}
	}
	b, err := json.Marshal(patterns)
	if err != nil {
		return 0, fmt.Errorf("marshal protected rule patterns: %w", err)
	}
	var project sql.NullString
	if r.ProjectID != "" {
		project = sql.NullString{String: string(r.ProjectID), Valid: true}
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	id, err := s.qw.InsertLearnProtectedRule(ctx, gen.InsertLearnProtectedRuleParams{
		ProjectID: project, Text: r.Text, PatternsJson: string(b), Note: r.Note,
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	})
	if err != nil {
		return 0, fmt.Errorf("insert protected rule: %w", err)
	}
	return id, nil
}

// ListLearnProtectedRules returns every pinned rule, oldest first.
func (s *Store) ListLearnProtectedRules(ctx context.Context) ([]domain.LearnProtectedRule, error) {
	rows, err := s.qr.ListLearnProtectedRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("list protected rules: %w", err)
	}
	out := make([]domain.LearnProtectedRule, 0, len(rows))
	for _, r := range rows {
		p := domain.LearnProtectedRule{
			ID: r.ID, ProjectID: domain.ProjectID(r.ProjectID.String), Text: r.Text, Note: r.Note,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		}
		if err := json.Unmarshal([]byte(r.PatternsJson), &p.Patterns); err != nil {
			return nil, fmt.Errorf("protected rule %d: patterns: %w", r.ID, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// DeleteLearnProtectedRule unpins a rule. It reports whether the rule existed.
func (s *Store) DeleteLearnProtectedRule(ctx context.Context, id int64) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.DeleteLearnProtectedRule(ctx, id)
	if err != nil {
		return false, fmt.Errorf("delete protected rule: %w", err)
	}
	return n > 0, nil
}

// learnRuleSpendSince is what atomizing chunks cost since the moment.
func (s *Store) learnRuleSpendSince(ctx context.Context, since time.Time) (float64, error) {
	v, err := s.qr.SumLearnRuleChunkCostSince(ctx, since)
	if err != nil {
		return 0, fmt.Errorf("sum learn rule chunk cost: %w", err)
	}
	return v, nil
}

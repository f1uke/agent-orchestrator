package learnrules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// maxSourceBytes skips a file too large to be an instruction file (a
// generated dump checked in by accident).
const maxSourceBytes = 1 << 20

// Files lists the sources from disk and from AO's own prompts. Only projects
// that learn from sessions contribute, and the human's global files only while
// at least one does.
type Files struct {
	// Home is the human's home directory (~/.claude lives under it).
	Home string
	// DataDir is AO's data dir, where AO installs its own skills.
	DataDir string
	// KnowledgeDir is the knowledge store whose <project>/INDEX.md agents are
	// told to read (prompts.go names ~/.ao/knowledge).
	KnowledgeDir string
	// Projects lists the registered projects.
	Projects func(ctx context.Context) ([]domain.ProjectRecord, error)
	// Prompts returns AO's assembled standing prompt per session kind
	// ("orchestrator", "worker") for a project. Nil leaves them out.
	Prompts func(ctx context.Context, projectID domain.ProjectID) (map[string]string, error)
}

// List implements Lister.
func (f Files) List(ctx context.Context) ([]Source, error) {
	projects, err := f.Projects(ctx)
	if err != nil {
		return nil, err
	}
	var learning []domain.ProjectRecord
	for _, p := range projects {
		if p.Config.LearnFromSessions && p.ArchivedAt.IsZero() {
			learning = append(learning, p)
		}
	}
	if len(learning) == 0 {
		return nil, nil
	}
	var out []Source
	add := func(scope domain.LearnRuleScope, project domain.ProjectID, kind domain.LearnRuleSourceKind, path string, deterministic bool) {
		b, err := os.ReadFile(path)
		if err != nil || len(b) == 0 || len(b) > maxSourceBytes {
			return
		}
		label := f.label(path)
		out = append(out, Source{Key: key(scope, project, kind, label), Scope: scope, ProjectID: project, Kind: kind,
			Label: label, Text: string(b), Deterministic: deterministic})
	}

	claude := filepath.Join(f.Home, ".claude")
	add(domain.LearnRuleGlobal, "", domain.LearnRuleSourceClaudeMD, filepath.Join(claude, "CLAUDE.md"), false)
	for _, p := range skillFiles(filepath.Join(claude, "skills")) {
		add(domain.LearnRuleGlobal, "", domain.LearnRuleSourceSkill, p, false)
	}
	for _, p := range skillFiles(filepath.Join(f.DataDir, "skills")) {
		add(domain.LearnRuleGlobal, "", domain.LearnRuleSourceSkill, p, false)
	}

	for _, p := range learning {
		id := domain.ProjectID(p.ID)
		if p.Path != "" {
			add(domain.LearnRuleProject, id, domain.LearnRuleSourceClaudeMD, filepath.Join(p.Path, "CLAUDE.md"), false)
			add(domain.LearnRuleProject, id, domain.LearnRuleSourceAgentsMD, filepath.Join(p.Path, "AGENTS.md"), false)
			for _, s := range skillFiles(filepath.Join(p.Path, ".claude", "skills")) {
				add(domain.LearnRuleProject, id, domain.LearnRuleSourceSkill, s, false)
			}
		}
		add(domain.LearnRuleProject, id, domain.LearnRuleSourceKnowledgeIndex, filepath.Join(f.KnowledgeDir, p.ID, "INDEX.md"), true)
		if f.Prompts == nil {
			continue
		}
		prompts, err := f.Prompts(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("standing prompts for %s: %w", p.ID, err)
		}
		kinds := make([]string, 0, len(prompts))
		for k := range prompts {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		for _, k := range kinds {
			if strings.TrimSpace(prompts[k]) == "" {
				continue
			}
			label := "AO " + k + " prompt (" + p.ID + ")"
			out = append(out, Source{Key: key(domain.LearnRuleProject, id, domain.LearnRuleSourceAOPrompt, label),
				Scope: domain.LearnRuleProject, ProjectID: id, Kind: domain.LearnRuleSourceAOPrompt, Label: label, Text: prompts[k]})
		}
	}
	return out, nil
}

// skillFiles returns dir/*/SKILL.md, following symlinked skill directories,
// in name order.
func skillFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		p := filepath.Join(dir, e.Name(), "SKILL.md")
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			out = append(out, p)
		}
	}
	return out
}

// label shows a path with the home directory as ~.
func (f Files) label(path string) string {
	if f.Home != "" {
		if rel, err := filepath.Rel(f.Home, path); err == nil && !strings.HasPrefix(rel, "..") {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return path
}

func key(scope domain.LearnRuleScope, project domain.ProjectID, kind domain.LearnRuleSourceKind, label string) string {
	return string(scope) + ":" + string(project) + ":" + string(kind) + ":" + label
}

// Package skills reads and checks skill files for learning's decide stage:
// an index of every skill an agent may already have (so a lesson updates the
// skill it belongs to instead of duplicating it), and the frontmatter and size
// rules a learned skill must meet before it is ever proposed.
package skills

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Source is where a skill comes from.
type Source string

const (
	// SourceUser is ~/.claude/skills.
	SourceUser Source = "user"
	// SourcePlugin is an installed Claude Code plugin.
	SourcePlugin Source = "plugin"
	// SourceAO is a skill AO installs (using-ao).
	SourceAO Source = "ao"
	// SourceRepo is a repo's team-shared .claude/skills (read only).
	SourceRepo Source = "repo"
	// SourceLearned is a skill learning wrote under ~/.ao/learned.
	SourceLearned Source = "learned"
)

// Skill is one skill file.
type Skill struct {
	Name        string
	Description string
	Source      Source
	// Scope is "global" or "project:<id>".
	Scope string
	Path  string
	Body  string
}

// Limits a learned skill must meet (design §3.8).
const (
	MaxDescriptionBytes = 300
	MaxBodyBytes        = 8 << 10
	MaxGlobalLearned    = 20
	MaxProjectLearned   = 30
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Frontmatter is the part of a SKILL.md the harness reads.
type Frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// Parse splits a SKILL.md into its frontmatter and body.
func Parse(content string) (Frontmatter, string, error) {
	var fm Frontmatter
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		return fm, "", errors.New("no frontmatter: a skill file starts with ---")
	}
	head, body, ok := strings.Cut(rest, "\n---")
	if !ok {
		return fm, "", errors.New("frontmatter is not closed with ---")
	}
	if err := yaml.Unmarshal([]byte(head), &fm); err != nil {
		return fm, "", fmt.Errorf("frontmatter: %w", err)
	}
	body = strings.TrimPrefix(body, "\n")
	return fm, body, nil
}

// Check applies the rules a learned skill file must meet: frontmatter that
// parses, a name the harness accepts, a short description that says when to
// use it, and a bounded body.
func Check(content string) (Frontmatter, error) {
	fm, body, err := Parse(content)
	if err != nil {
		return fm, err
	}
	switch {
	case !namePattern.MatchString(fm.Name):
		return fm, fmt.Errorf("name %q must be lowercase letters, digits and dashes", fm.Name)
	case strings.TrimSpace(fm.Description) == "":
		return fm, errors.New("description is required")
	case len(fm.Description) > MaxDescriptionBytes:
		return fm, fmt.Errorf("description is %d bytes, at most %d", len(fm.Description), MaxDescriptionBytes)
	case !strings.Contains(strings.ToLower(fm.Description), "use when"):
		return fm, errors.New(`description must say when to use the skill ("Use when ...")`)
	case len(body) > MaxBodyBytes:
		return fm, fmt.Errorf("body is %d bytes, at most %d", len(body), MaxBodyBytes)
	case strings.TrimSpace(body) == "":
		return fm, errors.New("body is empty")
	}
	return fm, nil
}

// Dirs says where to look for skills.
type Dirs struct {
	Home    string
	DataDir string
	// Learned is ~/.ao/learned.
	Learned string
	// Repos maps a project id to its repo path.
	Repos map[string]string
}

// Index lists every skill in every source, by source then name. A file that
// does not parse is listed by its directory name with no description, so it
// still counts against name uniqueness.
func Index(d Dirs) []Skill {
	var out []Skill
	add := func(src Source, scope, dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			p := filepath.Join(dir, e.Name(), "SKILL.md")
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			fm, body, err := Parse(string(b))
			if err != nil || fm.Name == "" {
				fm.Name = e.Name()
			}
			out = append(out, Skill{Name: fm.Name, Description: strings.TrimSpace(fm.Description), Source: src, Scope: scope, Path: p, Body: body})
		}
	}
	add(SourceUser, "global", filepath.Join(d.Home, ".claude", "skills"))
	for _, dir := range pluginSkillDirs(d.Home) {
		add(SourcePlugin, "global", dir)
	}
	add(SourceAO, "global", filepath.Join(d.DataDir, "skills"))
	add(SourceLearned, "global", filepath.Join(d.Learned, "global", "skills"))
	projects := make([]string, 0, len(d.Repos))
	for id := range d.Repos {
		projects = append(projects, id)
	}
	sort.Strings(projects)
	for _, id := range projects {
		add(SourceLearned, "project:"+id, filepath.Join(d.Learned, "projects", id, "skills"))
		if d.Repos[id] != "" {
			add(SourceRepo, "project:"+id, filepath.Join(d.Repos[id], ".claude", "skills"))
		}
	}
	return out
}

// pluginSkillDirs reads Claude Code's installed-plugins manifest; the cache
// also holds old versions, which no session loads.
func pluginSkillDirs(home string) []string {
	b, err := os.ReadFile(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
	if err != nil {
		return nil
	}
	var m struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(bytes.TrimSpace(b), &m) != nil {
		return nil
	}
	var out []string
	for _, installs := range m.Plugins {
		for _, i := range installs {
			if i.InstallPath != "" {
				out = append(out, filepath.Join(i.InstallPath, "skills"))
			}
		}
	}
	sort.Strings(out)
	return out
}

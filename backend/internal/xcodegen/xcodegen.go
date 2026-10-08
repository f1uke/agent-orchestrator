// Package xcodegen runs `xcodegen generate` over a worktree's specs, and says
// whether a generated project has fallen behind the spec it came from.
package xcodegen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/fsatomic"
)

// SpecFile is the name xcodegen reads by default.
const SpecFile = "project.yml"

// Status is how a generate went as a whole.
type Status string

const (
	StatusNotInstalled Status = "not-installed"
	StatusNoSpecs      Status = "no-specs"
	StatusRan          Status = "ran"
)

// DirResult is one spec directory's generate.
type DirResult struct {
	// Dir is relative to the worktree, "." for its root.
	Dir      string `json:"dir"`
	OK       bool   `json:"ok"`
	ExitCode *int   `json:"exitCode"`
	Output   string `json:"output"`
}

// Result is a generate over a whole worktree.
type Result struct {
	Status  Status      `json:"status" enum:"not-installed,no-specs,ran"`
	Root    string      `json:"root,omitempty"`
	Results []DirResult `json:"results"`
}

// Exec runs one command in a directory and returns its combined output and exit
// code. A missing binary is exec.ErrNotFound.
type Exec func(ctx context.Context, dir, name string, args ...string) (output []byte, exitCode int, err error)

// skipDir names the directories a spec is never in: dependency checkouts, build
// output (nter keeps DerivedData in-tree as `derivedDataPath`) and dot dirs.
func skipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "Pods", "Carthage", "build", "SourcePackages":
		return true
	}
	return strings.Contains(strings.ToLower(name), "deriveddata")
}

// FindSpecDirs is every directory under root holding a project.yml, sorted.
func FindSpecDirs(root string) []string {
	var found []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == SpecFile {
			found = append(found, filepath.Dir(path))
		}
		return nil
	})
	sort.Strings(found)
	return found
}

// HasRootSpec is whether root itself holds a project.yml: an xcodegen project
// whose Xcode project has never been generated still deserves a run bar.
func HasRootSpec(root string) bool {
	info, err := os.Stat(filepath.Join(root, SpecFile))
	return err == nil && !info.IsDir()
}

// Generator runs xcodegen and remembers what each spec looked like when it last
// generated, in one JSON file.
type Generator struct {
	exec      Exec
	binary    func() (string, error)
	stateFile string
	tempDir   func() string

	mu sync.Mutex
}

// New builds a Generator. stateFile is where the fingerprints are kept; empty
// keeps none, and nothing is ever stale.
func New(stateFile string, opts ...Option) *Generator {
	g := &Generator{exec: runInDir, binary: lookPath, stateFile: stateFile, tempDir: os.TempDir}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// Option configures a Generator.
type Option func(*Generator)

// WithExec replaces the command runner; tests use it.
func WithExec(exec Exec) Option { return func(g *Generator) { g.exec = exec } }

// WithBinary replaces how the xcodegen binary is found; tests use it.
func WithBinary(find func() (string, error)) Option { return func(g *Generator) { g.binary = find } }

func lookPath() (string, error) {
	if path, err := exec.LookPath("xcodegen"); err == nil {
		return path, nil
	}
	for _, path := range []string{"/opt/homebrew/bin/xcodegen", "/usr/local/bin/xcodegen"} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", exec.ErrNotFound
}

func runInDir(ctx context.Context, dir, name string, args ...string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // xcodegen, resolved by this package
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out, exit.ExitCode(), nil
	}
	if err != nil {
		return out, -1, err
	}
	return out, 0, nil
}

// Generate runs `xcodegen generate` in every spec directory under root, one at a
// time: a spec's postGenCommand (nter's runs `pod install`) must not race
// another's.
func (g *Generator) Generate(ctx context.Context, root string) Result {
	dirs := FindSpecDirs(root)
	if len(dirs) == 0 {
		return Result{Status: StatusNoSpecs, Root: root}
	}
	binary, err := g.binary()
	if err != nil {
		return Result{Status: StatusNotInstalled}
	}
	result := Result{Status: StatusRan, Root: root}
	for _, dir := range dirs {
		rel, relErr := filepath.Rel(root, dir)
		if relErr != nil {
			rel = dir
		}
		out, code, err := g.exec(ctx, dir, binary, "generate")
		if errors.Is(err, exec.ErrNotFound) {
			return Result{Status: StatusNotInstalled}
		}
		dirResult := DirResult{Dir: rel, OK: err == nil && code == 0, Output: strings.TrimSpace(string(out))}
		if err == nil {
			exitCode := code
			dirResult.ExitCode = &exitCode
		} else if dirResult.Output == "" {
			dirResult.Output = err.Error()
		}
		if dirResult.OK {
			g.baseline(ctx, binary, dir)
		}
		result.Results = append(result.Results, dirResult)
	}
	return result
}

// Spec is one spec directory's standing, relative to the worktree.
type Spec struct {
	Dir string `json:"dir"`
	// Stale is set when the spec, a spec it includes, or the set of source files
	// it names changed since the project was last generated, or the project was
	// never generated at all.
	Stale bool `json:"stale"`
}

// Specs is every spec under root and whether its project is behind it.
// Without xcodegen installed the specs are still listed, none of them stale:
// nothing can tell.
func (g *Generator) Specs(ctx context.Context, root string) (installed bool, specs []Spec) {
	dirs := FindSpecDirs(root)
	binary, err := g.binary()
	installed = err == nil
	specs = make([]Spec, 0, len(dirs))
	for _, dir := range dirs {
		rel, relErr := filepath.Rel(root, dir)
		if relErr != nil {
			rel = dir
		}
		spec := Spec{Dir: rel}
		if installed {
			spec.Stale = g.stale(ctx, binary, dir)
		}
		specs = append(specs, spec)
	}
	return installed, specs
}

type record struct {
	ProjectModTime time.Time `json:"projectModTime"`
	Fingerprint    string    `json:"fingerprint"`
}

// stale compares xcodegen's own cache key for the spec - the resolved spec with
// its includes merged, plus every source path it names - with the one recorded
// when the project file was last written. xcodegen rewrites project.pbxproj on
// every generate, so a project file newer than the record means somebody
// generated (in a terminal too) and the record is taken again.
func (g *Generator) stale(ctx context.Context, binary, dir string) bool {
	fingerprint, name, err := g.fingerprint(ctx, binary, dir)
	if err != nil {
		return false
	}
	project := filepath.Join(dir, name+".xcodeproj", "project.pbxproj")
	info, err := os.Stat(project)
	if err != nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	records := g.read()
	if rec, ok := records[dir]; ok && rec.ProjectModTime.Equal(info.ModTime()) {
		return rec.Fingerprint != fingerprint
	}
	if specsNewerThan(dir, info.ModTime()) {
		return true
	}
	records[dir] = record{ProjectModTime: info.ModTime(), Fingerprint: fingerprint}
	g.write(records)
	return false
}

// baseline records the spec as it is right after a successful generate.
func (g *Generator) baseline(ctx context.Context, binary, dir string) {
	fingerprint, name, err := g.fingerprint(ctx, binary, dir)
	if err != nil {
		return
	}
	info, err := os.Stat(filepath.Join(dir, name+".xcodeproj", "project.pbxproj"))
	if err != nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	records := g.read()
	records[dir] = record{ProjectModTime: info.ModTime(), Fingerprint: fingerprint}
	g.write(records)
}

// fingerprint runs `xcodegen cache`, which writes the cache key without
// generating anything (0.45 s on nter), and returns its hash and the project
// name the spec declares.
func (g *Generator) fingerprint(ctx context.Context, binary, dir string) (string, string, error) {
	f, err := os.CreateTemp(g.tempDir(), "ao-xcodegen-cache-")
	if err != nil {
		return "", "", err
	}
	path := f.Name()
	_ = f.Close()
	defer func() { _ = os.Remove(path) }()
	out, code, err := g.exec(ctx, dir, binary, "cache", "--cache-path", path, "--quiet")
	if err != nil || code != 0 {
		return "", "", fmt.Errorf("xcodegen cache in %s: exit %d: %s", dir, code, strings.TrimSpace(string(out)))
	}
	body, err := os.ReadFile(path) //nolint:gosec // this package's own temp file
	if err != nil {
		return "", "", err
	}
	name := cachedProjectName(body)
	if name == "" {
		return "", "", fmt.Errorf("xcodegen cache in %s named no project", dir)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), name, nil
}

// cachedProjectName reads `name` from the "# SPEC" JSON section of a cache file.
func cachedProjectName(cache []byte) string {
	_, rest, ok := bytes.Cut(cache, []byte("# SPEC\n"))
	if !ok {
		return ""
	}
	if end := bytes.Index(rest, []byte("\n# ")); end >= 0 {
		rest = rest[:end]
	}
	var spec struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rest, &spec); err != nil {
		return ""
	}
	return spec.Name
}

// specsNewerThan is whether project.yml or any spec it includes changed after t.
// It is the check for a project generated before AO first looked: a record
// taken then would bless a spec that was already ahead of it.
func specsNewerThan(dir string, t time.Time) bool {
	seen := map[string]bool{}
	var newer func(path string, depth int) bool
	newer = func(path string, depth int) bool {
		if seen[path] || depth > 8 {
			return false
		}
		seen[path] = true
		info, err := os.Stat(path)
		if err != nil {
			return false
		}
		if info.ModTime().After(t) {
			return true
		}
		for _, include := range includesOf(path) {
			if !filepath.IsAbs(include) {
				include = filepath.Join(filepath.Dir(path), include)
			}
			if newer(include, depth+1) {
				return true
			}
		}
		return false
	}
	return newer(filepath.Join(dir, SpecFile), 0)
}

// includesOf reads a spec's top-level `include`, which xcodegen accepts as one
// path, a list of paths, or a list of {path: ...}.
func includesOf(path string) []string {
	body, err := os.ReadFile(path) //nolint:gosec // a spec inside the session's worktree
	if err != nil {
		return nil
	}
	var spec struct {
		Include yaml.Node `yaml:"include"`
	}
	if err := yaml.Unmarshal(body, &spec); err != nil {
		return nil
	}
	var paths []string
	add := func(n *yaml.Node) {
		switch n.Kind {
		case yaml.ScalarNode:
			paths = append(paths, n.Value)
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == "path" {
					paths = append(paths, n.Content[i+1].Value)
				}
			}
		}
	}
	switch spec.Include.Kind {
	case yaml.ScalarNode:
		add(&spec.Include)
	case yaml.SequenceNode:
		for _, item := range spec.Include.Content {
			add(item)
		}
	}
	return paths
}

func (g *Generator) read() map[string]record {
	records := map[string]record{}
	if g.stateFile == "" {
		return records
	}
	body, err := os.ReadFile(g.stateFile)
	if err != nil {
		return records
	}
	_ = json.Unmarshal(body, &records)
	return records
}

func (g *Generator) write(records map[string]record) {
	if g.stateFile == "" {
		return
	}
	body, err := json.Marshal(records)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(g.stateFile), 0o750); err != nil {
		return
	}
	_ = fsatomic.WriteFile(g.stateFile, body, 0o600)
}

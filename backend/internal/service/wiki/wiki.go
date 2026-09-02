// Package wiki is the read/run surface behind the Wiki destination: a personal
// note vault on disk, and one coding agent running inside it.
//
// The agent pane is deliberately NOT an AO session. It has no database row, no
// worktree, no branch, and no board card — it is a bare runtime handle
// (`ao-wiki`) created straight against the runtime adapter, the same shape the
// reviewer pane already uses (internal/review/launcher.go). Nothing that sweeps
// sessions can see it: the reaper walks session records, and so does the idle
// closer, so a handle with no record is never reaped by either.
//
// Nothing AO says is injected into the agent. The launch carries the vault as
// its working directory and nothing else — no prompt, no system prompt, no
// tool restrictions — so the agent behaves exactly as it would if the user had
// opened a terminal in the vault themselves.
package wiki

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// HandleID is the runtime handle the Wiki's agent pane runs under. It is fixed
// (there is exactly one vault, so exactly one pane) and matches tmux's session
// name charset.
const HandleID = "ao-wiki"

// maxVaultFiles bounds the vault index. A note vault is thousands of small
// files at most; the cap exists so a mis-pointed vault (a home directory, say)
// cannot make the rail unbounded.
const maxVaultFiles = 20000

// maxNoteBytes bounds a single note read. Well past any hand-written note,
// short enough that a stray binary never streams into the renderer.
const maxNoteBytes = 2 << 20

// Settings is the vault configuration this service reads and writes.
// *wikisettings.Store satisfies it.
type Settings interface {
	VaultPath() string
	Harness() string
	SetHarness(harness string) error
}

// Runtime is the slice of the runtime adapter the Wiki needs: start a pane in a
// directory, tear it down, and ask whether it is still there. The tmux runtime
// satisfies it.
type Runtime interface {
	Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error)
	Destroy(ctx context.Context, handle ports.RuntimeHandle) error
	IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error)
}

// preLauncher is the optional adapter capability invoked immediately before the
// pane is created — claude-code uses it to record folder trust, without which
// the agent would sit on a blocking "do you trust this folder?" prompt in a
// vault it has never seen.
type preLauncher interface {
	PreLaunch(ctx context.Context, cfg ports.LaunchConfig) error
}

// Service answers the Wiki's HTTP surface.
type Service struct {
	settings Settings
	agents   ports.AgentResolver
	runtime  Runtime
	now      func() time.Time

	mu        sync.Mutex
	harness   domain.AgentHarness
	startedAt time.Time
}

// Deps are Service's collaborators.
type Deps struct {
	Settings Settings
	Agents   ports.AgentResolver
	Runtime  Runtime
	// Now supplies the pane's start stamp. nil means time.Now.
	Now func() time.Time
}

// New builds the Wiki service.
func New(deps Deps) *Service {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &Service{settings: deps.Settings, agents: deps.Agents, runtime: deps.Runtime, now: now}
}

// Status is the whole state of the Wiki page in one read.
type Status struct {
	// Configured is false when no vault path is set. Everything else is then
	// meaningless and the surface is hidden.
	Configured bool
	VaultPath  string
	// DisplayPath is the vault path with the user's home replaced by "~". It is
	// computed here because the renderer has no way to know the home directory,
	// and a topbar reading `/Users/someone/Notes` when every other tool in the
	// user's life writes `~/Notes` is noise in the one place the page has to
	// stay quiet.
	DisplayPath string
	// Harness is the agent running, or — when nothing is running — the one the
	// user last chose, so the picker can pre-select it.
	Harness string
	Running bool
	// HandleID is the terminal handle to attach a pane to. Empty unless running.
	HandleID  string
	StartedAt time.Time
}

// Status reports whether a vault is configured and whether its agent is live.
func (s *Service) Status(ctx context.Context) (Status, error) {
	vault := s.vault()
	if vault == "" {
		return Status{}, nil
	}
	st := Status{Configured: true, VaultPath: vault, DisplayPath: HomeRelative(vault), Harness: s.settings.Harness()}
	alive, err := s.runtime.IsAlive(ctx, ports.RuntimeHandle{ID: HandleID})
	if err != nil {
		// A probe that failed is not proof the pane is dead (the same rule the
		// reaper follows), so report it as not-running only when the runtime is
		// definite. An indefinite answer keeps the last known state.
		s.mu.Lock()
		running := !s.startedAt.IsZero()
		harness, startedAt := s.harness, s.startedAt
		s.mu.Unlock()
		if running {
			st.Running, st.HandleID, st.StartedAt = true, HandleID, startedAt
			st.Harness = string(harness)
		}
		return st, nil
	}
	if !alive {
		s.mu.Lock()
		s.startedAt = time.Time{}
		s.mu.Unlock()
		return st, nil
	}
	s.mu.Lock()
	if s.startedAt.IsZero() {
		// The daemon restarted under a pane that outlived it. It is still the
		// user's vault agent, so adopt it rather than orphan it.
		s.startedAt = s.now().UTC()
		if s.harness == "" {
			s.harness = domain.AgentHarness(s.settings.Harness())
		}
	}
	harness, startedAt := s.harness, s.startedAt
	s.mu.Unlock()
	st.Running, st.HandleID, st.StartedAt = true, HandleID, startedAt
	if harness != "" {
		st.Harness = string(harness)
	}
	return st, nil
}

// Start launches the vault's agent, replacing any pane already running. It is
// the one entry point behind both "open" and "switch agent": a switch is a
// start under a different harness.
func (s *Service) Start(ctx context.Context, harness domain.AgentHarness) (Status, error) {
	vault, err := s.requireVault()
	if err != nil {
		return Status{}, err
	}
	if harness == "" {
		harness = domain.AgentHarness(s.settings.Harness())
	}
	if !harness.IsKnown() {
		return Status{}, apierr.Invalid("WIKI_UNKNOWN_HARNESS", fmt.Sprintf("Unknown agent %q", harness), nil)
	}
	agent, ok := s.agents.Agent(harness)
	if !ok {
		return Status{}, apierr.Invalid("WIKI_UNKNOWN_HARNESS", fmt.Sprintf("No adapter for agent %q", harness), nil)
	}

	// Deliberately bare: the vault as the working directory, and nothing else.
	// No Prompt, no SystemPrompt, no tool lists — the agent is the user's, not
	// AO's. Permissions are bypassed because the vault is the user's own notes
	// and the point of the page is that the agent can edit them freely.
	launch := ports.LaunchConfig{
		WorkspacePath: vault,
		Permissions:   ports.PermissionModeBypassPermissions,
	}
	if pl, ok := agent.(preLauncher); ok {
		if err := pl.PreLaunch(ctx, launch); err != nil {
			return Status{}, fmt.Errorf("wiki pre-launch: %w", err)
		}
	}
	argv, err := agent.GetLaunchCommand(ctx, launch)
	if err != nil {
		if errors.Is(err, ports.ErrAgentBinaryNotFound) {
			return Status{}, apierr.Invalid("WIKI_AGENT_NOT_INSTALLED", fmt.Sprintf("%s is not installed", harness), nil)
		}
		return Status{}, fmt.Errorf("wiki launch command: %w", err)
	}

	// Tear down whatever is under the handle first. Destroy is idempotent, so
	// this is equally the "no pane yet" and the "switch agents" path, and it is
	// what keeps `new-session` from failing on a lingering keep-alive shell.
	if err := s.runtime.Destroy(ctx, ports.RuntimeHandle{ID: HandleID}); err != nil {
		return Status{}, fmt.Errorf("wiki stop previous agent: %w", err)
	}
	if _, err := s.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     domain.SessionID(HandleID),
		WorkspacePath: vault,
		Argv:          argv,
	}); err != nil {
		return Status{}, fmt.Errorf("wiki start agent: %w", err)
	}

	s.mu.Lock()
	s.harness, s.startedAt = harness, s.now().UTC()
	startedAt := s.startedAt
	s.mu.Unlock()
	// Remembering the choice is a convenience; a settings write that fails must
	// not undo a pane that is already running.
	_ = s.settings.SetHarness(string(harness))

	return Status{
		Configured:  true,
		VaultPath:   vault,
		DisplayPath: HomeRelative(vault),
		Harness:     string(harness),
		Running:     true,
		HandleID:    HandleID,
		StartedAt:   startedAt,
	}, nil
}

// Restart relaunches the same agent in the same vault with a fresh
// conversation.
func (s *Service) Restart(ctx context.Context) (Status, error) {
	s.mu.Lock()
	harness := s.harness
	s.mu.Unlock()
	if harness == "" {
		harness = domain.AgentHarness(s.settings.Harness())
	}
	if harness == "" {
		return Status{}, apierr.Invalid("WIKI_NO_AGENT", "No agent to restart", nil)
	}
	return s.Start(ctx, harness)
}

// Stop tears the pane down. The vault stays readable with nothing running, so
// this is not a teardown of the page — only of the agent. Idempotent.
func (s *Service) Stop(ctx context.Context) (Status, error) {
	if _, err := s.requireVault(); err != nil {
		return Status{}, err
	}
	if err := s.runtime.Destroy(ctx, ports.RuntimeHandle{ID: HandleID}); err != nil {
		return Status{}, fmt.Errorf("wiki stop agent: %w", err)
	}
	s.mu.Lock()
	s.startedAt = time.Time{}
	s.mu.Unlock()
	return s.Status(ctx)
}

// Note is one file in the vault.
type Note struct {
	// Path is vault-relative and slash-separated, so the renderer can build the
	// folder tree without knowing where the vault lives.
	Path       string
	Size       int64
	ModifiedAt time.Time
}

// Files is the vault index.
type Files struct {
	Notes []Note
	// Truncated reports that the vault holds more files than the cap.
	Truncated bool
}

// ListFiles indexes the vault: every regular file, minus dot-directories (which
// is what keeps `.obsidian`'s own workspace state out of the reader's rail).
func (s *Service) ListFiles(ctx context.Context) (Files, error) {
	vault, err := s.requireVault()
	if err != nil {
		return Files{}, err
	}
	notes := make([]Note, 0, 128)
	truncated := false
	walkErr := filepath.WalkDir(vault, func(p string, d os.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return nil //nolint:nilerr // an unreadable entry is skipped, not fatal
		}
		if d.IsDir() {
			if p != vault && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		rel, relErr := filepath.Rel(vault, p)
		if relErr != nil {
			return nil //nolint:nilerr // unrelatable path, skip
		}
		note := Note{Path: filepath.ToSlash(rel)}
		if info, infoErr := d.Info(); infoErr == nil {
			note.Size = info.Size()
			note.ModifiedAt = info.ModTime().UTC()
		}
		notes = append(notes, note)
		if len(notes) >= maxVaultFiles {
			truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil && ctx.Err() != nil {
		return Files{}, ctx.Err()
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].Path < notes[j].Path })
	return Files{Notes: notes, Truncated: truncated}, nil
}

// NoteContent is one note's raw markdown, as read off disk. It is returned
// unparsed on purpose: rendering happens in the renderer, which treats it as
// untrusted text.
type NoteContent struct {
	Path       string
	Content    string
	Size       int64
	ModifiedAt time.Time
	// Backlinks are the other notes whose text wikilinks to this one, sorted by
	// path. It is the one thing a reader cannot see from the note itself, which
	// is why it is computed here rather than left to the renderer.
	Backlinks []string
}

// ReadNote reads one vault-relative file. The path may not escape the vault —
// unlike the session workspace reader, which is deliberately unconfined, this
// surface is reachable with no session at all and has no business serving
// anything outside the vault.
func (s *Service) ReadNote(ctx context.Context, relPath string) (NoteContent, error) {
	if err := ctx.Err(); err != nil {
		return NoteContent{}, err
	}
	vault, err := s.requireVault()
	if err != nil {
		return NoteContent{}, err
	}
	abs, rel, ok := confined(vault, relPath)
	if !ok {
		return NoteContent{}, apierr.NotFound("WIKI_NOTE_NOT_FOUND", "Note not found")
	}
	info, statErr := os.Stat(abs)
	if statErr != nil || !info.Mode().IsRegular() {
		return NoteContent{}, apierr.NotFound("WIKI_NOTE_NOT_FOUND", "Note not found")
	}
	if info.Size() > maxNoteBytes {
		return NoteContent{}, apierr.Invalid("WIKI_NOTE_TOO_LARGE", "This note is too large to display", nil)
	}
	data, readErr := os.ReadFile(abs) //nolint:gosec // abs is confined to the configured vault above
	if readErr != nil {
		return NoteContent{}, apierr.NotFound("WIKI_NOTE_NOT_FOUND", "Note not found")
	}
	return NoteContent{
		Path:       rel,
		Content:    string(data),
		Size:       info.Size(),
		ModifiedAt: info.ModTime().UTC(),
		Backlinks:  backlinksTo(ctx, vault, rel),
	}, nil
}

// wikilinkPattern captures the target of an Obsidian wikilink: the text up to
// the first "|" (a display alias) or "#" (a heading anchor), which is the part
// that names the note.
var wikilinkPattern = regexp.MustCompile(`\[\[([^\]|#\n]+)`)

// maxBacklinkScanBytes bounds one file's contribution to the backlink scan. A
// note is prose; anything larger is not one, and reading it whole would make
// opening a note cost as much as indexing the vault.
const maxBacklinkScanBytes = 512 << 10

// backlinksTo finds the markdown notes that wikilink to target. A wikilink
// names a note by its basename ("[[compaction]]"), its path
// ("[[agents/compaction]]"), or either with the extension, so all four spellings
// resolve to the same note here — matching how the vault's own editor resolves
// them, rather than only the one spelling this reader happened to use.
//
// A note never lists itself.
func backlinksTo(ctx context.Context, vault, target string) []string {
	names := map[string]bool{
		strings.ToLower(target):                            true,
		strings.ToLower(strings.TrimSuffix(target, ".md")): true,
	}
	base := path.Base(target)
	names[strings.ToLower(base)] = true
	names[strings.ToLower(strings.TrimSuffix(base, ".md"))] = true

	var found []string
	_ = filepath.WalkDir(vault, func(p string, d os.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return nil //nolint:nilerr // an unreadable entry is skipped, not fatal
		}
		if d.IsDir() {
			if p != vault && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		rel, relErr := filepath.Rel(vault, p)
		if relErr != nil {
			return nil //nolint:nilerr // unrelatable path, skip
		}
		rel = filepath.ToSlash(rel)
		if rel == target {
			return nil
		}
		if info, infoErr := d.Info(); infoErr != nil || info.Size() > maxBacklinkScanBytes {
			//nolint:nilerr // a note we cannot stat, or one too large to be prose, is skipped
			return nil
		}
		body, readErr := os.ReadFile(p) //nolint:gosec // p came from walking the configured vault
		if readErr != nil {
			return nil //nolint:nilerr // unreadable note, skip
		}
		for _, m := range wikilinkPattern.FindAllSubmatch(body, -1) {
			if names[strings.ToLower(strings.TrimSpace(string(m[1])))] {
				found = append(found, rel)
				return nil
			}
		}
		return nil
	})
	sort.Strings(found)
	return found
}

// HomeRelative rewrites a path under the user's home directory as "~/…". A path
// outside the home (or a machine with no resolvable home) is returned unchanged.
func HomeRelative(dir string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || dir == "" {
		return dir
	}
	if dir == home {
		return "~"
	}
	if strings.HasPrefix(dir, home+string(filepath.Separator)) {
		return "~/" + filepath.ToSlash(dir[len(home)+1:])
	}
	return dir
}

func (s *Service) vault() string {
	if s.settings == nil {
		return ""
	}
	return strings.TrimSpace(s.settings.VaultPath())
}

func (s *Service) requireVault() (string, error) {
	vault := s.vault()
	if vault == "" {
		return "", apierr.Invalid("WIKI_NOT_CONFIGURED", "No wiki vault is configured", nil)
	}
	info, err := os.Stat(vault)
	if err != nil || !info.IsDir() {
		return "", apierr.Invalid("WIKI_VAULT_MISSING", "The configured wiki vault is not a directory", map[string]any{"path": vault})
	}
	return vault, nil
}

// confined resolves a vault-relative path, returning the absolute file and the
// canonical vault-relative path, and refuses anything that lands outside the
// vault. Both sides are symlink-resolved, so a link inside the vault pointing
// elsewhere cannot be used to read the rest of the disk — and the returned
// relative path is computed against the RESOLVED root, so a vault reached
// through a symlink (/var -> /private/var on macOS) still answers with a clean
// relative path rather than a chain of "..".
func confined(vault, relPath string) (abs, rel string, ok bool) {
	clean := strings.TrimSpace(relPath)
	if clean == "" || filepath.IsAbs(clean) || strings.HasPrefix(clean, "~") {
		return "", "", false
	}
	root, err := filepath.EvalSymlinks(vault)
	if err != nil {
		return "", "", false
	}
	abs, err = filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(clean)))
	if err != nil {
		return "", "", false
	}
	within, err := filepath.Rel(root, abs)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", "", false
	}
	return abs, filepath.ToSlash(within), true
}

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/tmux"
)

// maxDisplayNameLen caps the sidebar label set by `--name`. Mirrored by the
// daemon's spawn handler so a direct API call is held to the same limit.
const maxDisplayNameLen = 20

type spawnOptions struct {
	project        string
	harness        string
	from           string
	branch         string
	prompt         string
	promptFile     string
	issue          string
	name           string
	claimPR        string
	noTakeover     bool
	skipAgentCheck bool
	todo           bool
	keepWarm       bool
	taskSize       string
}

// spawnRequest mirrors the daemon's SpawnSessionRequest body for
// POST /api/v1/sessions. The CLI keeps its own copy so it need not import httpd.
type spawnRequest struct {
	ProjectID string `json:"projectId"`
	IssueID   string `json:"issueId,omitempty"`
	Harness   string `json:"harness,omitempty"`
	Branch    string `json:"branch,omitempty"`
	// BaseBranch is the branch the new worktree is created from (the UI's
	// "Start from" field). `ao spawn` requires it via --from.
	BaseBranch string `json:"baseBranch,omitempty"`
	// AutoNameBranch asks the daemon to AI-name the new branch from the task
	// when Branch is empty, matching the UI's "AI names it if blank" behavior.
	AutoNameBranch bool   `json:"autoNameBranch,omitempty"`
	Prompt         string `json:"prompt,omitempty"`
	DisplayName    string `json:"displayName,omitempty"`
	// StartImmediately is nil for a normal spawn (start now, unchanged) and set
	// to false by `--todo` to stage the worker as a prepared TODO on the board.
	StartImmediately *bool `json:"startImmediately,omitempty"`
	// CreatedBy records the orchestrator session queuing a `--todo`, for the
	// report-back once the worker finishes.
	CreatedBy string `json:"createdBy,omitempty"`
	// KeepWarmOnMerge asks the daemon to SUSPEND the worker in place (card stays
	// on the board, resumable) instead of terminating it to Done when its PR
	// merges — for a worker expected to open more PRs (feature/merge-suspend-in-place).
	KeepWarmOnMerge bool `json:"keepWarmOnMerge,omitempty"`
	// TaskSize is the worker ceremony level: mechanical / standard / deep. Empty
	// (the default) means standard. A mechanical task is authorized in the worker
	// prompt to skip the process skills (`ao spawn --task-size`).
	TaskSize string `json:"taskSize,omitempty"`
}

type spawnResult struct {
	Session struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		ProjectID string `json:"projectId"`
		Branch    string `json:"branch"`
	} `json:"session"`
}

type agentProbeResult struct {
	Agent     agentInfo `json:"agent"`
	Supported bool      `json:"supported"`
	Installed bool      `json:"installed"`
}

func newSpawnCommand(ctx *commandContext) *cobra.Command {
	var opts spawnOptions
	cmd := &cobra.Command{
		Use:   "spawn",
		Short: "Spawn a worker agent session in a registered project",
		Long: "Spawn a worker agent session in a registered project.\n\n" +
			"The session runs the chosen agent in a\n" +
			"fresh git worktree. Register the project first with `ao project add`.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.noTakeover && opts.claimPR == "" {
				return usageError{fmt.Errorf("--no-takeover requires --claim-pr")}
			}
			// A TODO is not started, so there is no session to claim a PR into.
			if opts.todo && opts.claimPR != "" {
				return usageError{fmt.Errorf("--todo cannot be combined with --claim-pr (a queued task has no live session to claim into)")}
			}
			if explicitName := strings.TrimSpace(opts.name); utf8.RuneCountInString(explicitName) > maxDisplayNameLen {
				return usageError{fmt.Errorf("--name must be %d characters or fewer", maxDisplayNameLen)}
			}
			// --task-size is optional; normalize and reject an unknown value here so a
			// typo fails fast (exit 2) rather than reaching the daemon. Empty is left
			// as-is and the daemon resolves it to standard.
			opts.taskSize = strings.ToLower(strings.TrimSpace(opts.taskSize))
			if opts.taskSize != "" && !validTaskSize(opts.taskSize) {
				return usageError{fmt.Errorf("--task-size must be one of mechanical, standard, deep")}
			}
			// --from is required and names the branch the worktree is created from
			// (the UI "Start from" field). Reject a missing base fast, before any
			// daemon round-trip, so misuse exits 2 rather than spawning off an
			// unintended default branch.
			baseBranch := strings.TrimSpace(opts.from)
			if baseBranch == "" {
				return usageError{fmt.Errorf("--from is required: name the branch the worktree starts from, e.g. --from main")}
			}
			// Resolve the initial prompt from --prompt / --prompt-file. This runs
			// after the cheap synchronous flag checks above so a missing --from (or
			// an overlong --name) fails fast rather than blocking on `--prompt-file
			// -` stdin; it still precedes any daemon round-trip so a bad prompt-file
			// exits 2 without a network call.
			prompt, err := resolvePrompt(opts.prompt, opts.promptFile, cmd.InOrStdin())
			if err != nil {
				return err
			}
			opts.prompt = prompt

			project, err := ctx.resolveSpawnProject(cmd.Context(), opts.project)
			if err != nil {
				return err
			}
			opts.project = project.ID

			harness, err := resolveSpawnHarness(opts.harness, project)
			if err != nil {
				return err
			}
			opts.harness = harness

			name := resolveSpawnDisplayName(opts.name, opts.prompt)
			// A TODO is not launched now, so skip the agent install/auth preflight:
			// the check runs when it is later started.
			if !opts.skipAgentCheck && !opts.todo {
				if err := ctx.preflightSpawnAgentAuth(cmd.Context(), cmd, opts.harness); err != nil {
					return err
				}
			}
			claimRef := ""
			if opts.claimPR != "" {
				claimRef, err = ctx.resolvePRRef(cmd.Context(), opts.claimPR, project)
				if err != nil {
					return err
				}
			}
			// A blank --branch mirrors the UI: ask the daemon to AI-name the new
			// branch from the task; a non-empty value is honored verbatim.
			newBranch := strings.TrimSpace(opts.branch)
			req := spawnRequest{
				ProjectID:       opts.project,
				IssueID:         opts.issue,
				Harness:         opts.harness,
				Branch:          newBranch,
				BaseBranch:      baseBranch,
				AutoNameBranch:  newBranch == "",
				Prompt:          opts.prompt,
				DisplayName:     name,
				KeepWarmOnMerge: opts.keepWarm,
				TaskSize:        opts.taskSize,
			}
			if opts.todo {
				// --todo stages a prepared TODO (no branch/worktree/tmux until
				// Start). createdBy links it back to the queuing orchestrator.
				deferred := false
				req.StartImmediately = &deferred
				req.CreatedBy = strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
			}
			var res spawnResult
			if err := ctx.postJSON(cmd.Context(), "sessions", req, &res); err != nil {
				return err
			}
			// A queued TODO has nothing to attach to; report it and stop here.
			if opts.todo {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "queued TODO session %s — start it with `ao session start %s`\n", res.Session.ID, res.Session.ID)
				return err
			}
			claimed := ""
			if opts.claimPR != "" {
				var claim claimPRResponse
				if err := ctx.postJSON(cmd.Context(), "sessions/"+url.PathEscape(res.Session.ID)+"/pr/claim", claimPRRequest{PR: claimRef, AllowTakeover: !opts.noTakeover}, &claim); err != nil {
					if killErr := ctx.rollbackSpawnedSession(cmd.Context(), res.Session.ID); killErr != nil {
						return fmt.Errorf("failed to claim PR %s: %w; rollback of session %s failed: %w", opts.claimPR, err, res.Session.ID, killErr)
					}
					return fmt.Errorf("failed to claim PR %s: %w; rolled back session %s", opts.claimPR, err, res.Session.ID)
				}
				if len(claim.PRs) > 0 {
					claimed = claim.PRs[0].URL
				}
			}
			out := cmd.OutOrStdout()
			claimLabel := ""
			if claimed != "" {
				claimLabel = fmt.Sprintf(" (claimed %s)", claimed)
			}
			if _, err := fmt.Fprintf(out, "spawned session %s (%s)%s\n", res.Session.ID, res.Session.Status, claimLabel); err != nil {
				return err
			}
			_, err = fmt.Fprintf(out, "attach with: %s\n", spawnAttachHint(res.Session.ProjectID, res.Session.Branch, res.Session.ID))
			return err
		},
	}
	f := cmd.Flags()
	// --agent is an alias for --harness so the more intuitive `ao spawn --agent
	// droid` works identically; both resolve to the same harness flag.
	f.SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "agent" {
			name = "harness"
		}
		return pflag.NormalizedName(name)
	})
	f.StringVar(&opts.project, "project", "", "Project id to spawn the session in (default: AO_PROJECT_ID or current registered repo)")
	f.StringVar(&opts.harness, "harness", "", "Agent harness / --agent: claude-code, codex, aider, opencode, grok, droid, amp, agy, crush, cursor, qwen, copilot, goose, auggie, continue, devin, cline, kimi, kiro, kilocode, vibe, pi, autohand (default: project worker.agent; required if the project has none)")
	f.StringVar(&opts.from, "from", "", "REQUIRED source branch the worktree is created from, e.g. main (matches the UI \"Start from\" field)")
	f.StringVar(&opts.branch, "branch", "", "New branch name for the session worktree (default: AI-named from the task, like the UI when left blank)")
	f.StringVar(&opts.prompt, "prompt", "", "Initial prompt for the agent")
	f.StringVar(&opts.promptFile, "prompt-file", "", "Read the initial prompt from a file, or '-' for stdin; mutually exclusive with --prompt. Use for large prompts that would exceed the shell's argument-length limit.")
	f.StringVar(&opts.issue, "issue", "", "Issue id to associate with the session")
	f.StringVar(&opts.name, "name", "", "Display name shown in the sidebar (default: derived from --prompt, max 20 characters)")
	f.StringVar(&opts.claimPR, "claim-pr", "", "Immediately claim an existing PR for the spawned session: a github.com PR URL/number, or a full GitLab merge-request URL")
	f.BoolVar(&opts.noTakeover, "no-takeover", false, "Refuse if another active session owns the claimed PR (requires --claim-pr)")
	f.BoolVar(&opts.skipAgentCheck, "skip-agent-check", false, "Skip advisory agent catalog install/auth preflight before spawning")
	f.BoolVar(&opts.todo, "todo", false, "Stage the worker as a prepared TODO on the board instead of starting it now (no branch/worktree/tmux until `ao session start <id>`)")
	f.BoolVar(&opts.keepWarm, "keep-warm", false, "Keep the worker on the board (suspend in place, resumable) instead of archiving it to Done when its PR merges — for a worker that will open more PRs")
	f.StringVar(&opts.taskSize, "task-size", "", "Worker ceremony level: mechanical | standard | deep (default standard). `mechanical` authorizes the worker to skip the brainstorm/plan/TDD process skills and go straight to edit + verify; use only for small, well-scoped changes")
	return cmd
}

// validTaskSize reports whether s is one of the accepted `--task-size` values.
// Kept in sync with domain.TaskSize.Valid() across the CLI's hand-mirrored
// boundary (the CLI does not import the daemon's DTOs).
func validTaskSize(s string) bool {
	switch s {
	case "mechanical", "standard", "deep":
		return true
	}
	return false
}

// spawnAttachHint returns a copy-pasteable attach hint for the selected runtime.
// On Darwin/Linux it is a tmux attach-session using the sanitised session name
// (branch-mirroring, falling back to the id); on Windows ConPTY has no
// user-facing attach CLI, so it points at the AO dashboard.
func spawnAttachHint(projectID, branch, id string) string {
	if runtime.GOOS == "windows" {
		return "Attach from the AO dashboard (ConPTY sessions have no CLI attach command)"
	}
	name, err := tmux.SessionNameFor(projectID, branch, id)
	if err != nil {
		name = id
	}
	return fmt.Sprintf("tmux attach -t %s", name)
}

func (c *commandContext) fetchAgentInventory(ctx context.Context, refresh bool) (agentInventory, error) {
	var inv agentInventory
	if refresh {
		if err := c.postJSON(ctx, "agents/refresh", struct{}{}, &inv); err != nil {
			return agentInventory{}, err
		}
		return inv, nil
	}
	if err := c.getJSON(ctx, "agents", &inv); err != nil {
		return agentInventory{}, err
	}
	return inv, nil
}

func (c *commandContext) resolveSpawnProject(ctx context.Context, explicit string) (projectDetails, error) {
	if id := strings.TrimSpace(explicit); id != "" {
		return c.fetchProjectDetails(ctx, id)
	}
	if id := strings.TrimSpace(os.Getenv("AO_PROJECT_ID")); id != "" {
		return c.fetchProjectDetails(ctx, id)
	}
	if sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID")); sessionID != "" {
		project, err := c.resolveProjectFromSession(ctx, sessionID)
		if err != nil {
			return projectDetails{}, err
		}
		return project, nil
	}
	project, ok, err := c.resolveProjectFromCWD(ctx)
	if err != nil {
		return projectDetails{}, err
	}
	if ok {
		return project, nil
	}
	return projectDetails{}, usageError{fmt.Errorf("project could not be resolved; pass --project or run `ao project add --path <repo-path> --worker-agent <agent>`")}
}

func (c *commandContext) resolveProjectFromSession(ctx context.Context, sessionID string) (projectDetails, error) {
	sess, err := c.fetchScopedSession(ctx, sessionID, "")
	if err != nil {
		return projectDetails{}, usageError{fmt.Errorf("project could not be resolved from AO_SESSION_ID %q; pass --project", sessionID)}
	}
	if strings.TrimSpace(sess.ProjectID) == "" {
		return projectDetails{}, usageError{fmt.Errorf("project could not be resolved from AO_SESSION_ID %q; pass --project", sessionID)}
	}
	return c.fetchProjectDetails(ctx, sess.ProjectID)
}

func (c *commandContext) resolveProjectFromCWD(ctx context.Context) (projectDetails, bool, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return projectDetails{}, false, err
	}
	cwd, err = normalizeProjectMatchPath(cwd)
	if err != nil {
		return projectDetails{}, false, err
	}

	var list projectListResult
	if err := c.getJSON(ctx, "projects", &list); err != nil {
		return projectDetails{}, false, err
	}
	sort.Slice(list.Projects, func(i, j int) bool {
		return list.Projects[i].ID < list.Projects[j].ID
	})

	var best projectDetails
	bestLen := -1
	ambiguous := false
	for _, summary := range list.Projects {
		project, err := c.fetchProjectDetails(ctx, summary.ID)
		if err != nil {
			return projectDetails{}, false, err
		}
		if project.Path == "" {
			continue
		}
		projectPath, err := normalizeProjectMatchPath(project.Path)
		if err != nil {
			continue
		}
		if !pathContains(projectPath, cwd) {
			continue
		}
		pathLen := len(projectPath)
		switch {
		case pathLen > bestLen:
			best = project
			bestLen = pathLen
			ambiguous = false
		case pathLen == bestLen:
			ambiguous = true
		}
	}
	if bestLen == -1 {
		return projectDetails{}, false, nil
	}
	if ambiguous {
		return projectDetails{}, false, usageError{fmt.Errorf("current directory matches multiple registered projects; pass --project")}
	}
	return best, true, nil
}

func normalizeProjectMatchPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if realPath, err := filepath.EvalSymlinks(abs); err == nil {
		abs = realPath
	}
	return filepath.Clean(abs), nil
}

func pathContains(root, child string) bool {
	if root == child {
		return true
	}
	rel, err := filepath.Rel(root, child)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolveSpawnHarness(explicit string, project projectDetails) (string, error) {
	if harness := strings.TrimSpace(explicit); harness != "" {
		return harness, nil
	}
	if project.Config != nil {
		if harness := strings.TrimSpace(project.Config.Worker.Agent); harness != "" {
			return harness, nil
		}
	}
	return "", usageError{fmt.Errorf("agent could not be resolved; pass --agent or configure `ao project set-config %s --worker-agent <agent>`", project.ID)}
}

// resolvePrompt returns the effective initial prompt from --prompt /
// --prompt-file. The two are mutually exclusive. --prompt-file "-" reads stdin;
// any other value reads that file. Loading from a file (or stdin) also lets a
// large prompt bypass the shell's ARG_MAX that a literal --prompt would hit on
// the command line. Mirrors `ao review submit --body`.
func resolvePrompt(prompt, promptFile string, stdin io.Reader) (string, error) {
	file := strings.TrimSpace(promptFile)
	if file == "" {
		return prompt, nil
	}
	if prompt != "" {
		return "", usageError{errors.New("--prompt and --prompt-file are mutually exclusive; pass only one")}
	}
	var (
		raw []byte
		err error
	)
	if file == "-" {
		raw, err = io.ReadAll(stdin)
	} else {
		raw, err = os.ReadFile(file)
	}
	if err != nil {
		return "", usageError{fmt.Errorf("read prompt file %q: %w", file, err)}
	}
	if strings.TrimSpace(string(raw)) == "" {
		return "", usageError{fmt.Errorf("prompt file %q is empty", file)}
	}
	return string(raw), nil
}

func resolveSpawnDisplayName(explicit, prompt string) string {
	if name := strings.TrimSpace(explicit); name != "" {
		return name
	}
	return deriveDisplayNameFromPrompt(prompt)
}

func deriveDisplayNameFromPrompt(prompt string) string {
	fields := strings.Fields(strings.TrimSpace(prompt))
	if len(fields) == 0 {
		return ""
	}
	var b strings.Builder
	for _, field := range fields {
		next := strings.Trim(field, " \t\r\n.,;:!?()[]{}\"'")
		if next == "" {
			continue
		}
		if b.Len() > 0 {
			next = " " + next
		}
		if utf8.RuneCountInString(b.String()+next) > maxDisplayNameLen {
			break
		}
		b.WriteString(next)
	}
	return b.String()
}

func (c *commandContext) preflightSpawnAgentAuth(ctx context.Context, cmd *cobra.Command, agentID string) error {
	inv, err := c.fetchAgentInventory(ctx, true)
	if err != nil {
		return err
	}
	state := agentCatalogStateFor(inv, agentID)
	if !state.supported {
		return fmt.Errorf("agent %q is not supported by this daemon; pass a supported --agent or run `ao agent ls`", agentID)
	}
	if !state.installed || state.authStatus == "unauthorized" {
		fresh, err := c.probeSpawnAgent(ctx, agentID)
		if err != nil {
			if agentProbeUnavailable(err) {
				_, err = fmt.Fprintf(cmd.ErrOrStderr(), "warning: agent %q fresh readiness probe is unavailable; continuing and letting spawn validate runtime readiness\n", agentID)
				return err
			}
			return err
		}
		if !fresh.Supported {
			return fmt.Errorf("agent %q is not supported by this daemon; pass a supported --agent or run `ao agent ls`", agentID)
		}
		if !fresh.Installed {
			return fmt.Errorf("agent %q needs install; install the agent CLI or pass --skip-agent-check to let spawn validate it", agentID)
		}
		state.installed = true
		state.authorized = fresh.Agent.AuthStatus == "authorized"
		state.authStatus = fresh.Agent.AuthStatus
	}
	if state.authorized {
		return nil
	}
	if state.authStatus == "unauthorized" {
		_, err = fmt.Fprintf(cmd.ErrOrStderr(), "warning: agent %q may need auth according to a fresh local probe; continuing and letting spawn validate runtime readiness\n", agentID)
		return err
	}
	_, err = fmt.Fprintf(cmd.ErrOrStderr(), "warning: agent %q auth status is unknown; continuing and letting spawn validate runtime readiness\n", agentID)
	return err
}

func (c *commandContext) probeSpawnAgent(ctx context.Context, agentID string) (agentProbeResult, error) {
	var result agentProbeResult
	if err := c.postJSON(ctx, "agents/"+url.PathEscape(agentID)+"/probe", struct{}{}, &result); err != nil {
		return agentProbeResult{}, err
	}
	return result, nil
}

func agentProbeUnavailable(err error) bool {
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusNotFound || apiErr.StatusCode == http.StatusNotImplemented
}

type agentCatalogState struct {
	supported  bool
	installed  bool
	authorized bool
	authStatus string
}

func agentCatalogStateFor(inv agentInventory, agentID string) agentCatalogState {
	state := agentCatalogState{}
	for _, info := range inv.Supported {
		if info.ID == agentID {
			state.supported = true
			break
		}
	}
	for _, info := range inv.Authorized {
		if info.ID == agentID {
			state.installed = true
			state.authorized = true
			state.authStatus = "authorized"
			return state
		}
	}
	for _, info := range inv.Installed {
		if info.ID == agentID {
			state.installed = true
			state.authorized = info.AuthStatus == "authorized"
			state.authStatus = info.AuthStatus
			return state
		}
	}
	return state
}

// rollbackSpawnedSession reverses a partial `spawn` whose out-of-band follow-up
// (PR claim) failed. It calls the daemon's `/rollback` endpoint, which deletes
// the seed-state row outright instead of marking it terminated — so the user
// does not see an orphan terminated session under `--include-terminated`. If
// spawn output has already landed (workspace + runtime), the daemon falls back
// to a Kill on the server side so teardown still happens.
func (c *commandContext) rollbackSpawnedSession(ctx context.Context, id string) error {
	var res rollbackSessionResponse
	return c.postJSON(ctx, "sessions/"+url.PathEscape(id)+"/rollback", struct{}{}, &res)
}

// rollbackSessionResponse mirrors the daemon's RollbackSessionResponse body.
type rollbackSessionResponse struct {
	OK        bool   `json:"ok"`
	SessionID string `json:"sessionId"`
	Deleted   bool   `json:"deleted,omitempty"`
	Killed    bool   `json:"killed,omitempty"`
}

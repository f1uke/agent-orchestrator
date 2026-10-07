package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// scriptsWorktreeView mirrors the daemon's ScriptsStoreWorktreeView.
type scriptsWorktreeView struct {
	Owner       string   `json:"owner"`
	Store       string   `json:"store"`
	Path        string   `json:"path"`
	Branch      string   `json:"branch"`
	BaseBranch  string   `json:"baseBranch"`
	State       string   `json:"state"`
	HeldReason  string   `json:"heldReason,omitempty"`
	HeldFiles   []string `json:"heldFiles,omitempty"`
	Uncommitted []string `json:"uncommitted"`
	Unpublished int      `json:"unpublished"`
	PublishedAt *string  `json:"publishedAt,omitempty"`
}

type scriptsStatusResponse struct {
	Worktree   scriptsWorktreeView `json:"worktree"`
	StoreDirty []string            `json:"storeDirty"`
}

type scriptsPublishResponse struct {
	Worktree scriptsWorktreeView `json:"worktree"`
	Outcome  string              `json:"outcome"`
	SHA      string              `json:"sha,omitempty"`
	Commits  int                 `json:"commits"`
	Hold     string              `json:"hold,omitempty"`
	Detail   string              `json:"detail,omitempty"`
	Files    []string            `json:"files,omitempty"`
}

type scriptsOptions struct {
	session string
	json    bool
}

// newScriptsCommand is a worker's handle on its workspace's own worktree of
// the mobile scripts store: what it holds, and the publish of its commits into
// the store's main checkout.
func newScriptsCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scripts",
		Short: "Publish and inspect this task's worktree of the mobile scripts store",
		Long: "A worker on a project with mobileScripts writes scripts into its own git worktree of\n" +
			"the scripts store ($AO_SCRIPTS_STORE), on branch ao/<session>. Commit there, then\n" +
			"`ao scripts publish` merges the commits into the store's main checkout. Uncommitted\n" +
			"files are never published, and teardown refuses to drop them.",
		Args: noArgs,
	}
	addFlags := func(c *cobra.Command, opts *scriptsOptions) {
		c.Flags().StringVar(&opts.session, "session", "", "Session whose workspace to use (default: $AO_SESSION_ID); a crew member means its dev's workspace")
		c.Flags().BoolVar(&opts.json, "json", false, "Print the daemon's JSON response")
	}

	var statusOpts scriptsOptions
	status := &cobra.Command{
		Use:   "status",
		Short: "Show the worktree, its uncommitted files and unpublished commits, and the main checkout's own dirty files",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := scriptsPath(statusOpts.session, "")
			if err != nil {
				return err
			}
			var res scriptsStatusResponse
			if err := ctx.getJSON(cmd.Context(), path, &res); err != nil {
				return err
			}
			if statusOpts.json {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeScriptsStatus(cmd.OutOrStdout(), res)
		},
	}
	addFlags(status, &statusOpts)

	var publishOpts scriptsOptions
	publish := &cobra.Command{
		Use:   "publish",
		Short: "Merge this task's committed scripts into the store's main checkout",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := scriptsPath(publishOpts.session, "/publish")
			if err != nil {
				return err
			}
			var res scriptsPublishResponse
			if err := ctx.postJSON(cmd.Context(), path, nil, &res); err != nil {
				return err
			}
			if publishOpts.json {
				if err := writeJSON(cmd.OutOrStdout(), res); err != nil {
					return err
				}
				if res.Outcome == "refused" {
					return errors.New("publish refused: " + res.Detail)
				}
				return nil
			}
			return writeScriptsPublish(cmd.OutOrStdout(), res)
		},
	}
	addFlags(publish, &publishOpts)

	cmd.AddCommand(status, publish)
	return cmd
}

func scriptsPath(session, suffix string) (string, error) {
	id := strings.TrimSpace(session)
	if id == "" {
		id = strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	}
	if id == "" {
		return "", usageError{errors.New("ao scripts needs a session: run it inside an AO session or pass --session")}
	}
	return "sessions/" + url.PathEscape(id) + "/scripts" + suffix, nil
}

func writeScriptsStatus(out io.Writer, res scriptsStatusResponse) error {
	w := res.Worktree
	var b strings.Builder
	fmt.Fprintf(&b, "Scripts store worktree of %s\n", w.Owner)
	fmt.Fprintf(&b, "  path:        %s\n", w.Path)
	fmt.Fprintf(&b, "  branch:      %s (publishes into %s of %s)\n", w.Branch, w.BaseBranch, w.Store)
	state := w.State
	if w.HeldReason != "" {
		state += " (" + w.HeldReason + ")"
	}
	fmt.Fprintf(&b, "  state:       %s\n", state)
	fmt.Fprintf(&b, "  unpublished: %d commit(s)\n", w.Unpublished)
	fmt.Fprintf(&b, "  uncommitted: %d file(s)\n", len(w.Uncommitted))
	for _, f := range w.Uncommitted {
		fmt.Fprintf(&b, "    %s\n", f)
	}
	fmt.Fprintf(&b, "  main checkout's own uncommitted files (read only; a publish cannot touch them): %d\n", len(res.StoreDirty))
	for _, f := range res.StoreDirty {
		fmt.Fprintf(&b, "    %s\n", f)
	}
	switch {
	case w.Unpublished > 0:
		b.WriteString("Run `ao scripts publish` to merge the commits into the store.\n")
	case len(w.Uncommitted) > 0:
		b.WriteString("Commit the files in the worktree, then run `ao scripts publish`.\n")
	}
	_, err := io.WriteString(out, b.String())
	return err
}

// writeScriptsPublish says what the publish did. A refusal is an error (exit
// 1) that names the files and the fix, because the agent that wrote the
// scripts is the one who can resolve it.
func writeScriptsPublish(out io.Writer, res scriptsPublishResponse) error {
	w := res.Worktree
	var b strings.Builder
	switch res.Outcome {
	case "fast_forward":
		fmt.Fprintf(&b, "Published %d commit(s) from %s into %s of %s (fast forward to %s).\n", res.Commits, w.Branch, w.BaseBranch, w.Store, shortSHA(res.SHA))
	case "merged":
		fmt.Fprintf(&b, "Published %d commit(s) from %s into %s of %s with merge commit %s.\n", res.Commits, w.Branch, w.BaseBranch, w.Store, shortSHA(res.SHA))
	case "nothing":
		fmt.Fprintf(&b, "Nothing to publish: %s has no commits %s lacks.\n", w.Branch, w.BaseBranch)
	case "refused":
		return errors.New(scriptsRefusal(res))
	default:
		fmt.Fprintf(&b, "Publish answered %q.\n", res.Outcome)
	}
	if n := len(w.Uncommitted); n > 0 {
		fmt.Fprintf(&b, "%d uncommitted file(s) stay in %s; only commits publish:\n", n, w.Path)
		for _, f := range w.Uncommitted {
			fmt.Fprintf(&b, "  %s\n", f)
		}
	}
	_, err := io.WriteString(out, b.String())
	return err
}

func scriptsRefusal(res scriptsPublishResponse) string {
	w := res.Worktree
	var b strings.Builder
	fmt.Fprintf(&b, "publish refused (%s): %s", res.Hold, res.Detail)
	for _, f := range res.Files {
		fmt.Fprintf(&b, "\n  %s", f)
	}
	b.WriteString("\n\nFix: ")
	switch res.Hold {
	case "publish_conflict":
		fmt.Fprintf(&b, "merge %s into your branch in $AO_SCRIPTS_STORE (git -C %s merge %s), resolve, commit, and run `ao scripts publish` again.", w.BaseBranch, w.Path, w.BaseBranch)
	case "store_dirty_overlap":
		fmt.Fprintf(&b, "the store's main checkout %s has uncommitted edits in those files. Whoever made them commits or removes them there (never edit the main checkout from a session), then run `ao scripts publish` again.", w.Store)
	case "store_off_base":
		fmt.Fprintf(&b, "the store's main checkout %s must be back on %s before anything can publish; ask the human, it is a shared checkout. Then run `ao scripts publish` again.", w.Store, w.BaseBranch)
	default:
		fmt.Fprintf(&b, "run `ao scripts publish` again; if it keeps failing, read `git -C %s status`.", w.Store)
	}
	return b.String()
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

package cli

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// trainSourcesResponse mirrors controllers.ReleaseTrainSourcesResponse.
type trainSourcesResponse struct {
	Config struct {
		State   string `json:"state"`
		Message string `json:"message,omitempty"`
		Path    string `json:"path"`
	} `json:"config"`
	Resources []trainResource `json:"resources"`
	LastPass  struct {
		At       *time.Time     `json:"at,omitempty"`
		Requests map[string]int `json:"requests"`
	} `json:"lastPass"`
}

type trainResource struct {
	Kind          string     `json:"kind"`
	Scope         string     `json:"scope"`
	App           string     `json:"app,omitempty"`
	Mode          string     `json:"mode"`
	CompleteAt    *time.Time `json:"completeAt,omitempty"`
	LastAttemptAt *time.Time `json:"lastAttemptAt,omitempty"`
	ErrorClass    string     `json:"errorClass,omitempty"`
	ErrorMessage  string     `json:"errorMessage,omitempty"`
	NextAttemptAt *time.Time `json:"nextAttemptAt,omitempty"`
	Backfilling   bool       `json:"backfilling"`
}

// trainConfigInitResponse mirrors controllers.ReleaseTrainConfigInitResponse.
type trainConfigInitResponse struct {
	Path string `json:"path"`
}

func newTrainCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "train",
		Short: "Watch the mobile release trains (read-only)",
		Long: "The daemon polls GitLab and Jira, read-only, for the release trains that " +
			"release-train.json in the data dir describes.",
	}
	config := &cobra.Command{Use: "config", Short: "Create the release-train config file"}
	config.AddCommand(newTrainConfigInitCommand(ctx))
	cmd.AddCommand(newTrainSourcesCommand(ctx), config)
	return cmd
}

func newTrainSourcesCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "sources",
		Short: "Show the config and how fresh each observed GitLab and Jira resource is",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res trainSourcesResponse
			if err := ctx.getJSON(cmd.Context(), "release-trains/sources", &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeTrainSources(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the daemon's JSON")
	return cmd
}

func newTrainConfigInitCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Write a template release-train.json to the data dir; never overwrites one",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res trainConfigInitResponse
			if err := ctx.postJSON(cmd.Context(), "release-trains/config", struct{}{}, &res); err != nil {
				var apiErr apiResponseError
				if errors.As(err, &apiErr) && apiErr.ErrorBody.Code == "RELEASE_TRAIN_CONFIG_EXISTS" {
					return usageError{err}
				}
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "wrote %s; fill in your GitLab project ids, Jira project and boards\n", res.Path)
			return err
		},
	}
}

func writeTrainSources(w io.Writer, res trainSourcesResponse) error {
	var b strings.Builder
	fmt.Fprintf(&b, "config: %s (%s)\n", res.Config.State, res.Config.Path)
	if res.Config.Message != "" {
		fmt.Fprintf(&b, "  %s\n", res.Config.Message)
	}
	if res.LastPass.At != nil {
		hosts := make([]string, 0, len(res.LastPass.Requests))
		for h := range res.LastPass.Requests {
			hosts = append(hosts, h)
		}
		sort.Strings(hosts)
		parts := make([]string, len(hosts))
		for i, h := range hosts {
			parts[i] = fmt.Sprintf("%s %d requests", h, res.LastPass.Requests[h])
		}
		fmt.Fprintf(&b, "last pass %s: %s\n", res.LastPass.At.UTC().Format(time.RFC3339), strings.Join(parts, ", "))
	}
	if len(res.Resources) == 0 {
		b.WriteString("no resources observed yet\n")
		_, err := io.WriteString(w, b.String())
		return err
	}
	b.WriteString("\n")
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tSCOPE\tAPP\tSTATE\tCOMPLETE\tNEXT")
	var errs []string
	for _, r := range res.Resources {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Kind, r.Scope, orDash(r.App), trainState(r), stamp(r.CompleteAt), stamp(r.NextAttemptAt))
		if r.ErrorMessage != "" {
			errs = append(errs, fmt.Sprintf("%s %s: %s", r.Kind, r.Scope, r.ErrorMessage))
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(errs) > 0 {
		b.WriteString("\n" + strings.Join(errs, "\n") + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func trainState(r trainResource) string {
	switch {
	case r.ErrorClass != "":
		return r.ErrorClass
	case r.Backfilling:
		return "backfilling"
	case r.CompleteAt == nil:
		return "pending"
	}
	return "ok"
}

func stamp(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

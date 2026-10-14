package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// trainBoardBody mirrors the fields of GET /release-trains/board the CLI renders.
type trainBoardBody struct {
	Config struct {
		State   string `json:"state"`
		Message string `json:"message"`
		Path    string `json:"path"`
	} `json:"config"`
	Board *trainBoard `json:"board"`
}

type trainBoard struct {
	Today string `json:"today"`
	Apps  []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"apps"`
	Sources []struct {
		System    string     `json:"system"`
		Resources int        `json:"resources"`
		AsOf      *time.Time `json:"asOf"`
		Issues    []struct {
			Kind       string `json:"kind"`
			ErrorClass string `json:"errorClass"`
		} `json:"issues"`
	} `json:"sources"`
	Trains []struct {
		ID       string `json:"id"`
		Label    string `json:"label"`
		Phase    string `json:"phase"`
		Freeze   string `json:"freeze"`
		Publish  string `json:"publish"`
		Versions []struct {
			App       string `json:"app"`
			Version   string `json:"version"`
			ReleaseMR *struct {
				MR struct {
					IID    int    `json:"iid"`
					Author string `json:"author"`
				} `json:"mr"`
			} `json:"releaseMr"`
			Jira *struct {
				Approvers []struct {
					Status string `json:"status"`
				} `json:"approvers"`
			} `json:"jira"`
			Stages []struct {
				Key    string `json:"key"`
				Status string `json:"status"`
			} `json:"stages"`
			Cards []struct {
				Code struct {
					Kind string `json:"kind"`
				} `json:"code"`
			} `json:"cards"`
		} `json:"versions"`
	} `json:"trains"`
	Alerts struct {
		Hits  []trainHit `json:"hits"`
		Rules []struct {
			Rule    string `json:"rule"`
			App     string `json:"app"`
			On      bool   `json:"on"`
			Outcome struct {
				Kind string `json:"kind"`
			} `json:"outcome"`
		} `json:"rules"`
	} `json:"alerts"`
}

type trainHit struct {
	ID       string `json:"id"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Train    string `json:"train"`
	App      string `json:"app"`
	Text     string `json:"text"`
	Bucket   string `json:"bucket"`
}

var severityOrder = []string{"block", "data", "risk", "hygiene", "info"}

func severityLabel(s string) string {
	if s == "hygiene" {
		return "CLEANUP"
	}
	return strings.ToUpper(s)
}

// trainFilter is the local filter over one board; the daemon serves every train and app.
type trainFilter struct {
	train string
	apps  []string
}

func (f trainFilter) app(id string) bool { return len(f.apps) == 0 || slices.Contains(f.apps, id) }

func (f trainFilter) trainKey(key string) bool {
	return f.train == "" || key == f.train || strings.HasPrefix(key, f.train+"-")
}

func (f trainFilter) hit(h trainHit) bool {
	return f.app(h.App) && (f.train == "" || f.trainKey(h.Train))
}

var errTrainMe = usageError{errors.New("--me needs the identity facts (jira.myself, gitlab.user) on the Board, which arrive with sprint rows and identity; until then filter with --app")}

func addTrainFilterFlags(cmd *cobra.Command, train *string, apps *[]string, me *bool) {
	if train != nil {
		cmd.Flags().StringVar(train, "train", "", "Only this train number, e.g. 22 or 20-1")
	}
	cmd.Flags().StringSliceVar(apps, "app", nil, "Only these app ids, e.g. di,da")
	cmd.Flags().BoolVar(me, "me", false, "Only what concerns you (needs identity, not yet available)")
}

func (c *commandContext) trainBoard(cmd *cobra.Command) (trainBoardBody, json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.getJSON(cmd.Context(), "release-trains/board", &raw); err != nil {
		return trainBoardBody{}, nil, err
	}
	var body trainBoardBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return trainBoardBody{}, nil, fmt.Errorf("decode release-train board: %w", err)
	}
	return body, raw, nil
}

func (b *trainBoard) checkApps(apps []string) error {
	ids := make([]string, len(b.Apps))
	for i, a := range b.Apps {
		ids[i] = a.ID
	}
	for _, a := range apps {
		if !slices.Contains(ids, a) {
			return usageError{fmt.Errorf("unknown app %q; the board has %s", a, strings.Join(ids, ", "))}
		}
	}
	return nil
}

func (b *trainBoard) appName(id string) string {
	for _, a := range b.Apps {
		if a.ID == id {
			return a.Name
		}
	}
	return id
}

func (b *trainBoard) trainLabel(key string) string {
	for _, t := range b.Trains {
		if t.ID == key {
			return t.Label
		}
	}
	return "#" + key
}

func (b *trainBoard) versionOf(train, app string) string {
	for _, t := range b.Trains {
		if t.ID != train {
			continue
		}
		for _, v := range t.Versions {
			if v.App == app {
				return v.Version
			}
		}
	}
	return ""
}

func (b *trainBoard) subject(h trainHit) string {
	parts := []string{}
	if h.Train != "" {
		parts = append(parts, b.trainLabel(h.Train))
	}
	if h.App != "" {
		parts = append(parts, b.appName(h.App))
	}
	if v := b.versionOf(h.Train, h.App); v != "" {
		parts = append(parts, v)
	}
	return strings.Join(parts, " ")
}

func newTrainBoardCommand(ctx *commandContext, cmd *cobra.Command) {
	var f trainFilter
	var asJSON, gate, me bool
	addTrainFilterFlags(cmd, &f.train, &f.apps, &me)
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the daemon's JSON, filtered")
	cmd.Flags().BoolVar(&gate, "gate", false, "Exit 1 when an active block hit exists")
	cmd.Args = exactArgs(0)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if me {
			return errTrainMe
		}
		body, raw, err := ctx.trainBoard(cmd)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if body.Board == nil {
			return writeTrainNoBoard(out, body)
		}
		if err := body.Board.checkApps(f.apps); err != nil {
			return err
		}
		if asJSON {
			if err := writeFilteredBoardJSON(out, raw, f); err != nil {
				return err
			}
		} else if err := writeTrainBoard(out, body, f, ctx.deps.Now()); err != nil {
			return err
		}
		if gate {
			return body.Board.gate(f)
		}
		return nil
	}
}

func (b *trainBoard) gate(f trainFilter) error {
	n := 0
	for _, h := range b.Alerts.Hits {
		if h.Bucket == "badge" && h.Severity == "block" && f.hit(h) {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return exitStatusError{code: 1, err: fmt.Errorf("release trains: %d active block hit(s)", n)}
}

func newTrainAlertsCommand(ctx *commandContext) *cobra.Command {
	var apps []string
	var asJSON, history, hidden, me bool
	cmd := &cobra.Command{
		Use:   "alerts",
		Short: "List the release-train alerts that need attention",
		Args:  exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if me {
				return errTrainMe
			}
			body, _, err := ctx.trainBoard(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if body.Board == nil {
				return writeTrainNoBoard(out, body)
			}
			if err := body.Board.checkApps(apps); err != nil {
				return err
			}
			f := trainFilter{apps: apps}
			var hits []trainHit
			for _, h := range sortedHits(body.Board.Alerts.Hits) {
				if f.hit(h) && (h.Bucket == "badge" || (history && h.Bucket == "history") || (hidden && h.Bucket == "hidden")) {
					hits = append(hits, h)
				}
			}
			if asJSON {
				if hits == nil {
					hits = []trainHit{}
				}
				return writeJSON(out, hits)
			}
			return writeTrainHits(out, body.Board, hits, history || hidden)
		},
	}
	addTrainFilterFlags(cmd, nil, &apps, &me)
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the hits as JSON")
	cmd.Flags().BoolVar(&history, "history", false, "Include hits of trains that aged out")
	cmd.Flags().BoolVar(&hidden, "hidden", false, "Include hidden hits")
	return cmd
}

func sortedHits(hits []trainHit) []trainHit {
	out := slices.Clone(hits)
	slices.SortStableFunc(out, func(a, b trainHit) int {
		if d := slices.Index(severityOrder, a.Severity) - slices.Index(severityOrder, b.Severity); d != 0 {
			return d
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func writeTrainNoBoard(w io.Writer, body trainBoardBody) error {
	_, err := fmt.Fprintf(w, "Release trains: config %s (%s)\n  %s\n", body.Config.State, body.Config.Path, body.Config.Message)
	return err
}

func writeTrainBoard(w io.Writer, body trainBoardBody, f trainFilter, now time.Time) error {
	b := body.Board
	var s strings.Builder
	fmt.Fprintf(&s, "Release trains · %s\n", longDay(b.Today))
	if body.Config.State != "ok" {
		fmt.Fprintf(&s, "Config %s, showing the last good one: %s\n", body.Config.State, body.Config.Message)
	}
	s.WriteString("Sources  " + sourcesLine(b, now) + "\n")
	tw := tabwriter.NewWriter(&s, 0, 4, 2, ' ', 0)
	for _, t := range b.Trains {
		if !f.trainKey(t.ID) || t.Phase == "past" && f.train == "" {
			continue
		}
		rows := 0
		for _, v := range t.Versions {
			if f.app(v.App) {
				rows++
			}
		}
		if rows == 0 {
			continue
		}
		_ = tw.Flush()
		fmt.Fprintf(&s, "\n%s  freeze %s → publish %s  %s\n", t.Label, shortDay(t.Freeze), shortDay(t.Publish), t.Phase)
		for _, v := range t.Versions {
			if !f.app(v.App) {
				continue
			}
			key, status := nowStage(v.Stages)
			owner := "-"
			if v.ReleaseMR != nil {
				owner = fmt.Sprintf("!%d %s", v.ReleaseMR.MR.IID, v.ReleaseMR.MR.Author)
			}
			merged := 0
			for _, c := range v.Cards {
				if c.Code.Kind == "merged" {
					merged++
				}
			}
			approvals := "-"
			if v.Jira != nil && len(v.Jira.Approvers) > 0 {
				ok := 0
				for _, a := range v.Jira.Approvers {
					if a.Status == "APPROVED" {
						ok++
					}
				}
				approvals = fmt.Sprintf("%d/%d", ok, len(v.Jira.Approvers))
			}
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%d/%d merged\tapprovals %s\n",
				b.appName(v.App), v.Version, key, status, owner, merged, len(v.Cards), approvals)
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	s.WriteString("\n" + attentionLine(b, f) + "\n")
	var blocks []trainHit
	for _, h := range sortedHits(b.Alerts.Hits) {
		if h.Bucket == "badge" && h.Severity == "block" && f.hit(h) {
			blocks = append(blocks, h)
		}
	}
	if len(blocks) > 0 {
		if err := writeTrainHitRows(&s, b, blocks, false, "  "); err != nil {
			return err
		}
	}
	if line := unknownLine(b, f); line != "" {
		s.WriteString("Unknown  " + line + "\n")
	}
	_, err := io.WriteString(w, s.String())
	return err
}

func nowStage(stages []struct {
	Key    string `json:"key"`
	Status string `json:"status"`
}) (string, string) {
	for _, st := range stages {
		if st.Status != "done" && st.Status != "not_observable" {
			return st.Key, st.Status
		}
	}
	if len(stages) == 0 {
		return "-", "-"
	}
	last := stages[len(stages)-1]
	return last.Key, last.Status
}

func sourcesLine(b *trainBoard, now time.Time) string {
	parts := make([]string, 0, len(b.Sources))
	for _, src := range b.Sources {
		age := "never"
		if src.AsOf != nil {
			age = ageText(now.Sub(*src.AsOf))
		}
		part := src.System + " " + age
		if len(src.Issues) > 0 {
			kinds := make([]string, 0, len(src.Issues))
			for _, is := range src.Issues {
				k := is.Kind
				if is.ErrorClass != "" {
					k += " " + is.ErrorClass
				}
				if !slices.Contains(kinds, k) {
					kinds = append(kinds, k)
				}
			}
			part += fmt.Sprintf(", %d of %d not fresh: %s", len(src.Issues), src.Resources, strings.Join(kinds, ", "))
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " · ")
}

func ageText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

func attentionLine(b *trainBoard, f trainFilter) string {
	counts := map[string]int{}
	history, hidden := 0, 0
	for _, h := range b.Alerts.Hits {
		if !f.hit(h) {
			continue
		}
		switch h.Bucket {
		case "badge":
			counts[h.Severity]++
		case "history":
			history++
		case "hidden":
			hidden++
		}
	}
	var parts []string
	for _, sev := range severityOrder {
		if counts[sev] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[sev], strings.ToLower(severityLabel(sev))))
		}
	}
	line := "Needs attention  none"
	if len(parts) > 0 {
		line = "Needs attention  " + strings.Join(parts, " · ")
	}
	if history+hidden > 0 {
		line += fmt.Sprintf("   (%d in history, %d hidden)", history, hidden)
	}
	return line
}

func unknownLine(b *trainBoard, f trainFilter) string {
	var parts []string
	for _, r := range b.Alerts.Rules {
		if r.On && r.Outcome.Kind == "unknown" && f.app(r.App) {
			name := "all apps"
			if r.App != "" {
				name = b.appName(r.App)
			}
			parts = append(parts, r.Rule+" "+name)
		}
	}
	return strings.Join(parts, " · ")
}

func writeTrainHits(w io.Writer, b *trainBoard, hits []trainHit, withBucket bool) error {
	if len(hits) == 0 {
		_, err := io.WriteString(w, "No alerts need attention\n")
		return err
	}
	var s strings.Builder
	if err := writeTrainHitRows(&s, b, hits, withBucket, ""); err != nil {
		return err
	}
	_, err := io.WriteString(w, s.String())
	return err
}

func writeTrainHitRows(s *strings.Builder, b *trainBoard, hits []trainHit, withBucket bool, indent string) error {
	tw := tabwriter.NewWriter(s, 0, 4, 2, ' ', 0)
	for _, h := range hits {
		row := fmt.Sprintf("%s%s\t%s\t%s\t%s\t%s", indent, severityLabel(h.Severity), h.Rule, b.subject(h), h.Text, h.ID)
		if withBucket {
			row += "\t" + h.Bucket
		}
		_, _ = fmt.Fprintln(tw, row)
	}
	return tw.Flush()
}

func longDay(s string) string {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return s
	}
	return d.Format("Mon 2 Jan 2006")
}

func shortDay(s string) string {
	if s == "" {
		return "?"
	}
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return s
	}
	return d.Format("Mon 2 Jan")
}

func writeFilteredBoardJSON(w io.Writer, raw json.RawMessage, f trainFilter) error {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}
	board, _ := body["board"].(map[string]any)
	if board != nil && (f.train != "" || len(f.apps) > 0) {
		trains, _ := board["trains"].([]any)
		var kept []any
		for _, t := range trains {
			tm, _ := t.(map[string]any)
			if key, _ := tm["id"].(string); !f.trainKey(key) {
				continue
			}
			versions, _ := tm["versions"].([]any)
			tm["versions"] = keepByApp(versions, f)
			if len(tm["versions"].([]any)) > 0 {
				kept = append(kept, tm)
			}
		}
		if kept == nil {
			kept = []any{}
		}
		board["trains"] = kept
		if alerts, ok := board["alerts"].(map[string]any); ok {
			hits, _ := alerts["hits"].([]any)
			var keptHits []any
			for _, h := range hits {
				hm, _ := h.(map[string]any)
				app, _ := hm["app"].(string)
				train, _ := hm["train"].(string)
				if f.hit(trainHit{App: app, Train: train}) {
					keptHits = append(keptHits, hm)
				}
			}
			if keptHits == nil {
				keptHits = []any{}
			}
			alerts["hits"] = keptHits
		}
	}
	return writeJSON(w, body)
}

func keepByApp(items []any, f trainFilter) []any {
	out := []any{}
	for _, it := range items {
		m, _ := it.(map[string]any)
		if app, _ := m["app"].(string); f.app(app) {
			out = append(out, m)
		}
	}
	return out
}

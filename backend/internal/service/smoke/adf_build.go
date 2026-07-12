package smoke

// Builds the Atlassian Document Format (ADF) comment posted to a linked Jira
// issue: an intro line + a results table (Check · Status · Note · Evidence) over
// the run rows, and — when includeMedia is set — one mediaSingle per image
// attachment below the table. ADF nodes are plain map[string]any so the adapter
// marshals them straight to Jira's JSON; keeping the shapes here (not a shared
// builder) is deliberate — the table is smoke-specific.

import (
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// buildResultsADF renders the ADF document node for the comment. run is the
// run-only checks in seq order; uploads maps a check id to its uploaded
// attachments (referenced as links in the Evidence column). When includeMedia is
// true, image attachments are also embedded inline via media nodes.
func buildResultsADF(run []domain.SmokeCheck, uploads map[string][]uploadedEvidence, includeMedia bool) map[string]any {
	content := []any{
		adfParagraph(adfText(resultsIntro(run))),
		buildResultsTable(run, uploads),
	}
	if includeMedia {
		content = append(content, mediaSingles(run, uploads)...)
	}
	return map[string]any{"type": "doc", "version": 1, "content": content}
}

// resultsIntro is the summary line above the table.
func resultsIntro(run []domain.SmokeCheck) string {
	var pass, fail, skip int
	for _, c := range run {
		switch c.Verdict {
		case domain.SmokePass:
			pass++
		case domain.SmokeFail:
			fail++
		case domain.SmokeSkip:
			skip++
		}
	}
	b := &strings.Builder{}
	fmt.Fprintf(b, "Smoke test results — %d passed, %d failed", pass, fail)
	if skip > 0 {
		fmt.Fprintf(b, ", %d skipped", skip)
	}
	fmt.Fprintf(b, " of %d checks run. Posted from Agent Orchestrator.", len(run))
	return b.String()
}

// buildResultsTable is the ADF table: a header row plus one row per run check.
func buildResultsTable(run []domain.SmokeCheck, uploads map[string][]uploadedEvidence) map[string]any {
	rows := make([]any, 0, 1+len(run))
	rows = append(rows, adfRow(
		adfHeaderCell(adfParagraph(adfStrong("Check"))),
		adfHeaderCell(adfParagraph(adfStrong("Status"))),
		adfHeaderCell(adfParagraph(adfStrong("Note"))),
		adfHeaderCell(adfParagraph(adfStrong("Evidence"))),
	))
	for _, c := range run {
		rows = append(rows, adfRow(
			adfCell(checkCellParagraph(c)),
			adfCell(adfParagraph(adfText(statusText(c.Verdict)))),
			adfCell(adfParagraph(adfText(orDash(c.Note)))),
			adfCell(evidenceCellParagraph(uploads[c.ID])),
		))
	}
	return map[string]any{
		"type":    "table",
		"attrs":   map[string]any{"isNumberColumnEnabled": false, "layout": "default"},
		"content": rows,
	}
}

// checkCellParagraph is the Check cell: the case name (bold), with a PR/file ref
// on a second line when present.
func checkCellParagraph(c domain.SmokeCheck) map[string]any {
	nodes := []any{adfStrong(c.Name)}
	if ref := checkRef(c); ref != "" {
		nodes = append(nodes, adfHardBreak(), adfText(ref))
	}
	return map[string]any{"type": "paragraph", "content": nodes}
}

func checkRef(c domain.SmokeCheck) string {
	parts := make([]string, 0, 2)
	if c.PRNum > 0 {
		parts = append(parts, fmt.Sprintf("PR #%d", c.PRNum))
	}
	if strings.TrimSpace(c.FileRef) != "" {
		parts = append(parts, c.FileRef)
	}
	return strings.Join(parts, " · ")
}

// evidenceCellParagraph is the Evidence cell: each uploaded attachment as a link
// to its content URL (one per line), or an em dash when the row has none.
func evidenceCellParagraph(evs []uploadedEvidence) map[string]any {
	if len(evs) == 0 {
		return adfParagraph(adfText("—"))
	}
	nodes := make([]any, 0, len(evs)*2)
	for i, e := range evs {
		if i > 0 {
			nodes = append(nodes, adfHardBreak())
		}
		label := e.att.Filename
		if strings.TrimSpace(label) == "" {
			label = "attachment"
		}
		if strings.TrimSpace(e.att.ContentURL) != "" {
			nodes = append(nodes, adfLink(label, e.att.ContentURL))
		} else {
			nodes = append(nodes, adfText(label))
		}
	}
	return map[string]any{"type": "paragraph", "content": nodes}
}

// mediaSingles builds one mediaSingle block per image attachment (in run order),
// embedding it inline by attachment id. Videos and non-images are link-only.
func mediaSingles(run []domain.SmokeCheck, uploads map[string][]uploadedEvidence) []any {
	var out []any
	for _, c := range run {
		for _, e := range uploads[c.ID] {
			if !e.isImage || strings.TrimSpace(e.att.ID) == "" {
				continue
			}
			out = append(out, map[string]any{
				"type":  "mediaSingle",
				"attrs": map[string]any{"layout": "center"},
				"content": []any{
					map[string]any{
						"type":  "media",
						"attrs": map[string]any{"type": "file", "id": e.att.ID},
					},
				},
			})
		}
	}
	return out
}

func statusText(v domain.SmokeVerdict) string {
	switch v {
	case domain.SmokePass:
		return "✓ Pass"
	case domain.SmokeFail:
		return "✗ Fail"
	case domain.SmokeSkip:
		return "⊘ Skip"
	default:
		return "○ Pending"
	}
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// --- ADF node constructors -------------------------------------------------

func adfText(s string) map[string]any {
	return map[string]any{"type": "text", "text": s}
}

func adfStrong(s string) map[string]any {
	return map[string]any{"type": "text", "text": s, "marks": []any{map[string]any{"type": "strong"}}}
}

func adfLink(s, href string) map[string]any {
	return map[string]any{
		"type":  "text",
		"text":  s,
		"marks": []any{map[string]any{"type": "link", "attrs": map[string]any{"href": href}}},
	}
}

func adfHardBreak() map[string]any {
	return map[string]any{"type": "hardBreak"}
}

func adfParagraph(nodes ...map[string]any) map[string]any {
	content := make([]any, 0, len(nodes))
	for _, n := range nodes {
		content = append(content, n)
	}
	return map[string]any{"type": "paragraph", "content": content}
}

func adfCell(blocks ...map[string]any) map[string]any {
	return tableCellNode("tableCell", blocks)
}

func adfHeaderCell(blocks ...map[string]any) map[string]any {
	return tableCellNode("tableHeader", blocks)
}

func tableCellNode(kind string, blocks []map[string]any) map[string]any {
	content := make([]any, 0, len(blocks))
	for _, b := range blocks {
		content = append(content, b)
	}
	return map[string]any{"type": kind, "content": content}
}

func adfRow(cells ...map[string]any) map[string]any {
	content := make([]any, 0, len(cells))
	for _, c := range cells {
		content = append(content, c)
	}
	return map[string]any{"type": "tableRow", "content": content}
}

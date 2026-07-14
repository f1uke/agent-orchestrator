package smoke

// Builds the Atlassian Document Format (ADF) comment posted to a linked Jira
// issue. The results are laid out as a per-case TABLE (one row per run row:
// Case / Status / the worker-authored context) so a reader scans the outcome at
// a glance. Evidence is NOT put in a table cell — Jira does not reliably render
// media nodes inside table cells — so each screenshot/clip previews inline in an
// "Evidence" section BELOW the table, embedded via a media node that references
// the file's media-services id (which Jira resolves to an image preview or a
// video player). Any file that failed to upload, or whose media id could not be
// resolved, renders as a link/note instead. ADF nodes are plain map[string]any
// so the adapter marshals them straight to Jira's JSON; keeping the shapes here
// (not a shared builder) is deliberate — the layout is smoke-specific.

import (
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Column / section labels. "Why this matters" replaces the app's "WHY YOU'RE
// CHECKING" wording — it reads more naturally for a Jira reader skimming the
// comment without opening Agent Orchestrator, and states the purpose directly.
const (
	colCase       = "Case"
	colStatus     = "Status"
	labelWhy      = "Why this matters"
	labelSteps    = "Steps"
	labelExpected = "Expected result"
	labelNote     = "Note"
	labelEvidence = "Evidence"
)

// columnFlags records which optional columns any run row populates, so an
// all-empty column (e.g. no case has a note) is dropped rather than rendering a
// column of dashes.
type columnFlags struct {
	why      bool
	steps    bool
	expected bool
	note     bool
}

func columnsFor(run []domain.SmokeCheck) columnFlags {
	var f columnFlags
	for _, c := range run {
		if strings.TrimSpace(c.Why) != "" {
			f.why = true
		}
		if len(nonEmptyLines(c.Steps)) > 0 {
			f.steps = true
		}
		if strings.TrimSpace(c.Expected) != "" {
			f.expected = true
		}
		if strings.TrimSpace(c.Note) != "" {
			f.note = true
		}
	}
	return f
}

// buildResultsADF renders the ADF document node for the comment: an intro line,
// the per-case results table, then an evidence section whose screenshots/clips
// preview inline. run is the run-only checks in seq order; uploads maps a check
// id to its uploaded evidence. When includeMedia is true, evidence with a
// resolved media id embeds inline via media nodes; when false (the media-free
// 400 fallback), every evidence file renders as a link instead.
func buildResultsADF(run []domain.SmokeCheck, uploads map[string][]uploadedEvidence, includeMedia bool) map[string]any {
	content := make([]any, 0, 2+len(run))
	content = append(content, adfParagraph(adfText(resultsIntro(run))), resultsTable(run))
	content = append(content, evidenceSection(run, uploads, includeMedia)...)
	return map[string]any{"type": "doc", "version": 1, "content": content}
}

// resultsIntro is the summary line above the results table.
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

// resultsTable renders the per-case table: a header row plus one row per run
// row. Optional columns present only when at least one case populates them.
func resultsTable(run []domain.SmokeCheck) map[string]any {
	cols := columnsFor(run)

	headers := []any{tableHeader(adfParagraph(adfStrong(colCase))), tableHeader(adfParagraph(adfStrong(colStatus)))}
	if cols.why {
		headers = append(headers, tableHeader(adfParagraph(adfStrong(labelWhy))))
	}
	if cols.steps {
		headers = append(headers, tableHeader(adfParagraph(adfStrong(labelSteps))))
	}
	if cols.expected {
		headers = append(headers, tableHeader(adfParagraph(adfStrong(labelExpected))))
	}
	if cols.note {
		headers = append(headers, tableHeader(adfParagraph(adfStrong(labelNote))))
	}

	rows := make([]any, 0, 1+len(run))
	rows = append(rows, map[string]any{"type": "tableRow", "content": headers})
	for _, c := range run {
		cells := []any{
			tableCell(adfParagraph(adfStrong(strings.TrimSpace(c.Name)))),
			tableCell(adfParagraph(adfText(statusText(c.Verdict)))),
		}
		if cols.why {
			cells = append(cells, tableCell(textOrDash(c.Why)))
		}
		if cols.steps {
			if steps := nonEmptyLines(c.Steps); len(steps) > 0 {
				cells = append(cells, tableCell(adfOrderedList(steps)))
			} else {
				cells = append(cells, tableCell(adfParagraph(adfText("—"))))
			}
		}
		if cols.expected {
			cells = append(cells, tableCell(textOrDash(c.Expected)))
		}
		if cols.note {
			cells = append(cells, tableCell(textOrDash(c.Note)))
		}
		rows = append(rows, map[string]any{"type": "tableRow", "content": cells})
	}

	return map[string]any{
		"type":    "table",
		"attrs":   map[string]any{"isNumberColumnEnabled": false, "layout": "default"},
		"content": rows,
	}
}

// evidenceSection renders every run row's evidence below the table, grouped by
// case so each preview is attributable. Screenshots/clips with a resolved media
// id embed inline (when includeMedia) so Jira previews them; a failed upload
// becomes a short note, and anything without a media id (or when media is off)
// renders as a link. Returns nil when no run row has evidence.
func evidenceSection(run []domain.SmokeCheck, uploads map[string][]uploadedEvidence, includeMedia bool) []any {
	var body []any
	for _, c := range run {
		evs := uploads[c.ID]
		if len(evs) == 0 {
			continue
		}
		body = append(body, adfParagraph(adfStrong(strings.TrimSpace(c.Name))))
		for _, e := range evs {
			switch {
			case e.failed:
				body = append(body, adfParagraph(adfText("⚠ "+evidenceLabel(e)+" — attachment upload failed")))
			case includeMedia && strings.TrimSpace(e.mediaID) != "":
				body = append(body, mediaSingleNode(e.mediaID))
			default:
				body = append(body, adfParagraph(evidenceLinkNode(e)))
			}
		}
	}
	if len(body) == 0 {
		return nil
	}
	return append([]any{adfHeading(3, adfText(labelEvidence))}, body...)
}

// mediaSingleNode embeds one attachment (image or video) inline by its
// media-services file id. All three attrs (type, id, collection) are mandatory
// for Jira to resolve and preview the media; collection is intentionally the
// empty string (the file's default collection).
func mediaSingleNode(mediaID string) map[string]any {
	return map[string]any{
		"type":  "mediaSingle",
		"attrs": map[string]any{"layout": "center"},
		"content": []any{
			map[string]any{
				"type":  "media",
				"attrs": map[string]any{"type": "file", "id": mediaID, "collection": ""},
			},
		},
	}
}

// evidenceLinkNode renders one evidence file as a link to its attachment content
// URL (the reliable fallback when inline media is off or unavailable), or as
// plain text when no URL is known.
func evidenceLinkNode(e uploadedEvidence) map[string]any {
	label := evidenceLabel(e)
	if strings.TrimSpace(e.att.ContentURL) != "" {
		return adfLink(label, e.att.ContentURL)
	}
	return adfText(label)
}

// evidenceLabel is the display name for an evidence file: the uploaded
// attachment's filename, else the name captured before upload, else a default.
func evidenceLabel(e uploadedEvidence) string {
	if n := strings.TrimSpace(e.att.Filename); n != "" {
		return n
	}
	if n := strings.TrimSpace(e.name); n != "" {
		return n
	}
	return "attachment"
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

// nonEmptyLines drops blank entries so a stray empty step never renders an empty
// list item.
func nonEmptyLines(items []string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
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

func adfParagraph(nodes ...map[string]any) map[string]any {
	content := make([]any, 0, len(nodes))
	for _, n := range nodes {
		content = append(content, n)
	}
	return map[string]any{"type": "paragraph", "content": content}
}

// textOrDash is a paragraph with trimmed text, or an em dash when empty — so a
// table cell is never visually blank.
func textOrDash(s string) map[string]any {
	if t := strings.TrimSpace(s); t != "" {
		return adfParagraph(adfText(t))
	}
	return adfParagraph(adfText("—"))
}

func adfHeading(level int, nodes ...map[string]any) map[string]any {
	content := make([]any, 0, len(nodes))
	for _, n := range nodes {
		content = append(content, n)
	}
	return map[string]any{"type": "heading", "attrs": map[string]any{"level": level}, "content": content}
}

// tableHeader / tableCell wrap block content in a table cell node. ADF cells
// require block-level children (paragraphs, lists), never bare text.
func tableHeader(nodes ...map[string]any) map[string]any {
	return map[string]any{"type": "tableHeader", "attrs": map[string]any{}, "content": blockContent(nodes)}
}

func tableCell(nodes ...map[string]any) map[string]any {
	return map[string]any{"type": "tableCell", "attrs": map[string]any{}, "content": blockContent(nodes)}
}

func blockContent(nodes []map[string]any) []any {
	content := make([]any, 0, len(nodes))
	for _, n := range nodes {
		content = append(content, n)
	}
	return content
}

// adfOrderedList renders steps as a numbered list, one paragraph per item.
func adfOrderedList(items []string) map[string]any {
	li := make([]any, 0, len(items))
	for _, s := range items {
		li = append(li, map[string]any{
			"type":    "listItem",
			"content": []any{adfParagraph(adfText(s))},
		})
	}
	return map[string]any{"type": "orderedList", "attrs": map[string]any{"order": 1}, "content": li}
}

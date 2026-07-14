package smoke

// Builds the Atlassian Document Format (ADF) comment posted to a linked Jira
// issue. Rather than cram every case into one narrow four-column table, each run
// row becomes its own readable section: a heading with the case title, a status
// line, the worker-authored context (why it matters, the steps, the expected
// result), the user's note, and the evidence — image evidence embedded inline as
// media nodes (when includeMedia is set) so it previews directly on the issue,
// with videos and any file that failed to upload rendered as a link or a short
// note. ADF nodes are plain map[string]any so the adapter marshals them straight
// to Jira's JSON; keeping the shapes here (not a shared builder) is deliberate —
// the layout is smoke-specific.

import (
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Section labels. "Why this matters" replaces the app's "WHY YOU'RE CHECKING"
// wording — it reads more naturally for a Jira reader skimming the comment
// without opening Agent Orchestrator, and states the section's purpose directly.
const (
	labelStatus   = "Status"
	labelWhy      = "Why this matters"
	labelSteps    = "Steps"
	labelExpected = "Expected result"
	labelNote     = "Note"
	labelEvidence = "Evidence"
)

// buildResultsADF renders the ADF document node for the comment. run is the
// run-only checks in seq order; uploads maps a check id to its uploaded
// evidence. When includeMedia is true, image evidence is embedded inline via
// media nodes; when false (the media-free fallback), every evidence file renders
// as a link instead.
func buildResultsADF(run []domain.SmokeCheck, uploads map[string][]uploadedEvidence, includeMedia bool) map[string]any {
	content := make([]any, 0, 1+len(run))
	content = append(content, adfParagraph(adfText(resultsIntro(run))))
	for _, c := range run {
		content = append(content, caseSection(c, uploads[c.ID], includeMedia)...)
	}
	return map[string]any{"type": "doc", "version": 1, "content": content}
}

// resultsIntro is the summary line above the case sections.
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

// caseSection builds the block of nodes for one run row: a divider, the title
// heading (title ONLY — the file/PR ref is intentionally omitted from the post),
// the status + authored-context lines, the user's note, and the evidence.
func caseSection(c domain.SmokeCheck, evs []uploadedEvidence, includeMedia bool) []any {
	nodes := []any{
		adfRule(),
		adfHeading(3, adfText(c.Name)),
		labeledParagraph(labelStatus, statusText(c.Verdict)),
	}
	if why := strings.TrimSpace(c.Why); why != "" {
		nodes = append(nodes, labeledParagraph(labelWhy, why))
	}
	if steps := nonEmptyLines(c.Steps); len(steps) > 0 {
		nodes = append(nodes, adfParagraph(adfStrong(labelSteps)), adfOrderedList(steps))
	}
	if expected := strings.TrimSpace(c.Expected); expected != "" {
		nodes = append(nodes, labeledParagraph(labelExpected, expected))
	}
	if note := strings.TrimSpace(c.Note); note != "" {
		nodes = append(nodes, labeledParagraph(labelNote, note))
	}
	return append(nodes, evidenceNodes(evs, includeMedia)...)
}

// evidenceNodes renders a row's evidence under an "Evidence" label: images as
// inline media (when includeMedia), videos as links to the attachment, and any
// file that failed to upload as a short note so the comment still records what
// was captured. Returns nil when the row has no evidence.
func evidenceNodes(evs []uploadedEvidence, includeMedia bool) []any {
	if len(evs) == 0 {
		return nil
	}
	out := []any{adfParagraph(adfStrong(labelEvidence))}
	for _, e := range evs {
		switch {
		case e.failed:
			out = append(out, adfParagraph(adfText("⚠ "+evidenceLabel(e)+" — attachment upload failed")))
		case includeMedia && e.isImage && strings.TrimSpace(e.att.ID) != "":
			out = append(out, mediaSingleNode(e.att.ID))
		default:
			out = append(out, adfParagraph(evidenceLinkNode(e)))
		}
	}
	return out
}

// mediaSingleNode embeds one image attachment inline by its attachment id.
func mediaSingleNode(id string) map[string]any {
	return map[string]any{
		"type":  "mediaSingle",
		"attrs": map[string]any{"layout": "center"},
		"content": []any{
			map[string]any{"type": "media", "attrs": map[string]any{"type": "file", "id": id}},
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

// labeledParagraph is a "Label: value" line — the label bold, the value plain.
func labeledParagraph(label, value string) map[string]any {
	return adfParagraph(adfStrong(label+": "), adfText(value))
}

func adfHeading(level int, nodes ...map[string]any) map[string]any {
	content := make([]any, 0, len(nodes))
	for _, n := range nodes {
		content = append(content, n)
	}
	return map[string]any{"type": "heading", "attrs": map[string]any{"level": level}, "content": content}
}

func adfRule() map[string]any {
	return map[string]any{"type": "rule"}
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

// Package messagetemplates holds the built-in default text (Go text/template)
// for every runtime nudge AO sends into a worker's pane, the documented
// placeholder set for each, and a pure Execute. The lifecycle reactor and the
// settings API read one source of truth for defaults + Reset-to-default.
package messagetemplates

import (
	"bytes"
	"fmt"
	"text/template"
)

// Name enumerates the editable nudge templates.
type Name string

const (
	NameReviewCommentDispatch Name = "review-comment-dispatch"
	NameCIFailing             Name = "ci-failing"
	NameMergeConflict         Name = "merge-conflict"
	NameTrackerBotComment     Name = "tracker-bot-comment"
	NameAOReviewerBatch       Name = "ao-reviewer-batch"
	NameAOReviewerSingle      Name = "ao-reviewer-single"
)

// KnownNames is the stable order the settings UI renders editors in.
func KnownNames() []Name {
	return []Name{
		NameReviewCommentDispatch,
		NameCIFailing,
		NameMergeConflict,
		NameTrackerBotComment,
		NameAOReviewerBatch,
		NameAOReviewerSingle,
	}
}

// Valid reports whether n is a known template name.
func (n Name) Valid() bool {
	switch n {
	case NameReviewCommentDispatch, NameCIFailing, NameMergeConflict,
		NameTrackerBotComment, NameAOReviewerBatch, NameAOReviewerSingle:
		return true
	}
	return false
}

// ReviewCommentData is the render context for NameReviewCommentDispatch.
type ReviewCommentData struct{ Comments string }

// CIFailingData is the render context for NameCIFailing.
type CIFailingData struct{ LogTail string }

// MergeConflictData is the (empty) render context for NameMergeConflict.
type MergeConflictData struct{}

// TrackerBotData is the render context for NameTrackerBotComment.
type TrackerBotData struct{ Comments string }

// AOReviewItem is one review inside an AO reviewer batch.
type AOReviewItem struct {
	Index     int
	PRURL     string
	Verdict   string
	TargetSHA string
	ReviewID  string
	Body      string
}

// AOReviewerBatchData is the render context for NameAOReviewerBatch.
type AOReviewerBatchData struct {
	Count   int
	Reviews []AOReviewItem
}

// AOReviewerSingleData is the render context for NameAOReviewerSingle.
type AOReviewerSingleData struct {
	PRURL    string
	Verdict  string
	ReviewID string
	Body     string
}

// Placeholders returns the documented template tokens for a name, for the
// settings editor. Unknown names return nil.
func Placeholders(n Name) []string {
	switch n {
	case NameReviewCommentDispatch, NameTrackerBotComment:
		return []string{"{{.Comments}}"}
	case NameCIFailing:
		return []string{"{{.LogTail}}"}
	case NameMergeConflict:
		return nil
	case NameAOReviewerBatch:
		return []string{"{{.Count}}", "{{range .Reviews}}", "{{.Index}}", "{{.PRURL}}", "{{.Verdict}}", "{{.TargetSHA}}", "{{.ReviewID}}", "{{.Body}}", "{{end}}"}
	case NameAOReviewerSingle:
		return []string{"{{.PRURL}}", "{{.Verdict}}", "{{.ReviewID}}", "{{.Body}}"}
	}
	return nil
}

// Default returns the built-in default template for a name. Unknown names
// return "". These reproduce the exact pre-templating nudge text.
func Default(n Name) string {
	switch n {
	case NameReviewCommentDispatch:
		return reviewCommentDefault
	case NameCIFailing:
		return ciFailingDefault
	case NameMergeConflict:
		return mergeConflictDefault
	case NameTrackerBotComment:
		return trackerBotDefault
	case NameAOReviewerBatch:
		return aoReviewerBatchDefault
	case NameAOReviewerSingle:
		return aoReviewerSingleDefault
	}
	return ""
}

// Execute parses and renders tmplText against data. It is pure: no override
// resolution, no fallback. Missing keys error (Option "missingkey=error") so a
// typo'd placeholder in an operator edit surfaces instead of printing "<no value>".
func Execute(tmplText string, data any) (string, error) {
	t, err := template.New("msg").Option("missingkey=error").Parse(tmplText)
	if err != nil {
		return "", fmt.Errorf("messagetemplates: parse: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("messagetemplates: execute: %w", err)
	}
	return buf.String(), nil
}

const reviewCommentDefault = "A reviewer left feedback on your PR. Address it and push.{{if .Comments}}\n\n{{.Comments}}{{end}}"

const ciFailingDefault = "CI is failing on your PR. Review the output below and push a fix.{{if .LogTail}}\n\nFailing output:\n{{.LogTail}}{{end}}"

const mergeConflictDefault = "Your PR has merge conflicts. Rebase onto the base branch and resolve them."

const trackerBotDefault = "A bot left a new comment on your tracker issue. Address it and update the session.{{if .Comments}}\n\n{{.Comments}}{{end}}"

// aoReviewerBatchDefault reproduces the pre-templating loop in
// ApplyReviewBatch byte-for-byte. The leading intro line ends with "\n"; each
// review begins with a blank line ("\n" before "Review N").
const aoReviewerBatchDefault = "[AO reviewer] AO's internal code reviewer submitted {{.Count}} review(s) requesting changes.\n" +
	"{{range .Reviews}}\nReview {{.Index}}\nPR: {{.PRURL}}\nVerdict: {{.Verdict}}" +
	"{{if .TargetSHA}}\nHead commit: {{.TargetSHA}}{{end}}" +
	"{{if .ReviewID}}\nReview: {{.ReviewID}}\nOnce you have addressed it, reply on review {{.ReviewID}} with how you addressed it, then resolve the review comment threads you addressed.{{end}}" +
	"{{if .Body}}\n\nReview body:\n{{.Body}}\n{{end}}{{end}}"

// aoReviewerSingleDefault reproduces the pre-templating ApplyReviewResult text.
const aoReviewerSingleDefault = "[AO reviewer] AO's internal code reviewer submitted a review.\n\nPR: {{.PRURL}}\nVerdict: {{.Verdict}}" +
	"{{if .ReviewID}}\nReview: {{.ReviewID}}\n\nOnce you have addressed it, reply on review {{.ReviewID}} with how you addressed it, then resolve the review comment threads you addressed.{{end}}" +
	"{{if .Body}}\n\nReview body:\n{{.Body}}{{end}}"

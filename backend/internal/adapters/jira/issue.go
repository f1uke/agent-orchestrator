package jira

import "github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira/adf"

// Issue is the normalized, display-only projection of a Jira issue used by the
// Summary tab. Structured fields drive the surrounding UI; Description is the
// faithful ADF render tree (never parsed into cards).
type Issue struct {
	Key   string
	URL   string // human browse URL
	Type  string // issue-type name (Story, Bug, Sub-task, …)
	Title string // summary

	Status         string // e.g. "Ready for QA"
	StatusCategory string // new | indeterminate | done
	StatusColor    string // Jira status-category colorName (blue-gray, yellow, green, …)

	Priority string
	Assignee string
	Reporter string

	Parent      *ParentRef // set for subtasks / epic children (breadcrumb in the detail view)
	Sprint      *Sprint
	Description []adf.Node
	Subtasks    []Subtask
}

// Sprint is the issue's current/most-relevant sprint.
type Sprint struct {
	Name      string
	State     string // active | closed | future
	StartDate string // RFC3339
	EndDate   string // RFC3339
}

// Subtask is a display-only child issue row. Its status is movable later
// (Slice 3); here it is read-only.
type Subtask struct {
	Key            string
	Title          string
	Type           string
	Status         string
	StatusCategory string
	StatusColor    string
}

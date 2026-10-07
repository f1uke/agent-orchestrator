package domain

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// A task can be linked to Testiny test runs: the runs its qa (or its worker)
// played the task's manual cases in. AO stores only which runs belong to the
// task. Everything shown about a run is read live from Testiny, which stays the
// source of truth for titles, cases and results.

// TestinyRunID is a Testiny test run id. It is always > 0.
type TestinyRunID int64

// String renders the id the way Testiny shows it, e.g. TR-632.
func (id TestinyRunID) String() string { return "TR-" + strconv.FormatInt(int64(id), 10) }

// ErrBadRunRef reports a run reference that is not a run id, a TR-<id> label or
// a Testiny run URL.
var ErrBadRunRef = errors.New("not a Testiny run: give a run id (632), TR-632, or a run URL (https://app.testiny.io/<project>/testruns/tr/632)")

// testinyHost is the only host a run URL may name.
const testinyHost = "app.testiny.io"

var (
	testinyRunIDPattern   = regexp.MustCompile(`^(?i:tr-)?(\d+)$`)
	testinyRunPathPattern = regexp.MustCompile(`^/([^/]+)/testruns/tr/(\d+)/?$`)
)

// ParseTestinyRunRef accepts "565", "TR-565" (any case), or a run URL of the
// form https://app.testiny.io/<project_key>/testruns/tr/<id>. A URL's project key
// is returned so a link can refuse a run from another project; it is "" for the
// other two forms. Anything else is ErrBadRunRef.
func ParseTestinyRunRef(s string) (TestinyRunID, string, error) {
	s = strings.TrimSpace(s)
	if m := testinyRunIDPattern.FindStringSubmatch(s); m != nil {
		id, err := parseTestinyRunID(m[1])
		return id, "", err
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host != testinyHost {
		return 0, "", ErrBadRunRef
	}
	m := testinyRunPathPattern.FindStringSubmatch(u.Path)
	if m == nil {
		return 0, "", ErrBadRunRef
	}
	id, err := parseTestinyRunID(m[2])
	if err != nil {
		return 0, "", err
	}
	return id, m[1], nil
}

func parseTestinyRunID(digits string) (TestinyRunID, error) {
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || n <= 0 {
		return 0, ErrBadRunRef
	}
	return TestinyRunID(n), nil
}

// TestinyRunURL is the run's page in the Testiny web app, or "" when the
// project has no key to build it from.
func TestinyRunURL(projectKey string, id TestinyRunID) string {
	if projectKey == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/%s/testruns/tr/%d", testinyHost, url.PathEscape(projectKey), int64(id))
}

// TestinyRunLink records that a run belongs to a task. SessionID is the TASK's
// id (dev's, for a crew), so a link made from qa lands on the task.
type TestinyRunLink struct {
	SessionID SessionID    `json:"sessionId"`
	RunID     TestinyRunID `json:"runId"`
	// LinkedBy is the session id of the agent that linked the run, or "" when a
	// person linked it from the app.
	LinkedBy  string    `json:"linkedBy"`
	CreatedAt time.Time `json:"createdAt"`
}

// TestinyCaseStatus is a case's result in a run. Testiny's own values pass
// through unchanged, including any this list does not name.
type TestinyCaseStatus string

// The statuses Testiny records against a case in a run.
const (
	TestinyPassed  TestinyCaseStatus = "PASSED"
	TestinyFailed  TestinyCaseStatus = "FAILED"
	TestinyBlocked TestinyCaseStatus = "BLOCKED"
	TestinySkipped TestinyCaseStatus = "SKIPPED"
	TestinyNotRun  TestinyCaseStatus = "NOTRUN"
)

// TestinyRef names a Testiny entity a run belongs to (its plan or milestone).
type TestinyRef struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// TestinyCaseResult is one case in a run, with the result recorded against it.
type TestinyCaseResult struct {
	ID     int64             `json:"id"`
	Title  string            `json:"title"`
	Status TestinyCaseStatus `json:"status"`
	// Script is the store-relative path of the Maestro case script whose header
	// names this case, or "" when no script plays it yet.
	Script string `json:"script,omitempty"`
	// Recorded is the latest result AO wrote for this case, or nil when AO has
	// written none. Testiny's Status can differ from it when someone changed the
	// case in Testiny since.
	Recorded *TestinyResultRecord `json:"recorded,omitempty"`
	// Steps is the result Testiny holds for each step of a STEPS case that has
	// one, by step number. A step with none has not been run.
	Steps []TestinyRunStep `json:"steps"`
	// Evidence is every Google Drive link in the comments on the case's
	// result, in the order they were posted, one per Drive file.
	Evidence []TestinyEvidenceLink `json:"evidence"`
}

// TestinyRunStep is the result Testiny holds for one step of a case in a run.
type TestinyRunStep struct {
	N int `json:"n" description:"The step's number, counting from 1, when the result was recorded."`
	// RID is Testiny's id for the step row, or "" when it has none. It names the
	// step when the case's steps were reordered since; see SameTestinyStep.
	RID    string            `json:"rid"`
	Status TestinyCaseStatus `json:"status"`
}

// SameTestinyStep says whether a step result belongs to a step of the case:
// by its row id when both have one, else by its number.
func SameTestinyStep(r TestinyRunStep, s TestinyCaseStep) bool {
	if r.RID != "" && s.RID != "" {
		return r.RID == s.RID
	}
	return r.N == s.N
}

// TestinyResultRecord is a result AO wrote to Testiny, and who asked for it.
type TestinyResultRecord struct {
	Status  TestinyCaseStatus `json:"status"`
	Comment string            `json:"comment"`
	// By is the session id of the agent that wrote it, or "" when a person
	// set it in the app.
	By string `json:"by"`
	// ByRole is By's crew role, or "" for a solo worker or a person.
	ByRole CrewRole `json:"byRole,omitempty" enum:"dev,qa"`
	// SHA is the commit the agent tested, or "" when it did not say.
	SHA string    `json:"sha"`
	At  time.Time `json:"at"`
}

// TestinyResult is one case's result to record in a run. Status is "" when
// only Steps are given: the case keeps the status it has.
type TestinyResult struct {
	CaseID  int64               `json:"caseId"`
	Status  TestinyCaseStatus   `json:"status,omitempty"`
	Comment string              `json:"comment,omitempty"`
	Steps   []TestinyStepResult `json:"steps,omitempty"`
}

// TestinyStepResult is one step's result to record, N counting from 1. A step
// takes no comment.
type TestinyStepResult struct {
	N      int               `json:"n"`
	Status TestinyCaseStatus `json:"status"`
}

// TestinyResultEntry is one line of AO's log of the results it wrote to
// Testiny, as the writer asked for them: Status is "" when the write set only
// steps. SessionID is the task's id.
type TestinyResultEntry struct {
	SessionID SessionID
	RunID     TestinyRunID
	TestinyResult
	// SetBy is the session id of the agent that wrote it; "" is a person.
	SetBy     string
	SHA       string
	CreatedAt time.Time
}

// TestinyCommentMax is the longest comment a result may carry, in characters.
const TestinyCommentMax = 300

// ErrBadTestinyResult reports a result that Testiny's rules do not allow.
var ErrBadTestinyResult = errors.New("invalid Testiny result")

// testinyNeedsComment lists the statuses a result may be set to, and whether
// each must say why: a case that did not pass needs a comment, one that passed
// or was reset takes none.
var testinyNeedsComment = map[TestinyCaseStatus]bool{
	TestinyPassed:  false,
	TestinyFailed:  true,
	TestinyBlocked: true,
	TestinySkipped: true,
	TestinyNotRun:  false,
}

// ParseTestinyResults checks a batch of results against Testiny's rules and
// returns it normalised: statuses upper-cased, the comment trimmed, steps in
// order. A result gives a case status, steps, or both. The first result that
// breaks a rule is named in the error.
func ParseTestinyResults(in []TestinyResult) ([]TestinyResult, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("%w: no results given", ErrBadTestinyResult)
	}
	out := make([]TestinyResult, len(in))
	seen := make(map[int64]bool, len(in))
	for i, r := range in {
		if r.CaseID <= 0 {
			return nil, fmt.Errorf("%w: case id %d is not a Testiny case id", ErrBadTestinyResult, r.CaseID)
		}
		tc := fmt.Sprintf("TC-%d", r.CaseID)
		if seen[r.CaseID] {
			return nil, fmt.Errorf("%w: %s is given twice", ErrBadTestinyResult, tc)
		}
		seen[r.CaseID] = true
		steps, err := parseTestinySteps(tc, r.Steps)
		if err != nil {
			return nil, err
		}
		status := normalTestinyStatus(r.Status)
		comment := strings.TrimSpace(r.Comment)
		if status == "" {
			switch {
			case len(steps) == 0:
				return nil, fmt.Errorf("%w: %s: give a status, step results, or both", ErrBadTestinyResult, tc)
			case comment != "":
				return nil, fmt.Errorf("%w: %s: a comment goes with the case's status; give --status too", ErrBadTestinyResult, tc)
			}
			out[i] = TestinyResult{CaseID: r.CaseID, Steps: steps}
			continue
		}
		needsComment, known := testinyNeedsComment[status]
		if !known {
			return nil, fmt.Errorf("%w: %s: status %q %s", ErrBadTestinyResult, tc, r.Status, testinyStatusList)
		}
		switch n := utf8.RuneCountInString(comment); {
		case needsComment && n == 0:
			return nil, fmt.Errorf("%w: %s: %s needs a comment that says what happened", ErrBadTestinyResult, tc, status)
		case n > TestinyCommentMax:
			return nil, fmt.Errorf("%w: %s: the comment is %d characters; keep it to %d", ErrBadTestinyResult, tc, n, TestinyCommentMax)
		case !needsComment && n > 0:
			return nil, fmt.Errorf("%w: %s: %s takes no comment", ErrBadTestinyResult, tc, status)
		}
		out[i] = TestinyResult{CaseID: r.CaseID, Status: status, Comment: comment, Steps: steps}
	}
	return out, nil
}

const testinyStatusList = "is not one of PASSED, FAILED, BLOCKED, SKIPPED, NOTRUN"

func normalTestinyStatus(s TestinyCaseStatus) TestinyCaseStatus {
	return TestinyCaseStatus(strings.ToUpper(strings.TrimSpace(string(s))))
}

// parseTestinySteps checks one case's step results and returns them in step
// order, or nil for none.
func parseTestinySteps(tc string, in []TestinyStepResult) ([]TestinyStepResult, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]TestinyStepResult, len(in))
	seen := make(map[int]bool, len(in))
	for i, s := range in {
		step := fmt.Sprintf("%s step %d", tc, s.N)
		switch {
		case s.N < 1:
			return nil, fmt.Errorf("%w: %s: steps count from 1", ErrBadTestinyResult, step)
		case seen[s.N]:
			return nil, fmt.Errorf("%w: %s is given twice", ErrBadTestinyResult, step)
		}
		seen[s.N] = true
		status := normalTestinyStatus(s.Status)
		if _, known := testinyNeedsComment[status]; !known {
			return nil, fmt.Errorf("%w: %s: status %q %s", ErrBadTestinyResult, step, s.Status, testinyStatusList)
		}
		out[i] = TestinyStepResult{N: s.N, Status: status}
	}
	slices.SortFunc(out, func(a, b TestinyStepResult) int { return a.N - b.N })
	return out, nil
}

// TestinyFetchErrorKind says why a run could not be read from Testiny.
type TestinyFetchErrorKind string

// Why a read failed. Each one tells the person something different to do.
const (
	// TestinyErrAuth: Testiny refused the API key.
	TestinyErrAuth TestinyFetchErrorKind = "auth"
	// TestinyErrNotFound: the run no longer exists (or this key cannot see it).
	TestinyErrNotFound TestinyFetchErrorKind = "not_found"
	// TestinyErrUnavailable: Testiny could not be reached, timed out, or
	// answered with something unreadable.
	TestinyErrUnavailable TestinyFetchErrorKind = "unavailable"
	// TestinyErrRejected: Testiny rejected the request for another reason.
	TestinyErrRejected TestinyFetchErrorKind = "rejected"
	// TestinyErrBinaryMissing: the testiny CLI is not installed.
	TestinyErrBinaryMissing TestinyFetchErrorKind = "binary_missing"
	// TestinyErrCLITooOld: the testiny CLI lacks a command AO uses.
	TestinyErrCLITooOld TestinyFetchErrorKind = "cli_too_old"
)

// TestinyFetchError is a failed read of one run.
type TestinyFetchError struct {
	Kind    TestinyFetchErrorKind `json:"kind" enum:"auth,not_found,unavailable,rejected,binary_missing,cli_too_old"`
	Message string                `json:"message"`
}

// TestinyRunView is what the Testiny tab shows for one linked run.
type TestinyRunView struct {
	Link   TestinyRunLink `json:"link"`
	Title  string         `json:"title"`
	URL    string         `json:"url"`
	Closed bool           `json:"closed"`
	// Plan and Milestone are nil when the run has none.
	Plan      *TestinyRef               `json:"plan,omitempty"`
	Milestone *TestinyRef               `json:"milestone,omitempty"`
	Counts    map[TestinyCaseStatus]int `json:"counts"`
	Cases     []TestinyCaseResult       `json:"cases"`
	// EvidenceDir is the run's folder in the QA Evidence tree, or "" when none
	// exists yet.
	EvidenceDir string `json:"evidenceDir"`
	// FetchedAt is when the data above was read from Testiny. It is nil when it
	// never has been, which happens only together with FetchError.
	FetchedAt *time.Time `json:"fetchedAt,omitempty"`
	// FetchError is set when the latest read failed. The data above is then the
	// last good read (as of FetchedAt), or empty.
	FetchError *TestinyFetchError `json:"fetchError,omitempty"`
}

// ErrBadCaseRef reports a case reference that is not a case id or a TC-<id>
// label.
var ErrBadCaseRef = errors.New("not a Testiny case: give its id (7166) or TC-7166")

var testinyCaseIDPattern = regexp.MustCompile(`^(?i:tc-)?(\d+)$`)

// ParseTestinyCaseRef accepts "7166" or "TC-7166" (any case).
func ParseTestinyCaseRef(s string) (int64, error) {
	m := testinyCaseIDPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, ErrBadCaseRef
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrBadCaseRef
	}
	return id, nil
}

// TestinyCaseTemplate is how a case's steps are written. Testiny's own value
// passes through unchanged, including one this list does not name.
type TestinyCaseTemplate string

// The templates a Testiny case is written in.
const (
	// TestinyTemplateSteps: a table of steps, each an action and its expected result.
	TestinyTemplateSteps TestinyCaseTemplate = "STEPS"
	// TestinyTemplateText: the steps and the expected result as two texts.
	TestinyTemplateText TestinyCaseTemplate = "TEXT"
	// TestinyTemplateBDD: Gherkin scenarios.
	TestinyTemplateBDD TestinyCaseTemplate = "BDD"
)

// TestinyCasePriority is a case's priority: Testiny's level (0 is the most
// urgent) and the name Testiny shows for it.
type TestinyCasePriority struct {
	Level int    `json:"level"`
	Label string `json:"label"`
}

var testinyPriorityLabels = []string{"Critical", "High", "Medium", "Low"}

// NewTestinyCasePriority names a priority level. A level Testiny may add later
// is named by its number.
func NewTestinyCasePriority(level int) TestinyCasePriority {
	if level >= 0 && level < len(testinyPriorityLabels) {
		return TestinyCasePriority{Level: level, Label: testinyPriorityLabels[level]}
	}
	return TestinyCasePriority{Level: level, Label: strconv.Itoa(level)}
}

// TestinyCaseStep is one row of a STEPS case.
type TestinyCaseStep struct {
	N int `json:"n"`
	// RID is Testiny's id for the step row. It is "" for a step written as
	// Markdown that nobody has saved in Testiny's web app since, and such a
	// step cannot take a result.
	RID      string `json:"rid"`
	Action   string `json:"action"`
	Expected string `json:"expected"`
}

// TestinyCaseDetail is a Testiny case in full, read live. Rich text is plain
// text as the testiny CLI renders it: one block per line, lists as "- " and
// "1. " with nested content indented, a table row on one line. A field the
// case leaves empty is "" (or an empty list).
type TestinyCaseDetail struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	// Priority is nil when the case has none.
	Priority *TestinyCasePriority `json:"priority,omitempty"`
	Type     string               `json:"type" description:"Testiny's testcase_type, e.g. FUNCTIONAL."`
	Template TestinyCaseTemplate  `json:"template" description:"STEPS, TEXT or BDD; it says which of steps, stepsText/expectedText or bdd is filled."`
	// Platforms, Jira, Features, SubFeatures and Section are the project's
	// custom fields of the same names.
	Platforms   []string `json:"platforms"`
	Jira        string   `json:"jira"`
	Features    string   `json:"features"`
	SubFeatures string   `json:"subFeatures"`
	Section     string   `json:"section"`
	// TestData is what the case needs to be played, such as the int/uat test
	// account to log in with.
	TestData     string   `json:"testData"`
	Precondition string   `json:"precondition"`
	Description  string   `json:"description"`
	Remark       string   `json:"remark"`
	Automation   []string `json:"automation" description:"The case's automation status values, e.g. Manual."`
	// Steps is a STEPS case's table.
	Steps []TestinyCaseStep `json:"steps"`
	// StepsText and ExpectedText are a TEXT case's two texts.
	StepsText    string `json:"stepsText"`
	ExpectedText string `json:"expectedText"`
	// BDD is a BDD case's Gherkin feature file.
	BDD string `json:"bdd"`
	// Requirements are the Jira issues the case is linked to as a requirement,
	// in Testiny's order.
	Requirements []TestinyRequirement `json:"requirements"`
}

// TestinyRequirement is a Jira issue a case is linked to as a requirement, as
// Testiny last read it from Jira.
type TestinyRequirement struct {
	Key     string `json:"key" description:"The Jira issue key, e.g. MOBILITY-4839."`
	Summary string `json:"summary"`
	Status  string `json:"status" description:"The issue's Jira status name, e.g. In Progress."`
}

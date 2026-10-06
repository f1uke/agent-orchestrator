package domain

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
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
)

// TestinyFetchError is a failed read of one run.
type TestinyFetchError struct {
	Kind    TestinyFetchErrorKind `json:"kind" enum:"auth,not_found,unavailable,rejected,binary_missing"`
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

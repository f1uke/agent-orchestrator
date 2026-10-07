package domain

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A run's QA evidence is a folder of files on this Mac, named the way the
// Testiny skill's naming table says, uploaded as it is to a Google Drive
// folder that mirrors it. Each file's Drive link is then posted as a comment
// on its case's result in the run.
//
//	<Project>/<YYYY>/<milestone>/TP-<n> - <plan>/TR-<n> - <run>/
//	    README.md
//	    TC-2124 pass.png
//	    TC-2130 FAIL MOBILITY-4533.mp4
//	    TC-2131 pass - iPhone 15 iOS 18.png

// ErrBadTestinyEvidence reports an evidence folder, or a name in it, that the
// naming table does not allow.
var ErrBadTestinyEvidence = errors.New("invalid QA evidence")

// TestinyEvidenceVerdict is the verdict a file's name gives. The case of each
// is deliberate: a failure catches the eye in a file list.
type TestinyEvidenceVerdict string

// The verdicts an evidence file can carry.
const (
	TestinyEvidencePass TestinyEvidenceVerdict = "pass"
	TestinyEvidenceFail TestinyEvidenceVerdict = "FAIL"
)

// TestinyEvidenceFile is one evidence file, as its name reads.
type TestinyEvidenceFile struct {
	Name    string
	CaseID  int64
	Verdict TestinyEvidenceVerdict
	// JiraKey is the defect a FAIL names; "" for pass.
	JiraKey string
	// Device is the variant after " - ", or "".
	Device string
}

// TestinyEvidenceNaming is the naming rule, for a message that refuses a name.
const TestinyEvidenceNaming = `"TC-<id> pass[ - <device>].<ext>" or "TC-<id> FAIL <JIRA-KEY>[ - <device>].<ext>"`

// TestinyEvidenceReadme is the file every run's folder holds beside its evidence.
const TestinyEvidenceReadme = "README.md"

var (
	testinyEvidenceCase = regexp.MustCompile(`^TC-([1-9][0-9]*)$`)
	testinyEvidenceExt  = regexp.MustCompile(`^[A-Za-z0-9]+$`)
	jiraKeyPattern      = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-[1-9][0-9]*$`)
)

// ParseTestinyEvidenceFile reads an evidence file's name:
// "TC-<id> pass[ - <device>].<ext>" or "TC-<id> FAIL <JIRA-KEY>[ - <device>].<ext>".
// The error says what is wrong with the name.
func ParseTestinyEvidenceFile(name string) (TestinyEvidenceFile, error) {
	bad := func(why string) (TestinyEvidenceFile, error) {
		return TestinyEvidenceFile{}, fmt.Errorf("%w: %q %s", ErrBadTestinyEvidence, name, why)
	}
	dot := strings.LastIndexByte(name, '.')
	if dot <= 0 || !testinyEvidenceExt.MatchString(name[dot+1:]) {
		return bad("has no file extension")
	}
	head, device, hasDevice := strings.Cut(name[:dot], " - ")
	if hasDevice && strings.TrimSpace(device) == "" {
		return bad(`names no device after " - "`)
	}
	words := strings.Split(head, " ")
	m := testinyEvidenceCase.FindStringSubmatch(words[0])
	if m == nil {
		return bad("does not start with TC-<case id> and a space")
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return bad("does not start with TC-<case id> and a space")
	}
	f := TestinyEvidenceFile{Name: name, CaseID: id, Device: strings.TrimSpace(device)}
	if len(words) < 2 {
		return bad("gives no verdict: pass or FAIL")
	}
	switch verdict := TestinyEvidenceVerdict(words[1]); {
	case verdict == TestinyEvidencePass:
		if len(words) > 2 {
			return bad(`has text after "pass"; a device goes after " - "`)
		}
	case verdict == TestinyEvidenceFail:
		if len(words) < 3 || words[2] == "" {
			return bad("gives no Jira key after FAIL, e.g. FAIL MOBILITY-4533")
		}
		if len(words) > 3 {
			return bad(`has text after the Jira key; a device goes after " - "`)
		}
		if !jiraKeyPattern.MatchString(words[2]) {
			return bad(fmt.Sprintf("names %q, which is not a Jira key such as MOBILITY-4533", words[2]))
		}
		f.JiraKey = words[2]
	case strings.EqualFold(string(verdict), string(TestinyEvidencePass)):
		return bad("writes pass in the wrong case: it is lower case, pass")
	case strings.EqualFold(string(verdict), string(TestinyEvidenceFail)):
		return bad("writes FAIL in the wrong case: it is upper case, FAIL")
	default:
		return bad("gives no verdict: pass or FAIL")
	}
	f.Verdict = TestinyEvidenceVerdict(words[1])
	return f, nil
}

// CheckTestinyEvidenceFolder checks the entries of a run's evidence folder:
// README.md is there, every other file is named by the naming table for a
// case in the run, and there is no subfolder. Dotfiles are ignored. Every
// problem is reported at once, so the folder can be fixed in one pass. It
// returns the evidence files, README.md left out.
func CheckTestinyEvidenceFolder(entries []fs.DirEntry, inRun map[int64]bool) ([]TestinyEvidenceFile, error) {
	var (
		files    []TestinyEvidenceFile
		problems []string
		readme   bool
	)
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasPrefix(name, "."):
			continue
		case e.IsDir():
			problems = append(problems, fmt.Sprintf("%q is a folder: keep every file directly in the run's folder", name))
			continue
		case !e.Type().IsRegular():
			problems = append(problems, fmt.Sprintf("%q is not a regular file: put the file itself here, not a link to it", name))
			continue
		case name == TestinyEvidenceReadme:
			readme = true
			continue
		}
		f, err := ParseTestinyEvidenceFile(name)
		if err != nil {
			problems = append(problems, strings.TrimPrefix(err.Error(), ErrBadTestinyEvidence.Error()+": "))
			continue
		}
		if !inRun[f.CaseID] {
			problems = append(problems, fmt.Sprintf("%q is for TC-%d, which is not in the run", name, f.CaseID))
			continue
		}
		files = append(files, f)
	}
	if !readme {
		problems = append([]string{"README.md is missing: write one that says which run it documents, the build, who played it and when, and the verdict per case"}, problems...)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%w: %s; name each file %s", ErrBadTestinyEvidence, strings.Join(problems, "; "), TestinyEvidenceNaming)
	}
	return files, nil
}

// TestinyEvidenceTree is the folder names of one run's evidence, as Testiny
// names the run, its plan, its milestone and its project. The same path is
// used on this Mac and on Drive.
type TestinyEvidenceTree struct {
	Project   string
	Year      int
	Milestone string
	Plan      string
	Run       string
}

// NewTestinyEvidenceTree builds a run's folder names from what Testiny calls
// them. A name that cannot be one folder (empty, with a '/', a NUL, or a
// leading '.') is refused: AO never renames, so it has to be fixed in Testiny.
func NewTestinyEvidenceTree(project string, year int, milestone, plan TestinyRef, run TestinyRunID, runTitle string) (TestinyEvidenceTree, error) {
	t := TestinyEvidenceTree{
		Project:   project,
		Year:      year,
		Milestone: milestone.Title,
		Plan:      fmt.Sprintf("TP-%d - %s", plan.ID, plan.Title),
		Run:       fmt.Sprintf("%s - %s", run, runTitle),
	}
	for _, part := range []struct{ what, name string }{
		{"the Testiny project's name", project},
		{fmt.Sprintf("milestone %d's title", milestone.ID), milestone.Title},
		{fmt.Sprintf("plan TP-%d's title", plan.ID), plan.Title},
		{run.String() + "'s title", runTitle},
	} {
		if err := folderName(part.name); err != nil {
			return TestinyEvidenceTree{}, fmt.Errorf("%w: %s %s; change it in Testiny", ErrBadTestinyEvidence, part.what, err)
		}
	}
	return t, nil
}

func folderName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return errors.New("is empty, so it cannot name a folder")
	case strings.Contains(name, "/"):
		return fmt.Errorf("(%q) has a '/', which would split it into two folders", name)
	case strings.ContainsRune(name, 0):
		return fmt.Errorf("(%q) has a NUL character", name)
	case strings.HasPrefix(name, "."):
		return fmt.Errorf("(%q) starts with '.', which hides the folder", name)
	}
	return nil
}

// Rel is the run folder's path from the root of the tree, '/'-separated:
// MOBILITY/2026/Sprint 2026-19/TP-7 - plan/TR-191 - run.
func (t TestinyEvidenceTree) Rel() string {
	return strings.Join([]string{t.Project, strconv.Itoa(t.Year), t.Milestone, t.Plan, t.Run}, "/")
}

// TestinyEvidenceYear is the <YYYY> of a run's folder: the year its milestone
// starts in, in the given zone, or the year it was created when it has no
// start date. A sprint belongs to the year it starts.
func TestinyEvidenceYear(startAt, createdAt time.Time, zone *time.Location) int {
	at := startAt
	if at.IsZero() {
		at = createdAt
	}
	return at.In(zone).Year()
}

const driveHost = "drive.google.com"

var (
	driveFilePath = regexp.MustCompile(`^/file(?:/u/[0-9]+)?/d/([A-Za-z0-9_-]+)(?:/|$)`)
	driveIDValue  = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// DriveFileID is the Drive file id a Google Drive file link names, in any of
// the shapes Drive hands out: /file/d/<id>/..., /open?id=<id>, /uc?id=<id>.
func DriveFileID(raw string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host != driveHost {
		return "", false
	}
	if m := driveFilePath.FindStringSubmatch(u.Path); m != nil {
		return m[1], true
	}
	if u.Path == "/open" || u.Path == "/uc" {
		if id := u.Query().Get("id"); driveIDValue.MatchString(id) {
			return id, true
		}
	}
	return "", false
}

// DriveFileURL is the link Drive's own "Copy link" gives for a file. Who can
// open it is whatever the shared drive grants: AO changes no permission.
func DriveFileURL(id string) string {
	return "https://" + driveHost + "/file/d/" + id + "/view?usp=drive_link"
}

// TestinyEvidenceLink is a Google Drive link in a comment on a case's result.
type TestinyEvidenceLink struct {
	URL     string `json:"url"`
	DriveID string `json:"driveId" description:"The Drive file id the link names."`
	File    string `json:"file" description:"The evidence file's name when AO posted the link; empty for a link a person posted."`
}

// TestinyEvidenceCaseLinks is what one upload did for one case's result.
type TestinyEvidenceCaseLinks struct {
	CaseID int64 `json:"caseId"`
	// Linked is the files whose links this upload posted, in one comment.
	Linked []string `json:"linked" description:"Files whose links this upload posted, by name."`
	// CommentID is the comment that holds them, or 0 when none was posted.
	CommentID int64 `json:"commentId" description:"The Testiny comment this upload posted, or 0 when it posted none."`
	// AlreadyLinked is the files whose Drive link a comment on the result
	// already held, so they were not posted again.
	AlreadyLinked []string `json:"alreadyLinked" description:"Files whose Drive link the result's comments already held, by name."`
}

// TestinyEvidenceReport is what one evidence upload did.
type TestinyEvidenceReport struct {
	Folder string `json:"folder" description:"The run's evidence folder on this Mac."`
	Drive  string `json:"drive" description:"The rclone path it was uploaded to, e.g. finnomena:QA/MOBILITY/2026/Sprint 2026-19/TP-7 - plan/TR-191 - run."`
	// Uploaded is what rclone transferred this time: a file Drive already had
	// unchanged is not in it.
	Uploaded []string                   `json:"uploaded" description:"Files rclone transferred this time, by name; a file already on Drive unchanged is left out."`
	Cases    []TestinyEvidenceCaseLinks `json:"cases" description:"Per case with evidence files, in case id order, what was linked."`
	Run      TestinyRunView             `json:"run" description:"The run read fresh from Testiny after the upload."`
}

// TestinyEvidenceEvent is what one evidence log line records.
type TestinyEvidenceEvent string

// The events AO logs while uploading evidence.
const (
	// TestinyEvidenceUploaded: rclone transferred the file.
	TestinyEvidenceUploaded TestinyEvidenceEvent = "uploaded"
	// TestinyEvidenceLinked: AO posted the file's link on its case's result.
	TestinyEvidenceLinked TestinyEvidenceEvent = "linked"
)

// TestinyEvidenceEntry is one line of AO's log of the evidence it uploaded and
// linked. SessionID is the task's id.
type TestinyEvidenceEntry struct {
	SessionID SessionID
	RunID     TestinyRunID
	Event     TestinyEvidenceEvent
	File      string
	// CaseID is 0 for README.md.
	CaseID int64
	// DriveID is "" for an upload whose Drive id could not be read.
	DriveID string
	// CommentID is the comment a linked file's link is in; 0 for an upload.
	CommentID int64
	// SetBy is the session id of the agent that ran it; "" is a person.
	SetBy     string
	CreatedAt time.Time
}

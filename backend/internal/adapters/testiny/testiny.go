// Package testiny reads Testiny test runs and cases through the `testiny` CLI,
// records case results in a run, and comments on a result. Results and
// comments on them are the only things it writes.
//
// The CLI prints {"data":...,"meta":...} on stdout, and on failure prints
// {"error":{kind,message,code,status},"exit_code":N} on stderr with exit code
// 2 (usage), 3 (the API rejected it) or 4 (network failure). Every failure is
// mapped to one of this package's sentinels.
package testiny

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Binary is the CLI's name on PATH.
const Binary = "testiny"

const (
	defaultCallTimeout = 15 * time.Second
	projectsTTL        = 10 * time.Minute
)

// Why a call failed.
var (
	ErrBinaryMissing = errors.New("testiny CLI not installed")
	ErrAuth          = errors.New("testiny refused the API key")
	ErrNotFound      = errors.New("not found in testiny")
	ErrUnavailable   = errors.New("testiny unavailable")
	ErrRejected      = errors.New("testiny rejected the request")
	ErrCLITooOld     = errors.New("testiny CLI too old")
)

// Output is what one CLI run printed, and how it exited.
type Output struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner runs the CLI. A non-zero exit is reported in Output, not as an error;
// the error is for a run that could not complete at all.
type Runner func(ctx context.Context, name string, args ...string) (Output, error)

// LookPath resolves a binary, matching os/exec.LookPath.
type LookPath func(file string) (string, error)

// Options configures a Client. Zero fields take the real implementations.
type Options struct {
	LookPath LookPath
	Runner   Runner
	// Home is the user's home directory, for the ~/go/bin fallback.
	Home string
	Now  func() time.Time
}

// Client reads runs, cases, plans, milestones and projects.
type Client struct {
	lookPath    LookPath
	run         Runner
	home        string
	now         func() time.Time
	callTimeout time.Duration

	mu         sync.Mutex
	projects   []domain.TestinyProject
	projectsAt time.Time
}

// New builds a Client.
func New(opts Options) *Client {
	c := &Client{lookPath: opts.LookPath, run: opts.Runner, home: opts.Home, now: opts.Now, callTimeout: defaultCallTimeout}
	if c.lookPath == nil {
		c.lookPath = exec.LookPath
	}
	if c.run == nil {
		c.run = execRunner
	}
	if c.home == "" {
		c.home, _ = os.UserHomeDir()
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c
}

// Run is a test run's own fields.
type Run struct {
	ID        domain.TestinyRunID
	Title     string
	Closed    bool
	ProjectID int64
	// PlanID and MilestoneID are 0 when the run has none (Testiny ids start at 1).
	PlanID      int64
	MilestoneID int64
	// Steps is the step results Testiny holds for each case that has any, by
	// case id, in step order.
	Steps map[int64][]domain.TestinyRunStep
}

// Case is one case in a run with the status recorded against it.
type Case struct {
	ID     int64
	Title  string
	Status string
}

// Results is every case in a run, and a count per status.
type Results struct {
	Cases   []Case
	Summary map[string]int
}

// Ref is an entity's id and title.
type Ref struct {
	ID    int64
	Title string
}

// Run reads one test run, with the step results of its cases. A run that
// does not exist is ErrNotFound.
func (c *Client) Run(ctx context.Context, id domain.TestinyRunID) (Run, error) {
	var raw struct {
		ID         int64  `json:"id"`
		Title      string `json:"title"`
		IsClosed   bool   `json:"is_closed"`
		ProjectID  int64  `json:"project_id"`
		TestplanID *int64 `json:"testplan_id"`
		Milestone  *struct {
			MilestoneID int64 `json:"milestone_id"`
		} `json:"milestone_testrun_values"`
		Cases json.RawMessage `json:"testrun_testcase_values"`
	}
	if err := c.call(ctx, &raw, "run", "show", idArg(int64(id)), "--with", "case"); err != nil {
		return Run{}, err
	}
	run := Run{ID: domain.TestinyRunID(raw.ID), Title: raw.Title, Closed: raw.IsClosed, ProjectID: raw.ProjectID}
	if raw.TestplanID != nil {
		run.PlanID = *raw.TestplanID
	}
	if raw.Milestone != nil {
		run.MilestoneID = raw.Milestone.MilestoneID
	}
	steps, err := stepResults(raw.Cases)
	if err != nil {
		return Run{}, fmt.Errorf("%w: unreadable cases of testiny run %d: %w", ErrUnavailable, id, err)
	}
	run.Steps = steps
	return run, nil
}

type runCase struct {
	CaseID int64 `json:"testcase_id"`
	Steps  []struct {
		Idx int    `json:"idx"`
		RID string `json:"rid"`
		Res string `json:"res"`
	} `json:"result_per_step"`
}

// stepResults reads each case's result_per_step, whose idx counts from 0.
// Testiny gives the run's cases as a list (one case too, as read live); an
// object is read as one case, as the CLI's own result listing reads mappings.
func stepResults(raw json.RawMessage) (map[int64][]domain.TestinyRunStep, error) {
	var cases []runCase
	if trimmed := bytes.TrimSpace(raw); bytes.HasPrefix(trimmed, []byte("{")) {
		var one runCase
		if err := json.Unmarshal(trimmed, &one); err != nil {
			return nil, err
		}
		cases = []runCase{one}
	} else if len(trimmed) > 0 {
		if err := json.Unmarshal(trimmed, &cases); err != nil {
			return nil, err
		}
	}
	out := map[int64][]domain.TestinyRunStep{}
	for _, c := range cases {
		if len(c.Steps) == 0 {
			continue
		}
		steps := make([]domain.TestinyRunStep, len(c.Steps))
		for i, s := range c.Steps {
			steps[i] = domain.TestinyRunStep{N: s.Idx + 1, RID: s.RID, Status: domain.TestinyCaseStatus(s.Res)}
		}
		slices.SortFunc(steps, func(a, b domain.TestinyRunStep) int { return a.N - b.N })
		out[c.CaseID] = steps
	}
	return out, nil
}

// Results reads every case in a run. A run that does not exist has no cases,
// which is not an error: existence is Run's to decide.
func (c *Client) Results(ctx context.Context, id domain.TestinyRunID) (Results, error) {
	var raw struct {
		Data []struct {
			ID     int64  `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
		} `json:"data"`
		Meta struct {
			Summary map[string]int `json:"summary"`
		} `json:"meta"`
	}
	if err := c.callEnvelope(ctx, &raw, "run", "results", "ls", "--run="+idArg(int64(id))); err != nil {
		return Results{}, err
	}
	res := Results{Cases: make([]Case, len(raw.Data)), Summary: raw.Meta.Summary}
	for i, d := range raw.Data {
		res.Cases[i] = Case(d)
	}
	if res.Summary == nil {
		res.Summary = map[string]int{}
	}
	return res, nil
}

// SetResults records results in a run. A result with a comment or steps is
// sent alone, because only `--case` takes a `--comment` (and a comment needs
// the run's project id) or `--step`; the rest go in one `--result` batch.
// Every result needs its case status. Testiny replaces a case's step results
// with the ones a write gives, so a result's Steps must be every step result
// the case is to keep. Every value is its own argv element, so a comment is
// never read as a flag. It stops at the first call that fails and returns what
// was written before it, so a caller can account for a partial write.
func (c *Client) SetResults(ctx context.Context, run domain.TestinyRunID, projectID int64, results []domain.TestinyResult) ([]domain.TestinyResult, error) {
	written := make([]domain.TestinyResult, 0, len(results))
	var batch []domain.TestinyResult
	for _, r := range results {
		if r.Comment == "" && len(r.Steps) == 0 {
			batch = append(batch, r)
			continue
		}
		args := []string{"run", "results", "set", "--run=" + idArg(int64(run))}
		if r.Comment != "" {
			args = append(args, "--project-id="+idArg(projectID))
		}
		args = append(args, "--case="+idArg(r.CaseID), "--status="+string(r.Status))
		if r.Comment != "" {
			args = append(args, "--comment="+r.Comment)
		}
		for _, s := range r.Steps {
			args = append(args, "--step="+strconv.Itoa(s.N)+"="+string(s.Status))
		}
		if err := c.exec(ctx, args...); err != nil {
			return written, err
		}
		written = append(written, r)
	}
	if len(batch) == 0 {
		return written, nil
	}
	args := []string{"run", "results", "set", "--run=" + idArg(int64(run))}
	for _, r := range batch {
		args = append(args, "--result="+idArg(r.CaseID)+"="+string(r.Status))
	}
	if err := c.exec(ctx, args...); err != nil {
		return written, err
	}
	return append(written, batch...), nil
}

// Case reads one test case in full through `testiny case view`, which renders
// its rich text as plain text that keeps its structure, and the Jira issues it
// is linked to as a requirement through `testiny case show --with workitem`,
// which view does not carry. The two are read at the same time. A case that
// does not exist is ErrNotFound.
func (c *Client) Case(ctx context.Context, id int64) (domain.TestinyCaseDetail, error) {
	var (
		reqs    []domain.TestinyRequirement
		reqsErr error
		wg      sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		reqs, reqsErr = c.requirements(ctx, id)
	}()
	d, err := c.caseView(ctx, id)
	wg.Wait()
	if err != nil {
		return domain.TestinyCaseDetail{}, err
	}
	if reqsErr != nil {
		return domain.TestinyCaseDetail{}, reqsErr
	}
	d.Requirements = reqs
	return d, nil
}

func (c *Client) caseView(ctx context.Context, id int64) (domain.TestinyCaseDetail, error) {
	var raw []struct {
		ID           int64        `json:"id"`
		Title        string       `json:"title"`
		Priority     *int         `json:"priority"`
		Type         string       `json:"testcase_type"`
		Template     string       `json:"template"`
		Precondition string       `json:"precondition"`
		Steps        []caseStep   `json:"steps"`
		StepsText    string       `json:"steps_text"`
		ExpectedText string       `json:"expected_text"`
		BDD          string       `json:"bdd"`
		Custom       customFields `json:"custom"`
	}
	if err := c.call(ctx, &raw, "case", "view", idArg(id)); err != nil {
		return domain.TestinyCaseDetail{}, err
	}
	if len(raw) != 1 {
		return domain.TestinyCaseDetail{}, fmt.Errorf("%w: testiny case view %d returned %d cases", ErrUnavailable, id, len(raw))
	}
	r := raw[0]
	d := domain.TestinyCaseDetail{
		ID:           r.ID,
		Title:        r.Title,
		Type:         r.Type,
		Template:     domain.TestinyCaseTemplate(r.Template),
		Platforms:    r.Custom.list("cf__platform"),
		Jira:         r.Custom.text("cf__jira"),
		Features:     r.Custom.text("cf__features"),
		SubFeatures:  r.Custom.text("cf__subfeatures"),
		Section:      r.Custom.text("cf__section"),
		TestData:     r.Custom.text("cf__testdata"),
		Precondition: r.Precondition,
		Description:  r.Custom.text("cf__description"),
		Remark:       r.Custom.text("cf__remark"),
		Automation:   r.Custom.list("cf__automationstatus"),
		Steps:        make([]domain.TestinyCaseStep, len(r.Steps)),
		StepsText:    r.StepsText,
		ExpectedText: r.ExpectedText,
		BDD:          r.BDD,
	}
	for i, s := range r.Steps {
		d.Steps[i] = domain.TestinyCaseStep(s)
	}
	if r.Priority != nil {
		p := domain.NewTestinyCasePriority(*r.Priority)
		d.Priority = &p
	}
	return d, nil
}

// requirements reads the Jira issues a case is linked to as a requirement. A
// defect link is left out, and so is a link whose issue Testiny could not name.
func (c *Client) requirements(ctx context.Context, id int64) ([]domain.TestinyRequirement, error) {
	var raw struct {
		Links *[]struct {
			Type    string `json:"workitem_type"`
			Key     string `json:"workitem_key"`
			Summary string `json:"workitem_summary"`
			Status  string `json:"workitem_status"`
		} `json:"wi_tc_values"`
	}
	if err := c.call(ctx, &raw, "case", "show", idArg(id), "--with", "workitem"); err != nil {
		return nil, err
	}
	// The CLI reports a relation it could not read as null, and no links would
	// then claim the case is unlinked when nobody knows.
	if raw.Links == nil {
		return nil, fmt.Errorf("%w: testiny case show %d could not read the case's Jira links", ErrUnavailable, id)
	}
	reqs := []domain.TestinyRequirement{}
	for _, l := range *raw.Links {
		if l.Type == "REQUIREMENT" && l.Key != "" {
			reqs = append(reqs, domain.TestinyRequirement{Key: l.Key, Summary: l.Summary, Status: l.Status})
		}
	}
	return reqs, nil
}

type caseStep struct {
	N        int    `json:"n"`
	RID      string `json:"rid"`
	Action   string `json:"action"`
	Expected string `json:"expected"`
}

// customFields are a case's cf__ fields. Each project defines its own, so a
// field may be missing, null, or of a type other than the one AO reads it as.
type customFields map[string]json.RawMessage

// text reads a field as one string: a list's strings are joined by ", ", and
// a boolean or number is its JSON text.
func (f customFields) text(key string) string {
	var v any
	_ = json.Unmarshal(f[key], &v)
	switch v := v.(type) {
	case string:
		return v
	case bool, float64:
		return string(f[key])
	case []any:
		return strings.Join(f.list(key), ", ")
	}
	return ""
}

// list reads a field as a list of its strings: a lone string is a list of
// one, and anything else is empty.
func (f customFields) list(key string) []string {
	var v any
	_ = json.Unmarshal(f[key], &v)
	out := []string{}
	switch v := v.(type) {
	case string:
		if v != "" {
			out = append(out, v)
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// Plan reads a test plan's title.
func (c *Client) Plan(ctx context.Context, id int64) (Ref, error) {
	return c.ref(ctx, "plan", id)
}

// Milestone is a milestone's title, and the dates its year is read from.
type Milestone struct {
	ID    int64
	Title string
	// StartAt is zero when the milestone has no start date.
	StartAt   time.Time
	CreatedAt time.Time
}

// Milestone reads a milestone.
func (c *Client) Milestone(ctx context.Context, id int64) (Milestone, error) {
	var raw struct {
		ID        int64      `json:"id"`
		Title     string     `json:"title"`
		StartAt   *time.Time `json:"start_at"`
		CreatedAt time.Time  `json:"created_at"`
	}
	if err := c.call(ctx, &raw, "milestone", "show", idArg(id)); err != nil {
		return Milestone{}, err
	}
	m := Milestone{ID: raw.ID, Title: raw.Title, CreatedAt: raw.CreatedAt}
	if raw.StartAt != nil {
		m.StartAt = *raw.StartAt
	}
	return m, nil
}

// ResultComment is a comment on a case's result in a run.
type ResultComment struct {
	ID     int64
	CaseID int64
	// URLs is every link in the comment, in order, each once.
	URLs []string
}

// commentPage is how many comments one find asks for. Testiny allows 2000.
const commentPage = 1000

// ResultComments reads every comment on every case's result in a run, oldest
// first. The CLI has no command that lists them, so it asks Testiny's API
// through `testiny raw`, a page at a time until a page comes back short.
func (c *Client) ResultComments(ctx context.Context, run domain.TestinyRunID) ([]ResultComment, error) {
	var out []ResultComment
	for offset := 0; ; offset += commentPage {
		body, err := json.Marshal(map[string]any{
			"filter": map[string]string{"type": "TEXT"},
			"map": map[string]any{
				"entities": []string{"comment", "testrun", "testcase"},
				"ids":      map[string]int64{"testrun_id": int64(run)},
			},
			"pagination": map[string]int{"offset": offset, "limit": commentPage},
		})
		if err != nil {
			return nil, err
		}
		var page struct {
			Data []struct {
				ID        int64           `json:"id"`
				DeletedAt json.RawMessage `json:"deleted_at"`
				Text      string          `json:"text"`
				Result    *struct {
					CaseID int64 `json:"testcase_id"`
				} `json:"comment_testrun_values"`
			} `json:"data"`
		}
		if err := c.callEnvelope(ctx, &page, "raw", "POST", "/comment/find", "--body="+string(body)); err != nil {
			return nil, err
		}
		for _, d := range page.Data {
			if d.Result == nil || (len(d.DeletedAt) > 0 && string(d.DeletedAt) != "null") {
				continue
			}
			out = append(out, ResultComment{ID: d.ID, CaseID: d.Result.CaseID, URLs: commentURLs(d.Text)})
		}
		if len(page.Data) < commentPage {
			break
		}
	}
	slices.SortFunc(out, func(a, b ResultComment) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// slateNode is a node of the Slate document Testiny keeps a comment's text in.
// A link is {"t":"a","url":...} with the URL repeated in its text.
type slateNode struct {
	T        string      `json:"t"`
	URL      string      `json:"url"`
	Text     *string     `json:"text"`
	Children []slateNode `json:"children"`
	C        []slateNode `json:"c"`
}

// bareURL is a URL typed into text rather than made a link. Zero-width
// characters, which the web UI leaves around pasted links, end it.
var bareURL = regexp.MustCompile(`https?://[^\s<>"\x{200B}-\x{200D}\x{2060}\x{FEFF}]+`)

// commentURLs is every URL in a comment's text: each link's url, and each
// URL typed in its text. A text that is not a Slate document is read as plain
// text.
func commentURLs(text string) []string {
	var urls []string
	add := func(u string) {
		if u = strings.TrimRight(u, ".,;:!?)]}'\""); u != "" && !slices.Contains(urls, u) {
			urls = append(urls, u)
		}
	}
	var doc slateNode
	if json.Unmarshal([]byte(text), &doc) != nil || doc.T != "slate" {
		for _, u := range bareURL.FindAllString(text, -1) {
			add(u)
		}
		return urls
	}
	var walk func(nodes []slateNode)
	walk = func(nodes []slateNode) {
		for _, n := range nodes {
			if n.T == "a" && n.URL != "" {
				add(n.URL)
				continue
			}
			if n.Text != nil {
				for _, u := range bareURL.FindAllString(*n.Text, -1) {
					add(u)
				}
			}
			walk(n.Children)
			walk(n.C)
		}
	}
	walk(doc.C)
	return urls
}

// CommentOnResult posts a comment on a case's result in a run, without
// touching its status, and returns the comment's id. Each line of text is a
// paragraph and each URL a link. projectID is the run's project.
func (c *Client) CommentOnResult(ctx context.Context, run domain.TestinyRunID, caseID, projectID int64, text string) (int64, error) {
	var raw struct {
		ID int64 `json:"id"`
	}
	if err := c.call(ctx, &raw, "run", "results", "comment", "--run="+idArg(int64(run)), "--case="+idArg(caseID),
		"--project-id="+idArg(projectID), "--text="+text); err != nil {
		return 0, err
	}
	if raw.ID == 0 {
		return 0, fmt.Errorf("%w: testiny run results comment printed no comment id", ErrUnavailable)
	}
	return raw.ID, nil
}

func (c *Client) ref(ctx context.Context, entity string, id int64) (Ref, error) {
	var raw struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	}
	if err := c.call(ctx, &raw, entity, "show", idArg(id)); err != nil {
		return Ref{}, err
	}
	return Ref(raw), nil
}

// Project resolves what a person typed for a project (its key, name or id) to
// the project, matching without regard to case, key first. The list of
// projects is read at most once every ten minutes.
func (c *Client) Project(ctx context.Context, ref string) (domain.TestinyProject, error) {
	projects, err := c.listProjects(ctx)
	if err != nil {
		return domain.TestinyProject{}, err
	}
	ref = strings.TrimSpace(ref)
	for _, match := range []func(domain.TestinyProject) bool{
		func(p domain.TestinyProject) bool { return p.Key != "" && strings.EqualFold(p.Key, ref) },
		func(p domain.TestinyProject) bool { return strings.EqualFold(p.Name, ref) },
		func(p domain.TestinyProject) bool { return strconv.FormatInt(p.ID, 10) == ref },
	} {
		for _, p := range projects {
			if match(p) {
				return p, nil
			}
		}
	}
	return domain.TestinyProject{}, fmt.Errorf("%w: no Testiny project has the key, name or id %q", ErrNotFound, ref)
}

// ProjectByID finds a project by its id in the same cached list.
func (c *Client) ProjectByID(ctx context.Context, id int64) (domain.TestinyProject, error) {
	return c.Project(ctx, strconv.FormatInt(id, 10))
}

func (c *Client) listProjects(ctx context.Context) ([]domain.TestinyProject, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.projects != nil && c.now().Sub(c.projectsAt) < projectsTTL {
		return c.projects, nil
	}
	var raw []struct {
		ID   int64   `json:"id"`
		Name string  `json:"name"`
		Key  *string `json:"project_key"`
	}
	if err := c.call(ctx, &raw, "project", "ls"); err != nil {
		return nil, err
	}
	projects := make([]domain.TestinyProject, len(raw))
	for i, r := range raw {
		projects[i] = domain.TestinyProject{ID: r.ID, Name: r.Name}
		if r.Key != nil {
			projects[i].Key = *r.Key
		}
	}
	c.projects, c.projectsAt = projects, c.now()
	return projects, nil
}

// call runs the CLI and decodes the envelope's data into out.
func (c *Client) call(ctx context.Context, out any, args ...string) error {
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := c.callEnvelope(ctx, &env, args...); err != nil {
		return err
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("%w: unreadable output of testiny %s: %w", ErrUnavailable, strings.Join(args, " "), err)
	}
	return nil
}

// callEnvelope runs the CLI and decodes its whole stdout into out.
func (c *Client) callEnvelope(ctx context.Context, out any, args ...string) error {
	stdout, err := c.output(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(stdout, out); err != nil {
		return fmt.Errorf("%w: unreadable output of testiny %s: %w", ErrUnavailable, strings.Join(args, " "), err)
	}
	return nil
}

// exec runs a CLI command whose output is not needed: its exit code says
// whether it worked.
func (c *Client) exec(ctx context.Context, args ...string) error {
	_, err := c.output(ctx, args...)
	return err
}

// output runs the CLI and returns its stdout, or the sentinel for how it failed.
func (c *Client) output(ctx context.Context, args ...string) ([]byte, error) {
	bin, err := c.binary()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	res, err := c.run(ctx, bin, args...)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: testiny %s timed out after %s", ErrUnavailable, strings.Join(args, " "), c.callTimeout)
		}
		return nil, fmt.Errorf("%w: run testiny %s: %w", ErrUnavailable, strings.Join(args, " "), err)
	}
	if res.ExitCode != 0 {
		return nil, cliError(res, args)
	}
	return res.Stdout, nil
}

// binary finds the CLI on PATH, then in ~/go/bin: the desktop app's daemon
// starts with a PATH that does not include ~/go/bin.
func (c *Client) binary() (string, error) {
	if p, err := c.lookPath(Binary); err == nil {
		return p, nil
	}
	fallback := filepath.Join(c.home, "go", "bin", Binary)
	if info, err := os.Stat(fallback); err == nil && !info.IsDir() {
		return fallback, nil
	}
	return "", fmt.Errorf("%w: %s is not on PATH and %s does not exist", ErrBinaryMissing, Binary, fallback)
}

// unknownCommand is how the CLI refuses a subcommand it does not have, in its
// own usage report or in cobra's plain one. AO only calls subcommands the
// current CLI has, so this means the installed one is older than AO.
var unknownCommand = regexp.MustCompile(`unknown (command|[\w ]*subcommand) "`)

const updateCLI = "Update the testiny CLI: cd ~/Documents/Projects/testiny-cli && git pull && go install ./cmd/testiny"

// cliError maps a failed run's error report to a sentinel.
func cliError(res Output, args []string) error {
	var report struct {
		Error *struct {
			Message string `json:"message"`
			Code    string `json:"code"`
			Status  int    `json:"status"`
		} `json:"error"`
	}
	stderr := strings.TrimSpace(string(res.Stderr))
	reported := json.Unmarshal(lastJSONLine(res.Stderr), &report) == nil && report.Error != nil
	refusal, _, _ := strings.Cut(stderr, "\n")
	if reported {
		refusal = report.Error.Message
	}
	if unknownCommand.MatchString(refusal) {
		return fmt.Errorf("%w: it cannot run `testiny %s`. %s", ErrCLITooOld, strings.Join(args, " "), updateCLI)
	}
	if !reported {
		return fmt.Errorf("%w: testiny exited %d: %s", ErrUnavailable, res.ExitCode, stderr)
	}
	msg := report.Error.Message
	if report.Error.Code != "" {
		msg += " (" + report.Error.Code + ")"
	}
	switch {
	case report.Error.Status == 401 || report.Error.Status == 403:
		return fmt.Errorf("%w: %s", ErrAuth, msg)
	case report.Error.Status == 404:
		return fmt.Errorf("%w: %s", ErrNotFound, msg)
	case res.ExitCode == 4:
		return fmt.Errorf("%w: %s", ErrUnavailable, msg)
	default:
		return fmt.Errorf("%w: %s", ErrRejected, msg)
	}
}

// lastJSONLine is the last stderr line that looks like a JSON object: the CLI
// may print notes before its error report.
func lastJSONLine(stderr []byte) []byte {
	lines := bytes.Split(bytes.TrimSpace(stderr), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if line := bytes.TrimSpace(lines[i]); bytes.HasPrefix(line, []byte("{")) {
			return line
		}
	}
	return nil
}

func idArg(id int64) string { return strconv.FormatInt(id, 10) }

func execRunner(ctx context.Context, name string, args ...string) (Output, error) {
	var stdout, stderr bytes.Buffer
	cmd := aoprocess.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && ctx.Err() == nil {
		return Output{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: exitErr.ExitCode()}, nil
	}
	if err != nil {
		return Output{}, err
	}
	return Output{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, nil
}

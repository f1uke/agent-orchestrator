// Package testiny reads Testiny test runs and cases through the `testiny` CLI, and
// records case results in a run. Results are the only thing it writes.
//
// The CLI prints {"data":...,"meta":...} on stdout, and on failure prints
// {"error":{kind,message,code,status},"exit_code":N} on stderr with exit code
// 2 (usage), 3 (the API rejected it) or 4 (network failure). Every failure is
// mapped to one of this package's sentinels.
package testiny

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	projects   []Project
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

// Project is a Testiny project. Key is "" for a project that has none.
type Project struct {
	ID   int64
	Name string
	Key  string
}

// Run reads one test run. A run that does not exist is ErrNotFound.
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
	}
	if err := c.call(ctx, &raw, "run", "show", idArg(int64(id))); err != nil {
		return Run{}, err
	}
	run := Run{ID: domain.TestinyRunID(raw.ID), Title: raw.Title, Closed: raw.IsClosed, ProjectID: raw.ProjectID}
	if raw.TestplanID != nil {
		run.PlanID = *raw.TestplanID
	}
	if raw.Milestone != nil {
		run.MilestoneID = raw.Milestone.MilestoneID
	}
	return run, nil
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

// SetResults records results in a run. A result with a comment is sent alone,
// because only `--case` takes a `--comment` (and a comment needs the run's
// project id); the rest go in one `--result` batch. Every value is its own argv
// element, so a comment is never read as a flag. It stops at the first call
// that fails and returns what was written before it, so a caller can account
// for a partial write.
func (c *Client) SetResults(ctx context.Context, run domain.TestinyRunID, projectID int64, results []domain.TestinyResult) ([]domain.TestinyResult, error) {
	written := make([]domain.TestinyResult, 0, len(results))
	var batch []domain.TestinyResult
	for _, r := range results {
		if r.Comment == "" {
			batch = append(batch, r)
			continue
		}
		if err := c.exec(ctx, "run", "results", "set", "--run="+idArg(int64(run)), "--project-id="+idArg(projectID),
			"--case="+idArg(r.CaseID), "--status="+string(r.Status), "--comment="+r.Comment); err != nil {
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
// its rich text as plain text that keeps its structure. A case that does not
// exist is ErrNotFound.
func (c *Client) Case(ctx context.Context, id int64) (domain.TestinyCaseDetail, error) {
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

type caseStep struct {
	N        int    `json:"n"`
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

// Milestone reads a milestone's title.
func (c *Client) Milestone(ctx context.Context, id int64) (Ref, error) {
	return c.ref(ctx, "milestone", id)
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
func (c *Client) Project(ctx context.Context, ref string) (Project, error) {
	projects, err := c.listProjects(ctx)
	if err != nil {
		return Project{}, err
	}
	ref = strings.TrimSpace(ref)
	for _, match := range []func(Project) bool{
		func(p Project) bool { return p.Key != "" && strings.EqualFold(p.Key, ref) },
		func(p Project) bool { return strings.EqualFold(p.Name, ref) },
		func(p Project) bool { return strconv.FormatInt(p.ID, 10) == ref },
	} {
		for _, p := range projects {
			if match(p) {
				return p, nil
			}
		}
	}
	return Project{}, fmt.Errorf("%w: no Testiny project has the key, name or id %q", ErrNotFound, ref)
}

// ProjectByID finds a project by its id in the same cached list.
func (c *Client) ProjectByID(ctx context.Context, id int64) (Project, error) {
	return c.Project(ctx, strconv.FormatInt(id, 10))
}

func (c *Client) listProjects(ctx context.Context) ([]Project, error) {
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
	projects := make([]Project, len(raw))
	for i, r := range raw {
		projects[i] = Project{ID: r.ID, Name: r.Name}
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

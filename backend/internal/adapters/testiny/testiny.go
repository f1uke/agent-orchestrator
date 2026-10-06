// Package testiny reads Testiny test runs through the `testiny` CLI. It only
// ever runs read commands: AO never writes to Testiny itself.
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

// Client reads runs, plans, milestones and projects.
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
	bin, err := c.binary()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	res, err := c.run(ctx, bin, args...)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%w: testiny %s timed out after %s", ErrUnavailable, strings.Join(args, " "), c.callTimeout)
		}
		return fmt.Errorf("%w: run testiny %s: %w", ErrUnavailable, strings.Join(args, " "), err)
	}
	if res.ExitCode != 0 {
		return cliError(res)
	}
	if err := json.Unmarshal(res.Stdout, out); err != nil {
		return fmt.Errorf("%w: unreadable output of testiny %s: %w", ErrUnavailable, strings.Join(args, " "), err)
	}
	return nil
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

// cliError maps a failed run's error report to a sentinel.
func cliError(res Output) error {
	var report struct {
		Error *struct {
			Message string `json:"message"`
			Code    string `json:"code"`
			Status  int    `json:"status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(lastJSONLine(res.Stderr), &report); err != nil || report.Error == nil {
		return fmt.Errorf("%w: testiny exited %d: %s", ErrUnavailable, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
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

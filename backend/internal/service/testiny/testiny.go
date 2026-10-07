// Package testiny is the service behind a task's Testiny tab: which Testiny
// test runs belong to the task, what each one says right now, what a case in
// them asks for, recording a case's result in one, and uploading a run's QA
// evidence to Google Drive with each file's link posted on its case's result.
//
// AO stores the links and a log of the results and evidence it wrote. Every
// title, case and result is read live from Testiny through the adapter, held
// in memory for a few seconds, and kept as the last good read when a later
// read fails, so a Testiny outage shows as "data from 3 min ago" rather than
// an empty tab.
package testiny

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	rcloneadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/rclone"
	testinyadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/testiny"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/qaevidence"
)

const (
	// runTTL is how long one read of a run is served before Testiny is asked
	// again. A refresh skips it.
	runTTL = 15 * time.Second
	// caseTTL is how long one read of a case is served.
	caseTTL = 60 * time.Second
	// scriptsTTL is how long one scan of the case scripts is used.
	scriptsTTL = 60 * time.Second
	// readParallelism caps how many runs are read from Testiny at once.
	readParallelism = 4
)

// Errors the controller maps to a response. Testiny's own failures arrive as
// the adapter's sentinels.
var (
	ErrOff             = errors.New("this project does not use Testiny: turn it on first (ao project set-config <project> --testiny)")
	ErrSessionNotFound = errors.New("session not found")
	ErrRunNotFound     = errors.New("testiny has no such run")
	ErrWrongProject    = errors.New("the run is in another Testiny project")
	ErrProjectNotFound = errors.New("testiny has no such project")
	ErrRunNotLinked    = errors.New("the run is not linked to this task")
	ErrCaseNotInTask   = errors.New("the case is not in a run linked to this task")
	ErrWriteNotYours   = errors.New("this agent may not record results on this task")
	ErrSetByPerson     = errors.New("a person set this result")
	ErrEvidenceOff     = errors.New("QA evidence upload is off: set the Google Drive folder first, as an rclone path such as finnomena:QA (AO Settings, or PUT /api/v1/settings/qa-evidence)")
	ErrRunClosed       = errors.New("the run is closed")
	ErrDriveDuplicate  = errors.New("two files in the Google Drive folder have one name")
)

// RunReader reads Testiny. Satisfied by *adapters/testiny.Client.
type RunReader interface {
	Run(ctx context.Context, id domain.TestinyRunID) (testinyadapter.Run, error)
	Results(ctx context.Context, id domain.TestinyRunID) (testinyadapter.Results, error)
	Case(ctx context.Context, id int64) (domain.TestinyCaseDetail, error)
	Plan(ctx context.Context, id int64) (testinyadapter.Ref, error)
	Milestone(ctx context.Context, id int64) (testinyadapter.Milestone, error)
	ResultComments(ctx context.Context, run domain.TestinyRunID) ([]testinyadapter.ResultComment, error)
	Project(ctx context.Context, ref string) (domain.TestinyProject, error)
	ProjectByID(ctx context.Context, id int64) (domain.TestinyProject, error)
}

// ResultWriter records results in a run, and comments on them. Satisfied by
// *adapters/testiny.Client. SetResults returns the results written before any
// failure.
type ResultWriter interface {
	SetResults(ctx context.Context, run domain.TestinyRunID, projectID int64, results []domain.TestinyResult) ([]domain.TestinyResult, error)
	CommentOnResult(ctx context.Context, run domain.TestinyRunID, caseID, projectID int64, text string) (int64, error)
}

// Drive uploads files to a cloud folder and lists it. Satisfied by
// *adapters/rclone.Client.
type Drive interface {
	HasRemote(ctx context.Context, name string) (bool, error)
	Copy(ctx context.Context, src, dst string, names []string) ([]string, error)
	List(ctx context.Context, dir string) ([]rcloneadapter.File, error)
}

// EvidenceSettings is where evidence goes. Satisfied by *qaevidence.Store.
type EvidenceSettings interface {
	Get() qaevidence.Settings
}

// Client is everything the service asks of Testiny.
type Client interface {
	RunReader
	ResultWriter
}

// LinkStore keeps which runs belong to which task, and the log of the results
// AO wrote in them. Satisfied by the sqlite store.
type LinkStore interface {
	InsertTestinyRunLink(ctx context.Context, l domain.TestinyRunLink) error
	DeleteTestinyRunLink(ctx context.Context, sessionID domain.SessionID, runID domain.TestinyRunID) error
	ListTestinyRunLinks(ctx context.Context, sessionID domain.SessionID) ([]domain.TestinyRunLink, error)
	FillTestinyRunLinkProject(ctx context.Context, runID domain.TestinyRunID, p domain.TestinyProject) error
	AppendTestinyResults(ctx context.Context, entries []domain.TestinyResultEntry) error
	LatestTestinyResults(ctx context.Context, sessionID domain.SessionID, runID domain.TestinyRunID) ([]domain.TestinyResultEntry, error)
	TestinyStepResultLog(ctx context.Context, sessionID domain.SessionID, runID domain.TestinyRunID) ([]domain.TestinyResultEntry, error)
	AppendTestinyEvidence(ctx context.Context, entries []domain.TestinyEvidenceEntry) error
	TestinyEvidenceLinked(ctx context.Context, runID domain.TestinyRunID) ([]domain.TestinyEvidenceEntry, error)
}

// SessionGateway finds a task's project, its settings and its crew. Satisfied
// by the sqlite store.
type SessionGateway interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
}

// Options configures a Service. Zero fields take the real values.
type Options struct {
	// Home is the user's home directory: the QA Evidence tree is under its
	// Desktop, and a scripts store spelled with ~ is under it.
	Home string
	Now  func() time.Time
	// Drive and Evidence upload a run's evidence; without both, upload is off.
	Drive    Drive
	Evidence EvidenceSettings
	// Zone is the time zone a milestone's year is read in.
	Zone *time.Location
}

// Service links runs to tasks and reads them.
type Service struct {
	reader   Client
	links    LinkStore
	sessions SessionGateway
	home     string
	now      func() time.Time
	drive    Drive
	evidence EvidenceSettings
	zone     *time.Location

	mu      sync.Mutex
	reads   map[domain.TestinyRunID]domain.TestinyRunView
	cases   map[int64]caseRead
	scripts map[string]scriptIndex // by the cases folder scanned

	// writeMu holds the overwrite guard's read of Testiny and the write it
	// allows together, so a write in between cannot slip past the guard.
	writeMu sync.Mutex
}

// New builds a Service.
func New(reader Client, links LinkStore, sessions SessionGateway, opts Options) *Service {
	s := &Service{
		reader: reader, links: links, sessions: sessions, home: opts.Home, now: opts.Now,
		drive: opts.Drive, evidence: opts.Evidence, zone: opts.Zone,
		reads:   map[domain.TestinyRunID]domain.TestinyRunView{},
		cases:   map[int64]caseRead{},
		scripts: map[string]scriptIndex{},
	}
	if s.home == "" {
		s.home, _ = os.UserHomeDir()
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.zone == nil {
		s.zone = time.Local
	}
	return s
}

// Link links a run to a task. ref is the run's id (632 or TR-632) or its URL.
// Run ids are global in Testiny, so the run names its own Testiny project, and
// the link stores it: one task may hold runs from several projects. project,
// when given (a key, name or id), and a URL's key are typo guards: each must
// name the run's own project. The run must exist in Testiny, so a typo never
// sits in the list. by is the session id of the agent linking it, or "" for a
// person. Linking a run twice keeps the first link.
func (s *Service) Link(ctx context.Context, task domain.SessionID, ref, project, by string) (domain.TestinyRunView, error) {
	cfg, err := s.config(ctx, task)
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	id, urlKey, err := domain.ParseTestinyRunRef(ref)
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	var want domain.TestinyProject
	if project != "" {
		if want, err = s.project(ctx, project); err != nil {
			return domain.TestinyRunView{}, err
		}
	}
	run, err := s.reader.Run(ctx, id)
	if errors.Is(err, testinyadapter.ErrNotFound) {
		return domain.TestinyRunView{}, fmt.Errorf("%w: %s", ErrRunNotFound, id)
	}
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	in, err := s.reader.ProjectByID(ctx, run.ProjectID)
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	if urlKey != "" && !strings.EqualFold(urlKey, in.Key) {
		return domain.TestinyRunView{}, fmt.Errorf("%w: %s is in %s, not %s", ErrWrongProject, id, in.Label(), urlKey)
	}
	if project != "" && want.ID != in.ID {
		return domain.TestinyRunView{}, fmt.Errorf("%w: %s is in %s, not %s", ErrWrongProject, id, in.Label(), want.Label())
	}

	if err := s.links.InsertTestinyRunLink(ctx, domain.TestinyRunLink{SessionID: task, RunID: id, Project: in, LinkedBy: by, CreatedAt: s.now().UTC()}); err != nil {
		return domain.TestinyRunView{}, err
	}
	l, err := s.linkOf(ctx, task, id)
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	fresh, err := s.fetch(ctx, run)
	s.remember(id, fresh, err)
	return s.viewOf(ctx, l, cfg)
}

// viewOf is the remembered read of a link's run with AO's own facts filled in.
func (s *Service) viewOf(ctx context.Context, l domain.TestinyRunLink, cfg domain.ProjectConfig) (domain.TestinyRunView, error) {
	records, err := s.records(ctx, l)
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	files, err := s.evidenceFiles(ctx, l.RunID)
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	return s.view(l, s.caseScripts(cfg), records, files), nil
}

// Unlink removes a run from a task. A run that is not linked is not an error.
func (s *Service) Unlink(ctx context.Context, task domain.SessionID, id domain.TestinyRunID) error {
	return s.links.DeleteTestinyRunLink(ctx, task, id)
}

// Runs reads every run linked to a task, in the order they were linked. A run
// read in the last 15 s is served from memory unless refresh is set. A run
// Testiny cannot read right now carries a FetchError, with its last good read
// when there is one.
func (s *Service) Runs(ctx context.Context, task domain.SessionID, refresh bool) ([]domain.TestinyRunView, error) {
	cfg, err := s.config(ctx, task)
	if err != nil {
		return nil, err
	}
	links, err := s.taskLinks(ctx, task)
	if err != nil {
		return nil, err
	}
	scripts := s.caseScripts(cfg)

	records := make([]map[int64]*domain.TestinyResultRecord, len(links))
	files := make([]map[string]string, len(links))
	for i, l := range links {
		if records[i], err = s.records(ctx, l); err != nil {
			return nil, err
		}
		if files[i], err = s.evidenceFiles(ctx, l.RunID); err != nil {
			return nil, err
		}
	}

	views := make([]domain.TestinyRunView, len(links))
	slots := make(chan struct{}, readParallelism)
	var wg sync.WaitGroup
	for i, l := range links {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			if refresh || !s.fresh(l.RunID) {
				fresh, err := s.fetchByID(ctx, l.RunID)
				s.remember(l.RunID, fresh, err)
			}
			views[i] = s.view(l, scripts, records[i], files[i])
		}()
	}
	wg.Wait()
	return views, nil
}

// taskLinks is the task's links in the order they were linked, each with its
// run's Testiny project. A link made before AO stored the project gets it from
// Testiny here, and keeps it; when Testiny cannot say right now, the link is
// returned without it and the next read tries again.
func (s *Service) taskLinks(ctx context.Context, task domain.SessionID) ([]domain.TestinyRunLink, error) {
	links, err := s.links.ListTestinyRunLinks(ctx, task)
	if err != nil {
		return nil, err
	}
	for i, l := range links {
		if l.Project.ID != 0 {
			continue
		}
		run, err := s.reader.Run(ctx, l.RunID)
		if err != nil {
			continue
		}
		p, err := s.reader.ProjectByID(ctx, run.ProjectID)
		if err != nil {
			continue
		}
		if err := s.links.FillTestinyRunLinkProject(ctx, l.RunID, p); err != nil {
			return nil, err
		}
		links[i].Project = p
	}
	return links, nil
}

func (s *Service) config(ctx context.Context, task domain.SessionID) (domain.ProjectConfig, error) {
	rec, ok, err := s.sessions.GetSession(ctx, task)
	if err != nil {
		return domain.ProjectConfig{}, err
	}
	if !ok {
		return domain.ProjectConfig{}, fmt.Errorf("%w: %s", ErrSessionNotFound, task)
	}
	proj, ok, err := s.sessions.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return domain.ProjectConfig{}, err
	}
	if !ok || !proj.Config.UsesTestiny {
		return domain.ProjectConfig{}, ErrOff
	}
	return proj.Config, nil
}

// project resolves a Testiny project by its key, name or id.
func (s *Service) project(ctx context.Context, ref string) (domain.TestinyProject, error) {
	p, err := s.reader.Project(ctx, ref)
	if errors.Is(err, testinyadapter.ErrNotFound) {
		return p, fmt.Errorf("%w: %q (testiny project ls lists every project with its key)", ErrProjectNotFound, ref)
	}
	return p, err
}

// fetchByID reads a run in full. The run, its results and the comments on
// them are asked for at the same time; its plan, milestone and project need
// the run's ids.
func (s *Service) fetchByID(ctx context.Context, id domain.TestinyRunID) (domain.TestinyRunView, error) {
	var (
		run testinyadapter.Run
		g   errgroup.Group
	)
	g.Go(func() (err error) {
		run, err = s.reader.Run(ctx, id)
		return err
	})
	res, comments, err := s.resultsAndComments(ctx, id)
	if gErr := g.Wait(); gErr != nil {
		return domain.TestinyRunView{}, gErr
	}
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	return s.complete(ctx, run, res, comments)
}

// fetch reads the rest of a run whose own fields are already in hand.
func (s *Service) fetch(ctx context.Context, run testinyadapter.Run) (domain.TestinyRunView, error) {
	res, comments, err := s.resultsAndComments(ctx, run.ID)
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	return s.complete(ctx, run, res, comments)
}

// resultsAndComments reads a run's results and the comments on them at the
// same time.
func (s *Service) resultsAndComments(ctx context.Context, id domain.TestinyRunID) (testinyadapter.Results, []testinyadapter.ResultComment, error) {
	var (
		res      testinyadapter.Results
		comments []testinyadapter.ResultComment
		g        errgroup.Group
	)
	g.Go(func() (err error) {
		res, err = s.reader.Results(ctx, id)
		return err
	})
	g.Go(func() (err error) {
		comments, err = s.reader.ResultComments(ctx, id)
		return err
	})
	err := g.Wait()
	return res, comments, err
}

func (s *Service) complete(ctx context.Context, run testinyadapter.Run, res testinyadapter.Results, comments []testinyadapter.ResultComment) (domain.TestinyRunView, error) {
	var (
		plan, milestone *domain.TestinyRef
		project         domain.TestinyProject
		mu              sync.Mutex
		firstErr        error
		wg              sync.WaitGroup
	)
	read := func(f func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f(); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	if run.PlanID != 0 {
		read(func() error {
			ref, err := s.reader.Plan(ctx, run.PlanID)
			plan = &domain.TestinyRef{ID: ref.ID, Title: ref.Title}
			return err
		})
	}
	if run.MilestoneID != 0 {
		read(func() error {
			m, err := s.reader.Milestone(ctx, run.MilestoneID)
			milestone = &domain.TestinyRef{ID: m.ID, Title: m.Title}
			return err
		})
	}
	read(func() error {
		var err error
		project, err = s.reader.ProjectByID(ctx, run.ProjectID)
		return err
	})
	wg.Wait()
	if firstErr != nil {
		return domain.TestinyRunView{}, firstErr
	}

	at := s.now().UTC()
	v := domain.TestinyRunView{
		Title:     run.Title,
		URL:       domain.TestinyRunURL(project.Key, run.ID),
		Closed:    run.Closed,
		Plan:      plan,
		Milestone: milestone,
		Counts:    make(map[domain.TestinyCaseStatus]int, len(res.Summary)),
		Cases:     make([]domain.TestinyCaseResult, len(res.Cases)),
		FetchedAt: &at,
	}
	for status, n := range res.Summary {
		v.Counts[domain.TestinyCaseStatus(status)] = n
	}
	evidence := driveLinks(comments)
	for i, c := range res.Cases {
		steps := run.Steps[c.ID]
		if steps == nil {
			steps = []domain.TestinyRunStep{}
		}
		links := evidence[c.ID]
		if links == nil {
			links = []domain.TestinyEvidenceLink{}
		}
		v.Cases[i] = domain.TestinyCaseResult{ID: c.ID, Title: c.Title, Status: domain.TestinyCaseStatus(c.Status), Steps: steps, Evidence: links}
	}
	return v, nil
}

func (s *Service) fresh(id domain.TestinyRunID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.reads[id]
	return ok && v.FetchError == nil && s.now().Sub(*v.FetchedAt) < runTTL
}

// remember keeps a good read, or marks the last good read (or an empty one)
// with why the latest read failed.
func (s *Service) remember(id domain.TestinyRunID, v domain.TestinyRunView, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		s.reads[id] = v
		return
	}
	last, ok := s.reads[id]
	if !ok {
		last = domain.TestinyRunView{Counts: map[domain.TestinyCaseStatus]int{}, Cases: []domain.TestinyCaseResult{}}
	}
	last.FetchError = &domain.TestinyFetchError{Kind: fetchErrorKind(err), Message: err.Error()}
	s.reads[id] = last
}

// view is the remembered read of a link's run, with what is not Testiny's to
// say filled in: the link itself, the evidence folder, the case scripts, the
// results AO recorded, and the file each evidence link AO posted names
// (files, by Drive id).
func (s *Service) view(l domain.TestinyRunLink, scripts scriptsByCase, records map[int64]*domain.TestinyResultRecord, files map[string]string) domain.TestinyRunView {
	s.mu.Lock()
	v := s.reads[l.RunID]
	s.mu.Unlock()
	v.Link = l
	v.EvidenceDir = s.evidenceDir(l.RunID)
	cases := make([]domain.TestinyCaseResult, len(v.Cases))
	for i, c := range v.Cases {
		c.Script = scripts.of(l.Project.Key, c.ID)
		c.Recorded = records[c.ID]
		if len(c.Evidence) > 0 {
			links := make([]domain.TestinyEvidenceLink, len(c.Evidence))
			for j, link := range c.Evidence {
				link.File = files[link.DriveID]
				links[j] = link
			}
			c.Evidence = links
		}
		cases[i] = c
	}
	v.Cases = cases
	return v
}

func fetchErrorKind(err error) domain.TestinyFetchErrorKind {
	switch {
	case errors.Is(err, testinyadapter.ErrAuth):
		return domain.TestinyErrAuth
	case errors.Is(err, testinyadapter.ErrNotFound):
		return domain.TestinyErrNotFound
	case errors.Is(err, testinyadapter.ErrRejected):
		return domain.TestinyErrRejected
	case errors.Is(err, testinyadapter.ErrBinaryMissing):
		return domain.TestinyErrBinaryMissing
	case errors.Is(err, testinyadapter.ErrCLITooOld):
		return domain.TestinyErrCLITooOld
	default:
		return domain.TestinyErrUnavailable
	}
}

// evidenceDir is the run's folder in the QA Evidence tree the Testiny skill
// writes: ~/Desktop/QA Evidence/<Project>/<YYYY>/<milestone>/<TP-…>/TR-<id> - <title>.
// Only the "TR-<id> - " prefix is matched, so the skill's other naming rules
// are not restated here.
func (s *Service) evidenceDir(id domain.TestinyRunID) string {
	if dirs := s.evidenceDirs(id); len(dirs) > 0 {
		return dirs[0]
	}
	return ""
}

// evidenceDirs is every folder in the QA Evidence tree that is named as the
// run's folder, wherever in the tree it sits.
func (s *Service) evidenceDirs(id domain.TestinyRunID) []string {
	matches, _ := filepath.Glob(filepath.Join(s.evidenceRoot(), "*", "*", "*", "*", id.String()+" - *"))
	var dirs []string
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.IsDir() {
			dirs = append(dirs, m)
		}
	}
	return dirs
}

func (s *Service) evidenceRoot() string {
	return filepath.Join(s.home, "Desktop", "QA Evidence")
}

// caseRef names a Testiny case in a case script's header: the upper-cased key
// of its Testiny project and its id. A case id alone is not enough, since one
// store holds the scripts of every Testiny project.
type caseRef struct {
	projectKey string
	id         int64
}

// scriptsByCase maps a case to the store-relative path of the Maestro case
// script that plays it.
type scriptsByCase map[caseRef]string

// of is the script that plays a case of a run in the project. A project with
// no key has no scripts.
func (c scriptsByCase) of(projectKey string, id int64) string {
	if projectKey == "" {
		return ""
	}
	return c[caseRef{projectKey: strings.ToUpper(projectKey), id: id}]
}

type scriptIndex struct {
	at      time.Time
	scripts scriptsByCase
}

// caseScriptHeader is the line a case script carries for each Testiny case it
// plays: "# testiny: <project_key> TC-<id>".
var caseScriptHeader = regexp.MustCompile(`^\s*#\s*testiny:\s*(\S+)\s+TC-(\d+)\s*$`)

// caseScripts finds the Maestro case script of every case, in every Testiny
// project, on a project whose devices are driven by scripts. The scripts are
// the source of truth (INDEX.md can lag behind them). A scan of a store is
// used for a minute.
func (s *Service) caseScripts(cfg domain.ProjectConfig) scriptsByCase {
	if cfg.MobileScripts == nil {
		return nil
	}
	store := expandHome(cfg.MobileScripts.StoreOrDefault(), s.home)
	dir := filepath.Join(store, "projects", cfg.MobileScripts.Product, "cases")

	s.mu.Lock()
	idx, ok := s.scripts[dir]
	s.mu.Unlock()
	if ok && s.now().Sub(idx.at) < scriptsTTL {
		return idx.scripts
	}
	idx = scriptIndex{at: s.now(), scripts: scanCaseScripts(store, dir)}
	s.mu.Lock()
	s.scripts[dir] = idx
	s.mu.Unlock()
	return idx.scripts
}

// scanCaseScripts reads every .yaml under dir through a root-scoped view of
// it, so a symlink in the store cannot lead the scan outside it. Paths in the
// result are relative to the store. A file that cannot be read is skipped.
func scanCaseScripts(store, dir string) scriptsByCase {
	scripts := scriptsByCase{}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return scripts
	}
	defer func() { _ = root.Close() }()
	prefix, err := filepath.Rel(store, dir)
	if err != nil {
		return scripts
	}
	fsys := root.FS()
	_ = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr == nil && !d.IsDir() && filepath.Ext(path) == ".yaml" {
			for _, ref := range headerCases(fsys, path) {
				if _, taken := scripts[ref]; !taken {
					scripts[ref] = filepath.ToSlash(filepath.Join(prefix, path))
				}
			}
		}
		return nil
	})
	return scripts
}

// headerCases is the Testiny cases a script's header lines name.
func headerCases(fsys fs.FS, path string) []caseRef {
	body, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil
	}
	var refs []caseRef
	lines := bufio.NewScanner(bytes.NewReader(body))
	for lines.Scan() {
		m := caseScriptHeader.FindStringSubmatch(lines.Text())
		if len(m) != 3 {
			continue
		}
		if id, err := strconv.ParseInt(m[2], 10, 64); err == nil {
			refs = append(refs, caseRef{projectKey: strings.ToUpper(m[1]), id: id})
		}
	}
	return refs
}

func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return path
}

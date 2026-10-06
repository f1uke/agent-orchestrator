// Package testiny is the service behind a task's Testiny tab: which Testiny
// test runs belong to the task, and what each one says right now.
//
// AO stores only the links. Every title, case and result is read live from
// Testiny through the read-only adapter, held in memory for a few seconds, and
// kept as the last good read when a later read fails, so a Testiny outage shows
// as "data from 3 min ago" rather than an empty tab.
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

	testinyadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/testiny"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// runTTL is how long one read of a run is served before Testiny is asked
	// again. A refresh skips it.
	runTTL = 15 * time.Second
	// scriptsTTL is how long one scan of the case scripts is used.
	scriptsTTL = 60 * time.Second
	// readParallelism caps how many runs are read from Testiny at once.
	readParallelism = 4
)

// Errors the controller maps to a response. Testiny's own failures arrive as
// the adapter's sentinels.
var (
	ErrOff             = errors.New("this project does not use Testiny: set its Testiny project first (ao project set-config <project> --testiny-project <key>)")
	ErrSessionNotFound = errors.New("session not found")
	ErrRunNotFound     = errors.New("testiny has no such run")
	ErrWrongProject    = errors.New("the run is in another Testiny project")
	ErrProjectNotFound = errors.New("testiny has no such project")
)

// RunReader reads Testiny. Satisfied by *adapters/testiny.Client.
type RunReader interface {
	Run(ctx context.Context, id domain.TestinyRunID) (testinyadapter.Run, error)
	Results(ctx context.Context, id domain.TestinyRunID) (testinyadapter.Results, error)
	Plan(ctx context.Context, id int64) (testinyadapter.Ref, error)
	Milestone(ctx context.Context, id int64) (testinyadapter.Ref, error)
	Project(ctx context.Context, ref string) (testinyadapter.Project, error)
	ProjectByID(ctx context.Context, id int64) (testinyadapter.Project, error)
}

// LinkStore keeps which runs belong to which task. Satisfied by the sqlite store.
type LinkStore interface {
	InsertTestinyRunLink(ctx context.Context, l domain.TestinyRunLink) error
	DeleteTestinyRunLink(ctx context.Context, sessionID domain.SessionID, runID domain.TestinyRunID) error
	ListTestinyRunLinks(ctx context.Context, sessionID domain.SessionID) ([]domain.TestinyRunLink, error)
}

// SessionGateway finds a task's project and its settings. Satisfied by the
// sqlite store.
type SessionGateway interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// Options configures a Service. Zero fields take the real values.
type Options struct {
	// Home is the user's home directory: the QA Evidence tree is under its
	// Desktop, and a scripts store spelled with ~ is under it.
	Home string
	Now  func() time.Time
}

// Runs is the Testiny tab of one task.
type Runs struct {
	// Project is the project's Testiny setting, as the person typed it.
	Project string
	Runs    []domain.TestinyRunView
}

// Service links runs to tasks and reads them.
type Service struct {
	reader   RunReader
	links    LinkStore
	sessions SessionGateway
	home     string
	now      func() time.Time

	mu      sync.Mutex
	reads   map[domain.TestinyRunID]domain.TestinyRunView
	scripts map[scriptsKey]scriptIndex
}

// New builds a Service.
func New(reader RunReader, links LinkStore, sessions SessionGateway, opts Options) *Service {
	s := &Service{
		reader: reader, links: links, sessions: sessions, home: opts.Home, now: opts.Now,
		reads:   map[domain.TestinyRunID]domain.TestinyRunView{},
		scripts: map[scriptsKey]scriptIndex{},
	}
	if s.home == "" {
		s.home, _ = os.UserHomeDir()
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// Link links a run to a task. The run must exist in Testiny and belong to the
// project's Testiny project, so a typo never sits in the list. by is the
// session id of the agent linking it, or "" for a person. Linking a run twice
// keeps the first link.
func (s *Service) Link(ctx context.Context, task domain.SessionID, ref, by string) (domain.TestinyRunView, error) {
	cfg, err := s.config(ctx, task)
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	id, urlKey, err := domain.ParseTestinyRunRef(ref)
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	run, err := s.reader.Run(ctx, id)
	if errors.Is(err, testinyadapter.ErrNotFound) {
		return domain.TestinyRunView{}, fmt.Errorf("%w: %s", ErrRunNotFound, id)
	}
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	want, err := s.project(ctx, cfg.TestinyProject)
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	if run.ProjectID != want.ID {
		return domain.TestinyRunView{}, fmt.Errorf("%w: %s belongs to %s; this project uses %s",
			ErrWrongProject, id, s.projectLabel(ctx, run.ProjectID), label(want))
	}
	if urlKey != "" && !strings.EqualFold(urlKey, want.Key) {
		return domain.TestinyRunView{}, fmt.Errorf("%w: the URL names project %s; this project uses %s",
			ErrWrongProject, urlKey, label(want))
	}

	if err := s.links.InsertTestinyRunLink(ctx, domain.TestinyRunLink{SessionID: task, RunID: id, LinkedBy: by, CreatedAt: s.now().UTC()}); err != nil {
		return domain.TestinyRunView{}, err
	}
	links, err := s.links.ListTestinyRunLinks(ctx, task)
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	for _, l := range links {
		if l.RunID == id {
			fresh, err := s.fetch(ctx, run)
			s.remember(id, fresh, err)
			return s.view(l, s.caseScripts(ctx, cfg)), nil
		}
	}
	return domain.TestinyRunView{}, fmt.Errorf("link %s to %s: not stored", id, task)
}

// Unlink removes a run from a task. A run that is not linked is not an error.
func (s *Service) Unlink(ctx context.Context, task domain.SessionID, id domain.TestinyRunID) error {
	return s.links.DeleteTestinyRunLink(ctx, task, id)
}

// Runs reads every run linked to a task, in the order they were linked. A run
// read in the last 15 s is served from memory unless refresh is set. A run
// Testiny cannot read right now carries a FetchError, with its last good read
// when there is one.
func (s *Service) Runs(ctx context.Context, task domain.SessionID, refresh bool) (Runs, error) {
	cfg, err := s.config(ctx, task)
	if err != nil {
		return Runs{}, err
	}
	links, err := s.links.ListTestinyRunLinks(ctx, task)
	if err != nil {
		return Runs{}, err
	}
	scripts := s.caseScripts(ctx, cfg)

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
			views[i] = s.view(l, scripts)
		}()
	}
	wg.Wait()
	return Runs{Project: cfg.TestinyProject, Runs: views}, nil
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
	if !ok || proj.Config.TestinyProject == "" {
		return domain.ProjectConfig{}, ErrOff
	}
	return proj.Config, nil
}

func (s *Service) project(ctx context.Context, ref string) (testinyadapter.Project, error) {
	p, err := s.reader.Project(ctx, ref)
	if errors.Is(err, testinyadapter.ErrNotFound) {
		return p, fmt.Errorf("%w: %q (check the project's Testiny setting)", ErrProjectNotFound, ref)
	}
	return p, err
}

// projectLabel names a project for a message, and falls back to its id when
// it cannot be read.
func (s *Service) projectLabel(ctx context.Context, id int64) string {
	if p, err := s.reader.ProjectByID(ctx, id); err == nil {
		return label(p)
	}
	return "project " + strconv.FormatInt(id, 10)
}

func label(p testinyadapter.Project) string {
	if p.Key != "" {
		return p.Key
	}
	return p.Name
}

// fetchByID reads a run in full. The run and its results are asked for at the
// same time; its plan, milestone and project need the run's ids.
func (s *Service) fetchByID(ctx context.Context, id domain.TestinyRunID) (domain.TestinyRunView, error) {
	var (
		run    testinyadapter.Run
		runErr error
		wg     sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		run, runErr = s.reader.Run(ctx, id)
	}()
	res, resErr := s.reader.Results(ctx, id)
	wg.Wait()
	if runErr != nil {
		return domain.TestinyRunView{}, runErr
	}
	if resErr != nil {
		return domain.TestinyRunView{}, resErr
	}
	return s.complete(ctx, run, res)
}

// fetch reads the rest of a run whose own fields are already in hand.
func (s *Service) fetch(ctx context.Context, run testinyadapter.Run) (domain.TestinyRunView, error) {
	res, err := s.reader.Results(ctx, run.ID)
	if err != nil {
		return domain.TestinyRunView{}, err
	}
	return s.complete(ctx, run, res)
}

func (s *Service) complete(ctx context.Context, run testinyadapter.Run, res testinyadapter.Results) (domain.TestinyRunView, error) {
	var (
		plan, milestone *domain.TestinyRef
		project         testinyadapter.Project
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
			ref, err := s.reader.Milestone(ctx, run.MilestoneID)
			milestone = &domain.TestinyRef{ID: ref.ID, Title: ref.Title}
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
	for i, c := range res.Cases {
		v.Cases[i] = domain.TestinyCaseResult{ID: c.ID, Title: c.Title, Status: domain.TestinyCaseStatus(c.Status)}
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
// say filled in: the link itself, the evidence folder and the case scripts.
func (s *Service) view(l domain.TestinyRunLink, scripts map[int64]string) domain.TestinyRunView {
	s.mu.Lock()
	v := s.reads[l.RunID]
	s.mu.Unlock()
	v.Link = l
	v.EvidenceDir = s.evidenceDir(l.RunID)
	cases := make([]domain.TestinyCaseResult, len(v.Cases))
	for i, c := range v.Cases {
		c.Script = scripts[c.ID]
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
	default:
		return domain.TestinyErrUnavailable
	}
}

// evidenceDir is the run's folder in the QA Evidence tree the Testiny skill
// writes: ~/Desktop/QA Evidence/<Project>/<YYYY>/<milestone>/<TP-…>/TR-<id> - <title>.
// Only the "TR-<id> - " prefix is matched, so the skill's other naming rules
// are not restated here.
func (s *Service) evidenceDir(id domain.TestinyRunID) string {
	pattern := filepath.Join(s.home, "Desktop", "QA Evidence", "*", "*", "*", "*", id.String()+" - *")
	matches, _ := filepath.Glob(pattern)
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.IsDir() {
			return m
		}
	}
	return ""
}

type scriptsKey struct{ dir, projectKey string }

type scriptIndex struct {
	at     time.Time
	byCase map[int64]string
}

// caseScriptHeader is the line a case script carries for each Testiny case it
// plays: "# testiny: <project_key> TC-<id>".
var caseScriptHeader = regexp.MustCompile(`^\s*#\s*testiny:\s*(\S+)\s+TC-(\d+)\s*$`)

// caseScripts maps a case id to the store-relative path of the Maestro case
// script that plays it, on a project whose devices are driven by scripts. The
// scripts are the source of truth (INDEX.md can lag behind them). A scan is
// used for a minute.
func (s *Service) caseScripts(ctx context.Context, cfg domain.ProjectConfig) map[int64]string {
	if cfg.MobileScripts == nil {
		return nil
	}
	proj, err := s.reader.Project(ctx, cfg.TestinyProject)
	if err != nil || proj.Key == "" {
		return nil
	}
	store := expandHome(cfg.MobileScripts.StoreOrDefault(), s.home)
	key := scriptsKey{dir: filepath.Join(store, "projects", cfg.MobileScripts.Product, "cases"), projectKey: proj.Key}

	s.mu.Lock()
	idx, ok := s.scripts[key]
	s.mu.Unlock()
	if ok && s.now().Sub(idx.at) < scriptsTTL {
		return idx.byCase
	}
	idx = scriptIndex{at: s.now(), byCase: scanCaseScripts(store, key.dir, proj.Key)}
	s.mu.Lock()
	s.scripts[key] = idx
	s.mu.Unlock()
	return idx.byCase
}

// scanCaseScripts reads every .yaml under dir through a root-scoped view of
// it, so a symlink in the store cannot lead the scan outside it. Paths in the
// result are relative to the store. A file that cannot be read is skipped.
func scanCaseScripts(store, dir, projectKey string) map[int64]string {
	byCase := map[int64]string{}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return byCase
	}
	defer func() { _ = root.Close() }()
	prefix, err := filepath.Rel(store, dir)
	if err != nil {
		return byCase
	}
	fsys := root.FS()
	_ = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr == nil && !d.IsDir() && filepath.Ext(path) == ".yaml" {
			for _, id := range headerCaseIDs(fsys, path, projectKey) {
				if _, taken := byCase[id]; !taken {
					byCase[id] = filepath.ToSlash(filepath.Join(prefix, path))
				}
			}
		}
		return nil
	})
	return byCase
}

// headerCaseIDs is the Testiny case ids a script's header lines name for the
// project.
func headerCaseIDs(fsys fs.FS, path, projectKey string) []int64 {
	body, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil
	}
	var ids []int64
	lines := bufio.NewScanner(bytes.NewReader(body))
	for lines.Scan() {
		m := caseScriptHeader.FindStringSubmatch(lines.Text())
		if len(m) != 3 || !strings.EqualFold(m[1], projectKey) {
			continue
		}
		if id, err := strconv.ParseInt(m[2], 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
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

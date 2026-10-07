// Package scriptstore owns each workspace's own git worktree of the project's
// mobile scripts store: cutting it at spawn, re-attaching it at restore,
// publishing its commits into the store's main checkout, keeping it at a
// teardown that would lose work, and settling what a crash left behind.
//
// Every session used to write scripts into the one shared checkout, where
// several tasks' uncommitted files piled up with no owner. Now each task has
// its own branch (ao/<owner>), so every file has an owner, and the only write
// into the shared checkout is a publish this package serializes per store.
//
// The rule every path keeps: work is never lost silently. A worktree is removed
// only once its commits are in the store and nothing is left uncommitted, or
// when somebody asked for a discard.
package scriptstore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrNoWorktree means the session's workspace has no store worktree: its
// project has no mobileScripts, the store could not have one, or teardown
// already removed it.
var ErrNoWorktree = errors.New("scriptstore: this session's workspace has no scripts store worktree")

// Store is the persistence the service needs.
type Store interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	GetScriptsStoreWorktree(ctx context.Context, owner domain.SessionID) (domain.ScriptsStoreWorktree, bool, error)
	UpsertScriptsStoreWorktree(ctx context.Context, w domain.ScriptsStoreWorktree) error
	ListScriptsStoreWorktreesInStates(ctx context.Context, states ...domain.ScriptsStoreState) ([]domain.ScriptsStoreWorktree, error)
}

// Layout is where an owner's store worktree goes and the branch it is on. It is
// pure so a prompt can name the worktree before it exists.
func Layout(dataDir, store string, owner domain.SessionID) (path, branch string) {
	return filepath.Join(dataDir, "store-worktrees", filepath.Base(filepath.Clean(store)), string(owner)), "ao/" + string(owner)
}

// Root is the absolute path of a project's store main checkout: the configured
// store, or the default, with a leading ~ expanded.
func Root(cfg domain.MobileScriptsConfig) string {
	p := cfg.StoreOrDefault()
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// Options configures a Service.
type Options struct {
	Store   Store
	Trees   ports.ScriptsTrees
	DataDir string
	Now     func() time.Time
	Logger  *slog.Logger
}

// Service implements the store worktree lifecycle.
type Service struct {
	store   Store
	trees   ports.ScriptsTrees
	dataDir string
	now     func() time.Time
	log     *slog.Logger

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// New builds a Service.
func New(opts Options) *Service {
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Service{store: opts.Store, trees: opts.Trees, dataDir: opts.DataDir, now: now, log: log, locks: map[string]*sync.Mutex{}}
}

// lock serializes everything that writes into one store or reads-then-writes a
// row of it: two publishes into one main checkout run one after the other, and
// a refresh never writes back a row a teardown just changed.
func (s *Service) lock(store string) func() {
	s.mu.Lock()
	l, ok := s.locks[store]
	if !ok {
		l = &sync.Mutex{}
		s.locks[store] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// Probe reads whether a store can have worktrees cut from it.
func (s *Service) Probe(ctx context.Context, root string) (ports.ScriptsStoreProbe, error) {
	return s.trees.Probe(ctx, root)
}

// Get reads an owner's worktree while it exists (active or held).
func (s *Service) Get(ctx context.Context, owner domain.SessionID) (domain.ScriptsStoreWorktree, bool, error) {
	w, ok, err := s.store.GetScriptsStoreWorktree(ctx, owner)
	if err != nil || !ok || !w.State.Live() {
		return domain.ScriptsStoreWorktree{}, false, err
	}
	return w, true, nil
}

// Ensure makes the owner's worktree exist and be active: cut from base in root
// the first time, re-attached (repaired, or recreated from its kept branch)
// afterwards. Spawn and restore both call it; running it again changes
// nothing.
func (s *Service) Ensure(ctx context.Context, project domain.ProjectID, owner domain.SessionID, root, base string) (domain.ScriptsStoreWorktree, error) {
	existing, ok, err := s.Get(ctx, owner)
	if err != nil {
		return domain.ScriptsStoreWorktree{}, err
	}
	w := existing
	if !ok {
		path, branch := Layout(s.dataDir, root, owner)
		now := s.now()
		w = domain.ScriptsStoreWorktree{
			SessionID: owner, ProjectID: project, Store: root, Path: path, Branch: branch, BaseBranch: base,
			CreatedAt: now,
		}
	}
	unlock := s.lock(w.Store)
	defer unlock()
	if err := s.trees.Ensure(ctx, w.Store, w.Path, w.Branch, w.BaseBranch); err != nil {
		return domain.ScriptsStoreWorktree{}, fmt.Errorf("scriptstore: worktree for %s: %w", owner, err)
	}
	w.State, w.HeldReason, w.HeldFiles = domain.ScriptsStoreActive, "", nil
	if st, err := s.trees.Status(ctx, w.Path, w.Branch, w.BaseBranch); err == nil {
		w.Uncommitted, w.Unpublished = st.Uncommitted, st.Unpublished
	}
	w.UpdatedAt = s.now()
	if err := s.store.UpsertScriptsStoreWorktree(ctx, w); err != nil {
		return domain.ScriptsStoreWorktree{}, err
	}
	return w, nil
}

// Published is the result of a publish: the worktree as it is afterwards and
// what the publish did.
type Published struct {
	Worktree domain.ScriptsStoreWorktree
	Result   ports.ScriptsPublishResult
}

// Publish merges the owner's committed scripts into the store's main checkout.
// Uncommitted files stay where they are: only commits publish.
func (s *Service) Publish(ctx context.Context, owner domain.SessionID) (Published, error) {
	w, ok, err := s.Get(ctx, owner)
	if err != nil {
		return Published{}, err
	}
	if !ok {
		return Published{}, ErrNoWorktree
	}
	unlock := s.lock(w.Store)
	defer unlock()
	res, err := s.publishLocked(ctx, &w)
	if err != nil {
		return Published{}, err
	}
	s.readFacts(ctx, &w)
	if err := s.save(ctx, w); err != nil {
		return Published{}, err
	}
	return Published{Worktree: w, Result: res}, nil
}

func (s *Service) publishLocked(ctx context.Context, w *domain.ScriptsStoreWorktree) (ports.ScriptsPublishResult, error) {
	res, err := s.trees.Publish(ctx, w.Store, w.Branch, w.BaseBranch, "Merge scripts from "+string(w.SessionID))
	if err != nil {
		return ports.ScriptsPublishResult{}, fmt.Errorf("scriptstore: publish %s: %w", w.SessionID, err)
	}
	if res.Outcome == ports.PublishFastForward || res.Outcome == ports.PublishMerged {
		w.PublishedAt = s.now()
	}
	return res, nil
}

// Status is a worktree with its git facts read now, and the store's main
// checkout's own uncommitted files (which a publish cannot touch).
type Status struct {
	Worktree   domain.ScriptsStoreWorktree
	StoreDirty []string
}

// Status reads the owner's worktree now. The facts it reads are written back
// when they changed, so the board agrees with what the caller was just told.
func (s *Service) Status(ctx context.Context, owner domain.SessionID) (Status, error) {
	w, ok, err := s.Get(ctx, owner)
	if err != nil {
		return Status{}, err
	}
	if !ok {
		return Status{}, ErrNoWorktree
	}
	unlock := s.lock(w.Store)
	defer unlock()
	if err := s.refreshLocked(ctx, w); err != nil {
		return Status{}, err
	}
	w, _, err = s.Get(ctx, owner)
	if err != nil {
		return Status{}, err
	}
	dirty, err := s.trees.Dirty(ctx, w.Store)
	if err != nil {
		return Status{}, fmt.Errorf("scriptstore: read the store's main checkout: %w", err)
	}
	return Status{Worktree: w, StoreDirty: dirty}, nil
}

// Policy is what a teardown does with the owner's worktree, mirroring the
// session manager's dirty policies.
type Policy int

const (
	// SettleCheck publishes and reports, and removes nothing: the interactive
	// kill's preview.
	SettleCheck Policy = iota
	// SettleKeep publishes, then removes the worktree when nothing would be
	// lost, and otherwise keeps it held: every background teardown.
	SettleKeep
	// SettleDiscard publishes what it can, then removes the worktree and its
	// branch whatever they hold: a kill somebody confirmed.
	SettleDiscard
)

// Settlement is what a teardown found and did.
type Settlement struct {
	// Present is false when the owner has no worktree; nothing else is set.
	Present  bool
	Worktree domain.ScriptsStoreWorktree
	Publish  ports.ScriptsPublishResult
	// Blocked means removing the worktree would lose work: files nobody
	// committed, or commits the publish could not deliver.
	Blocked bool
	Removed bool
}

// Settle publishes the owner's worktree and then applies the policy. An error
// leaves the worktree where it is.
func (s *Service) Settle(ctx context.Context, owner domain.SessionID, policy Policy) (Settlement, error) {
	w, ok, err := s.Get(ctx, owner)
	if err != nil || !ok {
		return Settlement{}, err
	}
	unlock := s.lock(w.Store)
	defer unlock()
	out := Settlement{Present: true}
	if out.Publish, err = s.publishLocked(ctx, &w); err != nil {
		return Settlement{}, err
	}
	if _, statErr := os.Stat(w.Path); statErr == nil {
		st, err := s.trees.Status(ctx, w.Path, w.Branch, w.BaseBranch)
		if err != nil {
			return Settlement{}, fmt.Errorf("scriptstore: read %s: %w", w.Path, err)
		}
		w.Uncommitted, w.Unpublished = st.Uncommitted, st.Unpublished
	} else {
		w.Uncommitted = nil
	}
	out.Blocked = len(w.Uncommitted) > 0 || out.Publish.Outcome == ports.PublishRefused

	switch {
	case policy == SettleCheck:
	case policy == SettleKeep && out.Blocked:
		w.State = domain.ScriptsStoreHeld
		if len(w.Uncommitted) > 0 {
			w.HeldReason, w.HeldFiles = domain.HoldUncommitted, w.Uncommitted
		} else {
			w.HeldReason, w.HeldFiles = out.Publish.Hold, out.Publish.Files
		}
	default:
		if err := s.trees.Remove(ctx, w.Store, w.Path, w.Branch, policy == SettleDiscard); err != nil {
			return Settlement{}, fmt.Errorf("scriptstore: remove %s: %w", w.Path, err)
		}
		w.State, w.HeldReason, w.HeldFiles, w.Uncommitted, w.Unpublished = domain.ScriptsStoreRemoved, "", nil, nil, 0
		out.Removed = true
	}
	if err := s.save(ctx, w); err != nil {
		return Settlement{}, err
	}
	out.Worktree = w
	return out, nil
}

// Refresh re-reads the git facts of every worktree that still exists and
// writes the ones that changed. The lifecycle loop calls it; each row costs two
// cheap read-only git commands.
func (s *Service) Refresh(ctx context.Context) error {
	rows, err := s.store.ListScriptsStoreWorktreesInStates(ctx, domain.ScriptsStoreActive, domain.ScriptsStoreHeld)
	if err != nil {
		return err
	}
	var errs []error
	for _, w := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		unlock := s.lock(w.Store)
		err := s.refreshLocked(ctx, w)
		unlock()
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// refreshLocked re-reads one row under its store's lock, so a state change made
// since the caller read it is never written back over.
func (s *Service) refreshLocked(ctx context.Context, stale domain.ScriptsStoreWorktree) error {
	w, ok, err := s.Get(ctx, stale.SessionID)
	if err != nil || !ok {
		return err
	}
	if _, err := os.Stat(w.Path); err != nil {
		return nil
	}
	before := w
	s.readFacts(ctx, &w)
	if reflect.DeepEqual(nonNil(before.Uncommitted), nonNil(w.Uncommitted)) && before.Unpublished == w.Unpublished {
		return nil
	}
	return s.save(ctx, w)
}

func (s *Service) readFacts(ctx context.Context, w *domain.ScriptsStoreWorktree) {
	st, err := s.trees.Status(ctx, w.Path, w.Branch, w.BaseBranch)
	if err != nil {
		s.log.Warn("scriptstore: read worktree facts", "owner", w.SessionID, "path", w.Path, "error", err)
		return
	}
	w.Uncommitted, w.Unpublished = st.Uncommitted, st.Unpublished
}

func (s *Service) save(ctx context.Context, w domain.ScriptsStoreWorktree) error {
	w.UpdatedAt = s.now()
	return s.store.UpsertScriptsStoreWorktree(ctx, w)
}

// Reconcile runs at daemon start, before any session is restored. An active
// worktree of a live session whose folder a crash or a person removed is
// recreated from its kept branch; every row's facts are re-read; and a
// worktree AO laid out that no row owns is reported, never deleted, because it
// may hold the only copy of someone's scripts.
func (s *Service) Reconcile(ctx context.Context) error {
	rows, err := s.store.ListScriptsStoreWorktreesInStates(ctx, domain.ScriptsStoreActive, domain.ScriptsStoreHeld)
	if err != nil {
		return err
	}
	byStore := map[string][]domain.ScriptsStoreWorktree{}
	for _, w := range rows {
		byStore[w.Store] = append(byStore[w.Store], w)
	}
	var errs []error
	for store, ws := range byStore {
		if err := s.reconcileStore(ctx, store, ws); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) reconcileStore(ctx context.Context, store string, rows []domain.ScriptsStoreWorktree) error {
	unlock := s.lock(store)
	defer unlock()
	registered, err := s.trees.Registered(ctx, store)
	if err != nil {
		return fmt.Errorf("scriptstore: list worktrees of %s: %w", store, err)
	}
	owned := map[string]bool{}
	var errs []error
	for _, w := range rows {
		owned[canonical(w.Path)] = true
		if _, statErr := os.Stat(w.Path); statErr != nil && w.State == domain.ScriptsStoreActive && s.sessionLive(ctx, w.SessionID) {
			if err := s.trees.Ensure(ctx, w.Store, w.Path, w.Branch, w.BaseBranch); err != nil {
				errs = append(errs, fmt.Errorf("scriptstore: recreate %s: %w", w.Path, err))
				continue
			}
			s.log.Info("scriptstore: recreated a live session's store worktree", "owner", w.SessionID, "path", w.Path)
		}
		if err := s.refreshLocked(ctx, w); err != nil {
			errs = append(errs, err)
		}
	}
	layoutDir, _ := Layout(s.dataDir, store, "")
	for _, p := range registered {
		if within(p, layoutDir) && !owned[canonical(p)] {
			s.log.Warn("scriptstore: a store worktree no session owns; it is left in place", "store", store, "path", p)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) sessionLive(ctx context.Context, id domain.SessionID) bool {
	rec, ok, err := s.store.GetSession(ctx, id)
	return err == nil && ok && !rec.IsTerminated
}

func within(p, dir string) bool {
	rel, err := filepath.Rel(canonical(dir), canonical(p))
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}

// canonical resolves symlinks in the longest prefix of p that exists, so git's
// /private/var record and AO's /var path name one folder.
func canonical(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for dir := p; ; dir = filepath.Dir(dir) {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return p
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

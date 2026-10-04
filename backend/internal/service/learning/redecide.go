package learning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/apply"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/decide"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/redact"
)

// A decision is never final: a snoozed proposal can be brought back, a
// rejected one reopened, and what an approved one wrote edited or undone. What
// AO wrote is only ever taken back or rewritten while it is still what AO
// wrote; once someone else changed it, the person sees the change and confirms.

// Unsnooze brings a snoozed proposal back to the queue now.
func (s *Service) Unsnooze(ctx context.Context, id int64, by domain.LearnActor) (domain.LearnProposal, error) {
	p, err := s.decided(ctx, id)
	if err != nil {
		return p, err
	}
	if p.Status != domain.LearnProposalPending || p.SnoozedUntil.IsZero() {
		return p, fmt.Errorf("%w: #%d is not snoozed", ErrProposalWrongState, id)
	}
	ok, err := s.decisions.UnsnoozeLearnProposal(ctx, id, event(id, domain.LearnEventUnsnoozed, domain.LearnProposalPending, "", actor(by)), s.clock())
	if err != nil {
		return p, err
	}
	if !ok {
		return p, fmt.Errorf("%w: #%d is not snoozed", ErrProposalWrongState, id)
	}
	return s.reread(ctx, id)
}

// Reopen puts a rejected proposal (a conflict whose rule was kept included)
// back in the queue, to be decided again.
func (s *Service) Reopen(ctx context.Context, id int64, by domain.LearnActor) (domain.LearnProposal, error) {
	p, err := s.decided(ctx, id)
	if err != nil {
		return p, err
	}
	if p.Status != domain.LearnProposalRejected {
		return p, fmt.Errorf("%w: only a rejected proposal can be reopened; #%d is %s", ErrProposalWrongState, id, p.Status)
	}
	if err := s.canWaitAgain(ctx, p, true); err != nil {
		return p, err
	}
	return s.reopen(ctx, p, domain.LearnEventReopened, "", actor(by))
}

// Undo takes back what an approved proposal did and puts it back in the queue:
// a new memory's file and the MEMORY.md line it added are removed, a changed
// file gets its earlier version back, a pinned rule its earlier text. A
// conflict whose rule was kept is reopened. When what AO wrote changed since,
// nothing is touched unless confirm is the token of the state the person saw
// (*ChangedError carries it, with the diff).
func (s *Service) Undo(ctx context.Context, id int64, confirm string, by domain.LearnActor) (domain.LearnProposal, error) {
	p, err := s.decided(ctx, id)
	if err != nil {
		return p, err
	}
	by = actor(by)
	if p.Status == domain.LearnProposalRejected && p.Action == domain.LearnProposeConflict {
		if err := s.canWaitAgain(ctx, p, false); err != nil {
			return p, err
		}
		return s.reopen(ctx, p, domain.LearnEventUndone, "the rule is no longer kept over your words", by)
	}
	if p.Status != domain.LearnProposalApplied {
		return p, fmt.Errorf("%w: only an approved proposal can be undone; #%d is %s", ErrProposalWrongState, id, p.Status)
	}
	// Back in the queue must be possible before anything on disk is touched.
	if err := s.canWaitAgain(ctx, p, false); err != nil {
		return p, err
	}
	now := s.clock()
	var note string
	if p.Action == domain.LearnProposeConflict {
		if note, err = s.undoConflict(ctx, p, confirm); err != nil {
			return p, err
		}
	} else {
		st, err := apply.Undo(s.applyRoots, p, confirm, now)
		if err != nil {
			return p, s.changed(p, err)
		}
		note = undoNote(p, st)
	}
	return s.reopen(ctx, p, domain.LearnEventUndone, note, by)
}

func (s *Service) undoConflict(ctx context.Context, p domain.LearnProposal, confirm string) (string, error) {
	id, ok := protectedRuleID(p.TargetPath)
	if p.Resolution != domain.LearnWordsWin || !ok {
		return "nothing was written", nil
	}
	w, err := s.writtenRule(ctx, p, id)
	if err != nil {
		return "", err
	}
	if !w.Exists {
		return "the pinned rule is gone; nothing to put back", nil
	}
	if p.AppliedBefore == "" {
		return "", ErrNothingToRestore
	}
	if w.Changed && confirm != w.Token {
		return "", &ChangedError{Path: w.Path, Diff: w.Diff, Token: w.Token}
	}
	if _, err := s.decisions.UpdateLearnProtectedRuleText(ctx, id, p.AppliedBefore, s.clock()); err != nil {
		return "", err
	}
	return "put the pinned rule's text back", nil
}

// EditApplied replaces what an approved proposal wrote with the person's edit,
// after the same gates as an edit before approving. An edit started from the
// file as it is now confirms that state with its token.
func (s *Service) EditApplied(ctx context.Context, id int64, content, confirm string, by domain.LearnActor) (domain.LearnProposal, error) {
	p, err := s.decided(ctx, id)
	if err != nil {
		return p, err
	}
	if p.Status != domain.LearnProposalApplied || p.Action == domain.LearnProposeConflict {
		return p, fmt.Errorf("%w: only what an approved proposal wrote can be edited", ErrProposalWrongState)
	}
	st, err := apply.Inspect(s.applyRoots, p)
	if err != nil {
		return p, err
	}
	protected, err := s.protectedRules(ctx)
	if err != nil {
		return p, err
	}
	checked, err := decide.CheckRewrite(p, st.Content, content, protected, s.redactor(ctx))
	if err != nil {
		return p, fmt.Errorf("%w: %w", ErrInvalidDecision, err)
	}
	if checked == st.Content && !st.Changed {
		return p, nil
	}
	now := s.clock()
	sum, err := apply.Rewrite(s.applyRoots, p, checked, confirm, now)
	if err != nil {
		return p, s.changed(p, err)
	}
	before := p.AppliedBefore
	if p.Action == domain.LearnProposeCreateMemory {
		before = ""
	}
	ok, err := s.decisions.RewriteAppliedLearnProposal(ctx, id, checked, rediff(p, before, checked), sum, p.AppliedSHA256,
		event(id, domain.LearnEventEdited, domain.LearnProposalApplied, "", actor(by)), now)
	if err != nil {
		return p, err
	}
	if !ok {
		return p, fmt.Errorf("%w: #%d was decided again meanwhile", ErrProposalWrongState, id)
	}
	return s.reread(ctx, id)
}

// Written is what an approved proposal wrote as it is now. ok is false for a
// proposal that wrote nothing (not approved, or a conflict that changed no
// pinned rule).
func (s *Service) Written(ctx context.Context, p domain.LearnProposal) (Written, bool, error) {
	if p.Status != domain.LearnProposalApplied || s.decisions == nil {
		return Written{}, false, nil
	}
	if p.Action == domain.LearnProposeConflict {
		id, ok := protectedRuleID(p.TargetPath)
		if p.Resolution != domain.LearnWordsWin || !ok {
			return Written{}, false, nil
		}
		w, err := s.writtenRule(ctx, p, id)
		return w, err == nil, err
	}
	st, err := apply.Inspect(s.applyRoots, p)
	if err != nil {
		return Written{}, false, err
	}
	return writtenOf(p, st), true, nil
}

// History is a proposal's decisions, oldest first.
func (s *Service) History(ctx context.Context, id int64) ([]domain.LearnProposalEvent, error) {
	if s.decisions == nil {
		return nil, nil
	}
	return s.decisions.ListLearnProposalEvents(ctx, id)
}

// rediff is p's diff for content in place of what was proposed: the file from
// before to content, and the rest of the diff (a new memory's MEMORY.md line)
// as it was, so what the inbox shows as written is what was written.
func rediff(p domain.LearnProposal, before, content string) string {
	rest := ""
	if _, after, ok := strings.Cut(p.Diff, "\n--- "); ok {
		rest = "--- " + after
	}
	return decide.Diff(p.TargetPath, before, content) + rest
}

func writtenOf(p domain.LearnProposal, st apply.State) Written {
	w := Written{Path: st.Path, Exists: st.Exists, Content: st.Content, IndexPath: st.IndexPath, IndexLine: st.IndexLine,
		IndexLinePresent: st.IndexLinePresent, Changed: st.Changed, Token: st.Token}
	if st.Changed {
		w.Diff = decide.Diff(st.Path, p.NewContent, st.Content)
		if st.IndexLine != "" && !st.IndexLinePresent {
			w.Diff += decide.Diff(st.IndexPath, st.IndexLine+"\n", "")
		}
	}
	return w
}

func (s *Service) writtenRule(ctx context.Context, p domain.LearnProposal, id int64) (Written, error) {
	rule, found, err := s.protectedRule(ctx, id)
	if err != nil {
		return Written{}, err
	}
	w := Written{Path: p.TargetPath, Exists: found, Content: rule.Text}
	w.Changed = !found || rule.Text != p.NewContent
	if w.Changed {
		w.Diff = decide.Diff("pinned rule", p.NewContent+"\n", strings.TrimSuffix(rule.Text, "\n")+"\n")
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%t\n%s", found, rule.Text))
	w.Token = hex.EncodeToString(sum[:16])
	return w, nil
}

// changed turns apply's refusal of a changed file into the service's.
func (s *Service) changed(p domain.LearnProposal, err error) error {
	var c *apply.ChangedError
	if errors.As(err, &c) {
		w := writtenOf(p, c.State)
		return &ChangedError{Path: w.Path, Diff: w.Diff, Token: w.Token}
	}
	return err
}

// canWaitAgain refuses to put p back in the queue when another proposal for
// the same file is waiting (decide that one first), or - for a new memory
// being reopened - when its file exists already, so it is never written twice.
func (s *Service) canWaitAgain(ctx context.Context, p domain.LearnProposal, reopening bool) error {
	rows, err := s.decideStore.ListSkillProposals(ctx)
	if err != nil {
		return err
	}
	for _, o := range rows {
		if o.ID != p.ID && o.TargetPath == p.TargetPath && o.Status == domain.LearnProposalPending {
			return fmt.Errorf("%w: #%d (%s); decide that one first", domain.ErrLearnTargetPending, o.ID, o.Title)
		}
	}
	if !reopening || p.Action != domain.LearnProposeCreateMemory {
		return nil
	}
	if _, err := os.Stat(p.TargetPath); err == nil {
		by := "by hand or by an agent"
		for _, o := range rows {
			if o.ID != p.ID && o.TargetPath == p.TargetPath && o.Status == domain.LearnProposalApplied {
				by = fmt.Sprintf("by #%d (%s)", o.ID, o.Title)
			}
		}
		return fmt.Errorf("%w: %s was written %s; reopening would write it a second time", ErrMemoryExists, filepath.Base(p.TargetPath), by)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Service) reopen(ctx context.Context, p domain.LearnProposal, kind domain.LearnEventKind, note string, by domain.LearnActor) (domain.LearnProposal, error) {
	ok, err := s.decisions.ReopenLearnProposal(ctx, p.ID, p.Status, event(p.ID, kind, domain.LearnProposalPending, note, by), s.clock())
	if err != nil {
		return p, err
	}
	if !ok {
		return p, fmt.Errorf("%w: #%d was decided again meanwhile", ErrProposalWrongState, p.ID)
	}
	return s.reread(ctx, p.ID)
}

func undoNote(p domain.LearnProposal, st apply.State) string {
	name := filepath.Base(p.TargetPath)
	if p.Action != domain.LearnProposeCreateMemory {
		return "put the earlier " + name + " back"
	}
	note := "removed " + name
	if st.IndexLine != "" && st.IndexLinePresent {
		note += " and its MEMORY.md line"
	}
	return note
}

// decided is a proposal of any status, on a daemon that can decide.
func (s *Service) decided(ctx context.Context, id int64) (domain.LearnProposal, error) {
	if s.decisions == nil || s.decideStore == nil {
		return domain.LearnProposal{}, ErrDecideUnavailable
	}
	p, _, err := s.Proposal(ctx, id)
	return p, err
}

func (s *Service) reread(ctx context.Context, id int64) (domain.LearnProposal, error) {
	p, _, err := s.Proposal(ctx, id)
	return p, err
}

func (s *Service) redactor(ctx context.Context) *redact.Redactor {
	var dict []redact.Value
	if s.dictionary != nil {
		dict = s.dictionary(ctx)
	}
	return redact.New(dict)
}

func (s *Service) protectedRule(ctx context.Context, id int64) (domain.LearnProtectedRule, bool, error) {
	rules, err := s.protectedRules(ctx)
	if err != nil {
		return domain.LearnProtectedRule{}, false, err
	}
	for _, r := range rules {
		if r.ID == id {
			return r, true, nil
		}
	}
	return domain.LearnProtectedRule{}, false, nil
}

// actor is who decided, with an unknown surface taken as a bare API call.
func actor(a domain.LearnActor) domain.LearnActor {
	switch a.Via {
	case domain.LearnViaApp, domain.LearnViaCLI, domain.LearnViaAPI:
	default:
		a.Via = domain.LearnViaAPI
	}
	a.SessionID = strings.TrimSpace(a.SessionID)
	if len(a.SessionID) > 128 {
		a.SessionID = a.SessionID[:128]
	}
	return a
}

func event(id int64, kind domain.LearnEventKind, status domain.LearnProposalStatus, note string, by domain.LearnActor) domain.LearnProposalEvent {
	return domain.LearnProposalEvent{ProposalID: id, Kind: kind, Status: status, Note: note, Actor: by}
}

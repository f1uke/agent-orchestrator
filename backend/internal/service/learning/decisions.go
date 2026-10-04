package learning

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/apply"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/decide"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/redact"
)

// DecisionStore is the persistence the person's decisions need.
type DecisionStore interface {
	SettleLearnProposal(ctx context.Context, d domain.LearnSettlement, ev domain.LearnProposalEvent, now time.Time) (bool, error)
	SnoozeLearnProposal(ctx context.Context, id int64, until time.Time, ev domain.LearnProposalEvent, now time.Time) (bool, error)
	UnsnoozeLearnProposal(ctx context.Context, id int64, ev domain.LearnProposalEvent, now time.Time) (bool, error)
	ReopenLearnProposal(ctx context.Context, id int64, from domain.LearnProposalStatus, ev domain.LearnProposalEvent, now time.Time) (bool, error)
	RewriteAppliedLearnProposal(ctx context.Context, id int64, content, diff, sha, prevSHA string, ev domain.LearnProposalEvent, now time.Time) (bool, error)
	StaleLearnProposal(ctx context.Context, p domain.LearnProposal, ev domain.LearnProposalEvent, now time.Time) (bool, error)
	ListLearnProposalEvents(ctx context.Context, id int64) ([]domain.LearnProposalEvent, error)
	UpdateLearnProtectedRuleText(ctx context.Context, id int64, text string, now time.Time) (bool, error)
}

// Errors of a decision.
var (
	// ErrProposalNotPending is a proposal already decided (or a second click).
	ErrProposalNotPending = errors.New("the proposal is not waiting for a decision any more")
	// ErrProposalStale is a proposal whose file changed after it was made; it
	// is marked stale and will be proposed again against the file as it is.
	ErrProposalStale = errors.New("the file changed after this was proposed; it will be proposed again against the current file")
	// ErrInvalidDecision is a decision that cannot be applied as given.
	ErrInvalidDecision = errors.New("invalid decision")
	// ErrProposalWrongState is a decision the proposal's state does not allow
	// (unsnooze one that is not snoozed, reopen one that is not rejected, undo
	// one that wrote nothing).
	ErrProposalWrongState = errors.New("the proposal is not in a state that allows this")
	// ErrMemoryExists is a new memory that would be written a second time.
	ErrMemoryExists = errors.New("the memory already exists")
	// ErrNothingToRestore is an undo whose earlier version AO does not have.
	ErrNothingToRestore = apply.ErrNothingToRestore
)

// ChangedError is an undo or an edit of something that changed after AO wrote
// it (by hand, by an agent, by another proposal). Nothing was touched: the
// person reviews Diff and confirms by sending Token back.
type ChangedError struct {
	Path  string
	Diff  string
	Token string
}

func (e *ChangedError) Error() string {
	return fmt.Sprintf("%s changed after it was written; review the change and confirm with its token to go ahead", e.Path)
}

// Written is what an applied proposal wrote as it is now, against what was
// written: the file (or a pinned rule's text), and a new memory's index line.
type Written struct {
	Path             string
	Exists           bool
	Content          string
	IndexPath        string
	IndexLine        string
	IndexLinePresent bool
	// Changed is true when it is not what AO wrote; Diff is written -> now.
	Changed bool
	Diff    string
	// Token names this state; an undo or edit of a changed one confirms it.
	Token string
}

// MaxSnooze bounds how far a proposal can be snoozed.
const MaxSnooze = 90 * 24 * time.Hour

// WithDecisions wires in what deciding a proposal needs: the store, where it
// may write (Home) and back up (history), and the values no edit may add.
func (s *Service) WithDecisions(st DecisionStore, roots apply.Roots, dictionary func(context.Context) []redact.Value) *Service {
	s.decisions, s.applyRoots, s.dictionary = st, roots, dictionary
	return s
}

// Approve writes a pending proposal. content replaces what was proposed when
// the person edited it ("" keeps it); a conflict card needs the side that won.
func (s *Service) Approve(ctx context.Context, id int64, content string, resolution domain.LearnResolution, by domain.LearnActor) (domain.LearnProposal, error) {
	p, err := s.pending(ctx, id)
	if err != nil {
		return p, err
	}
	now, by := s.clock(), actor(by)
	if p.Action == domain.LearnProposeConflict {
		return s.resolveConflict(ctx, p, content, resolution, by, now)
	}
	if content != "" && content != p.NewContent {
		protected, err := s.protectedRules(ctx)
		if err != nil {
			return p, err
		}
		checked, err := decide.CheckEdit(p, content, protected, s.redactor(ctx))
		if err != nil {
			return p, fmt.Errorf("%w: %w", ErrInvalidDecision, err)
		}
		content = checked
	}
	w, err := apply.Apply(s.applyRoots, p, content, now)
	if errors.Is(err, apply.ErrStale) {
		ev := event(p.ID, domain.LearnEventStale, domain.LearnProposalStale, "the file changed after this was proposed", by)
		if _, serr := s.decisions.StaleLearnProposal(ctx, p, ev, now); serr != nil {
			return p, serr
		}
		return p, ErrProposalStale
	}
	if err != nil {
		return p, err
	}
	note, diff := "", p.Diff
	if content == "" {
		content = p.NewContent
	} else if content != p.NewContent {
		note, diff = "approved an edit", rediff(p, w.Before, content)
	}
	return s.settle(ctx, p, domain.LearnSettlement{ID: id, Status: domain.LearnProposalApplied, AppliedSHA256: w.SHA256, NewContent: content, Diff: diff,
		AppliedBefore: w.Before, AppliedIndexLine: w.IndexLine}, event(id, domain.LearnEventApproved, domain.LearnProposalApplied, note, by), now)
}

// resolveConflict records which side of a conflict card won. A pinned rule
// takes the person's newer words as its text; any other rule's file is left
// for the person to change (the decision and the new text are kept), and a
// repo file is never written by AO.
func (s *Service) resolveConflict(ctx context.Context, p domain.LearnProposal, content string, r domain.LearnResolution, by domain.LearnActor, now time.Time) (domain.LearnProposal, error) {
	if !r.Valid() {
		return p, fmt.Errorf("%w: pick which side wins (keep_rule, words_win or both)", ErrInvalidDecision)
	}
	text := strings.TrimSpace(content)
	if text == "" {
		text = p.NewContent
	}
	d := domain.LearnSettlement{ID: p.ID, Status: domain.LearnProposalApplied, Resolution: r, NewContent: text, Diff: p.Diff}
	switch r {
	case domain.LearnKeepRule:
		d.Status, d.RejectReason, d.NewContent = domain.LearnProposalRejected, "kept the rule", p.NewContent
	case domain.LearnWordsWin:
		if id, ok := protectedRuleID(p.TargetPath); ok {
			// Undo puts the pinned rule's text back, so it is kept first.
			if rule, found, err := s.protectedRule(ctx, id); err != nil {
				return p, err
			} else if found {
				d.AppliedBefore = rule.Text
			}
			if _, err := s.decisions.UpdateLearnProtectedRuleText(ctx, id, text, now); err != nil {
				return p, err
			}
		}
	case domain.LearnBoth:
		if strings.TrimSpace(content) == "" {
			return p, fmt.Errorf("%w: say where each one applies", ErrInvalidDecision)
		}
	}
	kind := domain.LearnEventApproved
	if d.Status == domain.LearnProposalRejected {
		kind = domain.LearnEventRejected
	}
	return s.settle(ctx, p, d, event(p.ID, kind, d.Status, string(r), by), now)
}

// Reject settles a pending proposal as rejected; decide reads the reason.
func (s *Service) Reject(ctx context.Context, id int64, reason string, by domain.LearnActor) (domain.LearnProposal, error) {
	p, err := s.pending(ctx, id)
	if err != nil {
		return p, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "no reason given"
	}
	return s.settle(ctx, p, domain.LearnSettlement{ID: id, Status: domain.LearnProposalRejected, RejectReason: reason, NewContent: p.NewContent, Diff: p.Diff},
		event(id, domain.LearnEventRejected, domain.LearnProposalRejected, reason, actor(by)), s.clock())
}

// Snooze hides a pending proposal until a time.
func (s *Service) Snooze(ctx context.Context, id int64, until time.Time, by domain.LearnActor) (domain.LearnProposal, error) {
	p, err := s.pending(ctx, id)
	if err != nil {
		return p, err
	}
	now := s.clock()
	if !until.After(now) || until.Sub(now) > MaxSnooze {
		return p, fmt.Errorf("%w: snooze until a time in the next 90 days", ErrInvalidDecision)
	}
	ev := event(id, domain.LearnEventSnoozed, domain.LearnProposalPending, "", actor(by))
	ev.SnoozedUntil = until
	ok, err := s.decisions.SnoozeLearnProposal(ctx, id, until, ev, now)
	if err != nil {
		return p, err
	}
	if !ok {
		return p, ErrProposalNotPending
	}
	p.SnoozedUntil = until
	return p, nil
}

func (s *Service) pending(ctx context.Context, id int64) (domain.LearnProposal, error) {
	if s.decisions == nil || s.decideStore == nil {
		return domain.LearnProposal{}, ErrDecideUnavailable
	}
	p, _, err := s.Proposal(ctx, id)
	if err != nil {
		return p, err
	}
	if p.Status != domain.LearnProposalPending {
		return p, ErrProposalNotPending
	}
	return p, nil
}

func (s *Service) settle(ctx context.Context, p domain.LearnProposal, d domain.LearnSettlement, ev domain.LearnProposalEvent, now time.Time) (domain.LearnProposal, error) {
	ok, err := s.decisions.SettleLearnProposal(ctx, d, ev, now)
	if err != nil {
		return p, err
	}
	if !ok {
		return p, ErrProposalNotPending
	}
	p.Status, p.RejectReason, p.Resolution, p.AppliedSHA256, p.NewContent, p.Diff, p.DecidedAt = d.Status, d.RejectReason, d.Resolution, d.AppliedSHA256, d.NewContent, d.Diff, now
	p.AppliedBefore, p.AppliedIndexLine = d.AppliedBefore, d.AppliedIndexLine
	return p, nil
}

func (s *Service) protectedRules(ctx context.Context) ([]domain.LearnProtectedRule, error) {
	if s.rulesStore == nil {
		return nil, nil
	}
	return s.rulesStore.ListLearnProtectedRules(ctx)
}

func protectedRuleID(target string) (int64, bool) {
	rest, ok := strings.CutPrefix(target, "rule:protected-")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	return id, err == nil
}

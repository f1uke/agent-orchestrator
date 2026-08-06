package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/messagetemplates"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The auto-send "unresolved review comments" nudge fires on reviewer feedback the
// worker has NOT been told about yet. That is a deliberate departure from the
// other PR nudges, which compare a signature of the CURRENT state:
//
//   - The worker replies on the thread and does not resolve it (the reply waits for
//     the human to confirm, and auto-resolve is opt-in), so an addressed thread
//     stays unresolved indefinitely. "Unresolved" therefore cannot mean "still
//     needs work", and resolution is not the terminating signal.
//   - A current-state signature also MOVES when our own reply lands, which re-fired
//     the nudge and quoted the worker's own reply back at it as a comment to
//     address — the loop this replaces.
//
// The terminating signal is instead "every reviewer note on this PR has already
// been dispatched". That set only grows, so a self reply, a resolve, a re-observed
// poll, or a partial review-thread window add nothing new and cannot re-fire the
// nudge, while a NEW reviewer note (on a fresh or an already-addressed thread) is
// new work and still nudges.

// reviewDecisionToken marks, inside a review dispatch mark, that the bare
// changes-requested decision has already been announced to the worker.
const reviewDecisionToken = "decision:changes_requested"

// reviewDispatchMark is the durable record of what the review-comment nudge has
// already told the worker about one PR. It is persisted as that nudge's dedup
// signature, so it rides the existing pr.last_nudge_signature payload and survives
// a daemon restart alongside the other reaction signatures.
type reviewDispatchMark struct {
	// notes holds the ids of reviewer notes already dispatched.
	notes map[string]bool
	// decision records whether a bare changes-requested decision (a review with no
	// inline threads) has been announced. It is cleared when the decision leaves
	// changes_requested, so a later re-request of changes is news again.
	decision bool
}

// parseReviewDispatchMark reads a persisted review signature. Signatures written
// before this scheme are a bare comma-joined list of dispatched comment ids, which
// parses as exactly that set of notes — so upgrading does not re-nudge a PR whose
// comments the worker has already been told about.
func parseReviewDispatchMark(sig string) reviewDispatchMark {
	mark := reviewDispatchMark{notes: make(map[string]bool)}
	for _, tok := range strings.Split(sig, ",") {
		switch tok {
		case "":
		case reviewDecisionToken:
			mark.decision = true
		default:
			mark.notes[tok] = true
		}
	}
	return mark
}

// with returns the mark after dispatching noteIDs and announcing (or clearing) the
// changes-requested decision.
func (r reviewDispatchMark) with(noteIDs []string, changesRequested bool) reviewDispatchMark {
	next := reviewDispatchMark{notes: make(map[string]bool, len(r.notes)+len(noteIDs)), decision: changesRequested}
	for id := range r.notes {
		next.notes[id] = true
	}
	for _, id := range noteIDs {
		next.notes[id] = true
	}
	return next
}

// encode renders the mark as a stable signature string. Note ids are sorted so the
// same dispatch state always encodes identically, whatever order the provider
// returned the threads in.
func (r reviewDispatchMark) encode() string {
	toks := make([]string, 0, len(r.notes)+1)
	for id := range r.notes {
		toks = append(toks, id)
	}
	sort.Strings(toks)
	if r.decision {
		toks = append(toks, reviewDecisionToken)
	}
	return strings.Join(toks, ",")
}

// queueReviewCommentNudge appends the review-comment nudge for this observation to
// nudges when there is genuinely new reviewer feedback to dispatch. When there is
// not, it still reconciles the durable mark, so a changes-requested decision that
// has since been withdrawn can announce itself again if it returns.
//
// maxAttempts is deliberately 0 (uncapped): the mark makes the nudge monotone —
// each send corresponds to reviewer notes the worker has never seen, and the same
// note can never be dispatched twice — so a lifetime attempt cap would only
// silently swallow the fourth round of real review feedback.
func (m *Manager) queueReviewCommentNudge(ctx context.Context, o ports.PRObservation, ident string, nudges *[]pendingNudge) error {
	key := "review:" + o.URL
	seen, err := m.reviewDispatchMark(ctx, o.URL, key)
	if err != nil {
		return err
	}
	items, freshNotes := pendingReviewItems(o.Comments, seen)
	changesRequested := o.Review == domain.ReviewChangesRequest
	next := seen.with(freshNotes, changesRequested)
	// A changes-requested decision is news exactly once per episode: the reviewer
	// requesting changes is the event, its lingering presence is not.
	announceDecision := changesRequested && !seen.decision

	if len(items) == 0 && !announceDecision {
		// Nothing new to say. Persist the mark anyway when it changed, which happens
		// only when the changes-requested decision was withdrawn: clearing it now is
		// what lets a future re-request nudge again.
		if sig := next.encode(); sig != seen.encode() {
			return m.setReactionSignature(ctx, o.URL, key, sig)
		}
		return nil
	}
	msg := m.renderNudge(messagetemplates.NameReviewCommentDispatch, messagetemplates.ReviewCommentData{
		PRIdentity: ident,
		PRURL:      domain.SanitizeControlChars(o.URL),
		Count:      len(items),
		Comments:   items,
	})
	*nudges = append(*nudges, pendingNudge{key: key, sig: next.encode(), msg: msg, maxAttempts: 0})
	return nil
}

// pendingReviewItems turns the observed review comments into the template's items,
// one per THREAD — the unit the human sees as a single open item on the forge —
// listing only the threads that carry reviewer notes not yet in seen.
//
// Skipped: resolved threads, our own replies (never feedback to address), and
// provider system notes such as GitLab's "changed this line in version N of the
// diff". File and Body are attacker-influenced (anyone who can comment on the PR)
// and get pasted into the agent's live pane, so both are stripped of control/escape
// chars; the returned note ids feed the dedup mark and are not sanitized.
func pendingReviewItems(comments []ports.PRCommentObservation, seen reviewDispatchMark) ([]messagetemplates.ReviewCommentItem, []string) {
	type threadGroup struct {
		file   string
		line   int
		bodies []string
	}
	groups := make(map[string]*threadGroup, len(comments))
	order := make([]string, 0, len(comments))
	fresh := make([]string, 0, len(comments))
	for _, c := range comments {
		if c.Resolved || c.SelfReply || c.System {
			continue
		}
		id := reviewNoteID(c)
		if seen.notes[id] {
			continue
		}
		key := c.ThreadID
		if key == "" {
			// Observations that predate review threads (and any provider that does not
			// expose a thread id) carry one comment per thread; keying on the note id
			// keeps those distinct instead of collapsing them into a single item.
			key = "note:" + id
		}
		g := groups[key]
		if g == nil {
			g = &threadGroup{file: domain.SanitizeControlChars(c.File), line: c.Line}
			groups[key] = g
			order = append(order, key)
		}
		g.bodies = append(g.bodies, domain.SanitizeControlChars(c.Body))
		fresh = append(fresh, id)
	}
	items := make([]messagetemplates.ReviewCommentItem, 0, len(order))
	for _, key := range order {
		g := groups[key]
		items = append(items, messagetemplates.ReviewCommentItem{
			Index: len(items) + 1,
			File:  g.file,
			Line:  g.line,
			// Every new note on the thread, oldest first: a reviewer can add several
			// before the next poll, and dropping any would lose real feedback.
			Body: strings.Join(g.bodies, "\n\n"),
		})
	}
	return items, fresh
}

// reviewNoteID is the dedup identity of one review note. Providers always supply a
// stable comment id (GitHub node id, GitLab note id); the content fallback exists
// so a provider that ever omits one degrades to "nudge once" instead of nudging
// forever (an empty id would be indistinguishable from an unset mark entry) or
// silently never nudging.
func reviewNoteID(c ports.PRCommentObservation) string {
	if c.ID != "" {
		return c.ID
	}
	sum := sha256.Sum256([]byte(c.ThreadID + "\x00" + c.File + "\x00" + c.Author + "\x00" + c.Body))
	return "sha:" + hex.EncodeToString(sum[:8])
}

// reviewDispatchMark reads the persisted dispatch mark for a PR's review nudge.
//
// It reads the same in-memory/persisted signature store sendOnce writes, so the two
// agree by construction. The read and the later send are separate critical sections;
// a concurrent observation of the SAME PR could therefore interleave, and the cost
// is bounded at one duplicate nudge (sendOnce still compares its own signature). PR
// observations are polled serially per session, so this does not happen in practice.
func (m *Manager) reviewDispatchMark(ctx context.Context, prURL, key string) (reviewDispatchMark, error) {
	m.react.mu.Lock()
	defer m.react.mu.Unlock()
	if prURL != "" && !m.react.loaded[prURL] {
		if err := m.loadPRSignaturesLocked(ctx, prURL); err != nil {
			return reviewDispatchMark{notes: map[string]bool{}}, err
		}
		m.react.loaded[prURL] = true
	}
	return parseReviewDispatchMark(m.react.seen[key]), nil
}

// setReactionSignature records a reaction signature WITHOUT sending anything. It is
// how a reaction retracts state it previously announced (here: a changes-requested
// decision that has been withdrawn) so returning to that state is news again.
func (m *Manager) setReactionSignature(ctx context.Context, prURL, key, sig string) error {
	m.react.mu.Lock()
	defer m.react.mu.Unlock()
	if prURL != "" && !m.react.loaded[prURL] {
		if err := m.loadPRSignaturesLocked(ctx, prURL); err != nil {
			return err
		}
		m.react.loaded[prURL] = true
	}
	if m.react.seen[key] == sig {
		return nil
	}
	if sig == "" {
		delete(m.react.seen, key)
	} else {
		m.react.seen[key] = sig
	}
	if prURL == "" {
		return nil
	}
	return m.persistPRSignaturesLocked(ctx, prURL)
}

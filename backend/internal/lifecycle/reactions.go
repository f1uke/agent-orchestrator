package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/messagetemplates"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const reviewMaxNudge = 3

// ReviewDeliveryOutcome reports what ApplyReviewResult did with a completed
// AO-internal review pass.
type ReviewDeliveryOutcome string

const (
	// ReviewDeliveryNoop means lifecycle did not send or confirm a review nudge
	// because the result was not relevant for delivery.
	ReviewDeliveryNoop ReviewDeliveryOutcome = "no_op"
	// ReviewDeliverySent means the worker nudge was sent or was already covered
	// by sendOnce dedup state and may be stamped delivered.
	ReviewDeliverySent ReviewDeliveryOutcome = "sent"
)

// ReviewResult is the already-persisted result of an AO-internal review pass.
// Lifecycle treats it as input to the reaction reducer; it does not write the
// review_run row.
type ReviewResult struct {
	RunID          string
	BatchID        string
	WorkerID       domain.SessionID
	PRURL          string
	TargetSHA      string
	Verdict        domain.ReviewVerdict
	Body           string
	GithubReviewID string
	DeliveredAt    *time.Time
}

// ApplyReviewBatch reacts to one reviewer CLI submission after the review
// service has decided which current-head changes-requested results are
// deliverable.
func (m *Manager) ApplyReviewBatch(ctx context.Context, workerID domain.SessionID, batchID string, results []ReviewResult) (ReviewDeliveryOutcome, error) {
	if batchID == "" || len(results) == 0 {
		return ReviewDeliveryNoop, nil
	}
	rec, ok, err := m.store.GetSession(ctx, workerID)
	if err != nil || !ok {
		return ReviewDeliveryNoop, err
	}
	if rec.IsTerminated || rec.Activity.State == domain.ActivityWaitingInput {
		return ReviewDeliveryNoop, nil
	}
	if m.messenger == nil {
		return ReviewDeliveryNoop, nil
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].PRURL != results[j].PRURL {
			return results[i].PRURL < results[j].PRURL
		}
		return results[i].RunID < results[j].RunID
	})
	data := messagetemplates.AOReviewerBatchData{Count: len(results)}
	var sigParts []string
	for i, r := range results {
		data.Reviews = append(data.Reviews, messagetemplates.AOReviewItem{
			Index:     i + 1,
			PRURL:     domain.SanitizeControlChars(r.PRURL),
			Verdict:   domain.SanitizeControlChars(string(r.Verdict)),
			TargetSHA: domain.SanitizeControlChars(r.TargetSHA),
			ReviewID:  domain.SanitizeControlChars(r.GithubReviewID),
			Body:      domain.SanitizeControlChars(r.Body),
		})
		sigParts = append(sigParts, strings.Join([]string{r.RunID, r.PRURL, r.TargetSHA, r.GithubReviewID, r.Body}, "\x00"))
	}
	msg := m.renderNudge(messagetemplates.NameAOReviewerBatch, data)
	anchorPR := results[0].PRURL
	key := "review-batch:" + anchorPR + ":" + batchID
	sig := strings.Join(sigParts, "\x01")
	if err := m.sendOnce(ctx, workerID, anchorPR, key, sig, msg, reviewMaxNudge); err != nil {
		return ReviewDeliveryNoop, err
	}
	return ReviewDeliverySent, nil
}

type reactionState struct {
	mu       sync.Mutex
	seen     map[string]string
	attempts map[string]int
	// loaded tracks PR URLs whose persisted dedup payload has been merged into
	// seen/attempts during this process. Lazy: we only pay the DB read on the
	// first reaction touching each PR after startup.
	loaded map[string]bool
}

func newReactionState() reactionState {
	return reactionState{seen: map[string]string{}, attempts: map[string]int{}, loaded: map[string]bool{}}
}

// reactionPayload is the JSON document persisted in pr.last_nudge_signature.
// Keeping the schema explicit (and stable) lets the daemon restart and resume
// the existing dedup state without re-nudging an agent.
type reactionPayload struct {
	Seen     map[string]string `json:"seen,omitempty"`
	Attempts map[string]int    `json:"attempts,omitempty"`
}

// pendingNudge is one actionable PR nudge queued by ApplyPRObservation. Queuing
// each condition's nudge (instead of sending inline and returning) keeps the
// conditions independent — none can suppress another — and centralizes the
// send + dedup in a single loop.
type pendingNudge struct {
	key         string
	sig         string
	msg         string
	maxAttempts int
}

// ApplyPRObservation reacts to a fetched PR observation after the PR service has
// persisted it. It does not write PR rows; it owns PR-driven lifecycle effects
// and sends actionable agent nudges such as rebase, fix-CI, and
// address-review-feedback prompts.
func (m *Manager) ApplyPRObservation(ctx context.Context, id domain.SessionID, o ports.PRObservation) error {
	if !o.Fetched {
		return nil
	}
	// A PR reaching a terminal state (merged or closed) no longer ends the
	// session on its own: a session may own several PRs. Terminate only when no
	// open PR remains and at least one of them merged. The observer persists the
	// PR row before calling lifecycle, so the store already reflects this
	// transition when sessionComplete reads it.
	if o.Merged || o.Closed {
		done, err := m.sessionComplete(ctx, id)
		if err != nil {
			return err
		}
		if !done {
			return nil
		}
		rec, ok, err := m.store.GetSession(ctx, id)
		if err != nil || !ok {
			return err
		}
		if rec.IsTerminated {
			return nil
		}
		// A worker EXPECTED TO OPEN MORE PRs (keep_warm_on_merge, set by
		// `ao spawn --keep-warm` or the board toggle) SUSPENDS in place instead of
		// terminating when it reaches the completion bar (all PRs merged/closed, ≥1
		// merged): keep its card on the board (is_suspended is orthogonal to lane;
		// status derivation surfaces a suspended-merged worker as needs_input) with a
		// "Merged · open to continue / Move to Done" affordance, tear its tmux down,
		// keep the worktree. The user then resumes it for the next PR (open the card →
		// wake) or archives it explicitly (Move to Done → kill). MarkSuspended BEFORE
		// the reap so the reaped agent's late "exited" hook is ignored
		// (ApplyActivitySignal skips suspended) rather than racing to terminate the
		// card into Done. Everything else — an ordinary single-PR worker, and every
		// orchestrator — still TERMINATES (auto-archives to Done) exactly as before,
		// so the common case is unchanged.
		if rec.Kind != domain.KindOrchestrator && rec.KeepWarmOnMerge {
			if err := m.MarkSuspended(ctx, id); err != nil {
				return err
			}
			if m.runtimeSuspender != nil {
				if err := m.runtimeSuspender(ctx, id); err != nil {
					// Best-effort: a failed tmux reap must not abort the observation or
					// undo the suspend. The card stays in its lane; the stray tmux is
					// reaped later (agent exit / daemon restart).
					slog.Default().Warn("lifecycle: merge-suspend runtime reap failed", "session", id, "err", err)
				}
			}
			return nil
		}
		return m.MarkTerminated(ctx, id)
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return err
	}
	if rec.IsTerminated || rec.Activity.State == domain.ActivityWaitingInput {
		return nil
	}
	// A single PR can trip several actionable conditions at once (failing CI,
	// unresolved review comments, a merge conflict). Queue every applicable nudge
	// and send them together, so each surfaces independently instead of one
	// returning early and hiding the rest — the bug this reducer had, where a CI
	// failure suppressed review feedback on the same PR. Each nudge self-dedups
	// via sendOnce; a send error short-circuits, and nudges already sent have
	// persisted their own dedup signature so the next poll retries only the rest.
	ident := prIdentity(o)
	var nudges []pendingNudge

	if o.CI == domain.CIFailing {
		if ch, ok := firstFailedCheck(o.Checks); ok {
			logTail := ""
			if ch.LogTail != "" {
				// LogTail is raw CI job output; sanitize before it reaches the
				// agent's live pane so embedded escape sequences can't drive the
				// terminal (the dedup signature stays on the raw bytes).
				logTail = domain.SanitizeControlChars(ch.LogTail)
			}
			msg := m.renderNudge(messagetemplates.NameCIFailing, messagetemplates.CIFailingData{PRIdentity: ident, PRURL: domain.SanitizeControlChars(o.URL), LogTail: logTail})
			nudges = append(nudges, pendingNudge{key: "ci:" + o.URL + ":" + ch.Name, sig: ch.CommitHash + ":" + ch.LogTail, msg: msg, maxAttempts: 0})
		}
	}
	// Auto-nudge the worker when its PR has unresolved human review comments (or
	// a changes-requested decision) — but only when this session opts in: a
	// per-session override wins, otherwise the global default. Dispatch is
	// otherwise manual (Comments tab / Send-to-worker). The observer still
	// fetches and persists these comments regardless (for that tab and for
	// merge-readiness gating).
	effective := m.autoNudgeDefault()
	if rec.AutoNudgeComments != nil {
		effective = *rec.AutoNudgeComments
	}
	if effective && (o.Review == domain.ReviewChangesRequest || hasUnresolvedComments(o.Comments)) {
		items, sig := reviewContent(o.Comments)
		msg := m.renderNudge(messagetemplates.NameReviewCommentDispatch, messagetemplates.ReviewCommentData{
			PRIdentity: ident,
			PRURL:      domain.SanitizeControlChars(o.URL),
			Count:      len(items),
			Comments:   items,
		})
		if sig == "" {
			sig = string(o.Review)
		}
		nudges = append(nudges, pendingNudge{key: "review:" + o.URL, sig: sig, msg: msg, maxAttempts: reviewMaxNudge})
	}
	// Suppress the merge-conflict nudge when the mergeability is stale — preserved
	// from the local DB row on a review-only refresh or a failed metadata fetch —
	// rather than a fresh provider read. A frozen "conflicting" value may already be
	// resolved server-side; nudging the worker to rebase an already-clean branch
	// drags it into needless, potentially destructive re-rebasing. A REAL current
	// conflict is always freshly fetched (MergeabilityStale=false) and still nudges.
	if o.Mergeability == domain.MergeConflicting && !o.MergeabilityStale {
		// Only the bottom of a stack is available for the rebase nudge. A PR
		// stacked on an open parent is expected to report conflicts against its
		// parent branch until the parent merges and it retargets, so nudging the
		// agent to rebase it now would be noise. Mergeability UNKNOWN (the brief
		// post-retarget recompute window) never reaches here.
		blocked, err := m.prBlockedByOpenParent(ctx, id, o.URL)
		if err != nil {
			return err
		}
		if !blocked {
			msg := m.renderNudge(messagetemplates.NameMergeConflict, messagetemplates.MergeConflictData{PRIdentity: ident, PRURL: domain.SanitizeControlChars(o.URL)})
			nudges = append(nudges, pendingNudge{key: "merge-conflict:" + o.URL, sig: string(o.Mergeability), msg: msg, maxAttempts: 0})
		}
	}

	for _, n := range nudges {
		if err := m.sendOnce(ctx, id, o.URL, n.key, n.sig, n.msg, n.maxAttempts); err != nil {
			return err
		}
	}
	return nil
}

// ApplyReviewResult reacts to a completed AO-internal review pass after the
// review service has persisted the run result. It mirrors ApplyPRObservation:
// no change_log reads, no review_run writes, only lifecycle side effects.
func (m *Manager) ApplyReviewResult(ctx context.Context, workerID domain.SessionID, r ReviewResult) (ReviewDeliveryOutcome, error) {
	if r.Verdict != domain.VerdictChangesRequested || r.DeliveredAt != nil {
		return ReviewDeliveryNoop, nil
	}
	rec, ok, err := m.store.GetSession(ctx, workerID)
	if err != nil || !ok {
		return ReviewDeliveryNoop, err
	}
	if rec.IsTerminated || rec.Activity.State == domain.ActivityWaitingInput {
		return ReviewDeliveryNoop, nil
	}
	if m.messenger == nil {
		return ReviewDeliveryNoop, nil
	}
	msg := m.renderNudge(messagetemplates.NameAOReviewerSingle, messagetemplates.AOReviewerSingleData{
		PRURL:    domain.SanitizeControlChars(r.PRURL),
		Verdict:  domain.SanitizeControlChars(string(r.Verdict)),
		ReviewID: domain.SanitizeControlChars(r.GithubReviewID),
		Body:     domain.SanitizeControlChars(r.Body),
	})
	key := "review:" + r.PRURL + ":ao:" + r.RunID
	sig := strings.Join([]string{r.TargetSHA, r.RunID, r.GithubReviewID, r.Body}, "\x00")
	err = m.sendOnce(ctx, workerID, r.PRURL, key, sig, msg, reviewMaxNudge)
	if err != nil {
		return ReviewDeliveryNoop, err
	}
	return ReviewDeliverySent, nil
}

// sessionComplete reports whether the session has reached the multi-PR
// completion bar: at least one PR merged and no PR still open. A session with no
// PRs, or with any open PR, is not complete.
func (m *Manager) sessionComplete(ctx context.Context, id domain.SessionID) (bool, error) {
	prs, err := m.store.ListPRsBySession(ctx, id)
	if err != nil {
		return false, err
	}
	merged := false
	for _, pr := range prs {
		if !pr.Merged && !pr.Closed {
			return false, nil
		}
		if pr.Merged {
			merged = true
		}
	}
	return merged, nil
}

// prBlockedByOpenParent reports whether the PR at prURL is stacked on top of
// another still-open PR in the same session — i.e. its target branch is the
// source branch of a sibling open PR. Such a PR is not the bottom of its stack
// and is exempt from merge-conflict nudges. Branch facts are read from the
// store, which the observer has already updated for this observation.
func (m *Manager) prBlockedByOpenParent(ctx context.Context, id domain.SessionID, prURL string) (bool, error) {
	prs, err := m.store.ListPRsBySession(ctx, id)
	if err != nil {
		return false, err
	}
	openSources := make(map[string]bool, len(prs))
	for _, pr := range prs {
		if !pr.Merged && !pr.Closed && pr.SourceBranch != "" {
			openSources[pr.SourceBranch] = true
		}
	}
	for _, pr := range prs {
		if pr.URL == prURL {
			return pr.TargetBranch != "" && openSources[pr.TargetBranch], nil
		}
	}
	return false, nil
}

// ApplySCMObservation is the provider-neutral lifecycle entrypoint used by the
// SCM observer. The existing reaction logic still operates on PRObservation, so
// lifecycle performs the compatibility projection internally instead of leaking
// the old PR DTO back into the observer/provider boundary.
func (m *Manager) ApplySCMObservation(ctx context.Context, id domain.SessionID, o ports.SCMObservation) error {
	if !o.Fetched {
		return nil
	}
	if err := m.ApplyPRObservation(ctx, id, scmToPRObservation(o)); err != nil {
		return err
	}
	intent, err := m.notificationIntentForCurrentSCM(ctx, id, o)
	if err != nil {
		return err
	}
	m.emitNotification(ctx, intent)
	return nil
}

func (m *Manager) notificationIntentForCurrentSCM(ctx context.Context, id domain.SessionID, o ports.SCMObservation) (*ports.NotificationIntent, error) {
	// Serialize the session snapshot with activity transitions so ready-to-merge
	// notifications do not race against a simultaneous waiting_input update.
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	// The transient observation only carries review threads on the slower review
	// cadence; on the frequent PR/CI-only cycles o.Review.Threads is empty, so it
	// cannot be trusted to prove there is no blocking review feedback. Read the
	// durable unresolved-comment fact the board status pill uses so ready-to-merge
	// agrees with the displayed status instead of flapping to "ready" every poll.
	facts, err := m.store.ListPRFactsForSession(ctx, id)
	if err != nil {
		return nil, err
	}
	prURL := firstSCMNonEmpty(o.PR.URL, o.PR.HTMLURL)
	unresolved := durableUnresolvedComments(facts, prURL)
	intent := m.notificationIntentForSCM(rec, o, unresolved)

	// Every one of these notifications is news only on the transition INTO its
	// state. The condition being true is not news; becoming true is. Without
	// this they re-fire on every later observation that reaches lifecycle while
	// the PR happens to still be in that state - the store's dedup index only
	// suppresses while the previous row is unread, so reading the notification
	// re-arms it.
	//
	// The marker means "the human has already been told about this episode", so
	// it is set only when a notification is actually produced. A PR that is
	// ready while the notification is suppressed for session reasons
	// (terminated, or the agent is awaiting input) leaves the marker untouched,
	// so it can still notify once the session becomes available.
	announcing := domain.NotificationType("")
	if intent != nil {
		announcing = intent.Type
	}
	fresh, err := m.syncAnnouncementMarks(ctx, prURL, scmAnnouncements(o, unresolved), announcing)
	if err != nil {
		return nil, err
	}
	if intent != nil && !fresh {
		return nil, nil
	}
	return intent, nil
}

// The reaction types namespace each announcement marker inside the per-PR
// reaction signatures persisted in pr.last_nudge_signature. Reusing that store
// (rather than adding a column) means the markers survive a daemon restart for
// free, exactly like the agent-nudge dedup beside it.
const (
	readyToMergeReactionType = "ready"
	mergedReactionType       = "merged"
	closedReactionType       = "closed"
)

// announcementState pairs an edge-triggered notification with the condition that
// defines the state it announces.
type announcementState struct {
	typ   domain.NotificationType
	key   string
	holds bool
}

// scmAnnouncements lists every edge-triggered SCM notification alongside whether
// its state currently holds. Merged wins over closed because a merged PR reads as
// closed on some providers, and notificationIntentForSCM resolves them in that
// same order.
func scmAnnouncements(o ports.SCMObservation, unresolvedComments bool) []announcementState {
	merged := o.PR.Merged
	return []announcementState{
		{typ: domain.NotificationPRMerged, key: mergedReactionType, holds: merged},
		{typ: domain.NotificationPRClosedUnmerged, key: closedReactionType, holds: o.PR.Closed && !merged},
		{typ: domain.NotificationReadyToMerge, key: readyToMergeReactionType, holds: scmObservationIsReadyToMerge(o, unresolvedComments)},
	}
}

// syncAnnouncementMarks maintains the durable "already told the human" marker for
// every state in states and reports whether announcing the `announcing` type now
// would be fresh news.
//
// Leaving a state clears its marker, so a later return to it is news again. This
// matters most for a reopened PR: closed goes false, the marker clears, and
// closing it a second time notifies again. It returns true only when the marker
// for `announcing` was absent and is now being set, i.e. this observation is the
// edge the human has not seen yet. A zero `announcing` means no notification is
// pending, in which case the return value is unused and only the clearing side
// effects matter.
func (m *Manager) syncAnnouncementMarks(ctx context.Context, prURL string, states []announcementState, announcing domain.NotificationType) (bool, error) {
	if prURL == "" {
		// Without a durable key there is nothing to dedup against, so treat the
		// announcement as fresh rather than silently swallowing it.
		return true, nil
	}

	m.react.mu.Lock()
	defer m.react.mu.Unlock()

	if !m.react.loaded[prURL] {
		if err := m.loadPRSignaturesLocked(ctx, prURL); err != nil {
			return false, err
		}
		m.react.loaded[prURL] = true
	}

	fresh, dirty := false, false
	for _, st := range states {
		key := st.key + ":" + prURL
		marked := m.react.seen[key] != ""
		switch {
		case !st.holds:
			if !marked {
				continue
			}
			// Clear by deletion rather than storing an empty marker: both read as
			// "not told" above, and deleting keeps the persisted payload free of a
			// residual entry for every PR that has ever left the state.
			delete(m.react.seen, key)
			dirty = true
		case st.typ != announcing, marked:
			// In the state, but either already announced or not announceable right
			// now. Either way there is nothing new to record.
			continue
		default:
			m.react.seen[key] = st.key
			dirty, fresh = true, true
		}
	}

	if !dirty {
		return fresh, nil
	}
	// Persist before reporting. Unlike a nudge (where the agent has already seen
	// the message, so persisting after sending is the safe order), nothing has
	// been delivered yet. Failing here and reporting "not fresh" costs at most a
	// delayed notification; the inverse would let a persist failure re-notify on
	// every restart.
	if err := m.persistPRSignaturesLocked(ctx, prURL); err != nil {
		return false, err
	}
	return fresh, nil
}

// durableUnresolvedComments reports whether the persisted PR facts for prURL
// record an unresolved, non-bot review comment — the same PRFacts.ReviewComments
// flag the status derivation reads for its changes-requested reason.
func durableUnresolvedComments(facts []domain.PRFacts, prURL string) bool {
	for _, f := range facts {
		if f.URL == prURL {
			return f.ReviewComments
		}
	}
	return false
}

func (m *Manager) notificationIntentForSCM(rec domain.SessionRecord, o ports.SCMObservation, unresolvedComments bool) *ports.NotificationIntent {
	prURL := firstSCMNonEmpty(o.PR.URL, o.PR.HTMLURL)
	base := ports.NotificationIntent{
		SessionID:          rec.ID,
		ProjectID:          rec.ProjectID,
		PRURL:              prURL,
		CreatedAt:          timeOr(o.ObservedAt, m.clock()),
		SessionDisplayName: rec.DisplayName,
		SessionKind:        rec.Kind,
		PRNumber:           o.PR.Number,
		PRTitle:            o.PR.Title,
		PRSourceBranch:     o.PR.SourceBranch,
		PRTargetBranch:     o.PR.TargetBranch,
		Provider:           o.Provider,
		Repo:               o.Repo,
	}
	if o.PR.Merged {
		base.Type = domain.NotificationPRMerged
		return &base
	}
	if o.PR.Closed {
		base.Type = domain.NotificationPRClosedUnmerged
		return &base
	}
	if rec.IsTerminated || rec.Activity.State == domain.ActivityWaitingInput || !scmObservationIsReadyToMerge(o, unresolvedComments) {
		return nil
	}
	base.Type = domain.NotificationReadyToMerge
	return &base
}

// scmObservationIsReadyToMerge reports whether the PR has no known merge
// blockers. unresolvedComments is the durable "has unresolved review comment"
// fact from PRFacts; it is authoritative because the transient o.Review.Threads
// is only populated on the slower review-refresh cadence and is empty on the
// common PR/CI-only poll cycles.
func scmObservationIsReadyToMerge(o ports.SCMObservation, unresolvedComments bool) bool {
	if o.PR.Merged || o.PR.Closed || o.PR.Draft {
		return false
	}
	ci := domain.CIState(o.CI.Summary)
	if ci == "" {
		ci = domain.CIUnknown
	}
	switch ci {
	case domain.CIFailing, domain.CIPending, domain.CIUnknown:
		return false
	}
	if domain.ReviewDecision(o.Review.Decision) == domain.ReviewChangesRequest || unresolvedComments || hasUnresolvedSCMComments(o.Review.Threads) {
		return false
	}
	return domain.Mergeability(o.Mergeability.State) == domain.MergeMergeable
}

func hasUnresolvedSCMComments(threads []ports.SCMReviewThreadObservation) bool {
	for _, th := range threads {
		if th.Resolved || th.IsBot {
			continue
		}
		for _, c := range th.Comments {
			if !c.IsBot {
				return true
			}
		}
	}
	return false
}

func scmToPRObservation(o ports.SCMObservation) ports.PRObservation {
	pr := ports.PRObservation{
		Fetched:           o.Fetched,
		URL:               firstSCMNonEmpty(o.PR.URL, o.PR.HTMLURL),
		Number:            o.PR.Number,
		Title:             o.PR.Title,
		SourceBranch:      o.PR.SourceBranch,
		TargetBranch:      o.PR.TargetBranch,
		Draft:             o.PR.Draft,
		Merged:            o.PR.Merged,
		Closed:            o.PR.Closed,
		CI:                domain.CIState(o.CI.Summary),
		Review:            domain.ReviewDecision(o.Review.Decision),
		Mergeability:      domain.Mergeability(o.Mergeability.State),
		MergeabilityStale: o.MetadataStale,
	}
	if pr.CI == "" {
		pr.CI = domain.CIUnknown
	}
	if pr.Review == "" {
		pr.Review = domain.ReviewNone
	}
	if pr.Mergeability == "" {
		pr.Mergeability = domain.MergeUnknown
	}
	checkCommit := firstSCMNonEmpty(o.CI.HeadSHA, o.PR.HeadSHA)
	for _, ch := range o.CI.FailedChecks {
		status := domain.PRCheckStatus(ch.Status)
		if status == "" {
			status = domain.PRCheckFailed
		}
		logTail := ch.LogTail
		if logTail == "" {
			logTail = o.CI.FailureLogTail
		}
		pr.Checks = append(pr.Checks, ports.PRCheckObservation{
			Name:       ch.Name,
			CommitHash: checkCommit,
			Status:     status,
			URL:        ch.URL,
			LogTail:    logTail,
		})
	}
	for _, th := range o.Review.Threads {
		if th.Resolved || th.IsBot {
			continue
		}
		for _, c := range th.Comments {
			if c.IsBot {
				continue
			}
			pr.Comments = append(pr.Comments, ports.PRCommentObservation{
				ID:       c.ID,
				Author:   c.Author,
				File:     th.Path,
				Line:     th.Line,
				Body:     c.Body,
				Resolved: th.Resolved,
			})
		}
	}
	return pr
}

// ApplyTrackerFacts reacts to a fetched Tracker issue observation. It owns the
// issue-driven side of session lifecycle and the initial bot-mention nudge;
// it does NOT persist tracker rows (the future Tracker observer in #35 owns
// the read-side persistence path).
//
// Reactions today:
//   - Issue terminal (state == done or cancelled) → MarkTerminated. The
//     reducer is idempotent — repeat observations on an already-terminated
//     session are no-ops because MarkTerminated skips when IsTerminated.
//   - Assignee changed → log only. No session-state reaction yet; the policy
//     for "assignee changed away from AO" is reserved for the write-side work
//     tracked by #40.
//   - New bot comment → one-time nudge using the same sendOnce + dedup
//     signature pattern as the SCM lane. Dedup is in-memory only for now;
//     cross-restart persistence lands with the Tracker observer (issue #35)
//     when issue-row signature storage is on the table.
func (m *Manager) ApplyTrackerFacts(ctx context.Context, id domain.SessionID, o ports.TrackerObservation) error {
	if !o.Fetched {
		return nil
	}
	if isTerminalTrackerState(o.Issue.State) {
		return m.MarkTerminated(ctx, id)
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return err
	}
	if rec.IsTerminated || rec.Activity.State == domain.ActivityWaitingInput {
		return nil
	}
	if o.Changed.Assignee {
		slog.Default().Info("lifecycle: tracker issue assignee changed",
			"session", id, "issue", o.Issue.URL, "assignee", o.Issue.Assignee)
	}
	if o.Changed.Comments {
		bodies, ids := newBotCommentContent(o.Comments)
		if len(ids) > 0 {
			msg := m.renderNudge(messagetemplates.NameTrackerBotComment, messagetemplates.TrackerBotData{Comments: strings.Join(bodies, "\n\n")})
			// Empty prURL routes sendOnce through its in-memory-only branch:
			// the PR-row signature load/persist is skipped, so the dedup
			// survives only for the lifetime of this Manager. Cross-restart
			// persistence ships with #35.
			return m.sendOnce(ctx, id, "", "tracker-bot:"+o.Issue.URL, strings.Join(ids, ","), msg, 0)
		}
	}
	return nil
}

func isTerminalTrackerState(state domain.NormalizedIssueState) bool {
	return state == domain.IssueDone || state == domain.IssueCancelled
}

func newBotCommentContent(comments []ports.TrackerCommentObservation) ([]string, []string) {
	bodies := make([]string, 0, len(comments))
	ids := make([]string, 0, len(comments))
	for _, c := range comments {
		if !c.IsBot {
			continue
		}
		// Both an ID and a body are required: ID anchors the dedup
		// signature (an empty ID collapses to "" which collides with
		// the zero value of m.react.seen[key] and silently suppresses
		// the nudge), and a body is what we actually need to surface
		// to the agent.
		if c.ID == "" || strings.TrimSpace(c.Body) == "" {
			continue
		}
		// Comment bodies are attacker-influenced (anyone/anything that can
		// comment on the tracker issue) and get pasted into the agent's live
		// pane; strip control/escape chars like the review-comment path does.
		// The dedup signature (sig, below) is built from comment IDs, not
		// bodies, so sanitizing here does not affect dedup.
		bodies = append(bodies, domain.SanitizeControlChars(c.Body))
		ids = append(ids, c.ID)
	}
	return bodies, ids
}

func firstSCMNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// prIdentity renders a short, sanitized PR identity ("PR #123 \"Title\"
// (feat/x → main)") for nudge messages so an agent in a multi-PR session can
// tell which PR — and where in a stack — a nudge refers to. Title and branch
// names are provider-controlled and reach the agent's live pane, so both are
// control-char sanitized. Falls back to "your PR" when the number is unknown.
func prIdentity(o ports.PRObservation) string {
	if o.Number <= 0 {
		return "your PR"
	}
	id := fmt.Sprintf("PR #%d", o.Number)
	if o.Title != "" {
		id += fmt.Sprintf(" %q", domain.SanitizeControlChars(o.Title))
	}
	if o.SourceBranch != "" && o.TargetBranch != "" {
		id += fmt.Sprintf(" (%s → %s)", domain.SanitizeControlChars(o.SourceBranch), domain.SanitizeControlChars(o.TargetBranch))
	}
	return id
}

// firstFailedCheck returns the first check in a failed state, preserving the
// original CI-nudge behavior of surfacing a single failing check. Extracting it
// lets the CI branch queue its nudge and fall through instead of returning from
// inside the loop, so review/merge-conflict feedback for the same PR is no
// longer skipped.
func firstFailedCheck(checks []ports.PRCheckObservation) (ports.PRCheckObservation, bool) {
	for _, ch := range checks {
		if ch.Status == domain.PRCheckFailed {
			return ch, true
		}
	}
	return ports.PRCheckObservation{}, false
}

func hasUnresolvedComments(comments []ports.PRCommentObservation) bool {
	for _, c := range comments {
		if !c.Resolved {
			return true
		}
	}
	return false
}

// reviewContent turns the unresolved review comments into the template's
// per-comment items (file:line + quoted body, so the worker knows where to make
// each change and reply) and the dedup signature. File and Body are
// attacker-influenced (anyone who can comment on the PR) and get pasted into the
// agent's live pane, so both are stripped of control/escape chars; the signature
// is built from comment IDs, not bodies, so dedup is unaffected.
func reviewContent(comments []ports.PRCommentObservation) ([]messagetemplates.ReviewCommentItem, string) {
	items := make([]messagetemplates.ReviewCommentItem, 0, len(comments))
	ids := make([]string, 0, len(comments))
	for _, c := range comments {
		if c.Resolved {
			continue
		}
		items = append(items, messagetemplates.ReviewCommentItem{
			Index: len(items) + 1,
			File:  domain.SanitizeControlChars(c.File),
			Line:  c.Line,
			Body:  domain.SanitizeControlChars(c.Body),
		})
		ids = append(ids, c.ID)
	}
	return items, strings.Join(ids, ",")
}

// renderNudge renders a nudge template, logging (but tolerating) a failed
// operator override — the Renderer returns the built-in default on failure.
func (m *Manager) renderNudge(name messagetemplates.Name, data any) string {
	msg, err := m.renderer.Render(name, data)
	if err != nil {
		slog.Default().Warn("lifecycle: nudge template render fell back to default", "template", name, "err", err)
	}
	return msg
}

func (m *Manager) sendOnce(ctx context.Context, id domain.SessionID, prURL, key, sig, msg string, maxAttempts int) error {
	if m.messenger == nil {
		return nil
	}
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
	attempts := m.react.attempts[key]
	if maxAttempts > 0 && attempts >= maxAttempts {
		return nil
	}
	if err := m.messenger.Send(ctx, id, msg); err != nil {
		return err
	}
	// Order: Send → in-memory mutation → durable persist. Sending first means a
	// transient persist failure does NOT swallow a real send (the agent saw the
	// message; subsequent polls in this process suppress re-sends via the
	// in-memory dedup). A persist failure that survives until a daemon restart
	// degrades to one extra nudge — preferred over the inverse (persist before
	// send, then crash mid-call) which would silently lose a real nudge.
	m.react.seen[key] = sig
	m.react.attempts[key] = attempts + 1
	if prURL != "" {
		if err := m.persistPRSignaturesLocked(ctx, prURL); err != nil {
			return err
		}
	}
	return nil
}

// loadPRSignaturesLocked merges any previously persisted reaction-dedup state
// for prURL into the in-memory maps. Caller must hold m.react.mu.
func (m *Manager) loadPRSignaturesLocked(ctx context.Context, prURL string) error {
	raw, err := m.store.GetPRLastNudgeSignature(ctx, prURL)
	if err != nil {
		return err
	}
	if raw == "" {
		return nil
	}
	// A corrupt persisted payload must not crash the lifecycle write path;
	// the worst case from a swallow is re-firing a nudge once.
	var p reactionPayload
	_ = json.Unmarshal([]byte(raw), &p)
	for k, v := range p.Seen {
		if _, ok := m.react.seen[k]; !ok {
			m.react.seen[k] = v
		}
	}
	for k, v := range p.Attempts {
		if cur, ok := m.react.attempts[k]; !ok || v > cur {
			m.react.attempts[k] = v
		}
	}
	return nil
}

// persistPRSignaturesLocked serialises every reaction-dedup entry whose key
// references prURL and writes the JSON payload back via the store. Caller must
// hold m.react.mu. A failed persist surfaces upward so the in-memory mutation
// (which the messenger already acted on) is not silently divergent from disk.
func (m *Manager) persistPRSignaturesLocked(ctx context.Context, prURL string) error {
	payload := reactionPayload{Seen: map[string]string{}, Attempts: map[string]int{}}
	for k, v := range m.react.seen {
		if reactionKeyTargetsPR(k, prURL) {
			payload.Seen[k] = v
		}
	}
	for k, v := range m.react.attempts {
		if reactionKeyTargetsPR(k, prURL) {
			payload.Attempts[k] = v
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return m.store.UpdatePRLastNudgeSignature(ctx, prURL, string(raw))
}

// reactionKeyTargetsPR matches the "<type>:<url>[:<extra>]" reaction keys used
// by ApplyPRObservation. Anchoring on the second colon-delimited segment keeps
// PR-specific keys grouped with the row that survives a restart.
func reactionKeyTargetsPR(key, prURL string) bool {
	if prURL == "" {
		return false
	}
	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return false
	}
	rest := parts[1]
	return rest == prURL || strings.HasPrefix(rest, prURL+":")
}

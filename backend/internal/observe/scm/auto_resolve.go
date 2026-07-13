package scm

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// autoResolveRepliedThreads resolves every currently-unresolved review thread on
// which OUR side has just posted a NEW reply, for a session that opted in via its
// per-session auto-resolve-on-reply gate.
//
// It runs inside the review refresh off the freshly fetched threads, before those
// threads are persisted. When the gate is off it returns immediately and does zero
// extra store/provider work, so it is purely additive to the existing flow. Any
// failure is logged and skipped — it never aborts the poll and never crashes.
//
// "our side" is the PR author (`s.known.Author`). In AO's worker model the worker
// opens the PR and replies to review threads with the same SCM token, so the PR
// author equals the reply author; the task sanctions the PR author as the "self"
// identity, so no extra viewer/current-user lookup is needed. A reviewer's reply
// carries a different author and is therefore never auto-resolved.
//
// "a NEW reply" is a fresh-observation, non-system comment authored by self whose
// id is NOT among the PR's previously stored comment ids. Comparing against the
// stored comments (DB-backed, so restart-safe) is what makes this idempotent and
// keeps it from re-resolving a thread a reviewer manually un-resolved: once we
// resolve, the thread reads Resolved on the next poll and is skipped; a plain
// un-resolve adds no new self comment, so there is nothing fresh to act on.
func (o *Observer) autoResolveRepliedThreads(ctx context.Context, s *subject, threads []ports.SCMReviewThreadObservation) {
	if !autoResolveEnabled(s.session) {
		return
	}
	self := strings.TrimSpace(s.known.Author)
	if self == "" {
		return // cannot identify our side; never resolve on a guess
	}
	if !anyUnresolvedSelfThread(threads, self) {
		return // nothing our side authored on any open thread; skip the store read
	}
	writer, ok := o.provider.(ReviewThreadWriter)
	if !ok {
		return // provider cannot resolve threads (read-only)
	}
	stored, err := o.store.ListPRComments(ctx, s.known.URL)
	if err != nil {
		o.logger.Warn("scm observer: auto-resolve: list stored comments failed", "pr", s.known.URL, "err", err)
		return // without the prior state we cannot tell a fresh reply from an old one
	}
	known := make(map[string]bool, len(stored))
	for _, c := range stored {
		known[c.ID] = true
	}
	ref := ports.SCMPRRef{Repo: s.repo, Number: s.known.Number, URL: s.known.URL}
	for _, th := range threads {
		if th.Resolved || !hasNewSelfReply(th, self, known) {
			continue
		}
		if err := writer.ResolveThread(ctx, ref, th.ID); err != nil {
			o.logger.Warn("scm observer: auto-resolve thread failed", "pr", s.known.URL, "thread", th.ID, "err", err)
			continue
		}
		o.logger.Info("scm observer: auto-resolved review thread after self reply", "pr", s.known.URL, "thread", th.ID)
	}
}

// autoResolveEnabled reports whether the session opted into auto-resolve. nil (the
// default) is OFF — there is no global default to inherit.
func autoResolveEnabled(rec domain.SessionRecord) bool {
	return rec.AutoResolveOnReply != nil && *rec.AutoResolveOnReply
}

// anyUnresolvedSelfThread reports whether any unresolved thread carries a
// non-system comment authored by self. It is a cheap pre-check that lets the caller
// avoid the stored-comments read for PRs our side has not commented on at all.
func anyUnresolvedSelfThread(threads []ports.SCMReviewThreadObservation, self string) bool {
	for _, th := range threads {
		if th.Resolved {
			continue
		}
		for _, c := range th.Comments {
			if !c.System && strings.EqualFold(strings.TrimSpace(c.Author), self) {
				return true
			}
		}
	}
	return false
}

// hasNewSelfReply reports whether the thread carries a fresh (not previously
// stored) non-system comment authored by self.
func hasNewSelfReply(th ports.SCMReviewThreadObservation, self string, stored map[string]bool) bool {
	for _, c := range th.Comments {
		if c.System || stored[c.ID] {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(c.Author), self) {
			return true
		}
	}
	return false
}

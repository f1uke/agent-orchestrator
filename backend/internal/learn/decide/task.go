// Package decide turns a finished task's drafts into proposals: it decides
// when a task is ready and how it ended, assembles what the strong model sees,
// parses what it and the verifier answer, and applies the gates no model can
// talk its way past. Everything here is pure; observe/learndecide runs it.
package decide

import (
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Timing.
const (
	// UnknownWait is how long a task that ended without saying how waits for
	// a later merge on its branch before it is decided as unknown.
	UnknownWait = 48 * time.Hour
	// DailyCut decides a session that never ends once its newest draft is
	// this old.
	DailyCut = 24 * time.Hour
)

// SessionFacts is one session of a task and its pull requests.
type SessionFacts struct {
	Session domain.SessionRecord
	PRs     []domain.PullRequest
}

// Readiness is whether a task can be decided now, and how it ended.
type Readiness struct {
	Ready   bool
	Outcome domain.LearnOutcome
	// Why explains a task that is not ready yet.
	Why string
}

// Assess decides whether the task named key is ready. sessions are the
// task's sessions (one for solo, every member for a crew; none when the rows
// are gone), newestDraft the creation time of its newest open draft.
func Assess(key string, sessions []SessionFacts, newestDraft, now time.Time) Readiness {
	if day, ok := orchestratorDay(key); ok {
		if now.UTC().Format("2006-01-02") > day {
			return Readiness{Ready: true, Outcome: domain.LearnOutcomeDay}
		}
		return Readiness{Outcome: domain.LearnOutcomeDay, Why: "the orchestrator's day is not over"}
	}
	if len(sessions) == 0 {
		// The rows are gone: nothing will ever say how it ended.
		if now.Sub(newestDraft) >= UnknownWait {
			return Readiness{Ready: true, Outcome: domain.LearnOutcomeUnknown}
		}
		return Readiness{Outcome: domain.LearnOutcomeUnknown, Why: "the task's sessions are gone; waiting before deciding as unknown"}
	}
	merged, abandoned, allEnded := false, false, true
	var lastEnd time.Time
	for _, s := range sessions {
		rec := s.Session
		for _, pr := range s.PRs {
			if pr.Merged {
				merged = true
			} else if pr.Closed {
				abandoned = true
			}
		}
		// A keep-warm worker whose work merged has delivered it, even though it
		// keeps running for the next PR: it reads as ended-by-merge, as it did
		// when a merge parked it (SleepReasonMerged, still on older rows).
		keptWarm := keptWarmAfterMerge(rec, s.PRs)
		if keptWarm {
			merged = true
		}
		ended := rec.IsTerminated || keptWarm
		if !ended {
			allEnded = false
			continue
		}
		t := rec.Termination
		switch t.Reason {
		case domain.TerminationCauseWorkComplete:
			merged = true
		case domain.TerminationCauseKill, domain.TerminationCauseDiscardWork, domain.TerminationCauseIssueClosed:
			abandoned = true
		}
		at := t.At
		if at.IsZero() {
			at = rec.UpdatedAt
		}
		if at.After(lastEnd) {
			lastEnd = at
		}
	}
	switch {
	case !allEnded:
		if now.Sub(newestDraft) >= DailyCut {
			return Readiness{Ready: true, Outcome: domain.LearnOutcomeOngoing}
		}
		return Readiness{Outcome: domain.LearnOutcomeOngoing, Why: "the task is still running"}
	case merged:
		return Readiness{Ready: true, Outcome: domain.LearnOutcomeMerged}
	case abandoned:
		return Readiness{Ready: true, Outcome: domain.LearnOutcomeAbandoned}
	case now.Sub(lastEnd) >= UnknownWait:
		return Readiness{Ready: true, Outcome: domain.LearnOutcomeUnknown}
	default:
		return Readiness{Outcome: domain.LearnOutcomeUnknown, Why: "ended without saying how; waiting for a later merge"}
	}
}

// keptWarmAfterMerge reports whether rec is a keep-warm worker that has reached
// the merge completion bar - every PR merged or closed, at least one merged -
// and carries on running, or one a merge parked before that (SleepReasonMerged).
func keptWarmAfterMerge(rec domain.SessionRecord, prs []domain.PullRequest) bool {
	if rec.IsTerminated {
		return false
	}
	if rec.SleepReason == domain.SleepReasonMerged {
		return true
	}
	if !rec.KeepWarmOnMerge || rec.Kind == domain.KindOrchestrator {
		return false
	}
	anyMerged := false
	for _, pr := range prs {
		if !pr.Merged && !pr.Closed {
			return false
		}
		anyMerged = anyMerged || pr.Merged
	}
	return anyMerged
}

// orchestratorDay is the day of an orchestrator task key
// ("orch:<project>:<yyyy-mm-dd>").
func orchestratorDay(key string) (string, bool) {
	rest, ok := strings.CutPrefix(key, "orch:")
	if !ok {
		return "", false
	}
	i := strings.LastIndex(rest, ":")
	if i < 0 {
		return "", false
	}
	return rest[i+1:], true
}

// IsOrchestrator reports whether key is an orchestrator's day.
func IsOrchestrator(key string) bool {
	_, ok := orchestratorDay(key)
	return ok
}

// CrewID is the crew of a crew task key, or "".
func CrewID(key string) string {
	if c, ok := strings.CutPrefix(key, "crew:"); ok {
		return c
	}
	return ""
}

// SoloSession is the session of a solo task key, or "".
func SoloSession(key string) domain.SessionID {
	if s, ok := strings.CutPrefix(key, "solo:"); ok {
		return domain.SessionID(s)
	}
	return ""
}

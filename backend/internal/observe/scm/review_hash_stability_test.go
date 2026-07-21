package scm

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The review semantic hash is the observer's "did review actually change?"
// cursor: a difference makes Changed.Review true, which delivers the
// observation to lifecycle, which in turn can emit a "ready to merge"
// notification. So the hash MUST be a function of the observed reality and of
// nothing else.
//
// It was not. domainFromObservation normalizes an empty decision to "none"
// before persisting it, but hashed the RAW decision. Two observations of the
// same untouched merge request therefore hashed differently depending only on
// which code path assembled them:
//
//   - a round that fetched metadata: GitLab's fetchOnePullRequest sets no
//     Review at all, so Decision is ""
//   - a review-only round: observationFromLocal seeds Decision from the stored
//     row, so Decision is "none"
//
// GitLab reaches the "" case constantly because approvalDecision returns ""
// whenever the project enforces no approval rule of its own. GitHub never does
// — its metadata path always sets a non-empty reviewDecision — which is exactly
// why only GitLab merge requests re-notified.

func TestReviewSemanticHashNormalizesUnsetDecision(t *testing.T) {
	// Both describe the same reality: no review decision recorded. Persistence
	// collapses both to "none", so the hash must collapse them too.
	fromMetadataRound := ports.SCMReviewObservation{Decision: ""}
	fromReviewOnlyRound := ports.SCMReviewObservation{Decision: string(domain.ReviewNone)}

	if got, want := reviewSemanticHash(fromMetadataRound), reviewSemanticHash(fromReviewOnlyRound); got != want {
		t.Fatalf("review hash differs for observations that persist identically:\n"+
			" Decision=%q -> %s\n Decision=%q -> %s\n"+
			"an untouched MR alternating between these two assembly paths looks like fresh review activity every poll",
			fromMetadataRound.Decision, got, fromReviewOnlyRound.Decision, want)
	}
}

// TestUntouchedMRDoesNotReportReviewChangedAcrossAssemblyPaths is the
// behavioural half: drive the real persistence preparation over two consecutive
// rounds that observe an unchanged merge request via the two different paths,
// and assert the observer does not claim review changed. Changed.Review is what
// hands the observation to lifecycle and lets the ready-to-merge notification
// fire again.
func TestUntouchedMRDoesNotReportReviewChangedAcrossAssemblyPaths(t *testing.T) {
	o := &Observer{}
	now := time.Date(2026, 7, 21, 3, 19, 0, 0, time.UTC)
	const sessionID = domain.SessionID("s1")

	ready := func(decision string) ports.SCMObservation {
		return ports.SCMObservation{
			Fetched:  true,
			Provider: "gitlab", Host: "gitlab.example.com", Repo: "grp/proj",
			PR: ports.SCMPRObservation{
				URL: "https://gitlab.example.com/grp/proj/-/merge_requests/3028", Number: 3028,
				State: string(domain.PRStateOpen), SourceBranch: "feat/x", TargetBranch: "main",
				HeadSHA: "caf315be", Title: "Untouched",
			},
			CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing), HeadSHA: "caf315be"},
			Review:       ports.SCMReviewObservation{Decision: decision},
			Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable), Mergeable: true},
		}
	}

	// Round 1 fetched metadata, so the GitLab provider left Decision empty.
	opts := persistenceOptions{reviewFetched: true}
	round1 := o.prepareForPersistence(ready(""), domain.PullRequest{}, opts, now)
	stored, _, _, _, _ := domainFromObservation(sessionID, round1, domain.PullRequest{}, opts, now)

	// Round 2 is a review-only refresh of the SAME untouched MR, so the
	// observation was rebuilt from the stored row and carries Decision "none".
	round2 := o.prepareForPersistence(ready(string(domain.ReviewNone)), stored, opts, now.Add(time.Minute))

	if round2.Changed.Review {
		t.Fatalf("Changed.Review = true for an untouched merge request "+
			"(stored decision %q, hash %s): the observation reaches lifecycle and can "+
			"re-fire the ready-to-merge notification even though nothing about the MR moved",
			stored.Review, stored.ReviewHash)
	}
}

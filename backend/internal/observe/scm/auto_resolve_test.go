package scm

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func autoResolveBoolPtr(b bool) *bool { return &b }

// selfComment / reviewerComment build normalized thread comments for the tests.
func arComment(id, author string) ports.SCMReviewCommentObservation {
	return ports.SCMReviewCommentObservation{ID: id, Author: author, Body: "b"}
}

const arSelf = "fluke.s"

// arSubject builds a subject for the auto-resolve pass: a session with the given
// gate value and a known PR authored by self.
func arSubject(gate *bool) *subject {
	rec := domain.SessionRecord{ID: "p-1", ProjectID: "p", AutoResolveOnReply: gate}
	return &subject{
		session: rec,
		repo:    testRepo,
		known:   domain.PullRequest{URL: "https://github.com/o/r/pull/1", Number: 1, Author: arSelf},
		hasPR:   true,
	}
}

func TestAutoResolveRepliedThreads(t *testing.T) {
	unresolvedWithSelfReply := []ports.SCMReviewThreadObservation{{
		ID:       "t1",
		Resolved: false,
		Comments: []ports.SCMReviewCommentObservation{arComment("c1", "ann"), arComment("c2", arSelf)},
	}}

	cases := []struct {
		name        string
		gate        *bool
		author      string // PR author override; "" keeps arSelf
		threads     []ports.SCMReviewThreadObservation
		storedIDs   []string // comment ids already persisted for the PR
		wantResolve []string // thread ids expected to be resolved
	}{
		{
			name:        "on: fresh self reply on unresolved thread resolves it",
			gate:        autoResolveBoolPtr(true),
			threads:     unresolvedWithSelfReply,
			storedIDs:   []string{"c1"}, // reviewer comment already seen; c2 is fresh
			wantResolve: []string{"t1"},
		},
		{
			name:        "off (nil): never resolves",
			gate:        nil,
			threads:     unresolvedWithSelfReply,
			storedIDs:   []string{"c1"},
			wantResolve: nil,
		},
		{
			name:        "off (explicit false): never resolves",
			gate:        autoResolveBoolPtr(false),
			threads:     unresolvedWithSelfReply,
			storedIDs:   []string{"c1"},
			wantResolve: nil,
		},
		{
			name: "reviewer reply only: not our side, no resolve",
			gate: autoResolveBoolPtr(true),
			threads: []ports.SCMReviewThreadObservation{{
				ID:       "t1",
				Comments: []ports.SCMReviewCommentObservation{arComment("c1", "ann"), arComment("c2", "bob")},
			}},
			storedIDs:   []string{"c1"},
			wantResolve: nil,
		},
		{
			name: "already resolved thread: skipped even with a fresh self reply",
			gate: autoResolveBoolPtr(true),
			threads: []ports.SCMReviewThreadObservation{{
				ID:       "t1",
				Resolved: true,
				Comments: []ports.SCMReviewCommentObservation{arComment("c1", "ann"), arComment("c2", arSelf)},
			}},
			storedIDs:   []string{"c1"},
			wantResolve: nil,
		},
		{
			name:        "self reply already stored (reviewer un-resolve): not fresh, no resolve",
			gate:        autoResolveBoolPtr(true),
			threads:     unresolvedWithSelfReply,
			storedIDs:   []string{"c1", "c2"}, // c2 already seen on a prior poll
			wantResolve: nil,
		},
		{
			name:        "first observation of a self-replied thread (nothing stored): resolves",
			gate:        autoResolveBoolPtr(true),
			threads:     unresolvedWithSelfReply,
			storedIDs:   nil,
			wantResolve: []string{"t1"},
		},
		{
			name: "system note authored by self: ignored",
			gate: autoResolveBoolPtr(true),
			threads: []ports.SCMReviewThreadObservation{{
				ID: "t1",
				Comments: []ports.SCMReviewCommentObservation{
					arComment("c1", "ann"),
					{ID: "c2", Author: arSelf, Body: "system", System: true},
				},
			}},
			storedIDs:   []string{"c1"},
			wantResolve: nil,
		},
		{
			name:        "unknown PR author: cannot identify self, no resolve",
			gate:        autoResolveBoolPtr(true),
			author:      "-", // sentinel: force empty author
			threads:     unresolvedWithSelfReply,
			storedIDs:   []string{"c1"},
			wantResolve: nil,
		},
		{
			name: "author match is case-insensitive",
			gate: autoResolveBoolPtr(true),
			threads: []ports.SCMReviewThreadObservation{{
				ID:       "t1",
				Comments: []ports.SCMReviewCommentObservation{arComment("c1", "ann"), arComment("c2", "Fluke.S")},
			}},
			storedIDs:   []string{"c1"},
			wantResolve: []string{"t1"},
		},
		{
			name: "multiple threads: only the one with a fresh self reply is resolved",
			gate: autoResolveBoolPtr(true),
			threads: []ports.SCMReviewThreadObservation{
				{ID: "t1", Comments: []ports.SCMReviewCommentObservation{arComment("c1", "ann")}},                          // reviewer only
				{ID: "t2", Comments: []ports.SCMReviewCommentObservation{arComment("c2", "ann"), arComment("c3", arSelf)}}, // fresh self reply
				{ID: "t3", Resolved: true, Comments: []ports.SCMReviewCommentObservation{arComment("c4", arSelf)}},         // resolved
			},
			storedIDs:   []string{"c1", "c2", "c4"},
			wantResolve: []string{"t2"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{comments: map[string][]domain.PullRequestComment{}}
			provider := &fakeProvider{}
			subj := arSubject(tc.gate)
			if tc.author == "-" {
				subj.known.Author = ""
			} else if tc.author != "" {
				subj.known.Author = tc.author
			}
			stored := make([]domain.PullRequestComment, 0, len(tc.storedIDs))
			for _, id := range tc.storedIDs {
				stored = append(stored, domain.PullRequestComment{ID: id})
			}
			store.comments[subj.known.URL] = stored

			o := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
			o.autoResolveRepliedThreads(context.Background(), subj, tc.threads)

			got := make([]string, 0, len(provider.resolvedThreads))
			for _, r := range provider.resolvedThreads {
				got = append(got, r.threadID)
				if r.ref.URL != subj.known.URL || r.ref.Number != subj.known.Number {
					t.Fatalf("resolve ref = %+v, want PR %s #%d", r.ref, subj.known.URL, subj.known.Number)
				}
			}
			if !equalStringSets(got, tc.wantResolve) {
				t.Fatalf("resolved threads = %v, want %v", got, tc.wantResolve)
			}
		})
	}
}

// TestAutoResolveRepliedThreads_ProviderFailureIsSkipped proves a failing resolve
// call is logged and skipped rather than aborting: a second, healthy thread in the
// same pass is still attempted.
func TestAutoResolveRepliedThreads_ProviderFailureIsSwallowed(t *testing.T) {
	store := &fakeStore{comments: map[string][]domain.PullRequestComment{}}
	provider := &fakeProvider{resolveErr: context.DeadlineExceeded}
	subj := arSubject(autoResolveBoolPtr(true))
	store.comments[subj.known.URL] = nil
	threads := []ports.SCMReviewThreadObservation{{ID: "t1", Comments: []ports.SCMReviewCommentObservation{arComment("c1", arSelf)}}}

	o := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	// Must not panic and must not record a successful resolve.
	o.autoResolveRepliedThreads(context.Background(), subj, threads)
	if len(provider.resolvedThreads) != 0 {
		t.Fatalf("resolve failure should not record a resolved thread, got %v", provider.resolvedThreads)
	}
}

// TestAutoResolveRepliedThreads_StoreErrorSkips proves that if the prior comments
// cannot be read, the pass skips rather than resolving on a guess.
func TestAutoResolveRepliedThreads_StoreErrorSkips(t *testing.T) {
	store := &fakeStore{comments: map[string][]domain.PullRequestComment{}, commentsErr: context.DeadlineExceeded}
	provider := &fakeProvider{}
	subj := arSubject(autoResolveBoolPtr(true))
	threads := []ports.SCMReviewThreadObservation{{ID: "t1", Comments: []ports.SCMReviewCommentObservation{arComment("c1", arSelf)}}}

	o := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	o.autoResolveRepliedThreads(context.Background(), subj, threads)
	if len(provider.resolvedThreads) != 0 {
		t.Fatalf("store read failure should skip resolve, got %v", provider.resolvedThreads)
	}
}

// TestPoll_AutoResolveFiresOnReviewRefresh proves the pass is wired into the review
// refresh end to end: with the gate on, a review poll that surfaces a fresh self
// reply on an unresolved thread resolves that thread on the provider.
func TestPoll_AutoResolveFiresOnReviewRefresh(t *testing.T) {
	store := testStoreWithSession()
	store.sessions[0].AutoResolveOnReply = autoResolveBoolPtr(true)
	local := knownPR(1)
	local.ReviewHash = "old"
	local.Author = arSelf
	store.prs["p-1"] = []domain.PullRequest{local}
	// The reviewer comment c1 is already stored; the self reply c2 is fresh.
	store.comments[local.URL] = []domain.PullRequestComment{{ThreadID: "t1", ID: "c1", Author: "ann"}}
	review := ports.SCMReviewObservation{
		Decision: string(domain.ReviewChangesRequest),
		Threads: []ports.SCMReviewThreadObservation{{
			ID:       "t1",
			Path:     "f.go",
			Line:     2,
			Comments: []ports.SCMReviewCommentObservation{arComment("c1", "ann"), arComment("c2", arSelf)},
		}},
	}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}},
		observations: map[string]ports.SCMObservation{},
		reviews:      map[string]ports.SCMReviewObservation{prKey(testRepo, 1): review},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(200, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.resolvedThreads) != 1 || provider.resolvedThreads[0].threadID != "t1" {
		t.Fatalf("expected thread t1 auto-resolved during review refresh, got %v", provider.resolvedThreads)
	}
	if provider.resolvedThreads[0].ref.Number != 1 {
		t.Fatalf("resolve ref number = %d, want 1", provider.resolvedThreads[0].ref.Number)
	}
}

// TestPoll_AutoResolveOffByDefault proves an untouched session (gate nil) never
// auto-resolves, even when a self reply is present.
func TestPoll_AutoResolveOffByDefault(t *testing.T) {
	store := testStoreWithSession() // gate nil
	local := knownPR(1)
	local.ReviewHash = "old"
	local.Author = arSelf
	store.prs["p-1"] = []domain.PullRequest{local}
	store.comments[local.URL] = []domain.PullRequestComment{{ThreadID: "t1", ID: "c1", Author: "ann"}}
	review := ports.SCMReviewObservation{
		Threads: []ports.SCMReviewThreadObservation{{
			ID:       "t1",
			Comments: []ports.SCMReviewCommentObservation{arComment("c1", "ann"), arComment("c2", arSelf)},
		}},
	}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}},
		observations: map[string]ports.SCMObservation{},
		reviews:      map[string]ports.SCMReviewObservation{prKey(testRepo, 1): review},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(200, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.resolvedThreads) != 0 {
		t.Fatalf("gate off should not auto-resolve, got %v", provider.resolvedThreads)
	}
}

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// gitlabIntent and githubIntent are the two shapes the observer actually
// produces, taken from real rows in the live database.
var testClock = time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)

func gitlabIntent(t domain.NotificationType) Intent {
	return Intent{
		Type:               t,
		ProjectID:          "demo-ios-app",
		CreatedAt:          testClock,
		SessionID:          "demo-ios-app-30",
		SessionKind:        domain.KindWorker,
		SessionDisplayName: "cta http proto",
		PRURL:              "https://gitlab.example.com/example-org/apps/demo-ios-app/-/merge_requests/3039",
		PRNumber:           3039,
		PRTitle:            "TEAM-4600 | CTA http prototype",
		PRTargetBranch:     "develop",
		Provider:           "gitlab",
		Repo:               "example-org/apps/demo-ios-app",
	}
}

func githubIntent(t domain.NotificationType) Intent {
	return Intent{
		Type:               t,
		ProjectID:          "agent-orchestrator",
		CreatedAt:          testClock,
		SessionID:          "agent-orchestrator-149",
		SessionKind:        domain.KindWorker,
		SessionDisplayName: "ui polish batch",
		PRURL:              "https://github.com/f1uke/agent-orchestrator/pull/149",
		PRNumber:           149,
		PRTitle:            "fix(ui): polish three visible renderer defects",
		PRTargetBranch:     "main-fluke",
		Provider:           "github",
		Repo:               "f1uke/agent-orchestrator",
	}
}

// A GitLab merge request must never be called "PR #N". AO enforces the
// #GitHub / !GitLab / @session convention on its agents; its own tray has to
// follow it too, because the human runs both forges side by side and
// identical-looking ids make them indistinguishable.
func TestGitLabMergeRequestUsesBangSigil(t *testing.T) {
	for _, typ := range []domain.NotificationType{
		domain.NotificationReadyToMerge,
		domain.NotificationPRMerged,
		domain.NotificationPRClosedUnmerged,
	} {
		rec, err := enrich(gitlabIntent(typ))
		if err != nil {
			t.Fatalf("%s: enrich: %v", typ, err)
		}
		if !strings.Contains(rec.Body, "!3039") {
			t.Errorf("%s body = %q, want the MR rendered as !3039", typ, rec.Body)
		}
		if strings.Contains(rec.Body, "#3039") || strings.Contains(rec.Title, "#3039") {
			t.Errorf("%s rendered a GitLab MR with the GitHub sigil: title=%q body=%q", typ, rec.Title, rec.Body)
		}
	}
}

func TestGitHubPullRequestUsesHashSigil(t *testing.T) {
	rec, err := enrich(githubIntent(domain.NotificationPRMerged))
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if !strings.Contains(rec.Body, "#149") {
		t.Errorf("body = %q, want the PR rendered as #149", rec.Body)
	}
	if strings.Contains(rec.Body, "!149") {
		t.Errorf("body = %q, must not use the GitLab sigil for GitHub", rec.Body)
	}
}

// Provider is populated on both observation assembly paths, but the URL shape is
// decisive on its own and is the fallback the old prNoun already relied on.
func TestSigilFallsBackToURLShapeWhenProviderIsEmpty(t *testing.T) {
	in := gitlabIntent(domain.NotificationReadyToMerge)
	in.Provider = ""
	rec, err := enrich(in)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if !strings.Contains(rec.Body, "!3039") {
		t.Errorf("body = %q, want !3039 inferred from the merge_requests URL", rec.Body)
	}
}

// The number is demoted to the body, never the title: nobody recognises "3039".
func TestTitleNamesTheWorkAndOmitsTheNumber(t *testing.T) {
	cases := map[domain.NotificationType]string{
		domain.NotificationReadyToMerge:     "Ready to merge: cta http proto",
		domain.NotificationPRMerged:         "Merged: cta http proto",
		domain.NotificationPRClosedUnmerged: "Closed: cta http proto",
	}
	for typ, want := range cases {
		rec, err := enrich(gitlabIntent(typ))
		if err != nil {
			t.Fatalf("%s: enrich: %v", typ, err)
		}
		if rec.Title != want {
			t.Errorf("%s title = %q, want %q", typ, rec.Title, want)
		}
		if strings.Contains(rec.Title, "3039") {
			t.Errorf("%s title = %q, must not carry the number", typ, rec.Title)
		}
	}
}

// The board label is a string the human typed. Showing it back transformed reads
// as a spelling error, so it must survive verbatim - including labels that would
// be mangled by naive capitalisation.
func TestBoardLabelIsShownVerbatim(t *testing.T) {
	for _, label := range []string{"gl approval gate", "pr conflict recheck", "macos noti fix", "e item #4", "PROJ-2271 verify"} {
		in := gitlabIntent(domain.NotificationReadyToMerge)
		in.SessionDisplayName = label
		rec, err := enrich(in)
		if err != nil {
			t.Fatalf("%s: enrich: %v", label, err)
		}
		if want := "Ready to merge: " + label; rec.Title != want {
			t.Errorf("title = %q, want %q (label must not be transformed)", rec.Title, want)
		}
	}
}

// The hedged, negated sentence the whole change is about.
func TestReadyToMergeBodyDropsTheHedgeAndCarriesTheLocation(t *testing.T) {
	rec, err := enrich(gitlabIntent(domain.NotificationReadyToMerge))
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if strings.Contains(rec.Body, "no known") {
		t.Errorf("body = %q, must not hedge with \"no known\"", rec.Body)
	}
	want := "!3039 in demo-ios-app, targeting develop. CI passed, no unresolved review comments."
	if rec.Body != want {
		t.Errorf("body = %q, want %q", rec.Body, want)
	}
}

// The body has to earn its place. "PR #149 was merged" / "... was merged." said
// the same thing twice.
func TestBodyDoesNotRestateTheTitle(t *testing.T) {
	for _, in := range []Intent{
		gitlabIntent(domain.NotificationReadyToMerge),
		gitlabIntent(domain.NotificationPRMerged),
		gitlabIntent(domain.NotificationPRClosedUnmerged),
		githubIntent(domain.NotificationPRMerged),
		{Type: domain.NotificationNeedsInput, ProjectID: "demo-ios-app", CreatedAt: testClock, SessionID: "demo-ios-app-30", SessionKind: domain.KindWorker, SessionDisplayName: "cta http proto"},
	} {
		rec, err := enrich(in)
		if err != nil {
			t.Fatalf("%s: enrich: %v", in.Type, err)
		}
		// The state words from the title must not reappear in the body.
		state := strings.TrimSuffix(strings.SplitN(rec.Title, ":", 2)[0], ":")
		if strings.Contains(strings.ToLower(rec.Body), strings.ToLower(state)) {
			t.Errorf("%s body = %q restates the title's state %q", in.Type, rec.Body, state)
		}
		if label := strings.TrimSpace(in.SessionDisplayName); label != "" && strings.Contains(rec.Body, label) {
			t.Errorf("%s body = %q restates the title's label %q", in.Type, rec.Body, label)
		}
	}
}

func TestMergedAndClosedBodiesCarryLocationAndPRTitle(t *testing.T) {
	rec, err := enrich(githubIntent(domain.NotificationPRMerged))
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	want := "#149 in agent-orchestrator - fix(ui): polish three visible renderer defects"
	if rec.Body != want {
		t.Errorf("body = %q, want %q", rec.Body, want)
	}
}

// The target branch tells the human what they are about to merge INTO, so it
// earns its place on a ready-to-merge notification and nowhere else. On a PR
// that has already merged or closed the same fact is spent history.
func TestOnlyReadyToMergeCarriesTheTargetBranch(t *testing.T) {
	ready, err := enrich(gitlabIntent(domain.NotificationReadyToMerge))
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if !strings.Contains(ready.Body, "targeting develop") {
		t.Errorf("ready body = %q, want the merge target", ready.Body)
	}
	for _, typ := range []domain.NotificationType{domain.NotificationPRMerged, domain.NotificationPRClosedUnmerged} {
		rec, err := enrich(gitlabIntent(typ))
		if err != nil {
			t.Fatalf("%s: enrich: %v", typ, err)
		}
		if strings.Contains(rec.Body, "targeting") {
			t.Errorf("%s body = %q, must not carry a spent merge target", typ, rec.Body)
		}
	}
}

// 41 of the 42 bare-session-id notifications in the live database were
// orchestrator sessions, which have no display name by design. The renderer
// already special-cases them to the literal word; the tray must agree.
func TestOrchestratorSessionIsNamedRatherThanShowingABareID(t *testing.T) {
	rec, err := enrich(Intent{
		Type:        domain.NotificationNeedsInput,
		ProjectID:   "demo-ios-app",
		CreatedAt:   testClock,
		SessionID:   "demo-ios-app-23",
		SessionKind: domain.KindOrchestrator,
	})
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if rec.Title != "Needs you: Orchestrator" {
		t.Errorf("title = %q, want %q", rec.Title, "Needs you: Orchestrator")
	}
	// The id still has to be reachable - it is the handle the human addresses -
	// but demoted to the body and carrying its sigil.
	if rec.Body != "Paused until you reply. (@demo-ios-app-23)" {
		t.Errorf("body = %q, want the sigil-qualified session id", rec.Body)
	}
}

// A session id must never appear bare. The three legacy no-name workers in the
// live database fall here, as does any future session missing a label.
func TestSessionIDFallbackCarriesTheAtSigil(t *testing.T) {
	rec, err := enrich(Intent{
		Type:        domain.NotificationNeedsInput,
		ProjectID:   "agent-orchestrator",
		CreatedAt:   testClock,
		SessionID:   "agent-orchestrator-56",
		SessionKind: domain.KindWorker,
	})
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if rec.Title != "Needs you: @agent-orchestrator-56" {
		t.Errorf("title = %q, want the id prefixed with @", rec.Title)
	}
	// The title is already showing the id, so the body must not repeat it.
	if rec.Body != "Paused until you reply." {
		t.Errorf("body = %q, want no duplicated id", rec.Body)
	}
}

// A worker with no board label but a tracked PR is better named by the PR title
// than by its id.
func TestUnlabelledWorkerFallsBackToPRTitleBeforeSessionID(t *testing.T) {
	in := gitlabIntent(domain.NotificationPRMerged)
	in.SessionDisplayName = ""
	rec, err := enrich(in)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if rec.Title != "Merged: TEAM-4600 | CTA http prototype" {
		t.Errorf("title = %q, want the PR title as the label", rec.Title)
	}
}

func TestRepoShortKeepsTheLastSegment(t *testing.T) {
	for in, want := range map[string]string{
		"example-org/apps/demo-ios-app": "demo-ios-app",
		"f1uke/agent-orchestrator":        "agent-orchestrator",
		"solo":                            "solo",
		"trailing/slash/":                 "slash",
		"":                                "",
		"   ":                             "",
	} {
		if got := repoShort(in); got != want {
			t.Errorf("repoShort(%q) = %q, want %q", in, got, want)
		}
	}
}

// Degenerate intents must still produce a sane string rather than "in " or a
// dangling separator.
func TestBodyDegradesCleanlyWithoutRepoOrTarget(t *testing.T) {
	in := gitlabIntent(domain.NotificationReadyToMerge)
	in.Repo = ""
	in.PRTargetBranch = ""
	rec, err := enrich(in)
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	want := "!3039. CI passed, no unresolved review comments."
	if rec.Body != want {
		t.Errorf("body = %q, want %q", rec.Body, want)
	}
	if strings.Contains(rec.Body, " in .") || strings.Contains(rec.Body, ", targeting .") {
		t.Errorf("body = %q has a dangling separator", rec.Body)
	}
}

// The tray must never interrupt with a shout or an emoji: these are OS-level
// notifications that interrupt someone.
func TestCopyStaysPlain(t *testing.T) {
	for _, in := range []Intent{
		gitlabIntent(domain.NotificationReadyToMerge),
		gitlabIntent(domain.NotificationPRMerged),
		gitlabIntent(domain.NotificationPRClosedUnmerged),
		{Type: domain.NotificationNeedsInput, ProjectID: "p", CreatedAt: testClock, SessionID: "s-1", SessionKind: domain.KindWorker, SessionDisplayName: "x"},
	} {
		rec, err := enrich(in)
		if err != nil {
			t.Fatalf("%s: enrich: %v", in.Type, err)
		}
		for _, s := range []string{rec.Title, rec.Body} {
			if strings.Contains(s, "!") && !strings.Contains(s, "!3039") {
				t.Errorf("%s: %q contains an exclamation mark", in.Type, s)
			}
			for _, r := range s {
				if r > 0x2000 {
					t.Errorf("%s: %q contains non-plain rune %q", in.Type, s, r)
				}
			}
		}
	}
}

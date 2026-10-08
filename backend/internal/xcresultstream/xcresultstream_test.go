package xcresultstream

import (
	"fmt"
	"strings"
	"testing"
)

func str(v string) string { return fmt.Sprintf(`{"_type":{"_name":"String"},"_value":%q}`, v) }

func event(name, payload string) string {
	return fmt.Sprintf(`{"_type":{"_name":"StreamedEvent"},"name":%s,"structuredPayload":%s}`+"\n", str(name), payload)
}

func advisory(message string, progress float64) string {
	return event("advisoryMessage", fmt.Sprintf(`{"message":%s,"progress":{"_value":"%v"}}`, str(message), progress))
}

func section(title string) string {
	return event("logSectionCreated", fmt.Sprintf(`{"head":{"title":%s},"sectionIndex":{"_value":"3"}}`, str(title)))
}

func graph(targets ...string) string {
	var annotations []string
	for _, t := range targets {
		annotations = append(annotations, fmt.Sprintf(`{"title":%s}`, str("Target '"+t+"' in project 'Demo' (no dependencies)")))
	}
	return event("logMessageEmitted", fmt.Sprintf(`{"message":{"title":%s,"annotations":{"_values":[%s]}}}`,
		str(fmt.Sprintf("Target dependency graph (%d targets)", len(targets))), strings.Join(annotations, ",")))
}

func issue(severity, message, url string) string {
	return event("issueEmitted", fmt.Sprintf(`{"severity":%s,"issue":{"message":%s,"documentLocationInCreatingWorkspace":{"url":%s}}}`,
		str(severity), str(message), str(url)))
}

const thin = " "

func TestCountsComeFromThisBuildsOwnTargets(t *testing.T) {
	var r Reader
	r.Feed([]byte(graph("Demo", "Charts") + advisory("Charts : 41"+thin+"/"+thin+"122", 0.33)))
	got := r.Snapshot().Counts
	if got == nil || got.Done != 41 || got.Total != 122 || got.Fraction != 0.33 {
		t.Fatalf("Counts = %+v, want 41/122 at 0.33", got)
	}
}

func TestCountsFromAnotherBuildOnThisMacAreIgnored(t *testing.T) {
	var r Reader
	r.Feed([]byte(graph("Demo") + advisory("Demo : 10 / 100", 0.1) + advisory("OtherApp : 90 / 95", 0.9)))
	if got := r.Snapshot().Counts; got == nil || got.Done != 10 {
		t.Fatalf("Counts = %+v, want the Demo reading 10/100 kept", got)
	}
}

func TestCountsBeforeTheTargetGraphAreNotTrusted(t *testing.T) {
	var r Reader
	r.Feed([]byte(advisory("Demo : 10 / 100", 0.1)))
	if got := r.Snapshot().Counts; got != nil {
		t.Fatalf("Counts = %+v, want nil before this build named its targets", got)
	}
}

func TestGenericMessagesMoveNoCounts(t *testing.T) {
	var r Reader
	r.Feed([]byte(graph("Demo") + advisory(" : Planning 865 / 3169", 0.04) + advisory("Finishing... : 10 / 20", 1)))
	if got := r.Snapshot().Counts; got != nil {
		t.Fatalf("Counts = %+v, want nil: planning and finishing are not this build's task counts", got)
	}
}

func TestCountsNeverGoBackwards(t *testing.T) {
	var r Reader
	r.Feed([]byte(graph("Demo") + advisory("Demo : 50 / 100", 0.5) + advisory("Demo : 40 / 100", 0.4)))
	if got := r.Snapshot().Counts; got.Done != 50 || got.Fraction != 0.5 {
		t.Fatalf("Counts = %+v, want 50/100 at 0.5 held", got)
	}
}

func TestAnEventSplitAcrossTwoReadsIsDecodedOnce(t *testing.T) {
	stream := graph("Demo") + advisory("Demo : 7 / 9", 0.7)
	var r Reader
	r.Feed([]byte(stream[:len(stream)-25]))
	if r.Snapshot().Counts != nil {
		t.Fatal("a half-written event must not be read")
	}
	r.Feed([]byte(stream[len(stream)-25:]))
	if got := r.Snapshot().Counts; got == nil || got.Done != 7 {
		t.Fatalf("Counts = %+v, want 7/9 once the event is complete", got)
	}
}

func TestPhaseFollowsTheSignificantTasks(t *testing.T) {
	cases := []struct {
		titles []string
		want   Phase
	}{
		{[]string{"Prepare packages"}, PhaseResolving},
		{[]string{"Create build description"}, PhasePlanning},
		{[]string{"Compile F1.swift (arm64)"}, PhaseCompiling},
		{[]string{"Compile F1.swift (arm64)", "Copy Demo.modulemap", "Write all-product-headers.yaml"}, PhaseCompiling},
		{[]string{"Compiling Clang module _Builtin_stdbool"}, PhaseCompiling},
		{[]string{"Compile F1.swift (arm64)", "Link Demo (arm64)"}, PhaseLinking},
		{[]string{"Link Demo (arm64)", "Sign Demo.app"}, PhaseSigning},
		{[]string{"Copy Demo.modulemap"}, PhaseBuilding},
	}
	for _, c := range cases {
		var r Reader
		for _, title := range c.titles {
			r.Feed([]byte(section(title)))
		}
		if got := r.Snapshot().Phase; got != c.want {
			t.Errorf("after %q phase = %q, want %q", c.titles, got, c.want)
		}
	}
}

func TestIssuesAreCountedAndTheFirstErrorsKeptWithOneBasedPositions(t *testing.T) {
	var r Reader
	r.Feed([]byte(
		issue("warning", "Variable 'x' was never used", "file:///w/Sources/A.swift#EndingColumnNumber=4&EndingLineNumber=2&StartingColumnNumber=4&StartingLineNumber=2&Timestamp=1") +
			issue("error", "Cannot find 'y' in scope", "file:///w/Sources/My%20File.swift#EndingColumnNumber=45&EndingLineNumber=0&StartingColumnNumber=37&StartingLineNumber=0&Timestamp=1") +
			issue("error", "Missing return", ""),
	))
	snap := r.Snapshot()
	if snap.Errors != 2 || snap.Warnings != 1 {
		t.Fatalf("errors=%d warnings=%d, want 2 and 1", snap.Errors, snap.Warnings)
	}
	first := snap.Issues[0]
	want := Issue{Severity: "error", Message: "Cannot find 'y' in scope", File: "/w/Sources/My File.swift", Line: 1, Column: 38}
	if first != want {
		t.Fatalf("first issue = %+v, want %+v", first, want)
	}
	if len(snap.Issues) != 3 || snap.Issues[1].File != "" || snap.Issues[2].Severity != "warning" {
		t.Fatalf("issues = %+v, want errors first in arrival order, then warnings", snap.Issues)
	}
}

func TestIssuesAreCappedButStillCounted(t *testing.T) {
	var r Reader
	for i := range MaxIssues + 5 {
		r.Feed([]byte(issue("error", fmt.Sprintf("e%d", i), "")))
	}
	snap := r.Snapshot()
	if snap.Errors != MaxIssues+5 || len(snap.Issues) != MaxIssues {
		t.Fatalf("errors=%d kept=%d, want %d counted and %d kept", snap.Errors, len(snap.Issues), MaxIssues+5, MaxIssues)
	}
}

func TestAMalformedStreamStopsReadingWithoutPanicking(t *testing.T) {
	var r Reader
	r.Feed([]byte(graph("Demo") + "{not json}\n" + advisory("Demo : 1 / 2", 0.5)))
	if r.Snapshot().Counts != nil {
		t.Fatal("events after a malformed one must not be trusted")
	}
}

func TestCountsSeenWhileAnotherBuildPostedAreNotKept(t *testing.T) {
	var r Reader
	r.Feed([]byte(graph("Demo") + advisory("Demo : 10 / 100", 0.1)))
	r.Share(true)
	r.Feed([]byte(advisory("Demo : 95 / 100", 0.95)))
	if got := r.Snapshot().Counts; got != nil {
		t.Fatalf("Counts = %+v while shared, want none", got)
	}
	r.Share(false)
	if got := r.Snapshot().Counts; got != nil {
		t.Fatalf("Counts = %+v right after sharing ended, want a fresh start", got)
	}
	r.Feed([]byte(advisory("Demo : 30 / 100", 0.3)))
	if got := r.Snapshot().Counts; got == nil || got.Done != 30 {
		t.Fatalf("Counts = %+v, want 30/100: the other build's 95 must not hold it down", got)
	}
}

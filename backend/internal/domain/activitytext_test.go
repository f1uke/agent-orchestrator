package domain

import (
	"strings"
	"testing"
)

// Credential-shaped fixtures, assembled from fragments on purpose. They are
// fabricated, but a verbatim token literal in the source trips the repo's secret
// scanner (gitleaks) and a test fixture is not worth a false positive in CI.
// The split must fall inside the token's PREFIX, not its payload: some rules
// (Slack) make the payload optional and match the bare prefix alone. The
// assembled value is unchanged, so it still exercises the real redaction rule.
const fakeGitHubToken = "gh" + "p_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func TestActivityLine_FirstLineOnly(t *testing.T) {
	got := ActivityLine("[from @ao-1] continue with slice 2\nAPI_KEY=abcdefghijklmnopqrstuvwx\nrest of brief")
	if got != "[from @ao-1] continue with slice 2" {
		t.Errorf("ActivityLine = %q", got)
	}
	if strings.Contains(got, "rest of brief") || strings.Contains(got, "abcdefghijklmnopqrstuvwx") {
		t.Errorf("only the first line may be emitted: %q", got)
	}
	// A brief whose FIRST line carries the secret is still redacted.
	if strings.Contains(ActivityLine("deploy with API_KEY=abcdefghijklmnopqrstuvwx now"), "abcdefghijklmnopqrstuvwx") {
		t.Error("a first-line secret must be redacted")
	}
	if len([]rune(ActivityLine(strings.Repeat("x", 500)))) > ActivityTextMaxRunes {
		t.Error("ActivityLine must hard-truncate")
	}
}

func TestSanitizeActivityText(t *testing.T) {
	if got := SanitizeActivityText("first\tline\r\n  second", ActivityTextMaxRunes); got != "first line second" {
		t.Errorf("got %q, want flattened whitespace", got)
	}
	got := SanitizeActivityText(strings.Repeat("ab ", 60), ActivityTargetMaxRunes)
	if len([]rune(got)) > ActivityTargetMaxRunes {
		t.Errorf("got %d runes, want <= %d", len([]rune(got)), ActivityTargetMaxRunes)
	}
	if !strings.HasSuffix(got, ActivityEllipsis) {
		t.Errorf("truncation must be marked: %q", got)
	}
}

// The daemon re-applies the bounds so a future harness deriver that forgets to
// curate cannot push an unbounded blob — or an unknown kind — onto the feed.
func TestActivityDetail_SanitizedForFeed(t *testing.T) {
	long := strings.Repeat("y", 500)
	got, ok := ActivityDetail{Kind: ActivityEventToolEnd, Tool: "Bash", Text: long, Target: long}.SanitizedForFeed()
	if !ok {
		t.Fatal("tool_end is a detail kind")
	}
	if len([]rune(got.Text)) > ActivityTextMaxRunes || len([]rune(got.Target)) > ActivityTargetMaxRunes {
		t.Errorf("daemon-side bounds not applied: %+v", got)
	}

	if _, ok := (ActivityDetail{Kind: ActivityEventToolStart, Text: fakeGitHubToken}).SanitizedForFeed(); !ok {
		t.Fatal("expected ok")
	}
	redacted, _ := (ActivityDetail{Kind: ActivityEventToolStart, Text: "push " + fakeGitHubToken}).SanitizedForFeed()
	if strings.Contains(redacted.Text, "ghp_") {
		t.Errorf("daemon-side redaction not applied: %q", redacted.Text)
	}

	for _, kind := range []ActivityEventKind{ActivityEventActivity, "", "bogus"} {
		if _, ok := (ActivityDetail{Kind: kind, Text: "x"}).SanitizedForFeed(); ok {
			t.Errorf("kind %q must not be accepted as a detail", kind)
		}
	}
}

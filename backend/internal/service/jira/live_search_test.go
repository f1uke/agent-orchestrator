package jira

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	jiraadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/jira"
)

// TestLive_SearchFindsHyphenatedText drives the REAL Jira through the real search
// path (buildJQL → REST) to prove the thing a fake cannot: that the JQL this
// service emits actually matches issues in Jira's text index.
//
// This exists because the bug it guards was invisible to fake-based tests. The
// operand of `~` is handed to a Lucene-style parser where `-` means NOT, so
// `summary ~ "checkout*"` returned ZERO rows against a project full of "E-Item"
// issues — and every unit test still passed, because they only assert the string we
// built. Worse, the obvious fix (backslash-escaping to `e\-item*`) also returns
// zero: a wildcard term bypasses the analyzer, and the index holds `e` + `item`,
// never the single token `checkout`. Only a live query distinguishes the three.
//
// Gated behind AO_JIRA_LIVE=1 so CI never runs it (no credential there). Read-only.
// Nothing is hardcoded — supply a project and a substring you know exists, with the
// separator of your choice:
//
//	AO_JIRA_LIVE=1 AO_JIRA_LIVE_PROJECT=PROJ AO_JIRA_LIVE_TEXT=checkout \
//	  go test -run TestLive_SearchFindsHyphenatedText ./internal/service/jira/ -v
func TestLive_SearchFindsHyphenatedText(t *testing.T) {
	if os.Getenv("AO_JIRA_LIVE") != "1" {
		t.Skip("set AO_JIRA_LIVE=1 to run the live Jira search test")
	}
	project := os.Getenv("AO_JIRA_LIVE_PROJECT")
	text := os.Getenv("AO_JIRA_LIVE_TEXT")
	if project == "" || text == "" {
		t.Skip("set AO_JIRA_LIVE_PROJECT=<KEY> and AO_JIRA_LIVE_TEXT=<text with a separator>")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	svc := New(nil, nil, nil, jiraadapter.NewClient())
	rows, err := svc.Search(ctx, SearchParams{Project: project, Text: text})
	if err != nil {
		t.Fatalf("live Search(%s, %q): %v", project, text, err)
	}
	if len(rows) == 0 {
		t.Fatalf("live Search(%s, %q) returned NO rows — the text operand is being "+
			"parsed as operators again, or the terms do not exist in this project", project, text)
	}
	// The separator-free words must actually appear in what came back, or we matched
	// something unrelated and the row count alone would be a false positive.
	for _, term := range textTerms(text) {
		if len(term) < 2 {
			continue // single letters are commonly analyzed away (stopwords)
		}
		found := false
		for _, r := range rows {
			if strings.Contains(strings.ToLower(r.Title), term) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no row's title contains the term %q — matched on description text only?", term)
		}
	}
	t.Logf("LIVE search %s %q → %d row(s); first: %s %q", project, text, len(rows), rows[0].Key, rows[0].Title)
}

// TestLive_SearchBareNumberResolvesKey proves the bare-number path against real
// Jira: with a project selected, a number resolves to that project's issue rather
// than being searched as prose (which can never match, since a key is not part of
// an issue's text). Gated and read-only, as above.
//
//	AO_JIRA_LIVE=1 AO_JIRA_LIVE_PROJECT=PROJ AO_JIRA_LIVE_NUMBER=2271 \
//	  go test -run TestLive_SearchBareNumberResolvesKey ./internal/service/jira/ -v
func TestLive_SearchBareNumberResolvesKey(t *testing.T) {
	if os.Getenv("AO_JIRA_LIVE") != "1" {
		t.Skip("set AO_JIRA_LIVE=1 to run the live Jira search test")
	}
	project := os.Getenv("AO_JIRA_LIVE_PROJECT")
	number := os.Getenv("AO_JIRA_LIVE_NUMBER")
	if project == "" || number == "" {
		t.Skip("set AO_JIRA_LIVE_PROJECT=<KEY> and AO_JIRA_LIVE_NUMBER=<existing issue number>")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	svc := New(nil, nil, nil, jiraadapter.NewClient())
	rows, err := svc.Search(ctx, SearchParams{Project: project, Text: number})
	if err != nil {
		t.Fatalf("live Search(%s, %q): %v", project, number, err)
	}
	want := strings.ToUpper(project) + "-" + number
	if len(rows) != 1 || rows[0].Key != want {
		t.Fatalf("live Search(%s, %q) = %d row(s), want exactly %s", project, number, len(rows), want)
	}
	t.Logf("LIVE bare number %q + project %s → %s %q", number, project, rows[0].Key, rows[0].Title)
}

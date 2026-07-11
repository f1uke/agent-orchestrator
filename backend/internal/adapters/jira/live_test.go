package jira

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLive_RealCLI exercises the real jira-cli shell-out end to end (binary on
// PATH + keychain auth + REST v3 parse). Gated behind AO_JIRA_LIVE=1 so it never
// runs in CI (no jira binary / no auth there). Supply a key from your own Jira —
// nothing is hardcoded. Run locally with:
//
//	AO_JIRA_LIVE=1 AO_JIRA_LIVE_KEY=<YOUR-KEY> go test -run TestLive_RealCLI ./internal/adapters/jira/ -v
func TestLive_RealCLI(t *testing.T) {
	if os.Getenv("AO_JIRA_LIVE") != "1" {
		t.Skip("set AO_JIRA_LIVE=1 to run the live jira-cli integration test")
	}
	key := os.Getenv("AO_JIRA_LIVE_KEY")
	if key == "" {
		t.Skip("set AO_JIRA_LIVE_KEY=<YOUR-KEY> to pick which issue to fetch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	iss, err := NewClient().Get(ctx, key)
	if err != nil {
		t.Fatalf("live Get(%s): %v", key, err)
	}
	if iss.Key != key || iss.Title == "" || iss.Status == "" {
		t.Fatalf("live issue looks empty: %+v", iss)
	}
	t.Logf("LIVE %s: %q [%s/%s] desc-nodes=%d subtasks=%d sprint=%v url=%s",
		iss.Key, iss.Title, iss.Status, iss.StatusCategory, len(iss.Description), len(iss.Subtasks), iss.Sprint != nil, iss.URL)
}

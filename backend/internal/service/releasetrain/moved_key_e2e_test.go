package releasetrain_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	svc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/rttest"
)

type movedBoard struct {
	Board struct {
		LLBs []struct {
			Branches []struct {
				Branch string `json:"branch"`
				Cards  []struct {
					Key string `json:"key"`
				} `json:"cards"`
				Moved []struct {
					Key     string `json:"key"`
					MovedTo string `json:"movedTo"`
				} `json:"moved"`
			} `json:"branches"`
		} `json:"llbs"`
		Alerts struct {
			Hits []struct {
				Rule string `json:"rule"`
				Copy struct {
					What string `json:"what"`
				} `json:"copy"`
				Subject struct {
					Kind string `json:"kind"`
					IID  int    `json:"iid"`
				} `json:"subject"`
			} `json:"hits"`
		} `json:"alerts"`
	} `json:"board"`
}

func movedKeyWorld(t *testing.T, mrs ...rttest.MR) *world {
	w := newSprintWorld(t)
	w.gl.SetMRs(1001, append(sprintMRs(), mrs...))
	w.jira.Set(`/rest/api/3/search/jql?jql=key = OLD-1 ORDER BY key`, `{"issues":[{"key":"NEW-9","fields":{"summary":"Unrelated web card","status":{"name":"Done","statusCategory":{"key":"done"}},"issuetype":{"subtask":false}}}]}`)
	return w
}

func (w *world) board() movedBoard {
	w.t.Helper()
	for range 6 {
		w.pass()
		w.now = w.now.Add(svc.Tick)
	}
	board := svc.NewBoardService(svc.BoardDeps{Config: svc.NewConfigLoader(w.configPath), Store: w.store, Clock: func() time.Time { return w.now }})
	body, _, err := board.Board(context.Background(), "", "")
	if err != nil {
		w.t.Fatal(err)
	}
	var resp movedBoard
	if err := json.Unmarshal(body, &resp); err != nil {
		w.t.Fatal(err)
	}
	return resp
}

func (b movedBoard) wallet(t *testing.T) (cards []string, moved []string) {
	t.Helper()
	for _, g := range b.Board.LLBs {
		for _, br := range g.Branches {
			if br.Branch != "feature/demo-wallet" {
				continue
			}
			for _, c := range br.Cards {
				cards = append(cards, c.Key)
			}
			for _, m := range br.Moved {
				moved = append(moved, m.Key+">"+m.MovedTo)
			}
			return cards, moved
		}
	}
	t.Fatal("no feature/demo-wallet long-lived branch on the Board")
	return nil, nil
}

func (b movedBoard) hits(rule string) []string {
	var out []string
	for _, h := range b.Board.Alerts.Hits {
		if h.Rule == rule {
			out = append(out, h.Subject.Kind+":"+h.Copy.What)
		}
	}
	return out
}

var oldKeyMR = rttest.MR{IID: 303, Title: "OLD-1 fix the top-up limit", Source: "feature/OLD-1", Target: "feature/demo-wallet", State: "merged",
	Author: "Dee Example", Username: "dee", CreatedAt: at("2026-10-05T02:00:00Z"), UpdatedAt: at("2026-10-06T03:00:00Z")}

func TestAKeyJiraMovedIsNoCardOfTheLongLivedBranchButAnMRHygieneHit(t *testing.T) {
	w := movedKeyWorld(t, oldKeyMR)
	w.jira.Set(`/rest/api/3/search/jql?jql=key in (DEMO-30, OLD-1) ORDER BY key`, `{"issues":[`+
		`{"key":"DEMO-30","fields":{"summary":"Wallet","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},"issuetype":{"subtask":false},"assignee":{"accountId":"acc-dee","displayName":"Dee Example"}}},`+
		`{"key":"NEW-9","fields":{"summary":"Unrelated web card","status":{"name":"Done","statusCategory":{"key":"done"}},"issuetype":{"subtask":false}}}]}`)
	b := w.board()

	cards, moved := b.wallet(t)
	if slices.Contains(cards, "OLD-1") || slices.Contains(cards, "NEW-9") || !slices.Contains(cards, "DEMO-30") {
		t.Errorf("wallet cards = %v; want DEMO-30 only: OLD-1 now names the unrelated NEW-9", cards)
	}
	if want := []string{"OLD-1>NEW-9"}; !slices.Equal(moved, want) {
		t.Errorf("wallet moved = %v, want %v", moved, want)
	}
	hits := b.hits("G6")
	if len(hits) != 1 || !strings.HasPrefix(hits[0], "mr:MR !303 ") || !strings.Contains(hits[0], "NEW-9") {
		t.Errorf("G6 hits = %q; want one hit on !303 naming NEW-9", hits)
	}
}

func TestAnMRWithAMovedKeyAndAValidKeyKeepsTheValidCard(t *testing.T) {
	both := oldKeyMR
	both.Title = "OLD-1 DEMO-31 top-up limit"
	w := movedKeyWorld(t, both)
	w.jira.Set(`/rest/api/3/search/jql?jql=key in (DEMO-30, DEMO-31, OLD-1) ORDER BY key`, `{"issues":[`+
		`{"key":"DEMO-30","fields":{"summary":"Wallet","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},"issuetype":{"subtask":false}}},`+
		`{"key":"DEMO-31","fields":{"summary":"Top-up limit","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},"issuetype":{"subtask":false}}},`+
		`{"key":"NEW-9","fields":{"summary":"Unrelated web card","status":{"name":"Done","statusCategory":{"key":"done"}},"issuetype":{"subtask":false}}}]}`)
	b := w.board()

	cards, moved := b.wallet(t)
	if want := []string{"DEMO-30", "DEMO-31"}; !slices.Equal(cards, want) {
		t.Errorf("wallet cards = %v, want %v", cards, want)
	}
	if want := []string{"OLD-1>NEW-9"}; !slices.Equal(moved, want) {
		t.Errorf("wallet moved = %v, want %v", moved, want)
	}
	if hits := b.hits("G6"); len(hits) != 1 {
		t.Errorf("G6 hits = %q; want one hit on !303", hits)
	}
}

func TestAKeyJiraKeepsIsACardAsBefore(t *testing.T) {
	kept := oldKeyMR
	kept.Title, kept.Source = "DEMO-31 top-up limit", "feature/DEMO-31"
	w := movedKeyWorld(t, kept)
	w.jira.Set(`/rest/api/3/search/jql?jql=key in (DEMO-30, DEMO-31) ORDER BY key`, `{"issues":[`+
		`{"key":"DEMO-30","fields":{"summary":"Wallet","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},"issuetype":{"subtask":false}}},`+
		`{"key":"DEMO-31","fields":{"summary":"Top-up limit","status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},"issuetype":{"subtask":false}}}]}`)
	b := w.board()

	cards, moved := b.wallet(t)
	if want := []string{"DEMO-30", "DEMO-31"}; !slices.Equal(cards, want) || len(moved) != 0 {
		t.Errorf("wallet cards = %v moved = %v; want %v and nothing moved", cards, moved, want)
	}
	if hits := b.hits("G6"); len(hits) != 0 {
		t.Errorf("G6 hits = %q; want none", hits)
	}
}

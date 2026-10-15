package releasetrain_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	svc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain/rttest"
)

func TestACompareThePlanDroppedMidBackfillLeavesGitLabFresh(t *testing.T) {
	w := newWorld(t)
	early, landed := at("2026-04-15T03:00:00Z"), at("2026-10-13T03:00:00Z")
	w.gl.SetMRs(1001, append(baseMRs(),
		rttest.MR{IID: 501, Title: "DEMO-50 card", Source: "feature/DEMO-50", Target: "feature/demo-card", State: "merged",
			Author: "Rae Example", Username: "rae", CreatedAt: early, UpdatedAt: early, MergedAt: &early},
		rttest.MR{IID: 502, Title: "Land demo card", Source: "feature/demo-card", Target: "feature/sprint-2026-20", State: "merged",
			Author: "Rae Example", Username: "rae", CreatedAt: landed, UpdatedAt: landed, MergedAt: &landed},
	))

	w.pass()
	w.now = w.now.Add(svc.Tick)
	w.gl.Fail("/projects/1001/repository/compare", http.StatusServiceUnavailable, nil)
	w.pass()
	w.gl.Heal()
	for range 16 {
		w.now = w.now.Add(svc.Tick)
		w.pass()
	}
	var compares []string
	for _, r := range w.incomplete() {
		if strings.HasPrefix(r, "gitlab.") {
			compares = append(compares, r)
		}
	}
	if len(compares) != 1 || !strings.HasPrefix(compares[0], "gitlab.compare p:1001:develop..feature/demo-card unavailable") {
		t.Fatalf("incomplete gitlab = %q; want only the demo-card compare, still stored and never retried", compares)
	}

	board := svc.NewBoardService(svc.BoardDeps{Config: svc.NewConfigLoader(w.configPath), Store: w.store, Clock: func() time.Time { return w.now }})
	body, _, err := board.Board(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Board struct {
			Sources []struct {
				System string `json:"system"`
				AsOf   string `json:"asOf"`
				Issues []struct {
					Kind       string `json:"kind"`
					Scope      string `json:"scope"`
					ErrorClass string `json:"errorClass"`
				} `json:"issues"`
			} `json:"sources"`
		} `json:"board"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	for _, s := range resp.Board.Sources {
		if s.System == "gitlab" && (len(s.Issues) > 0 || s.AsOf == "") {
			t.Fatalf("gitlab asOf %q issues %+v; want fresh: the demo-card compare was planned while MRs backfilled, failed once, and is no longer planned once its landing was read", s.AsOf, s.Issues)
		}
	}
}

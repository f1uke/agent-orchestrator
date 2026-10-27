package releasetrain_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	svc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
)

func TestSprintRulesFireEndToEnd(t *testing.T) {
	w := newSprintWorld(t)
	w.settle()
	board := svc.NewBoardService(svc.BoardDeps{Config: svc.NewConfigLoader(w.configPath), Store: w.store, Clock: func() time.Time { return w.now }})
	body, _, err := board.Board(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Board struct {
			Alerts struct {
				Hits []struct {
					ID string `json:"id"`
				} `json:"hits"`
			} `json:"alerts"`
		} `json:"board"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	var g []string
	for _, h := range resp.Board.Alerts.Hits {
		if strings.HasPrefix(h.ID, "G") || strings.HasPrefix(h.ID, "L8") {
			g = append(g, h.ID)
		}
	}
	if strings.Join(g, " ") != "G5~pf/21-2026/demo/merged G1~c/DEMO-360 G9~mr/di/401 G9~mr/di/402" {
		t.Fatalf("sprint hits = %v; want G5 for DEMO-362 missing from the train 21 page, G1 for DEMO-360, merged but in progress, and G9 for the two sprint 21 MRs without a milestone", g)
	}
}

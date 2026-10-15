package releasetrain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBoardJSONCarriesSprintRowsForTheThreeLineLabel(t *testing.T) {
	f := newFixture(t, today)
	f.version("di", "8", "3.6.0", "2026-10-26", "2026-11-02")
	s := demoSprint(f)
	f.sprintCard(s, "DEMO-1", "In Progress", true)
	f.mr("di", "DEMO-1 a", "feat/DEMO-1", "feature/sprint-7-2026", MROpened, "2026-10-07", "")
	f.sprintCard(s, "DEMO-2", "In Progress", false)
	f.mr("da", "DEMO-2 a", "feat/DEMO-2", "develop", MROpened, "2026-10-07", "")
	f.sprintCard(s, "DEMO-3", "Done", false)
	raw, err := json.Marshal(f.reconcile())
	if err != nil {
		t.Fatal(err)
	}
	var w struct {
		Sprints []struct {
			Board  struct{ Key string }   `json:"board"`
			Sprint struct{ Start string } `json:"sprint"`
			Number int                    `json:"number"`
			Counts map[string]int         `json:"counts"`
			Dests  []struct {
				To    map[string]any `json:"to"`
				Count int            `json:"count"`
			} `json:"dests"`
			Cards []struct {
				Key string `json:"key"`
				URL string `json:"url"`
				MRs []struct {
					App  string `json:"app"`
					IID  int    `json:"iid"`
					URL  string `json:"url"`
					Dest string `json:"dest"`
				} `json:"mrs"`
				Routes []struct {
					To map[string]any `json:"to"`
				} `json:"routes"`
			} `json:"cards"`
		} `json:"sprints"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	if len(w.Sprints) != 1 || w.Sprints[0].Board.Key != "DEMO" || w.Sprints[0].Number != 7 || w.Sprints[0].Sprint.Start != "2026-10-06" {
		t.Fatalf("sprints = %+v; want the DEMO 7 row", w.Sprints)
	}
	row := w.Sprints[0]
	if row.Counts["new"] != 1 || row.Counts["carriedWithMrs"] != 1 || row.Counts["carriedDone"] != 1 {
		t.Fatalf("counts = %v; want 1 new, 1 carried with MRs, 1 carried done", row.Counts)
	}
	var dests []string
	for _, d := range row.Dests {
		dests = append(dests, d.To["kind"].(string))
		if tr, ok := d.To["train"]; ok {
			dests[len(dests)-1] += " " + tr.(string)
		}
	}
	if strings.Join(dests, ", ") != "train 8-2026, off" {
		t.Fatalf("dests = %v; want the next train then off flow", dests)
	}
	c := row.Cards[0]
	if c.URL != "https://jira.example.com/browse/DEMO-1" || len(c.MRs) != 1 || c.MRs[0].Dest != "sprint_open" || c.MRs[0].URL == "" || c.Routes[0].To["kind"] != "train" {
		t.Fatalf("DEMO-1 = %+v; want its URL, one MR coloured sprint_open with its link, and a train route", c)
	}
}

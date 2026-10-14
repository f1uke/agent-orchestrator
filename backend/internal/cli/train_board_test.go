package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

const trainBoardJSON = `{
  "config": {"state": "ok", "path": "/data/release-train.json"},
  "board": {
    "today": "2026-10-14",
    "apps": [
      {"id": "di", "name": "Demo iOS", "family": "demo", "platform": "ios"},
      {"id": "da", "name": "Demo Android", "family": "demo", "platform": "android"}
    ],
    "sources": [
      {"system": "gitlab", "resources": 12, "asOf": "2026-10-14T02:59:00Z", "issues": []},
      {"system": "jira", "resources": 6, "asOf": "2026-10-14T02:40:00Z", "issues": [
        {"kind": "jira.versions", "scope": "project", "completeAt": "2026-10-14T02:40:00Z", "failures": 2, "errorClass": "rate_limited"}
      ]}
    ],
    "trains": [
      {"id": "20-2026", "label": "#20", "phase": "past", "families": ["demo"], "freeze": "2026-10-05", "publish": "2026-10-12",
       "versions": [
         {"train": "20-2026", "app": "di", "version": "2.3.0", "stages": [
           {"key": "freeze", "status": "done", "detail": "", "reads": []},
           {"key": "publish", "status": "done", "detail": "", "reads": []}
         ], "cards": []}
       ]},
      {"id": "21-2026", "label": "#21", "phase": "current", "families": ["demo"], "freeze": "2026-10-12", "publish": "2026-10-19",
       "versions": [
         {"train": "21-2026", "app": "di", "version": "2.4.0",
          "releaseMr": {"mr": {"iid": 77, "author": "Example Person"}},
          "jira": {"approvers": [{"status": "APPROVED"}, {"status": "PENDING"}]},
          "stages": [
            {"key": "freeze", "status": "done", "detail": "Release MR !77", "reads": []},
            {"key": "scan", "status": "failed", "detail": "Pipeline failed: build_test", "reads": []},
            {"key": "approve", "status": "waiting", "detail": "1 of 2 approved", "reads": []}
          ],
          "cards": [
            {"key": "DEMO-1", "code": {"kind": "merged", "mrs": [3]}},
            {"key": "DEMO-2", "code": {"kind": "open", "mrs": [4]}}
          ]},
         {"train": "21-2026", "app": "da", "version": "2.4.0",
          "stages": [
            {"key": "freeze", "status": "late", "detail": "Freeze was 2 day(s) ago", "reads": []},
            {"key": "scan", "status": "unknown", "detail": "", "reads": [], "missing": [{"kind": "gitlab.mrs", "scope": "p:1002"}]}
          ],
          "cards": []}
       ]}
    ],
    "hotfixes": [],
    "mismatches": [],
    "alerts": {
      "badge": {"count": 3, "worst": "block", "unknown": 2},
      "hits": [
        {"id": "C1~v/21-2026/di", "rule": "C1", "severity": "block", "train": "21-2026", "app": "di", "text": "!77 failed: build_test", "bucket": "badge"},
        {"id": "B1~v/21-2026/da", "rule": "B1", "severity": "risk", "train": "21-2026", "app": "da", "text": "Freeze was 2 day(s) ago", "bucket": "badge"},
        {"id": "A3~m/da/9", "rule": "A3", "severity": "data", "app": "da", "text": "Milestone has no Jira release", "bucket": "badge"},
        {"id": "E1~v/20-2026/di", "rule": "E1", "severity": "hygiene", "train": "20-2026", "app": "di", "text": "Jira release still unreleased", "bucket": "history"},
        {"id": "E2~v/20-2026/di", "rule": "E2", "severity": "hygiene", "train": "20-2026", "app": "di", "text": "1 card(s) not Done", "bucket": "hidden"}
      ],
      "rules": [
        {"rule": "C5", "app": "da", "on": true, "outcome": {"kind": "unknown", "missing": [{"kind": "gitlab.mr_detail", "scope": "p:1002!41"}]}},
        {"rule": "D1", "app": "di", "on": true, "outcome": {"kind": "unknown", "missing": [{"kind": "jira.versions", "scope": "project"}]}},
        {"rule": "C2", "app": "da", "on": false, "outcome": {"kind": "unknown", "missing": []}},
        {"rule": "C1", "app": "di", "on": true, "outcome": {"kind": "fired", "hits": ["C1~v/21-2026/di"]}}
      ]
    }
  }
}`

func runTrain(t *testing.T, body string, args ...string) (string, error) {
	t.Helper()
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, http.StatusOK, body)
	writeRunFileFor(t, cfg, srv)
	deps := aliveDeps()
	deps.Now = func() time.Time { return time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC) }
	out, _, err := executeCLI(t, deps, append([]string{"train"}, args...)...)
	if capture.method != http.MethodGet || capture.path != "/api/v1/release-trains/board" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	return out, err
}

func TestTrainPrintsSourcesTrainsAttentionAndUnknown(t *testing.T) {
	out, err := runTrain(t, trainBoardJSON)
	if err != nil {
		t.Fatal(err)
	}
	want := `Release trains · Wed 14 Oct 2026
Sources  gitlab 1m · jira 20m, 1 of 6 not fresh: jira.versions rate_limited

#21  freeze Mon 12 Oct → publish Mon 19 Oct  current
  Demo iOS      2.4.0  scan    failed  !77 Example Person  1/2 merged  approvals 1/2
  Demo Android  2.4.0  freeze  late    -                   0/0 merged  approvals -

Needs attention  1 block · 1 data · 1 risk   (1 in history, 1 hidden)
  BLOCK  C1  #21 Demo iOS 2.4.0  !77 failed: build_test  C1~v/21-2026/di
Unknown  C5 Demo Android · D1 Demo iOS
`
	if out != want {
		t.Fatalf("output =\n%s\nwant\n%s", out, want)
	}
}

func TestTrainFiltersByTrainAndApp(t *testing.T) {
	out, err := runTrain(t, trainBoardJSON, "--train", "20")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "#20  freeze Mon 5 Oct → publish Mon 12 Oct  past\n  Demo iOS  2.3.0  publish  done") || strings.Contains(out, "#21") {
		t.Fatalf("--train 20 =\n%s", out)
	}
	if !strings.Contains(out, "Needs attention  none") {
		t.Fatalf("--train 20 attention =\n%s", out)
	}

	out, err = runTrain(t, trainBoardJSON, "--app", "da")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Demo iOS") || !strings.Contains(out, "Demo Android  2.4.0  freeze  late") || !strings.Contains(out, "1 data · 1 risk") || strings.Contains(out, "BLOCK") {
		t.Fatalf("--app da =\n%s", out)
	}

	if _, err := runTrain(t, trainBoardJSON, "--app", "zz"); err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "di, da") {
		t.Fatalf("--app zz err = %v, want a usage error naming the apps", err)
	}
}

func TestTrainGateExitsOneOnAnActiveBlockHit(t *testing.T) {
	_, err := runTrain(t, trainBoardJSON, "--gate")
	if ExitCode(err) != 1 || !strings.Contains(err.Error(), "1 active block hit") {
		t.Fatalf("--gate with a block = %v (exit %d)", err, ExitCode(err))
	}
	if _, err := runTrain(t, trainBoardJSON, "--gate", "--app", "da"); err != nil {
		t.Fatalf("--gate on an app with no block = %v", err)
	}
}

func TestTrainJSONIsTheDaemonsBodyFiltered(t *testing.T) {
	out, err := runTrain(t, trainBoardJSON, "--json", "--app", "da")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Board struct {
			Trains []struct {
				Versions []struct {
					App    string            `json:"app"`
					Stages []json.RawMessage `json:"stages"`
				} `json:"versions"`
			} `json:"trains"`
			Alerts struct {
				Hits []struct{ ID string } `json:"hits"`
			} `json:"alerts"`
		} `json:"board"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json = %s: %v", out, err)
	}
	if len(got.Board.Trains) != 1 || len(got.Board.Trains[0].Versions) != 1 || got.Board.Trains[0].Versions[0].App != "da" || len(got.Board.Trains[0].Versions[0].Stages) != 2 {
		t.Fatalf("--json --app da trains = %+v", got.Board.Trains)
	}
	if len(got.Board.Alerts.Hits) != 2 {
		t.Fatalf("--json --app da hits = %+v", got.Board.Alerts.Hits)
	}
	if !strings.Contains(out, `"releaseMr"`) && !strings.Contains(out, `"stages"`) {
		t.Fatalf("--json dropped fields the CLI does not render: %s", out)
	}
}

func TestTrainAlertsListsActiveHitsAndOnRequestHistoryAndHidden(t *testing.T) {
	out, err := runTrain(t, trainBoardJSON, "alerts")
	if err != nil {
		t.Fatal(err)
	}
	want := `BLOCK  C1  #21 Demo iOS 2.4.0      !77 failed: build_test         C1~v/21-2026/di
DATA   A3  Demo Android            Milestone has no Jira release  A3~m/da/9
RISK   B1  #21 Demo Android 2.4.0  Freeze was 2 day(s) ago        B1~v/21-2026/da
`
	if out != want {
		t.Fatalf("alerts =\n%s\nwant\n%s", out, want)
	}
	out, err = runTrain(t, trainBoardJSON, "alerts", "--history", "--hidden")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "CLEANUP  E1") || !strings.Contains(out, "history") || !strings.Contains(out, "CLEANUP  E2") || !strings.Contains(out, "hidden") {
		t.Fatalf("alerts --history --hidden =\n%s", out)
	}
	out, err = runTrain(t, trainBoardJSON, "alerts", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var hits []struct{ ID string }
	if err := json.Unmarshal([]byte(out), &hits); err != nil || len(hits) != 3 {
		t.Fatalf("alerts --json = %s (%v)", out, err)
	}
}

func TestTrainWithoutConfigSaysHowToCreateOneAndExitsZero(t *testing.T) {
	body := `{"config":{"state":"absent","path":"/data/release-train.json","message":"no config at /data/release-train.json; run ` + "`ao train config init`" + ` to write a template, then fill in your ids"}}`
	for _, args := range [][]string{nil, {"--gate"}, {"alerts"}} {
		out, err := runTrain(t, body, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(out, "ao train config init") {
			t.Fatalf("%v output = %q", args, out)
		}
	}
}

func TestTrainMeSaysIdentityIsNotThereYet(t *testing.T) {
	setConfigEnv(t)
	for _, args := range [][]string{{"train", "--me"}, {"train", "alerts", "--me"}} {
		_, _, err := executeCLI(t, aliveDeps(), args...)
		if ExitCode(err) != 2 || !strings.Contains(err.Error(), "--me") || !strings.Contains(err.Error(), "identity") {
			t.Fatalf("%v = %v", args, err)
		}
	}
}

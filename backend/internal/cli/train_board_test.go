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
        {"id": "C1~v/21-2026/di", "rule": "C1", "severity": "block", "train": "21-2026", "app": "di", "copy": {"what": "Train 21 release pipeline failed: build_test", "do": "Retry the job."}, "bucket": "badge"},
        {"id": "B1~v/21-2026/da", "rule": "B1", "severity": "risk", "train": "21-2026", "app": "da", "copy": {"what": "Train 21 has no release MR yet", "do": "Open the release MR."}, "bucket": "badge"},
        {"id": "A3~m/da/9", "rule": "A3", "severity": "data", "app": "da", "copy": {"what": "Milestone has no Jira release for train 21", "do": "Create the Jira release."}, "bucket": "badge"},
        {"id": "E1~v/20-2026/di", "rule": "E1", "severity": "hygiene", "train": "20-2026", "app": "di", "copy": {"what": "Train 20 shipped, Jira release not marked released", "do": "Mark it released."}, "bucket": "history"},
        {"id": "E2~v/20-2026/di", "rule": "E2", "severity": "hygiene", "train": "20-2026", "app": "di", "copy": {"what": "1 card in train 20 is not Done after publish", "do": "Move it to Done."}, "bucket": "hidden"}
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
  BLOCK  C1  #21 Demo iOS 2.4.0  Train 21 release pipeline failed: build_test · Retry the job.  C1~v/21-2026/di
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
	want := `BLOCK  C1  #21 Demo iOS 2.4.0      Train 21 release pipeline failed: build_test · Retry the job.          C1~v/21-2026/di
DATA   A3  Demo Android            Milestone has no Jira release for train 21 · Create the Jira release.  A3~m/da/9
RISK   B1  #21 Demo Android 2.4.0  Train 21 has no release MR yet · Open the release MR.                  B1~v/21-2026/da
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

func TestTrainMeKeepsOnlyWhatConcernsMe(t *testing.T) {
	body := strings.Replace(trainBoardJSON, `"mismatches": [],`, `"mismatches": [], "identity": {"kind": "known", "jiraAccountId": "acc-ex", "gitlabUsername": "ex", "displayName": "Example Person"},`, 1)
	body = strings.Replace(body, `"releaseMr": {"mr": {"iid": 77, "author": "Example Person"}},`, `"releaseMr": {"mr": {"iid": 77, "author": "Example Person"}}, "mine": [{"why": "release_mr_author", "ref": "!77"}],`, 1)
	body = strings.Replace(body, `"copy": {"what": "Train 21 release pipeline failed: build_test", "do": "Retry the job."}, "bucket": "badge"`, `"copy": {"what": "Train 21 release pipeline failed: build_test", "do": "Retry the job."}, "bucket": "badge", "mine": [{"why": "release_mr_author", "ref": "!77"}]`, 1)

	out, err := runTrain(t, body, "--me")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Only me: Example Person\n") || !strings.Contains(out, "  Demo iOS  2.4.0  scan") || strings.Contains(out, "  Demo Android  2.4.0") {
		t.Fatalf("--me =\n%s", out)
	}
	if !strings.Contains(out, "Needs attention  1 block\n") || strings.Contains(out, "risk") {
		t.Fatalf("--me attention =\n%s", out)
	}

	out, err = runTrain(t, body, "alerts", "--me")
	if err != nil {
		t.Fatal(err)
	}
	if out != "BLOCK  C1  #21 Demo iOS 2.4.0  Train 21 release pipeline failed: build_test · Retry the job.  C1~v/21-2026/di\n" {
		t.Fatalf("alerts --me =\n%s", out)
	}

	out, err = runTrain(t, body, "--json", "--me")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Board struct {
			Trains []struct {
				Versions []struct{ App string } `json:"versions"`
			} `json:"trains"`
			Alerts struct {
				Hits []struct{ ID string } `json:"hits"`
			} `json:"alerts"`
		} `json:"board"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Board.Trains) != 1 || len(got.Board.Trains[0].Versions) != 1 || got.Board.Trains[0].Versions[0].App != "di" || len(got.Board.Alerts.Hits) != 1 {
		t.Fatalf("--json --me = %+v", got.Board)
	}
}

func TestTrainJSONMeFiltersLongLivedHotfixesAndSprintCardsToo(t *testing.T) {
	body := strings.Replace(trainBoardJSON, `"mismatches": [],`, `"mismatches": [], "identity": {"kind": "known", "jiraAccountId": "acc-ex", "gitlabUsername": "ex", "displayName": "Example Person"},
    "hotfixes": [{"app": "di", "version": "2.3.1", "mine": [{"why": "mr_author", "ref": "!80"}]}, {"app": "da", "version": "2.3.2"}],
    "llbs": [{"id": "demo-wallet", "mine": [{"why": "card", "ref": "DEMO-1"}]}, {"id": "demo-chat"}],
    "sprints": [{"number": 7, "cards": [{"key": "DEMO-1", "mine": [{"why": "assignee"}]}, {"key": "DEMO-2"}]}],`, 1)
	out, err := runTrain(t, body, "--json", "--me")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Board struct {
			Hotfixes []struct{ Version string } `json:"hotfixes"`
			LLBs     []struct{ ID string }      `json:"llbs"`
			Sprints  []struct {
				Number int                    `json:"number"`
				Cards  []struct{ Key string } `json:"cards"`
			} `json:"sprints"`
		} `json:"board"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	b := got.Board
	if len(b.Hotfixes) != 1 || b.Hotfixes[0].Version != "2.3.1" || len(b.LLBs) != 1 || b.LLBs[0].ID != "demo-wallet" ||
		len(b.Sprints) != 1 || b.Sprints[0].Number != 7 || len(b.Sprints[0].Cards) != 1 || b.Sprints[0].Cards[0].Key != "DEMO-1" {
		t.Fatalf("--json --me = %+v; want only my hotfix, my long-lived group and my sprint card, the row kept", b)
	}

	out, err = runTrain(t, body, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"demo-chat"`) || !strings.Contains(out, `"2.3.2"`) || !strings.Contains(out, `"DEMO-2"`) {
		t.Fatalf("--json without --me dropped something: %s", out)
	}
}

func TestTrainMeWithUnknownIdentitySaysWhyAndExitsZero(t *testing.T) {
	body := strings.Replace(trainBoardJSON, `"mismatches": [],`, `"mismatches": [], "identity": {"kind": "unknown", "reason": "the GitLab user (gitlab.user) has not been read"},`, 1)
	for _, args := range [][]string{{"--me"}, {"alerts", "--me"}, {"--me", "--json"}} {
		out, err := runTrain(t, body, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if out != "Only me is unavailable: the GitLab user (gitlab.user) has not been read\n" {
			t.Fatalf("%v = %q", args, out)
		}
	}
}

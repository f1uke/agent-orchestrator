package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

const trainSourcesJSON = `{
  "config": {"state": "ok", "path": "/data/release-train.json"},
  "resources": [
    {"kind": "gitlab.mrs", "scope": "p:1001", "app": "di", "mode": "incremental", "lastAttemptAt": "2026-10-14T09:00:00Z", "backfilling": true},
    {"kind": "gitlab.tags", "scope": "p:1001", "app": "di", "mode": "snapshot", "completeAt": "2026-10-14T09:00:00Z", "lastAttemptAt": "2026-10-14T09:02:00Z",
     "errorClass": "rate_limited", "errorMessage": "gitlab scm: 429 slow down", "nextAttemptAt": "2026-10-14T09:04:00Z", "backfilling": false},
    {"kind": "jira.myself", "scope": "me", "mode": "snapshot", "completeAt": "2026-10-14T09:00:30Z", "lastAttemptAt": "2026-10-14T09:00:30Z", "backfilling": false}
  ],
  "lastPass": {"at": "2026-10-14T09:02:00Z", "requests": {"gitlab": 12, "jira": 3}}
}`

func TestTrainSourcesPrintsATable(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, http.StatusOK, trainSourcesJSON)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, aliveDeps(), "train", "sources")
	if err != nil {
		t.Fatal(err)
	}
	if capture.method != http.MethodGet || capture.path != "/api/v1/release-trains/sources" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	want := `config: ok (/data/release-train.json)
last pass 2026-10-14T09:02:00Z: gitlab 12 requests, jira 3 requests

KIND         SCOPE   APP  STATE         COMPLETE              NEXT
gitlab.mrs   p:1001  di   backfilling   -                     -
gitlab.tags  p:1001  di   rate_limited  2026-10-14T09:00:00Z  2026-10-14T09:04:00Z
jira.myself  me      -    ok            2026-10-14T09:00:30Z  -

gitlab.tags p:1001: gitlab scm: 429 slow down
`
	if out != want {
		t.Fatalf("output =\n%s\nwant\n%s", out, want)
	}
}

func TestTrainSourcesJSONIsTheDaemonsBody(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := reviewServer(t, http.StatusOK, trainSourcesJSON)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, aliveDeps(), "train", "sources", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got trainSourcesResponse
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Resources) != 3 || got.LastPass.Requests["gitlab"] != 12 {
		t.Fatalf("--json = %s (%v)", out, err)
	}
}

func TestTrainSourcesWithoutConfigSaysHowToCreateIt(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := reviewServer(t, http.StatusOK, `{"config":{"state":"absent","path":"/data/release-train.json","message":"no config at /data/release-train.json; run `+"`ao train config init`"+` to write a template, then fill in your ids"},"resources":[],"lastPass":{"requests":{}}}`)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, aliveDeps(), "train", "sources")
	if err != nil {
		t.Fatal(err)
	}
	want := "config: absent (/data/release-train.json)\n  no config at /data/release-train.json; run `ao train config init` to write a template, then fill in your ids\nno resources observed yet\n"
	if out != want {
		t.Fatalf("output =\n%q\nwant\n%q", out, want)
	}
}

func TestTrainConfigInitWritesOnceAndRefusesTheSecondTime(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, http.StatusCreated, `{"path":"/data/release-train.json"}`)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, aliveDeps(), "train", "config", "init")
	if err != nil {
		t.Fatal(err)
	}
	if capture.method != http.MethodPost || capture.path != "/api/v1/release-trains/config" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	if out != "wrote /data/release-train.json; fill in your GitLab project ids, Jira project and boards\n" {
		t.Fatalf("output = %q", out)
	}

	cfg = setConfigEnv(t)
	srv, _ = reviewServer(t, http.StatusConflict, `{"error":"conflict","code":"RELEASE_TRAIN_CONFIG_EXISTS","message":"the config file already exists; edit it instead: /data/release-train.json"}`)
	writeRunFileFor(t, cfg, srv)
	_, _, err = executeCLI(t, aliveDeps(), "train", "config", "init")
	var usage usageError
	if !errors.As(err, &usage) || err.Error() != "the config file already exists; edit it instead: /data/release-train.json (RELEASE_TRAIN_CONFIG_EXISTS)" {
		t.Fatalf("second init err = %v", err)
	}
}

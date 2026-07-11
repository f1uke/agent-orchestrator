package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSmokeSetReadsStdinCases(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, 200, `{"worker":"fix gl note","checks":[{"id":"a","seq":1,"name":"A","verdict":"pending"}]}`)
	writeRunFileFor(t, cfg, srv)

	deps := aliveDeps()
	deps.In = strings.NewReader(`{"cases":[{"name":"A fresh MR shows up","why":"w","steps":["s1","s2"],"expected":"e","prNum":36,"fileRef":"f.go:1"}]}`)

	out, errOut, err := executeCLI(t, deps, "smoke", "set", "w1", "--from-file", "-")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != "PUT" || capture.path != "/api/v1/sessions/w1/smoke-checks" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	var req authorSmokeChecksRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(req.Cases) != 1 || req.Cases[0].Name != "A fresh MR shows up" || req.Cases[0].PRNum != 36 || req.Cases[0].FileRef != "f.go:1" {
		t.Fatalf("cases = %+v", req.Cases)
	}
	if len(req.Cases[0].Steps) != 2 {
		t.Fatalf("steps = %+v", req.Cases[0].Steps)
	}
	if !strings.Contains(out, "authored 1 smoke check(s) for w1") {
		t.Fatalf("output = %q", out)
	}
}

func TestSmokeSetAcceptsBareArray(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, 200, `{"worker":"w","checks":[{"id":"a","seq":1,"name":"A","verdict":"pending"}]}`)
	writeRunFileFor(t, cfg, srv)

	deps := aliveDeps()
	deps.In = strings.NewReader(`[{"name":"Only case","expected":"ok"}]`)

	if _, errOut, err := executeCLI(t, deps, "smoke", "set", "w1", "--from-file", "-"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var req authorSmokeChecksRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(req.Cases) != 1 || req.Cases[0].Name != "Only case" {
		t.Fatalf("cases = %+v", req.Cases)
	}
}

func TestSmokeSetUnderscoreFlagNormalizes(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, 200, `{"worker":"w","checks":[]}`)
	writeRunFileFor(t, cfg, srv)

	deps := aliveDeps()
	deps.In = strings.NewReader(`{"cases":[{"name":"A"}]}`)
	// Agents often type --from_file with an underscore.
	if _, errOut, err := executeCLI(t, deps, "smoke", "set", "w1", "--from_file", "-"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.path != "/api/v1/sessions/w1/smoke-checks" {
		t.Fatalf("path = %s", capture.path)
	}
}

func TestSmokeSetRequiresSessionAndFile(t *testing.T) {
	setConfigEnv(t)
	if _, _, err := executeCLI(t, aliveDeps(), "smoke", "set", "--from-file", "-"); err == nil || !strings.Contains(err.Error(), "session id is required") {
		t.Fatalf("err = %v, want session-required", err)
	}
	if _, _, err := executeCLI(t, aliveDeps(), "smoke", "set", "w1"); err == nil || !strings.Contains(err.Error(), "--from-file") {
		t.Fatalf("err = %v, want from-file-required", err)
	}
}

func TestSmokeListPrintsChecklist(t *testing.T) {
	cfg := setConfigEnv(t)
	resp := `{"worker":"fix gl note","checks":[{"id":"a","seq":1,"name":"A fresh MR shows up","verdict":"pass","note":"looked good","prNum":36,"fileRef":"f.go:1","evidence":[{"id":"ev1","kind":"image"}]}]}`
	srv, capture := reviewServer(t, 200, resp)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "smoke", "list", "w1")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != "GET" || capture.path != "/api/v1/sessions/w1/smoke-checks" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	for _, want := range []string{"CHECK 1 [PASS] A fresh MR shows up", "PR #36 · f.go:1", "note: looked good", "evidence: 1 attached"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestSmokeListJSON(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := reviewServer(t, 200, `{"worker":"w","checks":[{"id":"a","seq":1,"name":"A","verdict":"pending"}]}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "smoke", "list", "w1", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(out, `"worker": "w"`) || !strings.Contains(out, `"name": "A"`) {
		t.Fatalf("json output = %q", out)
	}
}

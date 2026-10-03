package controllers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
	"github.com/aoagents/agent-orchestrator/backend/internal/simtrust"
)

// trustRecorder is the simctl runner behind a real Truster, so the claim and
// listing paths are exercised end to end without a device.
type trustRecorder struct {
	mu    sync.Mutex
	calls [][]string
	fail  bool
}

func (r *trustRecorder) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string{name}, args...))
	if r.fail {
		return []byte("Unable to add root certificate"), errors.New("exit status 1")
	}
	return nil, nil
}

func (r *trustRecorder) installed() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.calls...)
}

type fakeTrustFiles struct {
	files []string
	err   error
	asked domain.SessionID
}

func (f *fakeTrustFiles) SimTrustFor(_ context.Context, id domain.SessionID) ([]string, error) {
	f.asked = id
	return f.files, f.err
}

func testCAFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "proxy-ca.pem")
	if err := os.WriteFile(path, []byte("-----BEGIN CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTrustTestServer(t *testing.T, screen httpd.SimScreen, files *fakeTrustFiles, store *simtrust.Store) *httptest.Server {
	t.Helper()
	deps := httpd.APIDeps{Sim: &fakeSimService{}, SimScreen: screen, SimDrags: simgesture.NewDrags(), SimTrust: store}
	if files != nil {
		deps.SimTrustFiles = files
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func claimURL(base, session string) string {
	return base + "/api/v1/sessions/" + session + "/sim-leases"
}

// A claim is the step every agent takes before driving, so it is where a
// device a human booted from Xcode gets to trust the proxy's CA - and the
// response says what it trusted, for `ao sim claim` to print.
func TestSimClaim_MakesABootedDeviceTrustTheSessionsCAs(t *testing.T) {
	rec := &trustRecorder{}
	screen := &fakeScreen{listing: oneBooted(), truster: simtrust.NewTruster(rec.run)}
	ca := testCAFile(t)
	files := &fakeTrustFiles{files: []string{ca}}
	srv := newTrustTestServer(t, screen, files, nil)

	code, body := postJSON(t, claimURL(srv.URL, "p-1"), map[string]any{"udid": testSimUDID})
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200: %v", code, body)
	}
	trust, _ := body["trust"].(map[string]any)
	trusted, _ := trust["trusted"].([]any)
	if len(trusted) != 1 || trusted[0] != ca {
		t.Fatalf("trust = %v, want %s trusted", body["trust"], ca)
	}
	calls := rec.installed()
	if len(calls) != 1 || strings.Join(calls[0], " ") != "xcrun simctl keychain "+testSimUDID+" add-root-cert "+ca {
		t.Fatalf("ran %v, want one add-root-cert of %s", calls, ca)
	}
	if files.asked != "p-1" {
		t.Fatalf("resolved trust for %q, want the claiming session", files.asked)
	}
}

// simctl's keychain needs a running device. Claiming one that is not booted
// is not an error, and is not a failed install either - the boot will do it.
func TestSimClaim_OfADeviceThatIsNotBootedTrustsNothing(t *testing.T) {
	rec := &trustRecorder{}
	screen := &fakeScreen{listing: oneBooted(), truster: simtrust.NewTruster(rec.run)}
	srv := newTrustTestServer(t, screen, &fakeTrustFiles{files: []string{testCAFile(t)}}, nil)

	code, body := postJSON(t, claimURL(srv.URL, "p-1"), map[string]any{"udid": otherSimUDID})
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	if _, ok := body["trust"]; ok || len(rec.installed()) != 0 {
		t.Fatalf("trust = %v, calls = %v; want nothing for a shut-down device", body["trust"], rec.installed())
	}
}

// A CA that will not install is news on the response, never a refused claim.
func TestSimClaim_AFailedInstallIsReportedNotRaised(t *testing.T) {
	rec := &trustRecorder{fail: true}
	screen := &fakeScreen{listing: oneBooted(), truster: simtrust.NewTruster(rec.run)}
	srv := newTrustTestServer(t, screen, &fakeTrustFiles{files: []string{testCAFile(t)}}, nil)

	code, body := postJSON(t, claimURL(srv.URL, "p-1"), map[string]any{"udid": testSimUDID})
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200: a claim must not fail over a CA", code)
	}
	trust, _ := body["trust"].(map[string]any)
	if failed, _ := trust["failed"].([]any); len(failed) != 1 {
		t.Fatalf("trust = %v, want one failure", body["trust"])
	}
}

// What a claim trusted is on the device listing afterwards - which is also how
// `ao sim boot` reads back what its boot trusted.
func TestSimDevices_CarryTheLastTrustPass(t *testing.T) {
	rec := &trustRecorder{}
	screen := &fakeScreen{listing: oneBooted(), truster: simtrust.NewTruster(rec.run)}
	ca := testCAFile(t)
	srv := newTrustTestServer(t, screen, &fakeTrustFiles{files: []string{ca}}, nil)
	postJSON(t, claimURL(srv.URL, "p-1"), map[string]any{"udid": testSimUDID})

	var listing struct {
		Devices []struct {
			UDID  string `json:"udid"`
			Trust *struct {
				Trusted []string `json:"trusted"`
			} `json:"trust"`
		} `json:"devices"`
	}
	if code := getJSON(t, srv.URL+"/api/v1/sim/devices", &listing); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	for _, d := range listing.Devices {
		switch d.UDID {
		case testSimUDID:
			if d.Trust == nil || len(d.Trust.Trusted) != 1 || d.Trust.Trusted[0] != ca {
				t.Fatalf("claimed device trust = %+v, want %s", d.Trust, ca)
			}
		case otherSimUDID:
			if d.Trust != nil {
				t.Fatalf("untouched device carries trust %+v", d.Trust)
			}
		}
	}
}

// A boot carries the session's trust request down to simpower, which installs
// it once the device is up.
func TestSimPower_BootCarriesTheSessionsTrust(t *testing.T) {
	screen := &fakeScreen{listing: oneBooted()}
	ca := testCAFile(t)
	srv := newTrustTestServer(t, screen, &fakeTrustFiles{files: []string{ca}}, nil)

	if code, _ := postJSON(t, powerURL(srv.URL, "p-1", otherSimUDID), map[string]any{"state": "booted"}); code != http.StatusAccepted {
		t.Fatalf("status %d, want 202", code)
	}
	ops := screen.powered()
	if len(ops) != 1 || ops[0].Setup == nil || ops[0].Setup.Trust == nil ||
		len(ops[0].Setup.Trust.Files) != 1 || ops[0].Setup.Trust.Files[0] != ca {
		t.Fatalf("powered %+v, want a boot carrying %s", ops, ca)
	}
}

func TestSimTrustSettings_RoundTripAndRejectARelativePath(t *testing.T) {
	store, err := simtrust.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := newTrustTestServer(t, nil, nil, store)
	url := srv.URL + "/api/v1/settings/sim-trust"

	var got struct {
		CAFiles        []string `json:"caFiles"`
		DefaultCAFiles []string `json:"defaultCaFiles"`
		Found          []bool   `json:"found"`
	}
	if code := getJSON(t, url, &got); code != http.StatusOK {
		t.Fatalf("GET status %d", code)
	}
	if len(got.CAFiles) != len(simtrust.DefaultCAFiles) || len(got.DefaultCAFiles) != len(simtrust.DefaultCAFiles) {
		t.Fatalf("fresh setting = %+v, want the default list", got)
	}

	ca := testCAFile(t)
	if code := putTrustJSON(t, url, map[string]any{"caFiles": []string{ca, "/nowhere/ca.pem"}}, &got); code != http.StatusOK {
		t.Fatalf("PUT status %d", code)
	}
	if len(got.CAFiles) != 2 || got.CAFiles[0] != ca || len(got.Found) != 2 || !got.Found[0] || got.Found[1] {
		t.Fatalf("after PUT = %+v, want [%s found, /nowhere missing]", got, ca)
	}

	if code := putTrustJSON(t, url, map[string]any{"caFiles": []string{"ca.pem"}}, nil); code != http.StatusBadRequest {
		t.Fatalf("PUT of a relative path: status %d, want 400", code)
	}
}

func putTrustJSON(t *testing.T, url string, in, out any) int {
	t.Helper()
	b, _ := json.Marshal(in)
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(b)) //nolint:noctx // test helper against httptest
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", url, err)
	}
	defer func() { _ = res.Body.Close() }()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

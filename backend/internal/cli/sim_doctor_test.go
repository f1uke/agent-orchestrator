package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// doctorDaemon serves the sim-doctor route with a canned report and records
// the request the CLI made.
func doctorDaemon(t *testing.T, report string) *url.URL {
	t.Helper()
	cfg := setConfigEnv(t)
	got := &url.URL{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sessions/mer-9/sim-doctor" {
			http.NotFound(w, r)
			return
		}
		*got = *r.URL
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(report))
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	return got
}

const doctorFailing = `{"checks":[
 {"name":"device","status":"OK","message":"iPhone 17 (AAAA) booted"},
 {"name":"lease","status":"WARN","message":"no AO session holds it, so a run will claim it"},
 {"name":"app","status":"FAIL","message":"com.example.app is not installed on this simulator"},
 {"name":"proxy CA","status":"OK","message":"/ca.pem trusted on this simulator"}],"ok":false}`

func TestSimDoctor_PrintsOneLinePerCheckAndExitsOneOnAFailure(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	t.Setenv("AO_SIM_APP", "")
	asked := doctorDaemon(t, doctorFailing)

	out, _, err := executeCLI(t, aliveDeps(), "sim", "doctor", "--udid", "AAAA", "--app", "com.example.app")
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("a FAIL line must exit 1, got err=%v", err)
	}
	want := "OK   device: iPhone 17 (AAAA) booted\n" +
		"WARN lease: no AO session holds it, so a run will claim it\n" +
		"FAIL app: com.example.app is not installed on this simulator\n" +
		"OK   proxy CA: /ca.pem trusted on this simulator\n"
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
	if q := asked.Query(); q.Get("udid") != "AAAA" || q.Get("app") != "com.example.app" || q.Has("expect") {
		t.Errorf("query = %q, want udid and app only", asked.RawQuery)
	}
}

func TestSimDoctor_JSONIsTheReportAndAPassingOneExitsZero(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	t.Setenv("AO_SIM_APP", "com.example.pinned")
	report := `{"checks":[{"name":"device","status":"OK","message":"booted"}],"ok":true}`
	asked := doctorDaemon(t, report)

	out, _, err := executeCLI(t, aliveDeps(), "sim", "doctor", "--json")
	if err != nil {
		t.Fatalf("a passing report must exit 0: %v", err)
	}
	var got simDoctorReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("--json output is not the report: %v\n%s", err, out)
	}
	if !got.OK || len(got.Checks) != 1 || got.Checks[0].Name != "device" {
		t.Fatalf("report = %+v", got)
	}
	if asked.Query().Get("app") != "com.example.pinned" {
		t.Errorf("$AO_SIM_APP did not pin the app: query %q", asked.RawQuery)
	}
}

// The daemon does not share the caller's working directory, so a relative
// --expect is sent as the absolute path the caller meant.
func TestSimDoctor_SendsExpectAsAnAbsolutePath(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "mer-9")
	asked := doctorDaemon(t, `{"checks":[],"ok":true}`)
	t.Chdir(t.TempDir())

	if _, _, err := executeCLI(t, aliveDeps(), "sim", "doctor", "--app", "com.example.app", "--expect", "build/App.app"); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := asked.Query().Get("expect"), filepath.Join(cwd, "build", "App.app"); got != want {
		t.Errorf("expect = %q, want %q", got, want)
	}
}

func TestSimDoctor_Refusals(t *testing.T) {
	cases := []struct {
		name    string
		session string
		args    []string
		want    string
	}{
		{"expect without app", "mer-9", []string{"--expect", "/tmp/App.app"}, "--expect names a build of an app"},
		{"outside a session", "", nil, "must run inside an AO session"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", tc.session)
			t.Setenv("AO_SIM_APP", "")
			_, _, err := executeCLI(t, aliveDeps(), append([]string{"sim", "doctor"}, tc.args...)...)
			if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want a usage error containing %q, got %v", tc.want, err)
			}
		})
	}
}

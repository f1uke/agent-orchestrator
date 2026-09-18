package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	iosrunsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/iosrun"
)

// fakeIOSRun answers whatever a test hands it, so the controller's own
// serialisation is what is under test rather than xcodebuild.
type fakeIOSRun struct {
	project iosrunsvc.Project
	// refreshed records what the handler asked for, so the query param that
	// makes an opening picker re-read the project is covered end to end.
	refreshed []bool
}

func (f *fakeIOSRun) Project(_ context.Context, _ domain.SessionID, refresh bool) (iosrunsvc.Project, error) {
	f.refreshed = append(f.refreshed, refresh)
	return f.project, nil
}

func (f *fakeIOSRun) Start(context.Context, domain.SessionID, string, string, string) (iosrunsvc.Run, error) {
	return iosrunsvc.Run{}, nil
}

func (f *fakeIOSRun) Current(context.Context, domain.SessionID) (iosrunsvc.Run, bool, error) {
	return iosrunsvc.Run{}, false, nil
}

func newIOSRunTestServer(t *testing.T, svc iosrunsvc.Manager) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil,
		httpd.APIDeps{IOSRun: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

// The spec declares `schemes` an array, so a project with none must serialise
// as `[]` and never as `null`. A CocoaPods workspace with no `Pods/` resolves
// to zero schemes, and a renderer reading `.length` off `null` throws and takes
// the whole view down - so this is asserted on the RAW body, not on a decoded
// Go slice, which cannot tell the two apart.
func TestIOSProject_SerialisesAnEmptySchemeListAsAnArray(t *testing.T) { //nolint:dupl // the sibling test asserts the other array field, and merging them would hide which one broke
	srv := newIOSRunTestServer(t, &fakeIOSRun{project: iosrunsvc.Project{
		Name:         "NterWorkspace.xcworkspace",
		Path:         "/w/NterWorkspace.xcworkspace",
		Kind:         "workspace",
		SchemesError: "This project has no schemes, so there is nothing to build.",
	}})

	res, err := http.Get(srv.URL + "/api/v1/sessions/p-1/ios-project") //nolint:noctx // test against httptest
	if err != nil {
		t.Fatalf("GET ios-project: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if strings.Contains(string(body), `"schemes":null`) {
		t.Fatalf("schemes serialised as null, which breaks the array contract: %s", body)
	}
	if !strings.Contains(string(body), `"schemes":[]`) {
		t.Fatalf("schemes is not an empty array: %s", body)
	}
}

// `configurations` is an array in the spec for the same reason `schemes` is, and
// it arrives empty on exactly the projects that already broke the renderer once:
// the ones whose listing could not be read.
func TestIOSProject_SerialisesAnEmptyConfigurationListAsAnArray(t *testing.T) { //nolint:dupl // see the sibling test
	srv := newIOSRunTestServer(t, &fakeIOSRun{project: iosrunsvc.Project{
		Name:                "NterWorkspace.xcworkspace",
		Path:                "/w/NterWorkspace.xcworkspace",
		Kind:                "workspace",
		Schemes:             []string{"NterApp"},
		ConfigurationsError: "No build configurations could be read from this project, so there is no safe one to build.",
	}})

	res, err := http.Get(srv.URL + "/api/v1/sessions/p-1/ios-project") //nolint:noctx // test against httptest
	if err != nil {
		t.Fatalf("GET ios-project: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(res.Body)
	if strings.Contains(string(body), `"configurations":null`) {
		t.Fatalf("configurations serialised as null, which breaks the array contract: %s", body)
	}
	if !strings.Contains(string(body), `"configurations":[]`) {
		t.Fatalf("configurations is not an empty array: %s", body)
	}
}

// Opening a picker must reach the project on disk, not the listing cached a
// minute ago: the workflow is `xcodegen` in the terminal, then straight to the
// dropdown.
func TestIOSProject_PassesRefreshThrough(t *testing.T) {
	svc := &fakeIOSRun{project: iosrunsvc.Project{Name: "App.xcodeproj", Kind: "project"}}
	srv := newIOSRunTestServer(t, svc)

	for _, query := range []string{"", "?refresh=true"} {
		res, err := http.Get(srv.URL + "/api/v1/sessions/p-1/ios-project" + query) //nolint:noctx // test against httptest
		if err != nil {
			t.Fatalf("GET ios-project%s: %v", query, err)
		}
		_ = res.Body.Close()
	}
	if len(svc.refreshed) != 2 || svc.refreshed[0] || !svc.refreshed[1] {
		t.Fatalf("asked with refresh=%v, want [false true]", svc.refreshed)
	}
}

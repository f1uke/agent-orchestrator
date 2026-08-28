package httpd

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

// The resolver is the only link in the slimming chain that can fail SILENTLY.
// A nil profile is, by design, indistinguishable from a project that opted out:
// nothing is run, the status entry is deleted, and every surface says nothing.
// So a resolver that answers nil when it should have answered a profile turns a
// slimmed project into a stock one with no report anywhere - which is the exact
// failure mode this feature exists to make impossible. These tests are what
// stand between that and a green build.

// fakeSessions answers Get and nothing else. The embedded interface is nil on
// purpose: any other method this resolver started calling would panic loudly in
// a test rather than pass quietly, which is the right way round.
type fakeSessions struct {
	controllers.SessionService
	session domain.Session
	err     error
}

func (f fakeSessions) Get(context.Context, domain.SessionID) (domain.Session, error) {
	return f.session, f.err
}

type fakeProjects struct {
	projectsvc.Manager
	result projectsvc.GetResult
	err    error
}

func (f fakeProjects) Get(context.Context, domain.ProjectID) (projectsvc.GetResult, error) {
	return f.result, f.err
}

// resolverOver wires a resolver to a session that belongs to a project with the
// given config.
func resolverOver(cfg *domain.ProjectConfig) simProfiles {
	return simProfiles{
		sessions: fakeSessions{session: domain.Session{SessionRecord: domain.SessionRecord{ID: "mer-9", ProjectID: "nter-ios-app"}}},
		projects: fakeProjects{result: projectsvc.GetResult{
			Status:  "ok",
			Project: &projectsvc.Project{ID: "nter-ios-app", Config: cfg},
		}},
	}
}

func TestSimProfileFor_CarriesTheProjectsKeepList(t *testing.T) {
	keep := []string{"com.apple.apsd", "com.apple.swcd"}
	r := resolverOver(&domain.ProjectConfig{SimProfile: &domain.SimProfileConfig{Keep: keep}})

	got, err := r.SimProfileFor(context.Background(), "mer-9")
	if err != nil {
		t.Fatalf("SimProfileFor: %v", err)
	}
	if got == nil {
		t.Fatal("a project that asked for a profile got none; its devices would boot stock and say nothing")
	}
	if len(got.Keep) != len(keep) {
		t.Fatalf("keep = %v, want %v", got.Keep, keep)
	}
	for i := range keep {
		if got.Keep[i] != keep[i] {
			// A daemon silently dropped from the keep list is a feature that
			// silently does nothing - apsd is the one that makes `simctl push`
			// print "Notification sent" and deliver nothing.
			t.Fatalf("keep = %v, want %v", got.Keep, keep)
		}
	}
}

func TestSimProfileFor_ProjectWithoutASimProfileDoesNotSlim(t *testing.T) {
	r := resolverOver(&domain.ProjectConfig{})

	got, err := r.SimProfileFor(context.Background(), "mer-9")
	if err != nil {
		t.Fatalf("SimProfileFor: %v", err)
	}
	if got != nil {
		t.Fatalf("profile = %+v, want nil - a project that says nothing must behave exactly as it does today", got)
	}
}

func TestSimProfileFor_ProjectWithNoConfigAtAllDoesNotSlim(t *testing.T) {
	r := resolverOver(nil)

	got, err := r.SimProfileFor(context.Background(), "mer-9")
	if err != nil {
		t.Fatalf("SimProfileFor: %v", err)
	}
	if got != nil {
		t.Fatalf("profile = %+v, want nil", got)
	}
}

// An empty-but-present Keep is a real instruction - "slim everything" - and is
// the reason SimProfile is a pointer. Collapsing it to nil here would turn the
// most aggressive profile a project can ask for into no profile at all, and the
// device would come up stock while the config said otherwise.
func TestSimProfileFor_PresentButEmptyKeepIsStillAProfile(t *testing.T) {
	r := resolverOver(&domain.ProjectConfig{SimProfile: &domain.SimProfileConfig{}})

	got, err := r.SimProfileFor(context.Background(), "mer-9")
	if err != nil {
		t.Fatalf("SimProfileFor: %v", err)
	}
	if got == nil {
		t.Fatal("an empty Keep means slim everything, which is not the same as do not slim")
	}
	if len(got.Keep) != 0 {
		t.Fatalf("keep = %v, want empty", got.Keep)
	}
}

// A lookup that fails must propagate, never degrade to nil. simpower turns the
// error into a Failed outcome the device reports; a nil would be silence, and
// "this project asked for a profile and we could not find out which" is exactly
// what may not pass quietly.
func TestSimProfileFor_SessionLookupErrorPropagates(t *testing.T) {
	boom := errors.New("no such session")
	r := simProfiles{sessions: fakeSessions{err: boom}, projects: fakeProjects{}}

	got, err := r.SimProfileFor(context.Background(), "mer-9")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the session service's own error", err)
	}
	if got != nil {
		t.Fatalf("profile = %+v, want nil alongside the error", got)
	}
}

func TestSimProfileFor_ProjectLookupErrorPropagates(t *testing.T) {
	boom := errors.New("project registry unreadable")
	r := simProfiles{
		sessions: fakeSessions{session: domain.Session{SessionRecord: domain.SessionRecord{ID: "mer-9", ProjectID: "nter-ios-app"}}},
		projects: fakeProjects{err: boom},
	}

	got, err := r.SimProfileFor(context.Background(), "mer-9")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the project manager's own error", err)
	}
	if got != nil {
		t.Fatalf("profile = %+v, want nil alongside the error", got)
	}
}

// A degraded project - its config failed to load - has no Config to read, and
// answering nil there is right: we do not know what it wanted, but we also
// never learned that it wanted anything.
func TestSimProfileFor_DegradedProjectDoesNotSlim(t *testing.T) {
	r := simProfiles{
		sessions: fakeSessions{session: domain.Session{SessionRecord: domain.SessionRecord{ID: "mer-9", ProjectID: "nter-ios-app"}}},
		projects: fakeProjects{result: projectsvc.GetResult{Status: "degraded", Degraded: &projectsvc.Degraded{}}},
	}

	got, err := r.SimProfileFor(context.Background(), "mer-9")
	if err != nil {
		t.Fatalf("SimProfileFor: %v", err)
	}
	if got != nil {
		t.Fatalf("profile = %+v, want nil", got)
	}
}

// The one `if` in NewAPI that decides whether a real daemon slims anything at
// all. Left unwired, the controller holds a nil resolver, every boot resolves
// to no profile, and there is nothing anywhere to say so.
func TestNewAPI_BuildsTheDefaultSimProfileResolver(t *testing.T) {
	api := NewAPI(config.Config{}, APIDeps{
		Sessions: fakeSessions{},
		Projects: fakeProjects{},
	})

	if api.simScreen.Profiles == nil {
		t.Fatal("no resolver was built from Sessions and Projects; every project would boot stock in silence")
	}
}

// An injected resolver wins, which is how the controller tests pin an answer
// without standing up a session service.
func TestNewAPI_KeepsAnInjectedSimProfileResolver(t *testing.T) {
	injected := resolverOver(nil)
	api := NewAPI(config.Config{}, APIDeps{
		Sessions:    fakeSessions{},
		Projects:    fakeProjects{},
		SimProfiles: injected,
	})

	if _, ok := api.simScreen.Profiles.(simProfiles); !ok {
		t.Fatalf("resolver = %T, want the injected one", api.simScreen.Profiles)
	}
}

// Without both services there is nothing to build a resolver over, and a
// half-built one would panic on the first boot rather than answer.
func TestNewAPI_LeavesTheResolverNilWithoutBothServices(t *testing.T) {
	api := NewAPI(config.Config{}, APIDeps{Sessions: fakeSessions{}})

	if api.simScreen.Profiles != nil {
		t.Fatalf("resolver = %#v, want nil with no project manager to read a config from", api.simScreen.Profiles)
	}
}

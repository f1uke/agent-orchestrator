package httpd

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simtrust"
)

func trustResolverOver(t *testing.T, cfg *domain.ProjectConfig, global []string) simTrustFiles {
	t.Helper()
	store, err := simtrust.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(simtrust.Settings{CAFiles: global}); err != nil {
		t.Fatal(err)
	}
	p := resolverOver(cfg)
	return simTrustFiles{sessions: p.sessions, projects: p.projects, global: store}
}

func TestSimTrustFor_AProjectWithoutItsOwnListGetsTheGlobalOne(t *testing.T) {
	global := []string{"~/proxyman-ca.pem"}
	for name, cfg := range map[string]*domain.ProjectConfig{
		"no config":   nil,
		"no simTrust": {HasIOSSimulator: true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := trustResolverOver(t, cfg, global).SimTrustFor(context.Background(), "mer-9")
			if err != nil || !reflect.DeepEqual(got, global) {
				t.Fatalf("SimTrustFor = %v, %v; want the global %v", got, err, global)
			}
		})
	}
}

// The project's list REPLACES the global one - including when it is empty,
// which is how a project says "trust nothing here".
func TestSimTrustFor_TheProjectsListReplacesTheGlobalOne(t *testing.T) {
	own := []string{"/opt/charles/ca.pem"}
	got, err := trustResolverOver(t, &domain.ProjectConfig{SimTrust: &domain.SimTrustConfig{CAFiles: own}},
		[]string{"~/proxyman-ca.pem"}).SimTrustFor(context.Background(), "mer-9")
	if err != nil || !reflect.DeepEqual(got, own) {
		t.Fatalf("SimTrustFor = %v, %v; want the project's %v", got, err, own)
	}

	none, err := trustResolverOver(t, &domain.ProjectConfig{SimTrust: &domain.SimTrustConfig{CAFiles: []string{}}},
		[]string{"~/proxyman-ca.pem"}).SimTrustFor(context.Background(), "mer-9")
	if err != nil || len(none) != 0 {
		t.Fatalf("SimTrustFor = %v, %v; want nothing for a project that trusts nothing", none, err)
	}
}

func TestSimTrustFor_ALookupFailureIsAnErrorNotTheGlobalList(t *testing.T) {
	r := trustResolverOver(t, nil, []string{"~/proxyman-ca.pem"})
	r.projects = fakeProjects{err: errors.New("project store is down")}
	if got, err := r.SimTrustFor(context.Background(), "mer-9"); err == nil {
		t.Fatalf("SimTrustFor = %v, want the error: a project that may have opted out must not get the global list", got)
	}
}

package httpd

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/simslim"
	"github.com/aoagents/agent-orchestrator/backend/internal/simtrust"
)

// simProfiles resolves a session's project's simulator profile.
//
// It lives here rather than in the controller because this is the one place
// that already holds every service, and it is the only thing in the chain that
// needs to know a session belongs to a project.
type simProfiles struct {
	sessions controllers.SessionService
	projects projectsvc.Manager
}

var _ controllers.SimProfileResolver = simProfiles{}

// SimProfileFor returns (nil, nil) when the project does not slim - which is
// every project that has not opted in.
func (r simProfiles) SimProfileFor(ctx context.Context, id domain.SessionID) (*simslim.Profile, error) {
	session, err := r.sessions.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	got, err := r.projects.Get(ctx, session.ProjectID)
	if err != nil {
		return nil, err
	}
	if got.Project == nil || got.Project.Config == nil || got.Project.Config.SimProfile == nil {
		return nil, nil
	}
	return &simslim.Profile{Keep: got.Project.Config.SimProfile.Keep}, nil
}

// simTrustFiles resolves which root CAs a session's simulators trust: the
// project's own list when it set one, the global setting otherwise.
type simTrustFiles struct {
	sessions controllers.SessionService
	projects projectsvc.Manager
	global   *simtrust.Store
}

var _ controllers.SimTrustResolver = simTrustFiles{}

// SimTrustFor returns the files to install. A project whose SimTrust is set
// replaces the global list whole - including with an empty one, which is how a
// project says "trust nothing here".
func (r simTrustFiles) SimTrustFor(ctx context.Context, id domain.SessionID) ([]string, error) {
	session, err := r.sessions.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	got, err := r.projects.Get(ctx, session.ProjectID)
	if err != nil {
		return nil, err
	}
	if got.Project != nil && got.Project.Config != nil && got.Project.Config.SimTrust != nil {
		return got.Project.Config.SimTrust.CAFiles, nil
	}
	return r.global.CAFiles(), nil
}

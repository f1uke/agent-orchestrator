package httpd

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/simslim"
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

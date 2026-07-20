package session

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// EnsurePreviewAllowed reports whether a session may set a browser preview
// target, refusing when its project has no web UI (ProjectConfig.HasWebUI).
//
// It exists as its own step, called before the controller resolves the target,
// so `ao preview` fails with "this project has it disabled" rather than with
// whatever the entry-point autodetection happens to say about a project that was
// never going to render in a browser. Silently succeeding is the outcome this
// guards against hardest: an agent that believes it demoed its change did not.
//
// Clearing a preview (`ao preview clear`) deliberately does NOT go through here.
// Emptying the panel can never mislead, and it stays available as the way to
// drop a target left over from before the project opted out.
func (s *Service) EnsurePreviewAllowed(ctx context.Context, id domain.SessionID) error {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return fmt.Errorf("get session %s: %w", id, err)
	}
	if !ok {
		return apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	project, ok, err := s.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return fmt.Errorf("get project %s: %w", rec.ProjectID, err)
	}
	if !ok {
		return apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	if project.Config.HasWebUI {
		return nil
	}
	name := project.DisplayName
	if name == "" {
		name = project.ID
	}
	return apierr.Conflict(
		"WEB_PREVIEW_DISABLED",
		fmt.Sprintf("Project %q has no web UI, so there is nothing to preview and `ao preview` is disabled for it. Turn on \"Web UI\" in the project's settings if it does render in a browser.", name),
		map[string]any{"projectId": project.ID},
	)
}

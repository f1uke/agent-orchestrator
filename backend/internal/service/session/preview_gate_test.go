package session

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// TestEnsurePreviewAllowed: `ao preview` on a project with no web UI must fail
// loudly. A command that appears to work but has no effect is the worst outcome
// here — the agent would believe it had demoed its change.
func TestEnsurePreviewAllowed(t *testing.T) {
	newSvc := func(cfg domain.ProjectConfig) *Service {
		st := newFakeStore()
		st.projects["mer"] = domain.ProjectRecord{ID: "mer", DisplayName: "Meridian", Config: cfg}
		st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}
		return &Service{store: st}
	}

	t.Run("refused when the project has no web UI", func(t *testing.T) {
		err := newSvc(domain.ProjectConfig{}).EnsurePreviewAllowed(context.Background(), "mer-1")
		if err == nil {
			t.Fatal("expected preview to be refused for a project with no web UI")
		}
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) {
			t.Fatalf("expected an api error the CLI can surface, got %T: %v", err, err)
		}
		if apiErr.Code != "WEB_PREVIEW_DISABLED" {
			t.Errorf("code = %q, want WEB_PREVIEW_DISABLED", apiErr.Code)
		}
		// The message has to tell the agent WHY and what to do, or it will retry.
		msg := apiErr.Message
		if !strings.Contains(msg, "Meridian") {
			t.Errorf("message should name the project, got %q", msg)
		}
		if !strings.Contains(strings.ToLower(msg), "web ui") {
			t.Errorf("message should name the setting to turn on, got %q", msg)
		}
	})

	t.Run("allowed once the project opts in", func(t *testing.T) {
		if err := newSvc(domain.ProjectConfig{HasWebUI: true}).EnsurePreviewAllowed(context.Background(), "mer-1"); err != nil {
			t.Fatalf("preview must be allowed for a project with a web UI: %v", err)
		}
	})

	t.Run("unknown session is a not-found, not a disabled-preview", func(t *testing.T) {
		err := newSvc(domain.ProjectConfig{}).EnsurePreviewAllowed(context.Background(), "nope-1")
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "SESSION_NOT_FOUND" {
			t.Fatalf("expected SESSION_NOT_FOUND, got %v", err)
		}
	})

	t.Run("unknown project does not silently allow preview", func(t *testing.T) {
		st := newFakeStore()
		st.sessions["orphan-1"] = domain.SessionRecord{ID: "orphan-1", ProjectID: "gone"}
		if err := (&Service{store: st}).EnsurePreviewAllowed(context.Background(), "orphan-1"); err == nil {
			t.Fatal("a session whose project cannot be read must not be treated as web-UI-enabled")
		}
	})
}

package ports

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// NotificationIntent is the lifecycle-to-notification-producer contract. It is
// not an HTTP DTO; lifecycle fills it from facts it already has after the
// underlying session/PR state write succeeds.
type NotificationIntent struct {
	Type      domain.NotificationType
	SessionID domain.SessionID
	ProjectID domain.ProjectID
	PRURL     string
	CreatedAt time.Time

	// Enrichment hints. These avoid storage reads on the hot path.
	SessionDisplayName string
	// SessionKind lets the copy name an orchestrator session, which carries no
	// display name by design, the way the renderer already names it.
	SessionKind    domain.SessionKind
	PRNumber       int
	PRTitle        string
	PRSourceBranch string
	PRTargetBranch string
	Provider       string
	Repo           string
}

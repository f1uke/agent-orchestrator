package scm

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ReviewThreadWriter is the provider-neutral write-capability contract for
// review threads. A provider adapter that supports replying to and
// resolving review threads implements this interface; the observer's
// runtime routing (a later task) type-asserts a Provider against it rather
// than requiring every read-only provider to satisfy it.
type ReviewThreadWriter interface {
	// ReplyToThread posts body as a reply on the given review thread and
	// returns the normalized comment the provider created.
	ReplyToThread(ctx context.Context, ref ports.SCMPRRef, threadID, body string) (ports.SCMReviewCommentObservation, error)
	// ResolveThread marks the given review thread resolved.
	ResolveThread(ctx context.Context, ref ports.SCMPRRef, threadID string) error
}

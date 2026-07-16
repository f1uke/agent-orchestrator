package controllers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/looptelemetry"
)

// LoopTelemetrySource is the controller-facing contract over the daemon's
// in-memory loop-timing registry.
type LoopTelemetrySource interface {
	Snapshot() []looptelemetry.LoopStatus
}

// DaemonController owns the read-only /daemon inspection routes.
type DaemonController struct {
	Loops LoopTelemetrySource
}

// DaemonLoop is one fixed-interval background loop's timing. LastRunAt/NextRunAt
// are omitted until the loop has ticked at least once (never-run state).
type DaemonLoop struct {
	Name        string     `json:"name"`
	DisplayName string     `json:"displayName"`
	Description string     `json:"description"`
	IntervalMs  int64      `json:"intervalMs"`
	LastRunAt   *time.Time `json:"lastRunAt,omitempty"`
	NextRunAt   *time.Time `json:"nextRunAt,omitempty"`
	Running     bool       `json:"running"`
}

// ListDaemonLoopsResponse is the body of GET /api/v1/daemon/loops.
type ListDaemonLoopsResponse struct {
	Loops []DaemonLoop `json:"loops"`
}

// Register mounts the daemon inspection routes on the supplied router.
func (c *DaemonController) Register(r chi.Router) {
	r.Get("/daemon/loops", c.listLoops)
}

func (c *DaemonController) listLoops(w http.ResponseWriter, r *http.Request) {
	if c.Loops == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/daemon/loops")
		return
	}
	snap := c.Loops.Snapshot()
	loops := make([]DaemonLoop, 0, len(snap))
	for _, l := range snap {
		loops = append(loops, DaemonLoop{
			Name:        l.Name,
			DisplayName: l.Display,
			Description: l.Description,
			IntervalMs:  l.IntervalMs,
			LastRunAt:   l.LastRunAt,
			NextRunAt:   l.NextRunAt,
			Running:     l.Running,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, ListDaemonLoopsResponse{Loops: loops})
}

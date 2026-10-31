package controllers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/chatusage"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// ChatUsageController serves the newest Claude subscription usage any chat
// session reported.
type ChatUsageController struct {
	Store *chatusage.Store
}

// Register mounts the chat usage route on the supplied router.
func (c *ChatUsageController) Register(r chi.Router) {
	r.Get("/chat/usage", c.get)
}

func (c *ChatUsageController) get(w http.ResponseWriter, r *http.Request) {
	if c.Store == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/chat/usage")
		return
	}
	u, ok := c.Store.Latest()
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ChatUsageResponse{
		FiveHour:  usageWindow(u.FiveHour),
		SevenDay:  usageWindow(u.SevenDay),
		UpdatedAt: u.UpdatedAt.UTC().Format(time.RFC3339),
	})
}

func usageWindow(w *chatproto.RateLimitWindow) *ChatUsageWindow {
	if w == nil {
		return nil
	}
	return &ChatUsageWindow{Utilization: w.Utilization, ResetsAt: w.ResetsAt.UTC().Format(time.RFC3339), Status: w.Status}
}

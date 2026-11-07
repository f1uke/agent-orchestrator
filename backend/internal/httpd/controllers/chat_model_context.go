package controllers

import (
	"net/http"
	"os"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatcontext"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

func (c *ChatFilesController) modelContext(w http.ResponseWriter, r *http.Request) {
	if c.Sessions == nil || c.LocateTranscript == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/chat/model-context")
		return
	}
	sess, err := c.Sessions.Get(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	path := c.LocateTranscript(sess.SessionRecord)
	if path == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	f, err := os.Open(path) //nolint:gosec // G304: the path comes from the session's own transcript locator
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	defer func() { _ = f.Close() }()
	ctx, err := chatcontext.Read(f)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	envelope.WriteJSON(w, http.StatusOK, modelContextResponse(ctx))
}

func modelContextResponse(ctx chatcontext.Context) ChatModelContextResponse {
	out := ChatModelContextResponse{
		Shapes:     make([]ChatModelShape, 0, len(ctx.Shapes)),
		Injections: make([]ChatContextInjection, 0, len(ctx.Injections)),
		Truncated:  ctx.Truncated,
	}
	for _, s := range ctx.Shapes {
		tools := make([]ChatModelTool, 0, len(s.Tools))
		for _, t := range s.Tools {
			tools = append(tools, ChatModelTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
		}
		out.Shapes = append(out.Shapes, ChatModelShape{TS: rfc3339(s.TS), System: s.System, Tools: tools})
	}
	for _, in := range ctx.Injections {
		out.Injections = append(out.Injections, ChatContextInjection{
			TS:        rfc3339(in.TS),
			Source:    in.Source,
			Role:      in.Role,
			Text:      in.Text,
			Truncated: in.Truncated,
		})
	}
	return out
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

package controllers

import (
	"io"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatfiles"
	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionhost"
)

const taskOutputTail = 64 << 10

func (c *ChatFilesController) taskOutput(w http.ResponseWriter, r *http.Request) {
	if c.Sessions == nil || c.DataDir == "" {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/chat/tasks/{taskId}/output")
		return
	}
	taskID := chi.URLParam(r, "taskId")
	if !chatfiles.ValidTaskID(taskID) {
		taskOutputNotFound(w, r)
		return
	}
	id := sessionID(r)
	if _, err := c.Sessions.Get(r.Context(), id); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	logPath, _ := sessionhost.Paths(c.DataDir, string(id))
	lf, err := os.Open(logPath) //nolint:gosec // G304: the session's own event log
	if err != nil {
		taskOutputNotFound(w, r)
		return
	}
	found, err := chatproto.FindTaskOutput(lf, taskID)
	_ = lf.Close()
	if err != nil || found.Path == "" {
		taskOutputNotFound(w, r)
		return
	}
	f, info, err := chatfiles.OpenTaskOutput(found.Path, taskID, found.ClaudeSessionIDs)
	if err != nil {
		taskOutputNotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	start := max(0, info.Size()-taskOutputTail)
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		taskOutputNotFound(w, r)
		return
	}
	text, err := io.ReadAll(io.LimitReader(f, taskOutputTail))
	if err != nil {
		taskOutputNotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	envelope.WriteJSON(w, http.StatusOK, ChatTaskOutputResponse{
		Text:       string(text),
		Size:       info.Size(),
		Truncated:  start > 0,
		ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
	})
}

func taskOutputNotFound(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "TASK_OUTPUT_NOT_FOUND", "Task output not found", nil)
}

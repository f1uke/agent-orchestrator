package controllers

import (
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatfiles"
	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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
	key := string(id) + "/" + taskID
	found, ok := c.taskOutputs.get(key)
	if !ok {
		found, ok = c.findTaskOutput(id, taskID)
	}
	if !ok {
		taskOutputNotFound(w, r)
		return
	}
	f, info, err := chatfiles.OpenTaskOutput(found.Path, taskID, found.ClaudeSessionIDs)
	if err != nil {
		taskOutputNotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	c.taskOutputs.put(key, found)
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

func (c *ChatFilesController) findTaskOutput(id domain.SessionID, taskID string) (chatproto.TaskOutput, bool) {
	logPath, _ := sessionhost.Paths(c.DataDir, string(id))
	lf, err := os.Open(logPath) //nolint:gosec // G304: the session's own event log
	if err != nil {
		return chatproto.TaskOutput{}, false
	}
	defer func() { _ = lf.Close() }()
	found, err := chatproto.FindTaskOutput(lf, taskID)
	return found, err == nil && found.Path != ""
}

const maxRememberedTasks = 512

type taskOutputPaths struct {
	mu    sync.Mutex
	paths map[string]chatproto.TaskOutput
}

func (t *taskOutputPaths) get(key string) (chatproto.TaskOutput, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	found, ok := t.paths[key]
	return found, ok
}

func (t *taskOutputPaths) put(key string, found chatproto.TaskOutput) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.paths == nil || len(t.paths) >= maxRememberedTasks {
		t.paths = map[string]chatproto.TaskOutput{}
	}
	t.paths[key] = found
}

func taskOutputNotFound(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "TASK_OUTPUT_NOT_FOUND", "Task output not found", nil)
}

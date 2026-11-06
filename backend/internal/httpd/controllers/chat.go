package controllers

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatfiles"
	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionhost"
)

// ChatService drives a chat session through its session host.
type ChatService interface {
	Send(ctx context.Context, id domain.SessionID, in chatproto.UserInput) (uuid string, err error)
	Answer(ctx context.Context, id domain.SessionID, requestID string, d chatproto.Decision) error
	Interrupt(ctx context.Context, id domain.SessionID) error
	SetMode(ctx context.Context, id domain.SessionID, mode chatproto.PermissionMode) error
	SetModel(ctx context.Context, id domain.SessionID, model string) error
	SetChrome(ctx context.Context, id domain.SessionID, enabled bool) error
	StopTask(ctx context.Context, id domain.SessionID, taskID string) error
	Rewind(ctx context.Context, id domain.SessionID, uuid string, restoreFiles bool) error
	RewindPreview(ctx context.Context, id domain.SessionID, uuid string) (sessionhost.RewindPreview, error)
	SetRemoteControl(ctx context.Context, id domain.SessionID, enabled bool) error
}

// ChatController owns the routes the chat view drives a session with.
type ChatController struct {
	Svc ChatService
}

// Register mounts the chat routes on the supplied router.
func (c *ChatController) Register(r chi.Router) {
	r.Post("/sessions/{sessionId}/chat/messages", c.send)
	r.Post("/sessions/{sessionId}/chat/answers", c.answer)
	r.Post("/sessions/{sessionId}/chat/interrupt", c.interrupt)
	r.Post("/sessions/{sessionId}/chat/tasks/{taskId}/stop", c.stopTask)
	r.Post("/sessions/{sessionId}/chat/rewind", c.rewind)
	r.Post("/sessions/{sessionId}/chat/rewind/preview", c.rewindPreview)
	r.Put("/sessions/{sessionId}/chat/mode", c.setMode)
	r.Put("/sessions/{sessionId}/chat/model", c.setModel)
	r.Put("/sessions/{sessionId}/chat/remote-control", c.setRemoteControl)
	r.Put("/sessions/{sessionId}/chat/chrome", c.setChrome)
}

func chatBadRequest(w http.ResponseWriter, r *http.Request, code, message string) {
	envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", code, message, nil)
}

func (c *ChatController) send(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/chat/messages")
		return
	}
	var body SendChatMessageRequest
	if err := decodeJSON(r, &body); err != nil {
		chatBadRequest(w, r, "INVALID_JSON", "Invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Text) == "" && len(body.Images) == 0 {
		chatBadRequest(w, r, "TEXT_REQUIRED", "Text or an image is required")
		return
	}
	if len(body.Images) > maxChatImages {
		chatBadRequest(w, r, "TOO_MANY_IMAGES", "A message carries at most 20 images")
		return
	}
	in := chatproto.UserInput{Text: body.Text}
	for _, img := range body.Images {
		if code, msg := checkChatImage(img); code != "" {
			chatBadRequest(w, r, code, msg)
			return
		}
		in.Images = append(in.Images, chatproto.Image{MediaType: img.MediaType, Base64: img.Base64})
	}
	uuid, err := c.Svc.Send(r.Context(), sessionID(r), in)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SendChatMessageResponse{UUID: uuid})
}

const (
	maxChatImages     = 20
	maxChatImageBytes = 5 << 20
)

// chatImageTypes are the image types claude reads; anything else is refused before it reaches the host.
var chatImageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

func checkChatImage(img ChatImage) (code, message string) {
	if !chatImageTypes[img.MediaType] || img.Base64 == "" {
		return "IMAGE_INVALID", "Each image needs a png, jpeg, gif or webp mediaType and base64 data"
	}
	if base64.StdEncoding.DecodedLen(len(img.Base64)) > maxChatImageBytes+2 {
		return "IMAGE_TOO_LARGE", "Each image is at most 5 MiB"
	}
	data, err := base64.StdEncoding.DecodeString(img.Base64)
	if err != nil {
		return "IMAGE_INVALID", "Image data is not base64"
	}
	if len(data) > maxChatImageBytes {
		return "IMAGE_TOO_LARGE", "Each image is at most 5 MiB"
	}
	return "", ""
}

func (c *ChatController) answer(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/chat/answers")
		return
	}
	var body AnswerChatRequest
	if err := decodeJSON(r, &body); err != nil {
		chatBadRequest(w, r, "INVALID_JSON", "Invalid JSON body")
		return
	}
	if body.RequestID == "" {
		chatBadRequest(w, r, "REQUEST_ID_REQUIRED", "requestId is required")
		return
	}
	var d chatproto.Decision
	switch body.Behavior {
	case "allow":
		d = chatproto.Allow{UpdatedInput: body.UpdatedInput, UpdatedPermissions: body.UpdatedPermissions}
	case "deny":
		d = chatproto.Deny{Message: body.Message, Interrupt: body.Interrupt}
	default:
		chatBadRequest(w, r, "INVALID_BEHAVIOR", "behavior must be allow or deny")
		return
	}
	if err := c.Svc.Answer(r.Context(), sessionID(r), body.RequestID, d); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ChatAckResponse{OK: true})
}

func (c *ChatController) interrupt(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/chat/interrupt")
		return
	}
	if err := c.Svc.Interrupt(r.Context(), sessionID(r)); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ChatAckResponse{OK: true})
}

func (c *ChatController) stopTask(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/chat/tasks/{taskId}/stop")
		return
	}
	taskID := chi.URLParam(r, "taskId")
	if !chatfiles.ValidTaskID(taskID) {
		chatBadRequest(w, r, "INVALID_TASK_ID", "taskId must use only letters, digits, underscore and dash")
		return
	}
	if err := c.Svc.StopTask(r.Context(), sessionID(r), taskID); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ChatAckResponse{OK: true})
}

func (c *ChatController) rewind(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/chat/rewind")
		return
	}
	var body RewindChatRequest
	if err := decodeJSON(r, &body); err != nil {
		chatBadRequest(w, r, "INVALID_JSON", "Invalid JSON body")
		return
	}
	if body.UUID == "" {
		chatBadRequest(w, r, "UUID_REQUIRED", "uuid is required")
		return
	}
	if err := c.Svc.Rewind(r.Context(), sessionID(r), body.UUID, body.RestoreFiles); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ChatAckResponse{OK: true})
}

func (c *ChatController) rewindPreview(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/chat/rewind/preview")
		return
	}
	var body RewindChatPreviewRequest
	if err := decodeJSON(r, &body); err != nil {
		chatBadRequest(w, r, "INVALID_JSON", "Invalid JSON body")
		return
	}
	if body.UUID == "" {
		chatBadRequest(w, r, "UUID_REQUIRED", "uuid is required")
		return
	}
	p, err := c.Svc.RewindPreview(r.Context(), sessionID(r), body.UUID)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, RewindChatPreviewResponse{
		CanRewind: p.CanRewind, Error: p.Error, FilesChanged: p.FilesChanged, Insertions: p.Insertions, Deletions: p.Deletions,
	})
}

func (c *ChatController) setMode(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/sessions/{sessionId}/chat/mode")
		return
	}
	var body SetChatModeRequest
	if err := decodeJSON(r, &body); err != nil {
		chatBadRequest(w, r, "INVALID_JSON", "Invalid JSON body")
		return
	}
	mode, err := chatproto.ParsePermissionMode(body.Mode)
	if err != nil {
		chatBadRequest(w, r, "INVALID_MODE", err.Error())
		return
	}
	if err := c.Svc.SetMode(r.Context(), sessionID(r), mode); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ChatAckResponse{OK: true})
}

func (c *ChatController) setModel(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/sessions/{sessionId}/chat/model")
		return
	}
	var body SetChatModelRequest
	if err := decodeJSON(r, &body); err != nil {
		chatBadRequest(w, r, "INVALID_JSON", "Invalid JSON body")
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		chatBadRequest(w, r, "MODEL_REQUIRED", "model is required")
		return
	}
	if err := c.Svc.SetModel(r.Context(), sessionID(r), model); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ChatAckResponse{OK: true})
}

func (c *ChatController) setRemoteControl(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/sessions/{sessionId}/chat/remote-control")
		return
	}
	var body SetChatRemoteControlRequest
	if err := decodeJSON(r, &body); err != nil {
		chatBadRequest(w, r, "INVALID_JSON", "Invalid JSON body")
		return
	}
	if body.Enabled == nil {
		chatBadRequest(w, r, "ENABLED_REQUIRED", "enabled is required")
		return
	}
	if err := c.Svc.SetRemoteControl(r.Context(), sessionID(r), *body.Enabled); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ChatAckResponse{OK: true})
}

func (c *ChatController) setChrome(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/sessions/{sessionId}/chat/chrome")
		return
	}
	var body SetChatChromeRequest
	if err := decodeJSON(r, &body); err != nil {
		chatBadRequest(w, r, "INVALID_JSON", "Invalid JSON body")
		return
	}
	if body.Enabled == nil {
		chatBadRequest(w, r, "ENABLED_REQUIRED", "enabled is required")
		return
	}
	if err := c.Svc.SetChrome(r.Context(), sessionID(r), *body.Enabled); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ChatAckResponse{OK: true})
}

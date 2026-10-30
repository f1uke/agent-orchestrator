package controllers

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// ChatService drives a chat session through its session host.
type ChatService interface {
	Send(ctx context.Context, id domain.SessionID, in chatproto.UserInput) (uuid string, err error)
	Answer(ctx context.Context, id domain.SessionID, requestID string, d chatproto.Decision) error
	Interrupt(ctx context.Context, id domain.SessionID) error
	SetMode(ctx context.Context, id domain.SessionID, mode chatproto.PermissionMode) error
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
	r.Put("/sessions/{sessionId}/chat/mode", c.setMode)
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
	if strings.TrimSpace(body.Text) == "" {
		chatBadRequest(w, r, "TEXT_REQUIRED", "Text is required")
		return
	}
	in := chatproto.UserInput{Text: body.Text}
	for _, img := range body.Images {
		if img.MediaType == "" || img.Base64 == "" {
			chatBadRequest(w, r, "IMAGE_INVALID", "Each image needs mediaType and base64")
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

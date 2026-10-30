// Package chat drives a chat session through its session host: it finds the
// session, checks it is a chat session, and calls the host on its socket.
package chat

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionhost"
)

// Sessions finds the sessions chat requests are addressed to. The session store
// satisfies it.
type Sessions interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// Service sends a chat session's human input to its session host.
type Service struct {
	sessions Sessions
	dataDir  string
}

// New returns a Service whose hosts keep their sockets under dataDir.
func New(sessions Sessions, dataDir string) *Service {
	return &Service{sessions: sessions, dataDir: dataDir}
}

// Send hands in to the session's claude as a message the human typed and
// returns the uuid the host gave it.
func (s *Service) Send(ctx context.Context, id domain.SessionID, in chatproto.UserInput) (string, error) {
	c, err := s.client(ctx, id)
	if err != nil {
		return "", err
	}
	uuid, err := c.Send(ctx, in, nil)
	return uuid, hostError(id, err)
}

// Answer settles the pending request requestID of the session with d.
func (s *Service) Answer(ctx context.Context, id domain.SessionID, requestID string, d chatproto.Decision) error {
	c, err := s.client(ctx, id)
	if err != nil {
		return err
	}
	return hostError(id, c.Answer(ctx, requestID, d))
}

// Interrupt stops the turn the session's claude is running.
func (s *Service) Interrupt(ctx context.Context, id domain.SessionID) error {
	c, err := s.client(ctx, id)
	if err != nil {
		return err
	}
	return hostError(id, c.Interrupt(ctx))
}

// SetMode changes the permission mode of the session's claude.
func (s *Service) SetMode(ctx context.Context, id domain.SessionID, mode chatproto.PermissionMode) error {
	c, err := s.client(ctx, id)
	if err != nil {
		return err
	}
	return hostError(id, c.SetMode(ctx, mode))
}

// SetModel switches the session's claude to model. Claude checks the model, so
// one it does not know comes back as CHAT_REQUEST_REJECTED with its reason.
func (s *Service) SetModel(ctx context.Context, id domain.SessionID, model string) error {
	c, err := s.client(ctx, id)
	if err != nil {
		return err
	}
	return hostError(id, c.SetModel(ctx, model))
}

func (s *Service) client(ctx context.Context, id domain.SessionID) (sessionhost.Client, error) {
	rec, ok, err := s.sessions.GetSession(ctx, id)
	switch {
	case err != nil:
		return sessionhost.Client{}, fmt.Errorf("chat: get session %s: %w", id, err)
	case !ok:
		return sessionhost.Client{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	case rec.Transport.WithDefault() != domain.TransportChat:
		return sessionhost.Client{}, apierr.Conflict("NOT_CHAT_SESSION", "Session is not a chat session", nil)
	}
	_, socket := sessionhost.Paths(s.dataDir, string(id))
	return sessionhost.NewClient(socket), nil
}

// HostNotRunning is the API error for a chat session whose host nobody answers.
func HostNotRunning() *apierr.Error {
	return apierr.Conflict("SESSION_HOST_NOT_RUNNING",
		"The session's host is not running: the session is asleep or restarting. Try again once it is awake.", nil)
}

// HostRefused is the API error for a request the session's host turned down,
// carrying the host's own reason, such as "claude is not running".
func HostRefused(refused *sessionhost.RefusedError) *apierr.Error {
	return apierr.Conflict("CHAT_REQUEST_REJECTED", refused.Reason, nil)
}

func hostError(id domain.SessionID, err error) error {
	var refused *sessionhost.RefusedError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sessionhost.ErrNotReachable):
		return HostNotRunning()
	case errors.As(err, &refused):
		return HostRefused(refused)
	}
	return fmt.Errorf("chat: session %s: %w", id, err)
}

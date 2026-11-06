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

// SetChrome turns Claude in Chrome on or off for the session by relaunching its
// claude on the same conversation. A turn in flight is CHAT_REQUEST_REJECTED.
func (s *Service) SetChrome(ctx context.Context, id domain.SessionID, enabled bool) error {
	c, err := s.client(ctx, id)
	if err != nil {
		return err
	}
	return hostError(id, c.SetChrome(ctx, enabled))
}

// StopTask ends the background task taskID of the session. Claude may refuse,
// in a mode that cannot stop tasks: that is CHAT_REQUEST_REJECTED with its reason.
func (s *Service) StopTask(ctx context.Context, id domain.SessionID, taskID string) error {
	c, err := s.client(ctx, id)
	if err != nil {
		return err
	}
	return hostError(id, c.StopTask(ctx, taskID))
}

// Rewind drops the user message uuid and everything after it from the
// session's conversation, and with restoreFiles puts the files claude changed
// since then back. Claude's refusal, such as a running turn, is
// CHAT_REQUEST_REJECTED with its reason.
func (s *Service) Rewind(ctx context.Context, id domain.SessionID, uuid string, restoreFiles bool) error {
	c, err := s.client(ctx, id)
	if err != nil {
		return err
	}
	return hostError(id, c.Rewind(ctx, uuid, restoreFiles))
}

// RewindPreview reports what restoring files to before the user message uuid
// would change.
func (s *Service) RewindPreview(ctx context.Context, id domain.SessionID, uuid string) (sessionhost.RewindPreview, error) {
	c, err := s.client(ctx, id)
	if err != nil {
		return sessionhost.RewindPreview{}, err
	}
	p, err := c.RewindPreview(ctx, uuid)
	return p, hostError(id, err)
}

// SetRemoteControl turns Remote Control on or off for the session. Claude may
// refuse, for an account or a setup that cannot use it: that is
// CHAT_REQUEST_REJECTED with its reason.
func (s *Service) SetRemoteControl(ctx context.Context, id domain.SessionID, enabled bool) error {
	c, err := s.client(ctx, id)
	if err != nil {
		return err
	}
	return hostError(id, c.RemoteControl(ctx, enabled))
}

// RunBash runs command, which the person typed after "!" in the chat, in the
// session's worktree and returns the run's id. The host refuses it while a turn
// is open, a permission is pending or another command has not finished: that
// is CHAT_REQUEST_REJECTED with its reason.
func (s *Service) RunBash(ctx context.Context, id domain.SessionID, command string) (string, error) {
	c, err := s.client(ctx, id)
	if err != nil {
		return "", err
	}
	runID, err := c.RunBash(ctx, command)
	return runID, hostError(id, err)
}

// StopBash stops the session's "!" command run runID. A finished or unknown
// run is a no-op.
func (s *Service) StopBash(ctx context.Context, id domain.SessionID, runID string) error {
	c, err := s.client(ctx, id)
	if err != nil {
		return err
	}
	return hostError(id, c.StopBash(ctx, runID))
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

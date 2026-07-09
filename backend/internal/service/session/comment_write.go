package session

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// resolveThreadRef runs the Phase-4a authz (session exists + PR belongs to
// session) and rebuilds the SCM ref for a write. Returns the ref for the
// matched PR.
func (s *Service) resolveThreadRef(ctx context.Context, id domain.SessionID, prURL string) (ports.SCMPRRef, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return ports.SCMPRRef{}, err
	}
	if !ok {
		return ports.SCMPRRef{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	prs, err := s.store.ListPRsBySession(ctx, id)
	if err != nil {
		return ports.SCMPRRef{}, err
	}
	var number int
	found := false
	for _, p := range prs {
		if p.URL == prURL {
			number, found = p.Number, true
			break
		}
	}
	if !found {
		return ports.SCMPRRef{}, apierr.NotFound("PR_NOT_FOUND", "Unknown PR for session")
	}
	if s.scm == nil {
		return ports.SCMPRRef{}, ErrSCMUnavailable
	}
	var origin string
	if proj, ok, err := s.store.GetProject(ctx, string(rec.ProjectID)); err == nil && ok {
		origin = proj.RepoOriginURL
	}
	repo, err := scmRepoForClaim(s.scm, origin, prURL)
	if err != nil {
		return ports.SCMPRRef{}, err
	}
	return ports.SCMPRRef{Repo: repo, Number: number, URL: prURL}, nil
}

// mapThreadWriteErr converts provider-neutral SCM write sentinels into the API
// error vocabulary. ErrSCMNotFound maps to a 404 THREAD_NOT_FOUND; ErrSCMForbidden
// maps to ErrSCMWriteForbidden (the controller renders 403); everything else
// maps to ErrSCMUnavailable (503).
func mapThreadWriteErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ports.ErrSCMNotFound):
		return apierr.NotFound("THREAD_NOT_FOUND", "Review thread not found")
	case errors.Is(err, ports.ErrSCMForbidden):
		return ErrSCMWriteForbidden
	default:
		return fmt.Errorf("%w: %w", ErrSCMUnavailable, err)
	}
}

// ReplyToThread posts a reply comment on a PR review thread and returns the
// newly created comment as the session-facing read model.
func (s *Service) ReplyToThread(ctx context.Context, id domain.SessionID, prURL, threadID, body string) (PRThreadComment, error) {
	ref, err := s.resolveThreadRef(ctx, id, prURL)
	if err != nil {
		return PRThreadComment{}, err
	}
	obs, err := s.scm.ReplyToThread(ctx, ref, threadID, body)
	if err != nil {
		return PRThreadComment{}, mapThreadWriteErr(err)
	}
	return PRThreadComment{
		ID:        obs.ID,
		Author:    obs.Author,
		Body:      obs.Body,
		URL:       obs.URL,
		Resolved:  false,
		IsBot:     obs.IsBot,
		CreatedAt: s.clock().UTC(),
	}, nil
}

// ResolveThread marks a PR review thread resolved on the SCM provider.
func (s *Service) ResolveThread(ctx context.Context, id domain.SessionID, prURL, threadID string) error {
	ref, err := s.resolveThreadRef(ctx, id, prURL)
	if err != nil {
		return err
	}
	return mapThreadWriteErr(s.scm.ResolveThread(ctx, ref, threadID))
}

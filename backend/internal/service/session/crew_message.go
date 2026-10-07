package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// CREW TALK, AND THE THING THAT ENDS IT.
//
// Two agents that can both run and both message each other will loop forever if
// nothing stops them, so the caps here are the mechanism, not the prompt. See
// domain/crewmessage.go for the four rules and why each exists.
//
// Everything in this file is reached only when the sender and the recipient are
// two DIFFERENT members of ONE crew. A message from a human, from the
// orchestrator, from the CLI with no session behind it, or to a solo session, is
// none of those things and takes the untouched path.

// CrewTalk is the sender's half of a message: who sent it, and what durable
// artifact it is about.
type CrewTalk struct {
	// From is the session that sent it. Empty when a human or a tool sent it,
	// which is what makes an ordinary `ao send` ordinary.
	From domain.SessionID
	// Subject is the commit SHA, or on a Testiny project a case or run id, the
	// message is about. Required between crewmates and ignored everywhere else.
	Subject string
}

// CrewSend is one message from one crew member to the other, as the caller
// asked for it.
type CrewSend struct {
	Role    domain.CrewRole
	Message string
	Subject string
}

// CrewSendResult is what came of it: who it reached and what the queue did with
// it.
type CrewSendResult struct {
	Peer    domain.SessionID
	Outcome ports.SendOutcome
}

// SendToCrewmate delivers a message addressed by ROLE rather than by session id.
//
// It exists because dev CANNOT know qa's id from its environment: a crew is
// formed after dev's runtime is already launched, and a qa attached later
// arrives later still, so an env var would be empty exactly when it mattered.
// A role never goes stale, and the daemon is the thing that knows who fills it.
func (s *Service) SendToCrewmate(ctx context.Context, from domain.SessionID, in CrewSend) (CrewSendResult, error) {
	if !in.Role.Valid() {
		return CrewSendResult{}, apierr.Invalid("INVALID_CREW_ROLE", "Role must be dev or qa", nil)
	}
	sender, ok, err := s.store.GetSession(ctx, from)
	if err != nil {
		return CrewSendResult{}, fmt.Errorf("crew send %s: %w", from, err)
	}
	if !ok {
		return CrewSendResult{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if !sender.InCrew() {
		return CrewSendResult{}, apierr.Invalid("NOT_IN_A_CREW",
			"This session is working alone, so it has no crewmate to message", nil)
	}
	peer, found, err := s.manager.CrewMember(ctx, from, in.Role)
	if err != nil {
		return CrewSendResult{}, toAPIError(err)
	}
	if !found || peer.ID == from {
		return CrewSendResult{}, apierr.NotFound("CREW_ROLE_ABSENT",
			fmt.Sprintf("This task has no %s to message", in.Role))
	}
	sent, err := s.SendFrom(ctx, peer.ID, in.Message, CrewTalk{From: from, Subject: in.Subject})
	return CrewSendResult{Peer: peer.ID, Outcome: sent.Outcome}, err
}

// crewTalkCheck decides whether this message is crew talk at all and, if it is,
// whether it may go through. It returns the row to record - which is written
// whatever the answer, because a refusal is the escalation signal - or nothing
// at all when the two parties are not crewmates.
func (s *Service) crewTalkCheck(ctx context.Context, to domain.SessionRecord, talk CrewTalk) (*domain.CrewMessage, error) {
	if talk.From == "" || talk.From == to.ID || !to.InCrew() {
		return nil, nil
	}
	sender, ok, err := s.store.GetSession(ctx, talk.From)
	if err != nil {
		return nil, fmt.Errorf("crew talk %s: %w", talk.From, err)
	}
	// Not crewmates: an orchestrator, another task's worker, a stale id. None of
	// those is the runaway class this guards, so none of them is capped.
	if !ok || !sender.InCrew() || sender.CrewID != to.CrewID {
		return nil, nil
	}

	now := s.now()
	msg := &domain.CrewMessage{
		ID:        uuid.NewString(),
		CrewID:    to.CrewID,
		ProjectID: to.ProjectID,
		From:      sender.ID,
		To:        to.ID,
		Subject:   domain.NormalizeCrewSubject(talk.Subject),
		CreatedAt: now,
	}

	if msg.Subject == "" {
		msg.RefusedReason = "a message to your crewmate has to say what it is ABOUT - pass --about with the commit SHA, or on a Testiny project the case or run id, it concerns"
		return msg, nil
	}
	sent, err := s.store.CrewMessagesOnSubject(ctx, to.CrewID, msg.Subject, sender.ID, crewRoundStart(sender, to))
	if err != nil {
		return nil, err
	}
	if sent >= domain.CappedRepeat {
		msg.RefusedReason = fmt.Sprintf(
			"you have already sent %s %d messages about %s and nothing has moved; this task is now at NEEDS YOU for a human to look at. Answer with the artifact instead - commit, or record a result - and message again only about something new",
			roleOrID(to), sent, msg.Subject)
		return msg, nil
	}
	since := now.Add(-time.Hour)
	inHour, err := s.store.CrewMessagesSince(ctx, to.CrewID, since)
	if err != nil {
		return nil, err
	}
	if inHour >= domain.CrewMessagesPerHour {
		msg.RefusedReason = fmt.Sprintf(
			"this crew has exchanged %d messages in the last hour, which is its whole budget; the task is now at NEEDS YOU for a human to look at",
			inHour)
		return msg, nil
	}
	return msg, nil
}

// crewRoundStart is the moment this crew's CURRENT round began - the cutoff the
// per-subject cap counts from.
//
// It is the LATER of the two members' boundaries because a round belongs to the
// TASK, not to one seat: a qa brought back for a second look is a new round for
// dev's briefs AND for qa's findings, and scoping it to the recipient alone
// would clear one leg of the conversation while leaving the other refusing on a
// commit nobody disputes.
//
// The zero time means no member has ever been restored, and a zero cutoff counts
// every row - which is exactly what the cap did before rounds existed, so a crew
// that has only ever had one round behaves identically.
func crewRoundStart(a, b domain.SessionRecord) time.Time {
	if b.CrewRoundStartedAt.After(a.CrewRoundStartedAt) {
		return b.CrewRoundStartedAt
	}
	return a.CrewRoundStartedAt
}

// roleOrID names the recipient the way a human would - "qa" - falling back to
// the id when a member somehow has no role.
func roleOrID(rec domain.SessionRecord) string {
	if role := strings.TrimSpace(string(rec.CrewRole)); role != "" {
		return role
	}
	return string(rec.ID)
}

// crewTalkRefused reports whether this session's MOST RECENT message to its
// crewmate was refused, which is the whole of the NEEDS YOU derivation.
//
// It is the latest attempt and not "any refusal ever" on purpose: the escalation
// has to clear itself, and it does the moment a later message goes through -
// which happens as soon as the two move on to a new commit or a new case. A
// session that has never messaged a crewmate (every solo session, and most crew
// members) has no row and reads false.
func (s *Service) crewTalkRefused(ctx context.Context, rec domain.SessionRecord) (bool, error) {
	if !rec.InCrew() {
		return false, nil
	}
	latest, ok, err := s.store.LatestCrewMessageFrom(ctx, rec.ID)
	if err != nil {
		return false, err
	}
	return ok && latest.Refused(), nil
}

// lastHandback is the latest message this qa DELIVERED to its crewmate in its
// current round, which is what the board reads as "qa has handed back". Only qa
// hands back, so every other session skips the lookup.
func (s *Service) lastHandback(ctx context.Context, rec domain.SessionRecord) (*domain.CrewHandback, error) {
	if rec.CrewRole != domain.CrewRoleQA {
		return nil, nil
	}
	msg, ok, err := s.store.LatestDeliveredCrewMessageFrom(ctx, rec.ID, rec.CrewRoundStartedAt)
	if err != nil || !ok {
		return nil, err
	}
	return &domain.CrewHandback{At: msg.CreatedAt, About: msg.Subject}, nil
}

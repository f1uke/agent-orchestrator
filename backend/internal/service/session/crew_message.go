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
	// Subject is the commit SHA or smoke case id the message is about. Required
	// between crewmates and ignored everywhere else.
	Subject string
}

// CrewSend is one message from one crew member to the other, as the caller
// asked for it. StillWorking is the sender's own claim about its run and is the
// one field the daemon acts on beyond delivery; see handbackGap.
type CrewSend struct {
	Role    domain.CrewRole
	Message string
	Subject string
	// StillWorking is qa saying "I am NOT finished yet" - an ordinary mid-run
	// message rather than the end of its run. It exists so the completeness check
	// below never pushes qa into declaring cases undriveable just to get past it:
	// a gate that is easier to satisfy by lying is worse than no gate. The DEFAULT
	// is the checked shape, because the shape qa is prompted to hand back with is
	// a plain `ao send --crew dev` - so forgetting is caught and opting out costs
	// an explicit claim the human can read.
	StillWorking bool
}

// CrewSendResult is what came of it: who it reached, what the queue did with it,
// the message as DELIVERED (the gate may have appended a line of AO's own), and
// what the checklist said at handback.
type CrewSendResult struct {
	Peer     domain.SessionID
	Outcome  ports.SendOutcome
	Message  string
	Handback HandbackCompleteness
}

// HandbackCompleteness is the state of the task's smoke checklist at the moment
// qa said its run was over.
//
// It exists because "not driven yet" and "cannot be driven" are the SAME empty
// case on screen, so a run that ended with cases untouched looked exactly like
// one that ended with cases that could not be driven - and neither the human nor
// qa itself could tell. Both states are now sayable: driven is a recorded run,
// undriveable is `ao smoke record --verdict skip --note "<why>"`, which IS a
// recorded run. What remains in neither is this.
type HandbackCompleteness struct {
	// Checked is false for every message the gate did not look at: dev's, a
	// mid-run `--still-working`, a task with no checklist at all.
	Checked bool
	// Cases is how many active cases the person still has to play - the
	// denominator the gap is out of.
	Cases int
	// NotDriven names the cases carrying nothing from any machine, oldest first.
	NotDriven []string
}

// Incomplete reports whether qa handed back over cases it never touched.
func (h HandbackCompleteness) Incomplete() bool { return len(h.NotDriven) > 0 }

// SendToCrewmate delivers a message addressed by ROLE rather than by session id.
//
// It exists because dev CANNOT know qa's id from its environment: a crew is
// formed after dev's runtime is already launched, and a qa attached later
// arrives later still, so an env var would be empty exactly when it mattered.
// A role never goes stale, and the daemon is the thing that knows who fills it.
//
// It is also where qa's handback is CHECKED, because this is the one path a
// handback takes. See handbackGap for what is checked and why it warns instead
// of refusing.
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
	handback := s.handbackGap(ctx, sender, in)
	message := in.Message
	if handback.Incomplete() {
		message += handbackNotice(handback)
	}
	sent, err := s.SendFrom(ctx, peer.ID, message, CrewTalk{From: from, Subject: in.Subject})
	return CrewSendResult{Peer: peer.ID, Outcome: sent.Outcome, Message: message, Handback: handback}, err
}

// handbackGap answers "did qa just end its run over cases nobody drove?".
//
// It runs on ONE leg - qa -> dev, not claiming to be still working - because that
// is the shape of a handback, and it reads the checklist under the TASK's id
// (dev's, which is what $AO_CREW_ID is and what every `ao smoke` command takes).
//
// A store failure here returns no gap rather than an error. The check exists to
// make a silence visible; letting it break the handback would trade a reported
// gap for the stall the handback obligation was written to prevent.
func (s *Service) handbackGap(ctx context.Context, sender domain.SessionRecord, in CrewSend) HandbackCompleteness {
	if sender.CrewRole != domain.CrewRoleQA || in.Role != domain.CrewRoleDev || in.StillWorking {
		return HandbackCompleteness{}
	}
	checks, err := s.store.ListSmokeChecksBySession(ctx, sender.CrewID)
	if err != nil {
		return HandbackCompleteness{}
	}
	out := HandbackCompleteness{Checked: true, NotDriven: domain.SmokeHandbackGap(checks)}
	for _, c := range checks {
		if !c.Retired() {
			out.Cases++
		}
	}
	return out
}

// handbackNotice is the line AO adds to the message dev actually receives.
//
// The gate WARNS rather than refuses, and this is what "loudly" means for the
// member that would otherwise carry on believing the change had been verified.
// Refusing was the alternative and it is worse three times over: it recreates the
// silent stall the handback obligation exists to prevent, it is indistinguishable
// from the runaway-loop refusal that parks a task at NEEDS YOU, and it is the
// version of this gate that is easiest to satisfy by declaring the remaining
// cases undriveable. So the message goes, carrying the truth with it.
//
// It is written in AO's voice and marked as such: it is a fact about the
// checklist, not something qa said.
func handbackNotice(h HandbackCompleteness) string {
	noun, pronoun, is := "case", "it", "it is"
	if len(h.NotDriven) != 1 {
		noun, pronoun, is = "cases", "them", "they are"
	}
	return fmt.Sprintf("\n\n[AO] This handback left %d of %d checklist %s with nothing recorded by any machine: %s. Nothing was run against %s and no reason was given for leaving %s, so %s \"nobody looked\" and not \"nothing could reach %s\".",
		len(h.NotDriven), h.Cases, noun, strings.Join(h.NotDriven, ", "), pronoun, pronoun, is, pronoun)
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
		msg.RefusedReason = "a message to your crewmate has to say what it is ABOUT - pass --about with the commit SHA or the smoke case id it concerns"
		return msg, nil
	}
	sent, err := s.store.CrewMessagesOnSubject(ctx, to.CrewID, msg.Subject, sender.ID)
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

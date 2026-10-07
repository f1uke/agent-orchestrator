package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// WHAT STOPS TWO AGENTS TALKING FOREVER.
//
// dev and qa run at the same time and can each message the other. Nothing about
// being well-prompted terminates that: an agent that answers every message will
// answer this one too, and there is no human in the loop to notice the bill. So
// the stopping rules are mechanism, and these are the assertions that they are.
//
// Every test here needs TWO members of ONE crew. A message that is not between
// crewmates - from a human, from the orchestrator, to a solo session - is not the
// runaway class this guards and is never counted, never capped, never recorded.

func crewPair(st *fakeStore) (dev, qa domain.SessionRecord) {
	dev = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		CrewID: "mer-1", CrewRole: domain.CrewRoleDev,
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	qa = domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker,
		CrewID: "mer-1", CrewRole: domain.CrewRoleQA,
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	st.sessions[dev.ID] = dev
	st.sessions[qa.ID] = qa
	return dev, qa
}

func crewService(st *fakeStore, fc *fakeCommander) *Service {
	if fc.crewMembers == nil {
		fc.crewMembers = map[crewSeat]domain.SessionRecord{}
	}
	for _, rec := range st.sessions {
		for _, other := range st.sessions {
			if other.CrewID == rec.CrewID && other.CrewID != "" {
				fc.crewMembers[crewSeat{id: rec.ID, role: other.CrewRole}] = other
			}
		}
	}
	return &Service{store: st, manager: fc, clock: func() time.Time { return time.Unix(1000, 0).UTC() }}
}

// A message between crewmates with NO SUBJECT is refused. This is what removes
// "what do you think?" from the vocabulary: every message is about a durable
// artifact, which is what makes an exchange finite and checkable.
func TestCrewTalk_ASubjectIsRequired(t *testing.T) {
	st := newFakeStore()
	dev, qa := crewPair(st)
	svc := crewService(st, &fakeCommander{})

	_, err := svc.SendFrom(context.Background(), qa.ID, "have a look?", CrewTalk{From: dev.ID})
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindConflict {
		t.Fatalf("a subjectless message between crewmates = %v, want a conflict", err)
	}
	if !strings.Contains(e.Message, "--about") {
		t.Fatalf("the refusal does not say how to fix it: %q", e.Message)
	}
	if len(st.crewMessages) != 1 || !st.crewMessages[0].Refused() {
		t.Fatalf("the refused attempt was not recorded: %+v", st.crewMessages)
	}
}

// THE ONE THAT TERMINATES THE LOOP. Three messages about one subject in one
// direction go through; the fourth is REFUSED, and a refusal cannot be retried
// into a loop because the same subject always refuses.
func TestCrewTalk_TheFourthMessageAboutOneSubjectIsRefused(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	dev, qa := crewPair(st)
	svc := crewService(st, &fakeCommander{})

	for i := range domain.CappedRepeat {
		if _, err := svc.SendFrom(ctx, dev.ID, "look again", CrewTalk{From: qa.ID, Subject: "4a1b2c3"}); err != nil {
			t.Fatalf("message %d about one subject: %v", i+1, err)
		}
	}
	_, err := svc.SendFrom(ctx, dev.ID, "still broken?", CrewTalk{From: qa.ID, Subject: "4a1b2c3"})
	var e *apierr.Error
	if !errors.As(err, &e) || e.Code != "CREW_MESSAGE_CAPPED" {
		t.Fatalf("the fourth message about one subject = %v, want CREW_MESSAGE_CAPPED", err)
	}
	// Retrying the same subject keeps refusing, which is what makes it terminate
	// rather than merely slow down.
	if _, again := svc.SendFrom(ctx, dev.ID, "still broken?", CrewTalk{From: qa.ID, Subject: "4a1b2c3"}); again == nil {
		t.Fatal("a retry of a capped subject went through")
	}

	// The OTHER direction still has its own cap, because a conversation that is
	// actually moving alternates - and dev answering is not qa nagging.
	if _, err := svc.SendFrom(ctx, qa.ID, "fixed it", CrewTalk{From: dev.ID, Subject: "4a1b2c3"}); err != nil {
		t.Fatalf("the reply direction was capped by the other one's traffic: %v", err)
	}
	// And a NEW subject is a new conversation: work moving on is what clears this.
	if _, err := svc.SendFrom(ctx, dev.ID, "pushed 9f8e7d6", CrewTalk{From: qa.ID, Subject: "9f8e7d6"}); err != nil {
		t.Fatalf("a message about a new commit was refused: %v", err)
	}
}

// The per-hour budget catches the loop that escapes the per-subject cap by
// inventing a new subject every time - the obvious way around rule 2, and the
// one an agent would find without trying.
func TestCrewTalk_ThePerHourBudgetCatchesAVaryingSubject(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	dev, qa := crewPair(st)
	svc := crewService(st, &fakeCommander{})

	sent := 0
	for i := range domain.CrewMessagesPerHour + 5 {
		subject := "sha-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if _, err := svc.SendFrom(ctx, dev.ID, "another thought", CrewTalk{From: qa.ID, Subject: subject}); err != nil {
			break
		}
		sent++
	}
	if sent != domain.CrewMessagesPerHour {
		t.Fatalf("delivered %d messages in an hour, want the budget of %d", sent, domain.CrewMessagesPerHour)
	}
}

// A capped conversation is not silently stopped: the task goes to NEEDS YOU, so
// a human sees that two agents have something to say and no way to say it. It
// clears itself the moment a later message goes through.
func TestCrewTalk_ARefusalParksTheTaskAtNeedsYou(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	dev, qa := crewPair(st)
	svc := crewService(st, &fakeCommander{})

	if capped, err := svc.crewTalkRefused(ctx, qa); err != nil || capped {
		t.Fatalf("a crew that has not talked reads as capped: %v %v", capped, err)
	}
	for range domain.CappedRepeat {
		if _, err := svc.SendFrom(ctx, dev.ID, "look", CrewTalk{From: qa.ID, Subject: "4a1b2c3"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.SendFrom(ctx, dev.ID, "look", CrewTalk{From: qa.ID, Subject: "4a1b2c3"}); err == nil {
		t.Fatal("precondition: the fourth message must be refused")
	}

	capped, err := svc.crewTalkRefused(ctx, qa)
	if err != nil || !capped {
		t.Fatalf("after a refusal the sender reads capped=%v (%v), want true", capped, err)
	}
	detail := deriveStatusDetail(st.sessions[qa.ID], nil, time.Unix(2000, 0).UTC(), false, domain.ApprovalRule{}, crewRunFacts{TalkCapped: true})
	if detail.Status != domain.StatusNeedsInput || detail.Reason != domain.ReasonCrewTalkCapped {
		t.Fatalf("status = %q/%q, want needs_input/crew_talk_capped", detail.Status, detail.Reason)
	}

	// Work moves on, and the escalation clears itself with nothing to unwind.
	if _, err := svc.SendFrom(ctx, dev.ID, "pushed 9f8e7d6", CrewTalk{From: qa.ID, Subject: "9f8e7d6"}); err != nil {
		t.Fatal(err)
	}
	if capped, err := svc.crewTalkRefused(ctx, qa); err != nil || capped {
		t.Fatalf("a delivered message did not clear the escalation: %v %v", capped, err)
	}
}

// qa's handback is what lets the crew lane say "ready to merge": the latest
// message qa DELIVERED to its crewmate in the current round. A refusal reached
// nobody, a message from an earlier round was about code that has since been
// asked about again, and dev never hands anything back.
func TestLastHandback_IsQAsLatestDeliveredMessageThisRound(t *testing.T) {
	ctx := context.Background()
	t0 := time.Unix(5000, 0).UTC()
	msg := func(from, to domain.SessionRecord, subject, refused string, at time.Time) domain.CrewMessage {
		return domain.CrewMessage{
			ID: subject, CrewID: from.CrewID, From: from.ID, To: to.ID,
			Subject: subject, RefusedReason: refused, CreatedAt: at,
		}
	}

	t.Run("a delivered qa message this round is the handback", func(t *testing.T) {
		st := newFakeStore()
		dev, qa := crewPair(st)
		st.crewMessages = []domain.CrewMessage{msg(qa, dev, "1a2b3c4", "", t0)}
		got, err := crewService(st, &fakeCommander{}).Get(ctx, qa.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.LastHandback == nil || !got.LastHandback.At.Equal(t0) || got.LastHandback.About != "1a2b3c4" {
			t.Fatalf("LastHandback = %+v, want {%v 1a2b3c4}", got.LastHandback, t0)
		}
	})

	t.Run("a refused message is not a handback", func(t *testing.T) {
		st := newFakeStore()
		dev, qa := crewPair(st)
		st.crewMessages = []domain.CrewMessage{msg(qa, dev, "1a2b3c4", "capped", t0)}
		got, err := crewService(st, &fakeCommander{}).Get(ctx, qa.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.LastHandback != nil {
			t.Fatalf("a refusal read as a handback: %+v", got.LastHandback)
		}
	})

	t.Run("a message from before the round began is not this round's handback", func(t *testing.T) {
		st := newFakeStore()
		dev, qa := crewPair(st)
		qa.CrewRoundStartedAt = t0.Add(time.Minute)
		st.sessions[qa.ID] = qa
		st.crewMessages = []domain.CrewMessage{msg(qa, dev, "1a2b3c4", "", t0)}
		got, err := crewService(st, &fakeCommander{}).Get(ctx, qa.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.LastHandback != nil {
			t.Fatalf("last round's message read as this round's handback: %+v", got.LastHandback)
		}
	})

	t.Run("dev never hands back", func(t *testing.T) {
		st := newFakeStore()
		dev, qa := crewPair(st)
		st.crewMessages = []domain.CrewMessage{msg(dev, qa, "1a2b3c4", "", t0)}
		got, err := crewService(st, &fakeCommander{}).Get(ctx, dev.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.LastHandback != nil {
			t.Fatalf("dev's message read as a handback: %+v", got.LastHandback)
		}
	})
}

// THE PRESERVATION GUARD, and the one that matters most: a message that is not
// between two members of one crew is untouched by all of it. That covers every
// message on an ordinary board - a human typing into a session, the orchestrator
// nudging a worker, a CI reaction - none of which carries a subject and none of
// which may ever be refused.
func TestCrewTalk_EverythingThatIsNotCrewTalkIsUntouched(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	dev, qa := crewPair(st)
	solo := domain.SessionRecord{ID: "mer-9", ProjectID: "mer", Kind: domain.KindWorker}
	st.sessions[solo.ID] = solo
	orch := domain.SessionRecord{ID: "mer-8", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.sessions[orch.ID] = orch
	svc := crewService(st, &fakeCommander{})

	cases := []struct {
		name string
		to   domain.SessionID
		talk CrewTalk
	}{
		{"a human typing at a crew member", qa.ID, CrewTalk{}},
		{"a human typing at a solo session", solo.ID, CrewTalk{}},
		{"the orchestrator nudging a crew member", dev.ID, CrewTalk{From: orch.ID}},
		{"a crew member messaging a session in another task", solo.ID, CrewTalk{From: dev.ID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for range domain.CrewMessagesPerHour + domain.CappedRepeat + 1 {
				if _, err := svc.SendFrom(ctx, tc.to, "no subject, no cap", tc.talk); err != nil {
					t.Fatalf("%s was refused: %v", tc.name, err)
				}
			}
		})
	}
	if len(st.crewMessages) != 0 {
		t.Fatalf("messages that are not crew talk were recorded: %+v", st.crewMessages)
	}
}

// Addressing by ROLE is the only address a crew member can rely on: dev's
// environment is built before qa exists, so an id would be empty exactly when it
// mattered. The daemon resolves it, and the caps apply the same way.
func TestSendToCrewmate_ResolvesTheRoleAndCapsIt(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	dev, qa := crewPair(st)
	svc := crewService(st, &fakeCommander{})

	sent, err := svc.SendToCrewmate(ctx, dev.ID, CrewSend{Role: domain.CrewRoleQA, Message: "pushed the fix", Subject: "4a1b2c3"})
	if err != nil {
		t.Fatalf("dev messaging qa by role: %v", err)
	}
	if sent.Peer != qa.ID {
		t.Fatalf("--crew qa resolved to %q, want %q", sent.Peer, qa.ID)
	}
	if _, err := svc.SendToCrewmate(ctx, dev.ID, CrewSend{Role: domain.CrewRoleQA, Message: "and again"}); err == nil {
		t.Fatal("a role-addressed message with no subject was accepted")
	}
	// A solo session has no crewmate, and says so rather than doing something else
	// with somebody's only agent.
	solo := domain.SessionRecord{ID: "mer-9", ProjectID: "mer", Kind: domain.KindWorker}
	st.sessions[solo.ID] = solo
	if _, err := svc.SendToCrewmate(ctx, solo.ID, CrewSend{Role: domain.CrewRoleQA, Message: "hello", Subject: "4a1b2c3"}); err == nil {
		t.Fatal("a solo session was allowed to message a crewmate it does not have")
	}
}

// TestCrewTalk_ARestoredMemberStartsAFreshSubjectBudget is the cap being right
// across a ROUND rather than only inside one.
//
// The three reasons a finished qa is asked to look again - cases written after
// it closed, a simulator erased so the old evidence proves nothing, a case that
// turned out to expect the wrong thing - are all reasons to re-check code that
// has NOT moved. So the brief for round two names the same commit round one used
// its whole budget on, and the cap refused it with "nothing has moved" - which
// was true of the commit and beside the point. Restoring the member is the
// boundary: last round's rows stay in the table and stop counting.
func TestCrewTalk_ARestoredMemberStartsAFreshSubjectBudget(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	dev, qa := crewPair(st)
	svc := crewService(st, &fakeCommander{})
	roundOne := time.Unix(1000, 0).UTC()
	roundTwo := roundOne.Add(time.Hour)

	// ROUND ONE spends the whole per-subject budget on one commit.
	for i := range domain.CappedRepeat {
		if _, err := svc.SendFrom(ctx, qa.ID, "check this", CrewTalk{From: dev.ID, Subject: "4a1b2c3"}); err != nil {
			t.Fatalf("round one message %d: %v", i+1, err)
		}
	}
	if _, err := svc.SendFrom(ctx, qa.ID, "again", CrewTalk{From: dev.ID, Subject: "4a1b2c3"}); err == nil {
		t.Fatal("the fourth message of one round went through; the cap is not working at all")
	}

	// qa closed its round and was brought back for another. Nothing in the code
	// moved, so the subject is the same commit it always was.
	rec := st.sessions[qa.ID]
	rec.CrewRoundStartedAt = roundTwo
	st.sessions[qa.ID] = rec
	svc.clock = func() time.Time { return roundTwo }

	if _, err := svc.SendFrom(ctx, qa.ID, "here is what round two is for", CrewTalk{From: dev.ID, Subject: "4a1b2c3"}); err != nil {
		t.Fatalf("briefing a restored qa about the same unchanged commit: %v", err)
	}
	// The cap still terminates - a new round is a new budget, not no budget.
	for i := 1; i < domain.CappedRepeat; i++ {
		if _, err := svc.SendFrom(ctx, qa.ID, "more", CrewTalk{From: dev.ID, Subject: "4a1b2c3"}); err != nil {
			t.Fatalf("round two message %d: %v", i+1, err)
		}
	}
	_, err := svc.SendFrom(ctx, qa.ID, "and more", CrewTalk{From: dev.ID, Subject: "4a1b2c3"})
	var e *apierr.Error
	if !errors.As(err, &e) || e.Code != "CREW_MESSAGE_CAPPED" {
		t.Fatalf("round two never terminates: the %d+1th message = %v, want CREW_MESSAGE_CAPPED", domain.CappedRepeat, err)
	}
	// Round one's rows are still there. The boundary stops them counting; it does
	// not rewrite what the crew said.
	roundOneRows := 0
	for _, msg := range st.crewMessages {
		if msg.CreatedAt.Equal(roundOne) {
			roundOneRows++
		}
	}
	if roundOneRows != domain.CappedRepeat+1 {
		t.Fatalf("round one left %d rows, want %d - a new round must not delete the last one's history", roundOneRows, domain.CappedRepeat+1)
	}
}

// TestCrewTalk_ARoundBelongsToTheTaskNotToOneSeat. Either member coming back
// starts the round, and it starts for BOTH legs of the conversation: a qa
// restored for a second look has findings to report about the same commit dev
// briefed it on, and clearing only the leg that was addressed would leave qa
// unable to answer.
func TestCrewTalk_ARoundBelongsToTheTaskNotToOneSeat(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	dev, qa := crewPair(st)
	svc := crewService(st, &fakeCommander{})
	roundTwo := time.Unix(1000, 0).UTC().Add(time.Hour)

	// qa -> dev spends its budget on one commit in round one.
	for i := range domain.CappedRepeat {
		if _, err := svc.SendFrom(ctx, dev.ID, "found something", CrewTalk{From: qa.ID, Subject: "4a1b2c3"}); err != nil {
			t.Fatalf("round one message %d: %v", i+1, err)
		}
	}
	// QA is the member that was restored - the other seat entirely.
	rec := st.sessions[qa.ID]
	rec.CrewRoundStartedAt = roundTwo
	st.sessions[qa.ID] = rec
	svc.clock = func() time.Time { return roundTwo }

	if _, err := svc.SendFrom(ctx, dev.ID, "round two found this", CrewTalk{From: qa.ID, Subject: "4a1b2c3"}); err != nil {
		t.Fatalf("a restored qa cannot report on the commit it was brought back to re-check: %v", err)
	}
}

// TestTerminatedMessage_SurvivesWrappingAndNeverLeaksTheSentinel.
//
// The refusal's whole value is the sentence that says what to do next, and it is
// wrapped at least twice between where it is written and where a person reads
// it. Recovering it by trimming the sentinel's own text off the front worked
// until the first extra wrap, which put a bare Go token - "session: terminated:"
// - in front of the English. The type is what makes the depth irrelevant.
func TestTerminatedMessage_SurvivesWrappingAndNeverLeaksTheSentinel(t *testing.T) {
	remedy := "vr-2 has finished its round. `ao crew wake vr-2` brings it back"
	wrapped := fmt.Errorf("send vr-2: %w", fmt.Errorf("deliver: %w", sessionmanager.TerminatedError{Remedy: remedy}))

	var e *apierr.Error
	if !errors.As(toAPIError(wrapped), &e) || e.Code != "SESSION_TERMINATED" {
		t.Fatalf("a wrapped TerminatedError no longer maps to SESSION_TERMINATED: %v", toAPIError(wrapped))
	}
	if e.Message != remedy {
		t.Fatalf("the remedy did not survive two wraps:\ngot  %q\nwant %q", e.Message, remedy)
	}
	if strings.Contains(e.Message, sessionmanager.ErrTerminated.Error()) {
		t.Fatalf("the Go sentinel token reached the wire: %q", e.Message)
	}
	// And an ErrTerminated raised without a remedy still says something true.
	var bare *apierr.Error
	if !errors.As(toAPIError(fmt.Errorf("x: %w", sessionmanager.ErrTerminated)), &bare) {
		t.Fatal("a bare ErrTerminated no longer maps to an API error")
	}
	if bare.Message != "Session is terminated" {
		t.Fatalf("bare ErrTerminated message = %q", bare.Message)
	}
}

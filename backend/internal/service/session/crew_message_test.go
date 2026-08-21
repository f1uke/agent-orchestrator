package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
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

	peer, _, err := svc.SendToCrewmate(ctx, dev.ID, domain.CrewRoleQA, "pushed the fix", "4a1b2c3")
	if err != nil {
		t.Fatalf("dev messaging qa by role: %v", err)
	}
	if peer != qa.ID {
		t.Fatalf("--crew qa resolved to %q, want %q", peer, qa.ID)
	}
	if _, _, err := svc.SendToCrewmate(ctx, dev.ID, domain.CrewRoleQA, "and again", ""); err == nil {
		t.Fatal("a role-addressed message with no subject was accepted")
	}
	// A solo session has no crewmate, and says so rather than doing something else
	// with somebody's only agent.
	solo := domain.SessionRecord{ID: "mer-9", ProjectID: "mer", Kind: domain.KindWorker}
	st.sessions[solo.ID] = solo
	if _, _, err := svc.SendToCrewmate(ctx, solo.ID, domain.CrewRoleQA, "hello", "4a1b2c3"); err == nil {
		t.Fatal("a solo session was allowed to message a crewmate it does not have")
	}
}

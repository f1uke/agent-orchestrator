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

// THE HANDBACK GATE.
//
// A qa held a simulator, ran three bracketed machine runs and handed the task
// back with 0 of 7 cases played - and nothing anywhere looked at the checklist,
// because a case nobody drove and a case nobody CAN drive are the same empty
// row. These are the assertions that the second state now has to be said out
// loud, and that saying it is the only thing that clears the count.

func caseWithRun(id string, v domain.SmokeVerdict, note string) domain.SmokeCheck {
	recorded := time.Unix(1000, 0).UTC()
	return domain.SmokeCheck{
		ID: id, Runs: []domain.SmokeRun{{ID: "run-" + id, Seq: 1, Verdict: v, Note: note, RecordedAt: &recorded}},
	}
}

func TestHandback_QAIsToldWhatItLeftUndriven(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	dev, qa := crewPair(st)
	fc := &fakeCommander{}
	svc := crewService(st, fc)
	st.smokeChecks = map[domain.SessionID][]domain.SmokeCheck{
		// The checklist lives under the TASK's id, which is dev's.
		dev.ID: {
			caseWithRun("mr-appears", domain.SmokePass, "listed within 40s each time"),
			caseWithRun("press-hold", domain.SmokeSkip, "tried a 1.2s drag; the menu never opened"),
			{ID: "tab-stays-live"},
			{ID: "drag-scroll"},
		},
	}

	sent, err := svc.SendToCrewmate(ctx, qa.ID, CrewSend{Role: domain.CrewRoleDev, Message: "run done", Subject: "4a1b2c3"})
	if err != nil {
		t.Fatalf("qa handing back: %v", err)
	}
	// It is NOT refused. A handback that never lands is the stall the handback
	// obligation was written to prevent, and a refusal here is indistinguishable
	// from the runaway-loop refusal that parks a task at NEEDS YOU.
	if sent.Peer != dev.ID {
		t.Fatalf("the handback reached %q, want dev %q", sent.Peer, dev.ID)
	}
	if !sent.Handback.Checked || !sent.Handback.Incomplete() {
		t.Fatalf("the handback was not reported as incomplete: %+v", sent.Handback)
	}
	if got := sent.Handback.NotDriven; len(got) != 2 || got[0] != "tab-stays-live" || got[1] != "drag-scroll" {
		t.Fatalf("cases left undriven = %v, want [tab-stays-live drag-scroll]", got)
	}
	if sent.Handback.Cases != 4 {
		t.Fatalf("cases = %d, want the 4 active ones", sent.Handback.Cases)
	}
	// dev is the member that would otherwise carry on believing the change was
	// verified, so the fact travels with the message it is about.
	for _, want := range []string{"[AO]", "2 of 4", "tab-stays-live", "drag-scroll", "nobody looked"} {
		if !strings.Contains(fc.lastMessage, want) {
			t.Fatalf("dev's copy does not say %q:\n%s", want, fc.lastMessage)
		}
	}
	if !strings.HasPrefix(fc.lastMessage, "run done") {
		t.Fatalf("AO's line replaced qa's words instead of following them:\n%s", fc.lastMessage)
	}
}

// The two states that clear the count, and the two kinds of case that were never
// qa's to answer. A gate that fires on finished work is one people learn to
// satisfy by lying.
func TestHandback_DrivenAndDeclaredCasesLeaveNothingBehind(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	dev, qa := crewPair(st)
	fc := &fakeCommander{}
	svc := crewService(st, fc)
	retired := time.Unix(900, 0).UTC()
	st.smokeChecks = map[domain.SessionID][]domain.SmokeCheck{
		dev.ID: {
			caseWithRun("judged", domain.SmokePass, "ran clean"),
			caseWithRun("evidence-only", "", "captured it; the lag is not mine to call"),
			caseWithRun("undriveable", domain.SmokeSkip, "no gesture on this harness can hold a finger down"),
			{ID: "played-by-hand", Verdict: domain.SmokePass},
			{ID: "gone", RetiredAt: &retired, RetiredReason: "now covered by a Go test"},
		},
	}

	sent, err := svc.SendToCrewmate(ctx, qa.ID, CrewSend{Role: domain.CrewRoleDev, Message: "run done", Subject: "4a1b2c3"})
	if err != nil {
		t.Fatalf("qa handing back: %v", err)
	}
	if sent.Handback.Incomplete() {
		t.Fatalf("a complete handback was reported incomplete: %v", sent.Handback.NotDriven)
	}
	if sent.Handback.Cases != 4 {
		t.Fatalf("cases = %d, want the 4 active ones (the retired one is off the list)", sent.Handback.Cases)
	}
	if strings.Contains(fc.lastMessage, "[AO]") {
		t.Fatalf("a complete handback still carried a notice:\n%s", fc.lastMessage)
	}
}

// "I am not finished yet" has to be sayable, or the gate's cheapest escape is to
// declare the remaining cases undriveable - and a gate that is easier to satisfy
// by lying is worse than no gate.
func TestHandback_StillWorkingIsNotAHandback(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	dev, qa := crewPair(st)
	fc := &fakeCommander{}
	svc := crewService(st, fc)
	st.smokeChecks = map[domain.SessionID][]domain.SmokeCheck{dev.ID: {{ID: "tab-stays-live"}}}

	sent, err := svc.SendToCrewmate(ctx, qa.ID, CrewSend{
		Role: domain.CrewRoleDev, Message: "the button is dead, please look", Subject: "4a1b2c3", StillWorking: true,
	})
	if err != nil {
		t.Fatalf("qa's mid-run message: %v", err)
	}
	if sent.Handback.Checked {
		t.Fatalf("a mid-run message was checked as a handback: %+v", sent.Handback)
	}
	if fc.lastMessage != "the button is dead, please look" {
		t.Fatalf("a mid-run message was annotated:\n%s", fc.lastMessage)
	}
}

// Only qa hands back. dev's messages are a different act entirely and reading
// them as one would put a notice about qa's work on top of dev's words.
func TestHandback_DevIsNotHandingAnythingBack(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	dev, _ := crewPair(st)
	fc := &fakeCommander{}
	svc := crewService(st, fc)
	st.smokeChecks = map[domain.SessionID][]domain.SmokeCheck{dev.ID: {{ID: "tab-stays-live"}}}

	sent, err := svc.SendToCrewmate(ctx, dev.ID, CrewSend{Role: domain.CrewRoleQA, Message: "pushed the fix", Subject: "4a1b2c3"})
	if err != nil {
		t.Fatalf("dev messaging qa: %v", err)
	}
	if sent.Handback.Checked {
		t.Fatalf("dev's message was checked as a handback: %+v", sent.Handback)
	}
	if fc.lastMessage != "pushed the fix" {
		t.Fatalf("dev's message was annotated:\n%s", fc.lastMessage)
	}
}

// A checklist AO cannot read must not cost the handback. The check exists to
// make a silence visible; breaking the handback over it would trade a reported
// gap for the stall the whole obligation was written to prevent.
func TestHandback_AnUnreadableChecklistStillDelivers(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	_, qa := crewPair(st)
	fc := &fakeCommander{}
	svc := crewService(st, fc)
	st.smokeErr = errors.New("database is locked")

	sent, err := svc.SendToCrewmate(ctx, qa.ID, CrewSend{Role: domain.CrewRoleDev, Message: "run done", Subject: "4a1b2c3"})
	if err != nil {
		t.Fatalf("the handback failed because the checklist could not be read: %v", err)
	}
	if sent.Handback.Checked || fc.lastMessage != "run done" {
		t.Fatalf("an unreadable checklist produced a verdict anyway: %+v / %q", sent.Handback, fc.lastMessage)
	}
}

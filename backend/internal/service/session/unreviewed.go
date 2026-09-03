package session

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// THE WARNING THAT REPLACED THE TRIGGER.
//
// A qa used to appear on its own, the first time a task drove the app. That was
// removed because it fired at the wrong end of the work - dev takes the device
// when it STARTS looking at the change, not when it is done - so the qa it
// created went for the same device dev was still using. dev asks for its own qa
// now (`ao crew review`).
//
// But the trigger was buying something real, and taking it away without
// replacing it hands that failure straight back. The evidence is on the record: a
// task that COULD NOT trip the rule finished with no qa attached at all and had
// to ask for one afterwards - and it was the task whose headline finding was a
// 1.7x disagreement about memory, i.e. exactly where a second measurement was
// worth most. A UI gap quietly cost a task its tester and nothing anywhere said
// so.
//
// So the observation survives as a FACT (sessions.runtime_touch), and this is
// what is made of it: a task that drove the app and never had a qa is SAID OUT
// LOUD when dev closes out. It is the same shape #270 chose for qa's handback,
// and for the same three reasons - refusing recreates the silent stall the check
// exists to prevent, a refused message is already AO's escalation signal
// (crewTalkRefused -> NEEDS YOU), and a refusal is the version easiest to lie
// past. So the report goes, carrying the truth with it, to more than one party:
//
//  1. dev, on its own stdout, from `ao send`;
//  2. the ORCHESTRATOR, as one clearly-attributed [AO] line appended to the
//     report it receives - it is the party that would otherwise tell a human this
//     task is done;
//  3. the human, on the card: a solo task that drove the app says so under its
//     crew strip, beside the `+ qa` control that answers it. That half runs
//     whether or not dev ever reports to anybody.
//
// A task that never drove a runtime surface - a backend-only change - has nothing
// recorded, is never checked here, and stays the one-agent task it should be.

// UnreviewedRuntime is what AO found when dev reported out: this task drove the
// app, and no qa has ever been on it.
type UnreviewedRuntime struct {
	// Checked is false for every message this was not asked of: a human's send, a
	// message between crewmates, anything not addressed to an orchestrator.
	Checked bool
	// Touch is WHAT the task did with the app, and it is empty when there is
	// nothing to say - which is the ordinary case, including every task that had
	// a qa and every task that never drove anything.
	Touch domain.RuntimeTouch
}

// Unreviewed reports whether there is something to say.
func (u UnreviewedRuntime) Unreviewed() bool { return u.Touch != "" }

// unreviewedRuntime answers "is this dev closing out on work nobody but itself
// has looked at?".
//
// CLOSING OUT is read as a worker's message to an ORCHESTRATOR, and that is the
// most honest signal AO has: a worker's standing instructions make that report
// the last thing it does, and it is the point at which somebody else is about to
// be told the task is finished. It is not a perfect fence - a mid-task blocker
// takes the same route, and a project with no orchestrator has no such message at
// all - which is exactly why the card carries the same fact independently.
//
// A store failure yields no warning rather than an error. This exists to make a
// silence visible; breaking dev's report in order to complain about it would
// trade a missing warning for a missing report.
func (s *Service) unreviewedRuntime(ctx context.Context, to domain.SessionRecord, talk CrewTalk) UnreviewedRuntime {
	// Nobody asked, or a human did. A person running `ao send` is not closing out
	// a task and has no qa to call.
	if talk.From == "" || to.Kind != domain.KindOrchestrator {
		return UnreviewedRuntime{}
	}
	sender, ok, err := s.store.GetSession(ctx, talk.From)
	if err != nil || !ok {
		return UnreviewedRuntime{}
	}
	if sender.Kind != domain.KindWorker || sender.IsTodo {
		return UnreviewedRuntime{}
	}
	// Already crewed. dev gains its crew columns in the same write that creates
	// the member, and they are never cleared - so this stays true after a qa has
	// been stood down, which is right: somebody DID look. A qa reporting upward is
	// excluded here too, which is what it should be; qa reports through dev.
	if sender.InCrew() {
		return UnreviewedRuntime{}
	}
	// Never drove the app. The whole point of the change: a backend-only task
	// costs nothing extra and is not nagged about a tester it never needed.
	if !sender.RuntimeTouch.Valid() {
		return UnreviewedRuntime{Checked: true}
	}
	// Never ALLOWED a qa. A `mechanical` task is one agent by an explicit
	// decision, and a project that forms no crews automatically has made the same
	// decision for all of its work: telling either of them to call a qa they are
	// refused would be a warning nobody can act on, which is the kind nobody
	// reads.
	project, ok, err := s.store.GetProject(ctx, string(sender.ProjectID))
	if err != nil || !ok || !domain.CrewEligible(project, sender.Kind, sender.TaskSize.WithDefault()) {
		return UnreviewedRuntime{Checked: true}
	}
	return UnreviewedRuntime{Checked: true, Touch: sender.RuntimeTouch}
}

// unreviewedNotice is the line AO adds to the report the orchestrator receives.
//
// It is written in AO's voice and marked as such: it is a fact about the task,
// not something dev said. It names what dev did with the app, says plainly that
// nobody else has looked, and gives the one command that answers it - because a
// warning that does not say what to do about it is just a complaint.
func unreviewedNotice(u UnreviewedRuntime) string {
	return fmt.Sprintf("\n\n[AO] This task %s and no qa was ever on it, so nothing here has been checked by anything but the agent that wrote it. dev asks for one with `ao crew review` when the change is ready; a person can add one with the `+ qa` control on the task. Not a refusal - the report above stands as sent.", u.Touch.Describe())
}

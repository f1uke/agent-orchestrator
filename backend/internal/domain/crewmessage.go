package domain

import (
	"strings"
	"time"
)

// THE CONVERSATION BETWEEN TWO AGENTS, AND WHAT STOPS IT.
//
// dev and qa run at the same time and can message each other. That is the one
// genuinely new runaway class this shape creates: two agents with no human in
// the middle, each able to answer the other, spending real money at machine
// speed. Nothing about being well-prompted stops a loop, so the stopping rules
// are MECHANISM:
//
//  1. Every message names what it is ABOUT - a commit SHA or a smoke case id.
//     A message with no subject is refused, so there is no "what do you think?"
//     to answer, and every exchange is anchored to a durable artifact.
//  2. CappedRepeat messages per subject per DIRECTION. The next one is refused,
//     and refusing is what makes it terminate: the same subject always refuses,
//     so a retry cannot turn into a loop.
//  3. CrewMessagesPerHour delivered messages per crew per hour, which catches a
//     loop that escapes rule 2 by inventing a new subject each time.
//  4. And, in the prompts rather than here: NO REPLY OBLIGATION. dev answers a
//     finding by committing, qa answers a handoff by recording a result. An
//     obligation to reply is what manufactures loops in the first place.
//
// Rule 4 has one deliberate exception, which is not a reply: qa MUST message dev
// when a run finishes. Finishing is an initiation - the end of qa's run is the
// start of dev's - and the first real crew run stalled precisely because nobody
// was told.

// CrewMessagesPerHour is the crew-wide budget: how many messages the two members
// of one task may exchange in an hour before the next is refused.
//
// It is a BACKSTOP, not a working limit. A crew that is behaving sends a handful
// an hour - a handoff, a finding, a completion report - so this is set well above
// honest traffic and well below what a loop reaches in a minute. Unlike the
// per-subject cap it cannot be argued around by changing the subject.
const CrewMessagesPerHour = 20

// CrewMessage is one message attempt from one crew member to the other, whether
// it was delivered or refused.
type CrewMessage struct {
	ID        string    `json:"id"`
	CrewID    SessionID `json:"crewId"`
	ProjectID ProjectID `json:"projectId"`
	From      SessionID `json:"from"`
	To        SessionID `json:"to"`
	Subject   string    `json:"subject"`
	CreatedAt time.Time `json:"createdAt"`
	// RefusedReason is empty when the message went through. When it is set the
	// message was NOT delivered, and its presence on the crew's most recent
	// attempt is what parks the task at NEEDS YOU.
	RefusedReason string `json:"refusedReason,omitempty"`
}

// Refused reports whether this attempt was turned away.
func (m CrewMessage) Refused() bool { return m.RefusedReason != "" }

// NormalizeCrewSubject trims a subject and lower-cases nothing else: a subject is
// either a commit SHA or a smoke case id, and both are compared verbatim so that
// "the same subject" means the same artifact rather than something like it.
func NormalizeCrewSubject(subject string) string {
	return strings.TrimSpace(subject)
}

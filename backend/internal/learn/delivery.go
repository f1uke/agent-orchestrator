package learn

import (
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/transcript"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgdelivery"
)

// DeliveryAuthorFor decides who wrote a message AO is delivering, from the
// delivery's trigger and the body. It is decided here, at delivery, because
// this is the last place that knows: in the transcript a typed delivery looks
// exactly like the human typing.
//
//   - A plain send is the human's - the app's send box, or `ao send` typed in a
//     terminal outside any session - unless it carries the "[from @<id>]" prefix
//     `ao send` adds inside a session, which makes it another agent's.
//   - The Tests tab's report carries the human's verdicts and notes.
//   - Everything else (nudges, crew notices, forwarded review comments) is AO's.
func DeliveryAuthorFor(trigger, body string) domain.DeliveryAuthor {
	switch trigger {
	case "", msgdelivery.TriggerSend:
		if strings.HasPrefix(strings.TrimSpace(body), "[from @") {
			return domain.DeliveryAuthorAgent
		}
		return domain.DeliveryAuthorHuman
	case msgdelivery.TriggerSmokeReport:
		return domain.DeliveryAuthorHuman
	default:
		return domain.DeliveryAuthorAO
	}
}

// DeliveredFingerprintFor is the record of one delivery: the body's
// fingerprint and who wrote it. The body itself is not kept.
func DeliveredFingerprintFor(rec domain.SessionRecord, trigger, body string, now time.Time) domain.DeliveredFingerprint {
	fp, n := transcript.Fingerprint(body)
	return domain.DeliveredFingerprint{
		SessionID:   rec.ID,
		ProjectID:   rec.ProjectID,
		SHA256:      fp,
		Bytes:       n,
		Trigger:     trigger,
		Author:      DeliveryAuthorFor(trigger, body),
		DeliveredAt: now,
	}
}

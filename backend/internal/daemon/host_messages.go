package daemon

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgdelivery"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgorigin"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionhost"
)

type sessionTransportLookup interface {
	LiveSessionTransportByRuntimeHandle(ctx context.Context, handleID string) (domain.SessionID, domain.Transport, bool, error)
}

type hostRouter struct {
	sessions sessionTransportLookup
	dataDir  string
	journal  msgdelivery.Journal
}

func (r *hostRouter) route(ctx context.Context, handle ports.RuntimeHandle, message string) (bool, error) {
	if r == nil {
		return false, nil
	}
	id, transport, ok, err := r.sessions.LiveSessionTransportByRuntimeHandle(ctx, handle.ID)
	if err != nil {
		return true, fmt.Errorf("deliver to %s: %w", handle.ID, err)
	}
	if !ok || transport != domain.TransportChat {
		return false, nil
	}
	sender := msgorigin.Sender(ctx)
	report := msgdelivery.Report{Path: msgdelivery.PathHost, Sender: sender, NameOnWire: sender != ""}
	if wire := msgdelivery.WireOf(ctx); wire == msgdelivery.WirePane {
		err = fmt.Errorf("%w: %s refused: session %s is a chat session, which has no pane to type into", msgdelivery.ErrNotDelivered, wire.Flag(), id)
	} else {
		_, socket := sessionhost.Paths(r.dataDir, string(id))
		report.MsgID, err = sessionhost.NewClient(socket).Send(ctx, chatproto.UserInput{Text: message}, &chatproto.Origin{Via: "ao_send", From: sender})
		if err != nil {
			err = fmt.Errorf("session %s: session host: %w", id, err)
		}
	}
	if err != nil {
		report.Path, report.NameOnWire, report.Error = msgdelivery.PathNone, false, err.Error()
	}
	msgdelivery.Record(ctx, r.journal, handle.ID, report)
	return true, err
}

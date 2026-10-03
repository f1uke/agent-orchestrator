package controllers

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simtrust"
)

// SimTrustResolver answers which root-CA files a session's simulators should
// trust: the project's own list when it has one, the global setting otherwise.
//
// It is injected the way SimProfileResolver is, for the same reason - Screen,
// Power and the Truster are device-level surfaces that must never learn what a
// project is.
type SimTrustResolver interface {
	SimTrustFor(ctx context.Context, id domain.SessionID) ([]string, error)
}

// SimTrustView is what the last trust pass on a device did: which root CAs it
// now trusts and which could not be installed. A configured CA file that does
// not exist on this Mac is in neither list - it is skipped silently.
type SimTrustView struct {
	Trusted []string              `json:"trusted,omitempty" description:"Root-CA files the device was made to trust, as absolute paths."`
	Failed  []SimTrustFailureView `json:"failed,omitempty" description:"Root-CA files that exist but could not be installed. A failure never fails the boot or claim it rode on."`
	At      time.Time             `json:"at" description:"When the pass ran."`
}

// SimTrustFailureView is one root CA a device was not made to trust.
type SimTrustFailureView struct {
	File   string `json:"file,omitempty" description:"The CA file. Empty when the files to trust could not be worked out at all."`
	Reason string `json:"reason"`
}

// simTrustView puts a pass on the wire, or nothing when it had nothing to say.
func simTrustView(r simtrust.Result) *SimTrustView {
	if r.Empty() {
		return nil
	}
	v := &SimTrustView{Trusted: r.Trusted, At: r.At.UTC()}
	for _, f := range r.Failed {
		v.Failed = append(v.Failed, SimTrustFailureView(f))
	}
	return v
}

// trustRequest resolves a session's CA files. A resolver that cannot answer is
// carried in the request rather than swallowed (see simtrust.Request), and no
// resolver at all is nil: this daemon trusts nothing.
func trustRequest(ctx context.Context, resolver SimTrustResolver, id domain.SessionID) *simtrust.Request {
	if resolver == nil {
		return nil
	}
	files, err := resolver.SimTrustFor(ctx, id)
	return &simtrust.Request{Files: files, Err: err}
}

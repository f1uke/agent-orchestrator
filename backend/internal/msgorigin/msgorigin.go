// Package msgorigin carries WHO asked for a message that AO is delivering into
// a session, from the send call down to the transport that writes it.
//
// It rides the context rather than the messaging port because the two ends are
// far apart and everything between them is indifferent to it: the service, the
// session manager, the message queue and three runtime wrappers all pass a
// message through untouched, and only the claude-code peer transport has any
// use for a sender. Widening every one of those signatures - and the queue's
// SQLite rows - to thread a display name through would be a large change for a
// value that is metadata about the request, not a parameter of it. The context
// is already threaded end to end, and a missing value degrades to "AO sent
// this", which is true.
//
// The value is an ATTRIBUTION, not an authentication: it is what the caller of
// `ao send` said about itself ($AO_SESSION_ID). Treat it the way the rest of AO
// already treats that claim - good enough to label a message with, never
// grounds for authority.
package msgorigin

import "context"

type senderKey struct{}

// WithSender returns a context that names the AO session that authored the
// message being sent. An empty session leaves ctx untouched, so "a human, the
// UI, or the daemon itself sent this" stays distinguishable from "an agent
// sent it".
func WithSender(ctx context.Context, session string) context.Context {
	if session == "" {
		return ctx
	}
	return context.WithValue(ctx, senderKey{}, session)
}

// Sender reports the AO session that authored the message being sent, or an
// empty string when no session did.
func Sender(ctx context.Context) string {
	session, _ := ctx.Value(senderKey{}).(string)
	return session
}

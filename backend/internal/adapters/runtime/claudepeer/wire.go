package claudepeer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"time"

	"github.com/google/uuid"
)

const (
	// maxFrameBytes is the receiver's per-connection buffer ceiling: it drops
	// the connection once an unterminated line passes 1 MiB. Staying under it
	// keeps a message that would be refused on the pane path, where it is
	// chunked and has no such ceiling.
	maxFrameBytes = 1 << 20

	// dialTimeout and writeTimeout bound the whole attempt. A socket that does
	// not answer promptly is a socket we stop waiting on: the pane is right
	// there and always works.
	dialTimeout  = 2 * time.Second
	writeTimeout = 5 * time.Second

	// lingerTimeout bounds the wait for the receiver to finish reading the
	// frame. See linger: that wait is what earns the kernel-verified sender pid,
	// and it ends the moment the receiver closes the connection - about 40ms in
	// practice, measured against a live session.
	lingerTimeout = 2 * time.Second

	// envelopeTag is the element the receiver parses a sender's display name out
	// of. The format is undocumented and checked by an exact round trip on the
	// receiving side - it re-serialises what it parsed and compares - so this
	// builder mirrors it byte for byte, and declines to build one at all rather
	// than build one that is nearly right.
	envelopeTag = "cross-session-message"

	// maxSenderNameLen is the receiver's own cap on a display name. A longer one
	// is not refused, it is truncated - but truncation happens after the round
	// trip check, so keeping our names inside the cap costs nothing and keeps
	// both sides describing the same string.
	maxSenderNameLen = 64
)

// Dialer opens a connection to a session's messaging socket.
type Dialer func(ctx context.Context, socketPath string) (net.Conn, error)

func dialUnix(ctx context.Context, socketPath string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", socketPath)
}

// authFrame is the optional first line: it presents the token Claude Code
// published in the session's key file. The receiver only REQUIRES it on
// Windows; elsewhere it authenticates the connection by peer credentials and
// the 0700 socket directory. We send it whenever the key is readable because
// that is what a first-party sender does, and because a future release could
// start requiring it.
type authFrame struct {
	Type  string `json:"type"`
	Token string `json:"token"`
}

// userFrame is the message itself. msgV and msg_id mirror what a first-party
// sender puts on the wire; priority "next" means "deliver at the next turn
// boundary", which is the closest match to typing into the pane.
//
// Deliberately absent:
//   - from: an address the receiver could reply to, and only that - the receiver
//     documents it as sender-authored and uses it for reply routing alone. AO's
//     daemon does not listen on a socket in the receiver's namespace, so a from
//     would be unreachable; omitting it makes the receiver record the sender as
//     "unknown", which is true. The sender's NAME travels in the content
//     envelope instead (withSenderEnvelope), and the one field the receiver
//     verifies against the kernel is the connecting pid, which is AO's own.
//   - session_id: the receiver drops a frame whose session_id does not match its
//     CURRENT conversation, and that id changes on /clear before the descriptor
//     catches up. Setting it would turn a harmless race into silently lost mail;
//     the tmux-pane match already pins the destination.
type userFrame struct {
	MsgV     int              `json:"msgV"`
	MsgID    string           `json:"msg_id"`
	Type     string           `json:"type"`
	Message  userFrameMessage `json:"message"`
	Priority string           `json:"priority"`
}

type userFrameMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// builtFrame is the framed message plus what AO can say about it afterwards.
//
// The msgID and the envelope verdict exist for the delivery record: the
// receiver stores the same msg_id beside the message it accepted, so a
// persisted line here can be matched against the receiving agent's own
// transcript - and "the name was deliberately left off, for this reason" is a
// thing a human reading that record needs told, since it is correct behaviour
// that otherwise looks like a bug.
type builtFrame struct {
	bytes []byte
	msgID string
	// nameOnWire is true when the sender's name actually travelled.
	nameOnWire bool
	// nameDropped names why a known sender was left off, empty when none was.
	nameDropped string
}

// buildFrame renders the newline-delimited JSON the receiver reads. The message
// is the LAST line and ends in a newline, which is what makes a short write
// safe: the receiver enqueues only whole parseable lines and discards a
// trailing fragment, so an interrupted write delivers nothing at all.
//
// senderName is who the message is FROM, as AO understands it. It travels
// inside the content, in the envelope the receiver parses a display name out
// of, because the frame itself has no field for one.
func buildFrame(session Session, message, senderName string) (builtFrame, error) {
	if message == "" {
		// The receiver ignores an empty-content user frame outright, so sending
		// one would look like delivery and be nothing of the kind.
		return builtFrame{}, errors.New("claudepeer: empty message")
	}
	content, dropped := withSenderEnvelope(message, senderName)
	built := builtFrame{
		msgID:       uuid.NewString(),
		nameOnWire:  senderName != "" && dropped == "",
		nameDropped: dropped,
	}
	var buf []byte
	if session.PeerToken != "" {
		line, err := json.Marshal(authFrame{Type: "auth", Token: session.PeerToken})
		if err != nil {
			return builtFrame{}, fmt.Errorf("claudepeer: encode auth frame: %w", err)
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
	}
	line, err := json.Marshal(userFrame{
		MsgV:     1,
		MsgID:    built.msgID,
		Type:     "user",
		Message:  userFrameMessage{Role: "user", Content: content},
		Priority: "next",
	})
	if err != nil {
		return builtFrame{}, fmt.Errorf("claudepeer: encode user frame: %w", err)
	}
	buf = append(buf, line...)
	buf = append(buf, '\n')
	if len(buf) > maxFrameBytes {
		return builtFrame{}, fmt.Errorf("claudepeer: framed message is %d bytes, over the receiver's %d-byte line cap", len(buf), maxFrameBytes)
	}
	built.bytes = buf
	return built, nil
}

// envelopeMarkup matches the envelope as MARKUP - the bracketed open or close
// tag - which is the only shape that can break the receiver's rebuild-and-
// compare. It deliberately does NOT match the bare tag name: a body that merely
// DISCUSSES this subsystem, with no angle bracket anywhere, is an ordinary
// message and keeps its sender name. That those two were once the same test is
// why every report ever written about peer messaging arrived anonymous.
//
// It stays wider than a strict parser would be, because the trade is asymmetric:
// losing a name is cheap and leaking markup in front of a human is not. So it
// ignores case, tolerates whitespace a lenient parser might also tolerate
// (`< / cross-session-message`), and treats a `-` after the tag name as still
// ours. Attributes need no special case: the open tag is matched by its name.
var envelopeMarkup = regexp.MustCompile(`(?i)<\s*/?\s*` + envelopeTag + `\b`)

// withSenderEnvelope wraps message in the envelope the receiver reads a sender's
// display name out of, so the message renders as a named, expandable row
// instead of an anonymous block.
//
// The envelope carries a NAME and nothing else. The receiver's other envelope
// attributes are deliberately left off:
//
//   - from / from-session: addresses a UI would navigate back to. AO is not
//     addressable in either namespace, so both would be inventions.
//   - from-mode: an attestation of the SENDER's permission class. AO is not a
//     Claude session and has no permission mode to attest, and the receiver
//     only consults it to decide whether to hold a message at a session that
//     runs without asking - a session this adapter already refuses to use the
//     socket for.
//
// The receiver validates the envelope by rebuilding it from what it parsed and
// requiring byte equality, so anything it would have escaped or normalised
// differently makes the whole envelope invisible and leaves its markup in front
// of the human. That is why this returns the message untouched - today's exact
// behaviour - whenever the name or the body is not one both sides would write
// identically.
//
// The second return names WHY a name was left off, empty when one travelled or
// when there was no name to put on. It is reported, not just logged: a message
// arriving anonymous because its own body discusses the envelope is CORRECT and
// looks exactly like a regression to anyone reading the record.
func withSenderEnvelope(message, senderName string) (content, dropped string) {
	if senderName == "" {
		return message, ""
	}
	if !usableSenderName(senderName) {
		return message, "unusable-sender-name"
	}
	if envelopeMarkup.MatchString(message) {
		// A body carrying the tag as MARKUP is one the receiver would escape
		// before comparing, so an envelope around it could never round-trip.
		return message, "body-contains-envelope-markup"
	}
	return "<" + envelopeTag + ` from-name="` + senderName + `">` + "\n" + message + "\n</" + envelopeTag + ">", ""
}

// usableSenderName reports whether the receiver would read back exactly the
// name we wrote. It is deliberately narrower than what the receiver accepts:
// AO's own names are session ids, so anything outside that alphabet is a sign
// we are about to describe a sender we do not actually know.
func usableSenderName(name string) bool {
	if name == "" || len(name) > maxSenderNameLen {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// writeFrame is the commit point. It returns nil only when every byte of frame
// reached the socket; any other outcome means the receiver saw, at most, an
// incomplete line it will discard, so the caller is free to deliver through the
// pane instead without risking a double delivery.
func writeFrame(ctx context.Context, dial Dialer, socketPath string, frame []byte) error {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, err := dial(dialCtx, socketPath)
	if err != nil {
		return fmt.Errorf("claudepeer: dial %s: %w", socketPath, err)
	}
	defer func() { _ = conn.Close() }()

	deadline := time.Now().Add(writeTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetWriteDeadline(deadline)

	n, err := conn.Write(frame)
	if err != nil {
		return fmt.Errorf("claudepeer: wrote %d of %d bytes: %w", n, len(frame), err)
	}
	if n != len(frame) {
		return fmt.Errorf("claudepeer: short write, %d of %d bytes", n, len(frame))
	}
	// Half-close so the receiver sees end-of-input and stops waiting for more
	// lines. The bytes are already in its buffer by now, so a failure here does
	// not un-deliver the message and must not trigger a fallback.
	if halfCloser, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = halfCloser.CloseWrite()
	}
	linger(ctx, conn)
	return nil
}

// linger waits for the receiver to finish with the connection, and is the whole
// reason AO's messages carry a verified sender pid.
//
// The receiver reads the connecting process's pid off the CONNECTION, with
// SO_PEERCRED / LOCAL_PEERPID, at the moment it parses our line - not when we
// connect. Closing the socket the instant the write returns loses that race -
// measured against a live session, every time - and the message lands
// unidentified. Staying open until the receiver hangs up (it does so as soon as
// it has consumed the frame) closes the race without guessing at a delay.
//
// It is a courtesy, never a condition: the bytes are delivered before this runs,
// so every outcome here - EOF, error, timeout, a receiver that never hangs up -
// is ignored, and none of them may reach the caller as a reason to fall back.
func linger(ctx context.Context, conn net.Conn) {
	deadline := time.Now().Add(lingerTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return
	}
	// One read is enough: EOF means the receiver is done with us, and any byte
	// it sent means it got at least as far as reading our line.
	var scratch [1]byte
	_, _ = conn.Read(scratch[:])
}

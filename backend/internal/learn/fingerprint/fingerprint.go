// Package fingerprint is the one definition of a message body's identity that
// learning capture compares across processes: the agent hook (from the prompt
// it was handed), the messenger (from the body it is about to deliver) and
// capture (from the text the transcript recorded). It has no dependencies so
// all three can import it.
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Of returns the hex sha256 of text with whitespace collapsed, and the
// collapsed length in bytes. Whitespace is collapsed because the three sides
// see the same words with different line handling - a pane paste, a socket
// frame, a JSONL string.
func Of(text string) (string, int) {
	norm := strings.Join(strings.Fields(text), " ")
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:]), len(norm)
}

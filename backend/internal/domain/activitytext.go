package domain

import (
	"regexp"
	"strings"
)

// Bounds and markers for any string that may appear on the activity feed. They
// live in domain because two layers enforce them: the per-tool whitelist inside
// `ao hooks` (adapters/agent/toolcurate), which is the primary guard, and the
// daemon's activity endpoint, which re-applies them so a future harness deriver
// that forgets to curate cannot push an unbounded blob onto the feed.
const (
	// ActivityTextMaxRunes bounds a one-line bubble sentence.
	ActivityTextMaxRunes = 90
	// ActivityTargetMaxRunes bounds a curated noun (base name, pattern, host).
	ActivityTargetMaxRunes = 60
	// ActivityEllipsis marks a truncated string.
	ActivityEllipsis = "…"
	// ActivityRedactedMarker replaces a secret-shaped run.
	ActivityRedactedMarker = "[redacted]"
)

// ActivityLine reduces free text to one feed line: first line only, whitespace
// flattened, secret-shaped runs redacted, hard-truncated. Used for the message
// tap, where the body is a long brief that may carry paths and credentials.
func ActivityLine(s string) string {
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		s = s[:idx]
	}
	return SanitizeActivityText(s, ActivityTextMaxRunes)
}

// SanitizeActivityText flattens a string to one line, redacts secret-shaped
// runs and truncates it to maxRunes.
func SanitizeActivityText(s string, maxRunes int) string {
	s = strings.Join(strings.Fields(SanitizeControlChars(s)), " ")
	s = RedactSecrets(s)
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return strings.TrimRight(string(runes[:maxRunes-1]), " ") + ActivityEllipsis
}

// activitySecretPatterns is defence in depth. The fields the whitelist admits
// are model- or user-authored prose, so they are unlikely to carry a credential
// — but "the model wrote it" is not a guarantee, and this text is destined for a
// screen someone else may be looking at.
var activitySecretPatterns = []*regexp.Regexp{
	// key=value / key: value for credential-shaped names.
	regexp.MustCompile(`(?i)\b(?:api[_-]?keys?|apikey|access[_-]?tokens?|tokens?|secrets?|passwords?|passwd|passphrase|credentials?|bearer)\b\s*[:=]\s*\S+`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{16,}\b`),                            // GitHub
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{16,}\b`),                              // GitLab
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}\b`),                                 // OpenAI-style
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),                                      // AWS access key id
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),                          // Slack
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]+`), // JWT
	regexp.MustCompile(`\b[0-9a-fA-F]{32,}\b`),                                      // long hex digest
	regexp.MustCompile(`\b[A-Za-z0-9+/]{40,}={0,2}\b`),                              // long base64 run
}

// RedactSecrets replaces secret-shaped runs with ActivityRedactedMarker.
func RedactSecrets(s string) string {
	for _, re := range activitySecretPatterns {
		s = re.ReplaceAllString(s, ActivityRedactedMarker)
	}
	return s
}

// SanitizedForFeed re-bounds every field of a detail reported over the wire and
// reports whether the kind is one a detail may carry. It is the daemon-side
// backstop for the whitelist that already ran in the hook process: a detail that
// arrives with an unknown kind, or with text longer than the feed allows, is
// rejected or clamped rather than trusted.
func (d ActivityDetail) SanitizedForFeed() (ActivityDetail, bool) {
	switch d.Kind {
	case ActivityEventToolStart, ActivityEventToolEnd, ActivityEventToolFailed, ActivityEventMessage:
	default:
		return ActivityDetail{}, false
	}
	return ActivityDetail{
		Kind:   d.Kind,
		Tool:   SanitizeActivityText(d.Tool, ActivityTargetMaxRunes),
		Target: SanitizeActivityText(d.Target, ActivityTargetMaxRunes),
		Text:   SanitizeActivityText(d.Text, ActivityTextMaxRunes),
	}, true
}

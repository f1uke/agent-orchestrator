// Package redact takes secrets, account credentials, personal data and pasted
// blobs out of text before learning capture stores it.
//
// It is built for PRECISION, not recall. A lesson's value is in the method, so a
// stored turn loses nothing when a value is replaced by what kind of value it
// was; what it must not lose is the ordinary text around it. That rules out
// the shotgun patterns that wreck other tools' evidence - a "card number" rule
// that also eats millisecond timestamps, snowflake ids and line numbers - and
// leaves three kinds of rule:
//
//  1. Exact values AO already knows are secret: the test accounts in a mobile
//     project's script store, and env values whose names say they are secrets.
//     These are the values most likely to be pasted into a conversation, and an
//     exact match cannot misfire. An account value becomes [account:<id>],
//     which is still useful in a lesson: scripts take --account <id>.
//  2. Token formats specific enough to identify themselves (a GitHub token, a
//     private-key block, a JWT), and secret-looking assignments
//     (password: ..., FOO_TOKEN=...).
//  3. Emails, and Thai national ids and mobile numbers in their exact shapes.
//
// Blobs - a pasted JSON payload, a log, a page of code - are collapsed to their
// size. They are where customer data arrives, and a lesson never needs one.
package redact

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Counts is how many values of each kind a Redact call replaced.
type Counts map[string]int

// Add folds o into c.
func (c Counts) Add(o Counts) {
	for k, v := range o {
		c[k] += v
	}
}

// minDictionaryValueBytes keeps a known secret too short to be distinctive -
// "1", "dev", "true" - from blanking every occurrence of it in ordinary text.
// Four bytes still covers a PIN.
const minDictionaryValueBytes = 4

type dictEntry struct {
	value       string
	placeholder string
	kind        string
}

// Redactor applies the rules. The zero value applies the pattern rules only.
type Redactor struct {
	dict []dictEntry
}

// Value is one exact secret value and what to replace it with.
type Value struct {
	// Value is the secret text itself.
	Value string
	// Placeholder replaces it, e.g. "[account:nter-uat-1]".
	Placeholder string
	// Kind counts it in Counts, e.g. "account".
	Kind string
}

// New builds a Redactor over a dictionary of exact values. Values shorter than
// minDictionaryValueBytes are ignored; longer values are applied first, so a
// password that contains another dictionary value is replaced whole.
func New(values []Value) *Redactor {
	seen := map[string]bool{}
	r := &Redactor{}
	for _, v := range values {
		val := strings.TrimSpace(v.Value)
		if len(val) < minDictionaryValueBytes || seen[val] {
			continue
		}
		seen[val] = true
		r.dict = append(r.dict, dictEntry{value: val, placeholder: v.Placeholder, kind: v.Kind})
	}
	sort.SliceStable(r.dict, func(i, j int) bool { return len(r.dict[i].value) > len(r.dict[j].value) })
	return r
}

type patternRule struct {
	kind string
	re   *regexp.Regexp
	// keep is the number of leading submatches kept verbatim (a label such as
	// "password:"); the rest of the match is replaced. Zero replaces it all.
	keep int
	// valid, when set, must accept the match for it to be replaced.
	valid func(string) bool
}

var patternRules = []patternRule{
	{kind: "private-key", re: regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)},
	{kind: "token", re: regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})\b`)},
	{kind: "token", re: regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`)},
	{kind: "token", re: regexp.MustCompile(`\bsk-(?:ant-|proj-)?[A-Za-z0-9_-]{24,}`)},
	{kind: "token", re: regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`)},
	{kind: "token", re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{kind: "token", re: regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{kind: "token", re: regexp.MustCompile(`\bATATT3[A-Za-z0-9_=-]{20,}`)},
	{kind: "token", re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)},
	{kind: "token", re: regexp.MustCompile(`(?i)(\bbearer\s+)[A-Za-z0-9._~+/=-]{20,}`), keep: 1},
	// The separator is required: "password reset flow" is prose about a
	// password, "password: hunter22" is one. It runs before the secret-name rule
	// so prose is labelled a password; "DB_PASSWORD=..." has no word boundary
	// before "password" and falls through to that rule. A value already
	// replaced by an earlier rule ("[secret]") is not replaced again.
	{kind: "password", re: regexp.MustCompile(`(?i)((?:\bpassword|\bpasswd|\bpasscode|\bpwd|รหัสผ่าน)\s*(?:[:=]|คือ)\s*)[^\s,;\[][^\s,;]{3,}`), keep: 1},
	// A secret-named assignment keeps its name, which says what the value was
	// without being it: "JIRA_API_TOKEN=[secret]".
	{kind: "secret", re: regexp.MustCompile(`(?i)(\b[A-Z0-9_]*(?:TOKEN|SECRET|API_?KEY|ACCESS_?KEY|PRIVATE_?KEY|PASSWORD|PASSWD)[A-Z0-9_]*\s*[=:]\s*["']?)[^\s"'\[][^\s"']{5,}`), keep: 1},
	{kind: "email", re: regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)},
	{kind: "national-id", re: regexp.MustCompile(`\b\d-?\d{4}-?\d{5}-?\d{2}-?\d\b`), valid: thaiNationalIDValid},
	{kind: "phone", re: regexp.MustCompile(`\b0[689]\d-?\d{3}-?\d{4}\b`)},
}

// Redact returns s with every rule applied, and what was replaced.
func (r *Redactor) Redact(s string) (string, Counts) {
	counts := Counts{}
	if s == "" {
		return s, counts
	}
	if r != nil {
		for _, e := range r.dict {
			if n := strings.Count(s, e.value); n > 0 {
				s = strings.ReplaceAll(s, e.value, e.placeholder)
				counts[e.kind] += n
			}
		}
	}
	for _, rule := range patternRules {
		s = rule.re.ReplaceAllStringFunc(s, func(m string) string {
			if rule.valid != nil && !rule.valid(m) {
				return m
			}
			counts[rule.kind]++
			if rule.keep == 0 {
				return "[" + rule.kind + "]"
			}
			sub := rule.re.FindStringSubmatch(m)
			return sub[1] + "[" + rule.kind + "]"
		})
	}
	return s, counts
}

// thaiNationalIDValid checks the 13-digit Thai id checksum, so an arbitrary
// 13-digit number (a timestamp in milliseconds is 13 digits) is left alone.
func thaiNationalIDValid(m string) bool {
	digits := strings.ReplaceAll(m, "-", "")
	if len(digits) != 13 {
		return false
	}
	sum := 0
	for i := 0; i < 12; i++ {
		sum += int(digits[i]-'0') * (13 - i)
	}
	return (11-sum%11)%10 == int(digits[12]-'0')
}

// blobMinBytes is the size from which a pasted block is collapsed. A short
// snippet ("use `ao sim ax`", a one-line error) is often the very thing a lesson
// is about; a 2 KB response body never is.
const blobMinBytes = 300

var fenceRE = regexp.MustCompile("(?s)```[^\\n]*\\n.*?```")

// StripFences collapses fenced code blocks of blobMinBytes or more to a size
// marker, and returns how many it collapsed. It is the rule for the AGENT's
// text, whose ordinary markdown (nested lists, tables) would trip the looser
// heuristic in StripBlobs.
func StripFences(s string) (string, int) {
	n := 0
	s = fenceRE.ReplaceAllStringFunc(s, func(m string) string {
		if len(m) < blobMinBytes {
			return m
		}
		n++
		return fmt.Sprintf("[pasted %d bytes of code]", len(m))
	})
	return s, n
}

// StripBlobs collapses pasted payloads - fenced code and unfenced blocks that
// look like JSON, logs or code - of blobMinBytes or more to a size marker. It
// returns how many it collapsed. It is the rule for the HUMAN's text, where an
// unfenced paste is how a response body or a log usually arrives.
func StripBlobs(s string) (string, int) {
	s, n := StripFences(s)
	paragraphs := strings.Split(s, "\n\n")
	for i, p := range paragraphs {
		if len(p) >= blobMinBytes && looksLikeBlob(p) {
			paragraphs[i] = fmt.Sprintf("[pasted %d bytes]", len(p))
			n++
		}
	}
	return strings.Join(paragraphs, "\n\n"), n
}

// looksLikeBlob is true for a block that is data rather than prose: it opens as
// JSON/XML, or most of its lines carry structure characters or indentation.
// Thai and English prose has neither, so a long message the human wrote by
// hand survives.
func looksLikeBlob(p string) bool {
	t := strings.TrimSpace(p)
	if t == "" {
		return false
	}
	switch t[0] {
	case '{', '[', '<':
		return true
	}
	lines := strings.Split(t, "\n")
	if len(lines) < 4 {
		return false
	}
	structured := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "  ") || strings.HasPrefix(l, "\t") ||
			strings.ContainsAny(l, "{};") || strings.Contains(l, "\":") ||
			strings.Contains(l, "=>") || strings.HasPrefix(strings.TrimSpace(l), "at ") {
			structured++
		}
	}
	return structured*2 >= len(lines)
}

// Clip cuts s to at most maxBytes without splitting a UTF-8 character, keeping
// the head, and says how much was cut.
func Clip(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf(" [... %d more bytes]", len(s)-cut)
}

// ClipTail keeps the LAST maxBytes of s - the end of what the agent said is the
// part the human answered.
func ClipTail(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	start := len(s) - maxBytes
	for start < len(s) && !utf8Start(s[start]) {
		start++
	}
	return fmt.Sprintf("[... %d bytes before] ", start) + s[start:]
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

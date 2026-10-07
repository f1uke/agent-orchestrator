package transcript

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/fingerprint"
)

// Fingerprint is fingerprint.Of, the identity of a message body every side of
// the comparison computes the same way.
func Fingerprint(text string) (string, int) {
	return fingerprint.Of(text)
}

// Delivery is what AO recorded about a body it put into the session.
type Delivery struct {
	Author  domain.DeliveryAuthor
	Trigger string
}

// TriggerBrief marks a session's own spawn prompt in the delivered set. The
// brief is not a delivery the messenger makes, but it reaches the transcript the
// same way - as the first typed turn - and it is AO's (or the orchestrator's)
// text, never the human's.
const TriggerBrief = "brief"

// turnClass is what one transcript record is, for capture.
type turnClass int

const (
	// classNone is not a turn at all: a tool result, a skill body being loaded,
	// a compaction summary, an assistant record.
	classNone turnClass = iota
	// classHuman is the human speaking to the agent.
	classHuman
	// classMachine is a turn something other than the human put there: AO, an
	// agent, the harness. It is context for the human turns around it.
	classMachine
)

// classified is one user record after classification.
type classified struct {
	class  turnClass
	source domain.LearnSourceClass
	// text is the human's text (classHuman only), before redaction.
	text string
	// marker labels a machine turn in the surrounding windows ("AO notice").
	marker string
	// paneFP is the fingerprint of everything the harness submitted as a
	// prompt - typed at the pane or delivered over the socket, whoever wrote
	// it. Its submit hook saw exactly those, so this is what the hook's
	// fingerprints are matched against.
	paneFP string
}

// rawRecord is the subset of a Claude Code transcript line capture reads.
type rawRecord struct {
	Type             string `json:"type"`
	UUID             string `json:"uuid"`
	Timestamp        string `json:"timestamp"`
	IsMeta           bool   `json:"isMeta"`
	IsCompactSummary bool   `json:"isCompactSummary"`
	IsSidechain      bool   `json:"isSidechain"`
	Origin           *struct {
		Kind string `json:"kind"`
		// Body is the message a peer delivery carried, as the harness
		// recorded it before framing it for the model.
		Body string `json:"body"`
	} `json:"origin"`
	PromptSource string `json:"promptSource"`
	CWD          string `json:"cwd"`
	GitBranch    string `json:"gitBranch"`
	Message      struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// contentOf splits a message's content into its text, whether it carried an
// image, and its blocks. Content is either a bare string or an array of blocks.
func contentOf(raw json.RawMessage) (text string, image bool, blocks []contentBlock) {
	if len(raw) == 0 {
		return "", false, nil
	}
	if raw[0] == '"' {
		_ = json.Unmarshal(raw, &text)
		return text, false, nil
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", false, nil
	}
	var parts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "image":
			image = true
		}
	}
	return strings.Join(parts, "\n"), image, blocks
}

func onlyToolResults(blocks []contentBlock) bool {
	if len(blocks) == 0 {
		return false
	}
	for _, b := range blocks {
		if b.Type != "tool_result" {
			return false
		}
	}
	return true
}

// queuedDecoration is the line AO's message queue prepends to a message it held
// while the session slept (msgqueue.decorate). The body after it is what AO
// recorded at delivery.
var queuedDecoration = regexp.MustCompile(`^\[AO queued [^\]\n]*\]\n`)

// envelope is the frame the claudepeer socket path wraps a message in. The
// receiving harness records it inside a "sent a message" line.
var envelope = regexp.MustCompile(`(?s)<cross-session-message[^>]*>\n?(.*?)\n?</cross-session-message>`)

// fromPrefix opens every `ao send` made from inside a session.
const fromPrefix = "[from @"

// aoNoticePrefixes are the openings of AO's own default nudges and notices, for
// transcripts written before delivered fingerprints existed (or for a template
// an operator edited after the delivery). A typed turn that opens with one of
// these is AO's, not the human's.
var aoNoticePrefixes = []*regexp.Regexp{
	regexp.MustCompile(`^CI is failing on `),
	regexp.MustCompile(`^There are merge conflicts on `),
	regexp.MustCompile(`^Your PR has merge conflicts\.`),
	regexp.MustCompile(`^(?:Your PR|PR #\d+\b.*?) targets ` + "`"),
	regexp.MustCompile(`^A reviewer (?:left an unresolved comment|requested changes) on `),
	regexp.MustCompile(`^There are \d+ unresolved review comments on `),
	regexp.MustCompile(`^A bot left a new comment on your tracker issue\.`),
	regexp.MustCompile(`^\[AO reviewer\] `),
	regexp.MustCompile(`^Another Claude session sent a message`),
	regexp.MustCompile(`^<task-notification>`),
}

// classify decides what one user record is. delivered maps a body fingerprint
// to what AO recorded when it delivered that body (including the session's
// brief); it may be nil for a session AO holds no records for.
func classify(rec rawRecord, delivered map[string]Delivery) classified {
	if rec.Type != "user" || rec.IsSidechain || rec.IsCompactSummary {
		return classified{}
	}
	text, image, blocks := contentOf(rec.Message.Content)
	if onlyToolResults(blocks) {
		return classified{}
	}
	kind := ""
	if rec.Origin != nil {
		kind = rec.Origin.Kind
	}
	switch kind {
	case "human":
		if rec.IsMeta {
			return classified{}
		}
		return classifyTyped(text, image, rec.PromptSource, delivered)
	case "peer":
		return classifyPeer(rec.Origin.Body, text, delivered)
	case "":
		// No origin: a harness-written line (a bash-mode echo, a slash
		// command's expansion) - except the interrupt, which is the human
		// stopping the agent and is worth showing next to what follows.
		if strings.HasPrefix(strings.TrimSpace(text), "[Request interrupted by user") {
			return classified{class: classMachine, marker: "human interrupted the agent"}
		}
		return classified{}
	default:
		// task-notification, auto-continuation, scheduled, and whatever the
		// harness adds next: not the human.
		return classified{class: classMachine, marker: kind}
	}
}

func classifyTyped(text string, image bool, promptSource string, delivered map[string]Delivery) classified {
	body := queuedDecoration.ReplaceAllString(text, "")
	fp, _ := Fingerprint(text)
	out := classified{paneFP: fp}
	trimmed := strings.TrimSpace(body)
	if strings.HasPrefix(trimmed, "<command-") || strings.Contains(trimmed, "<command-name>") {
		out.class, out.marker = classMachine, "slash command"
		return out
	}
	bodyFP, _ := Fingerprint(body)
	if d, ok := delivered[bodyFP]; ok {
		return byDelivery(out, d, body)
	}
	for _, re := range aoNoticePrefixes {
		if re.MatchString(trimmed) {
			out.class, out.marker = classMachine, "AO notice"
			return out
		}
	}
	// A turn can hold several submissions run together: the human typed while
	// the agent was busy and an `ao send` landed in the same queue. Whatever
	// precedes the first "[from @" is the human's; the rest is another session's.
	if i := strings.Index(trimmed, fromPrefix); i >= 0 {
		human := strings.TrimSpace(trimmed[:i])
		if human == "" {
			out.class, out.marker = classMachine, "message from another session"
			return out
		}
		trimmed = human
	}
	out.class, out.source, out.text = classHuman, sourceOf(promptSource), trimmed
	if image && !strings.Contains(out.text, "[Image") {
		out.text = "[image] " + out.text
	}
	return out
}

// classifyPeer handles a message that arrived over the socket rather than the
// pane. It is the human only when AO recorded the body as the human's. The
// harness records the delivered body on the origin; the framed text is the
// fallback for a harness that does not.
func classifyPeer(originBody, text string, delivered map[string]Delivery) classified {
	body := text
	if originBody != "" {
		body = originBody
	} else if m := envelope.FindStringSubmatch(text); m != nil {
		body = m[1]
	} else if i := strings.Index(text, ":"); i >= 0 && strings.HasPrefix(text, "Another Claude session sent a message") {
		body = strings.TrimSpace(text[i+1:])
	}
	// The harness submits a socket delivery as a prompt too, framed, and its
	// submit hook sees that framed text; fingerprint what was submitted.
	submitted, _ := Fingerprint(submittedFrame(text))
	bodyFP, _ := Fingerprint(body)
	if d, ok := delivered[bodyFP]; ok {
		return byDelivery(classified{paneFP: submitted}, d, body)
	}
	return classified{class: classMachine, marker: "message from another session", paneFP: submitted}
}

// peerLead and peerNotice are the harness's own words around a socket
// delivery in the transcript: a line before it and a safety note after it.
// Neither was part of the submitted prompt, so the submit hook never saw them.
const (
	peerLead   = "Another Claude session sent a message:"
	peerNotice = "\n\nThis came from another Claude session"
)

// submittedFrame is the part of a recorded socket delivery that was actually
// submitted as the prompt: the envelope alone, or - when the sender's name was
// dropped and the body went out unwrapped - the body alone. Measured on Claude
// Code 2.1.287: of a 723-byte record the hook saw 138 bytes, exactly the
// <cross-session-message> envelope.
func submittedFrame(text string) string {
	const opening, closing = "<cross-session-message", "</cross-session-message>"
	if i := strings.Index(text, opening); i >= 0 {
		if j := strings.Index(text[i:], closing); j >= 0 {
			return text[i : i+j+len(closing)]
		}
	}
	out := strings.TrimPrefix(text, peerLead)
	if i := strings.Index(out, peerNotice); i >= 0 {
		out = out[:i]
	}
	return strings.TrimSpace(out)
}

func byDelivery(out classified, d Delivery, body string) classified {
	switch d.Author {
	case domain.DeliveryAuthorHuman:
		out.class, out.text = classHuman, strings.TrimSpace(body)
		out.source = domain.LearnSourceAppSend
	case domain.DeliveryAuthorAgent:
		out.class, out.marker = classMachine, "message from another session"
	default:
		out.class, out.marker = classMachine, "AO notice"
		if d.Trigger == TriggerBrief {
			out.marker = "task brief"
		}
	}
	return out
}

func sourceOf(promptSource string) domain.LearnSourceClass {
	switch promptSource {
	case "queued":
		return domain.LearnSourceQueued
	case "suggestion_accepted":
		return domain.LearnSourceSuggestionAccepted
	default:
		return domain.LearnSourceTyped
	}
}

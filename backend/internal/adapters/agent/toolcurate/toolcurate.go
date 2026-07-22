// Package toolcurate turns a raw agent tool payload into the small, safe detail
// that may leave the hook process.
//
// SAFETY CONTRACT. An agent's native tool payload is unfiltered: a Write's
// tool_input is the whole file body, a Bash command can carry an inline token,
// and a PostToolUse also ships tool_response (command output, file contents).
// None of that may ever reach the daemon, the store, a log, or an always-on-top
// desktop overlay. So curation happens HERE, inside `ao hooks`, before the
// loopback POST — the raw payload never crosses a process boundary.
//
// The guard is structural, not a rule someone has to remember:
//
//  1. the raw tool_input is decoded into `toolInput`, a struct that lists ONLY
//     whitelisted keys, so encoding/json physically discards everything else —
//     including keys that do not exist yet;
//  2. a per-tool whitelist then picks at most ONE of those fields;
//  3. a tool that is not on the whitelist (every MCP tool, anything new)
//     contributes nothing at all — not even its name;
//  4. whatever survives is flattened to one line, redacted of secret-shaped
//     runs, and hard-truncated.
package toolcurate

import (
	"encoding/json"
	"net/url"
	"path"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The bounds and markers live in domain because the daemon re-applies them as a
// backstop; see domain.SanitizeActivityText.
const (
	maxTextRunes   = domain.ActivityTextMaxRunes
	maxTargetRunes = domain.ActivityTargetMaxRunes
	ellipsis       = domain.ActivityEllipsis
	redactedMarker = domain.ActivityRedactedMarker
)

// toolInput is the ONLY shape a raw tool_input is ever decoded into. Every field
// here is whitelisted; everything else in the payload (content, command,
// old_string, new_string, prompt, env, tool_response, …) is dropped by the
// decoder before any rule below runs.
type toolInput struct {
	Description  string `json:"description"`
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Pattern      string `json:"pattern"`
	Query        string `json:"query"`
	URL          string `json:"url"`
}

// selector names the ONE whitelisted field a tool may contribute.
type selector uint8

const (
	// selNone: the tool is known and safe to name, but contributes no field.
	selNone selector = iota
	selDescription
	selFileBase
	selNotebookBase
	selPattern
	selQuery
	selURLHost
)

// whitelist is the per-tool table: tool name (lower-cased) -> the single field
// that becomes bubble content. A miss means the tool contributes nothing.
//
// Deliberate omissions: a Bash `command` is never read (a raw command can carry
// `TOKEN=… cmd`, and argv[0] is exactly where that lands), a Task `prompt` is
// never read (briefs carry private context), and no tool contributes more than
// one field. A Bash call with no `description` degrades to its bare name rather
// than falling back to the command.
var whitelist = map[string]struct {
	name string // canonical display name, emitted instead of the caller's string
	sel  selector
}{
	"bash":         {"Bash", selDescription},
	"task":         {"Task", selDescription},
	"read":         {"Read", selFileBase},
	"edit":         {"Edit", selFileBase},
	"write":        {"Write", selFileBase},
	"notebookedit": {"NotebookEdit", selNotebookBase},
	"glob":         {"Glob", selPattern},
	"grep":         {"Grep", selPattern},
	"websearch":    {"WebSearch", selQuery},
	"webfetch":     {"WebFetch", selURLHost},
	"todowrite":    {"TodoWrite", selNone},
}

// Curate maps a tool name plus its RAW native tool_input onto the curated detail
// that may be transmitted. rawInput is never retained and never re-emitted.
func Curate(kind domain.ActivityEventKind, tool string, rawInput json.RawMessage) domain.ActivityDetail {
	detail := domain.ActivityDetail{Kind: kind}
	rule, known := whitelist[strings.ToLower(strings.TrimSpace(tool))]
	if !known {
		// An unknown tool (every MCP tool, anything new) reports that SOMETHING
		// is happening and nothing about what. Degrading silently beats leaking.
		return detail
	}
	detail.Tool = rule.name

	var in toolInput
	if err := json.Unmarshal(rawInput, &in); err != nil {
		// A payload we cannot parse contributes no detail. The tool name stands.
		return detail
	}
	switch rule.sel {
	case selDescription:
		detail.Text = clean(in.Description, maxTextRunes)
	case selFileBase:
		detail.Target = clean(baseName(in.FilePath), maxTargetRunes)
	case selNotebookBase:
		detail.Target = clean(baseName(in.NotebookPath), maxTargetRunes)
	case selPattern:
		detail.Target = clean(in.Pattern, maxTargetRunes)
	case selQuery:
		detail.Target = clean(in.Query, maxTargetRunes)
	case selURLHost:
		detail.Target = clean(hostOf(in.URL), maxTargetRunes)
	case selNone:
	}
	return detail
}

// CurateName is the name-only path for harnesses whose tool hook carries the
// tool name but no payload AO has verified the shape of. It emits the tool name
// when it is whitelisted and nothing otherwise.
func CurateName(kind domain.ActivityEventKind, tool string) domain.ActivityDetail {
	return Curate(kind, tool, nil)
}

// baseName reduces a path to its final element so a curated line can never
// splash /Users/<name>/… onto a shared screen.
func baseName(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	// Normalise Windows separators before taking the last element.
	p = strings.ReplaceAll(p, `\`, "/")
	return path.Base(p)
}

// hostOf keeps a URL's host and drops everything else — a query string is a
// common carrier for access tokens.
func hostOf(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// clean flattens to one line, redacts secret-shaped runs and truncates.
func clean(s string, maxRunes int) string {
	return domain.SanitizeActivityText(s, maxRunes)
}

// Package transcript turns a Claude Code transcript file into the human's
// turns, each with a bounded window of what the agent did around it.
//
// It reads one file from a byte offset to its last complete line, and never
// loads the whole file: a long-lived orchestrator transcript runs to ~100 MB,
// and a pass must cost what is new, not what is old. It does no other I/O and
// keeps nothing - the caller (observe/learncapture) decides what to store.
//
// Contract, mirrored in docs/architecture.md ("Learning capture"): only the
// human's own turns are captured, as text; around each one only the agent's
// nearest words (clipped) and a curated list of its actions (tool name plus one
// whitelisted target, as the activity feed shows them) are kept. Tool results,
// file bodies, command lines and other sessions' messages are read only to be
// skipped.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/toolcurate"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/redact"
)

// Byte budgets for one captured turn. They are bytes, never characters: Thai
// is three bytes a character, and a budget in characters would let a Thai
// window run three times over.
const (
	// MaxHumanBytes bounds the human's text. Real turns are short (a median of
	// 44 bytes, p90 138, measured on this machine's transcripts); the bound only
	// stops a pasted page from being stored whole.
	MaxHumanBytes = 4096
	// MaxBeforeTextBytes keeps the END of what the agent said before the human
	// spoke: the question or claim the human is answering sits there.
	MaxBeforeTextBytes = 1500
	// MaxAfterTextBytes keeps the START of the agent's reply: how it took the
	// correction.
	MaxAfterTextBytes = 800
	// MaxActions bounds each window's action list.
	MaxActions = 20
	// maxActionBytes bounds one action's label.
	maxActionBytes = 120
)

// Options configure one pass.
type Options struct {
	// Delivered is what AO recorded delivering into the session, by body
	// fingerprint, including the session's brief.
	Delivered map[string]Delivery
	// Redactor removes secrets from every stored string. Nil applies the
	// pattern rules only.
	Redactor *redact.Redactor
	// FileQuiet is true when the file has not been written for long enough that
	// an open window can be closed at end of file: the agent has stopped.
	FileQuiet bool
}

// Carry is what a pass hands the next one when it stops at a human turn whose
// window is still open: the "before" it had already gathered for that turn.
type Carry struct {
	Before domain.LearnWindow `json:"before"`
}

// Turn is one finalized human turn, redacted.
type Turn struct {
	UUID       string
	At         time.Time
	Source     domain.LearnSourceClass
	Text       string
	Before     domain.LearnWindow
	After      domain.LearnWindow
	CWD        string
	GitBranch  string
	Redactions redact.Counts
}

// Result is what a pass read.
type Result struct {
	// Turns are the human turns whose windows closed in this pass.
	Turns []Turn
	// NextOffset is where the next pass starts.
	NextOffset int64
	// Carry is non-nil when NextOffset is the start of an open human turn.
	Carry *Carry
	// HumanTurns and MachineTurns count the turns read before NextOffset.
	HumanTurns   int
	MachineTurns int
	// PaneFingerprints are the fingerprints of every prompt submitted at the
	// pane before NextOffset, whoever wrote it - the hook saw exactly these.
	PaneFingerprints []string
	// CWD is the working directory the transcript last recorded.
	CWD string
}

// Head is what a transcript's first lines say about where and when it began.
type Head struct {
	CWD     string
	FirstAt time.Time
}

// ReadHead reads the working directory and first timestamp a transcript
// records. Capture uses it to tie a file to a session's worktree when the file
// name does not, and - where several sessions share one worktree, as every
// orchestrator of a project does - to the session that was live when it began.
func ReadHead(path string) (Head, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from AO's own enumeration of Claude Code's project dir
	if err != nil {
		return Head{}, err
	}
	defer func() { _ = f.Close() }()
	var h Head
	r := bufio.NewReaderSize(f, 1<<16)
	for i := 0; i < 50 && (h.CWD == "" || h.FirstAt.IsZero()); i++ {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var rec struct {
				CWD       string `json:"cwd"`
				Timestamp string `json:"timestamp"`
			}
			if json.Unmarshal(line, &rec) == nil {
				h.CWD = firstNonEmpty(h.CWD, rec.CWD)
				if h.FirstAt.IsZero() {
					h.FirstAt = parseTime(rec.Timestamp)
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return h, nil
			}
			return h, err
		}
	}
	return h, nil
}

// pendingTurn is a human turn whose "after" window is still being gathered.
type pendingTurn struct {
	offset int64
	turn   Turn
}

// window accumulates the agent's activity between two human turns.
type window struct {
	lastText  string
	firstText string
	actions   []string
}

func (w *window) addText(t string) {
	t = strings.TrimSpace(t)
	if t == "" {
		return
	}
	w.lastText = t
	if w.firstText == "" {
		w.firstText = t
	}
}

func (w *window) addAction(a string) {
	if a == "" {
		return
	}
	w.actions = append(w.actions, a)
}

// Read runs one pass over path from offset. carry must be the Carry the
// previous pass returned for that offset, or nil.
func Read(path string, offset int64, carry *Carry, opts Options) (Result, error) {
	f, err := os.Open(path) //nolint:gosec // path comes from AO's own enumeration of Claude Code's project dir
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return Result{}, fmt.Errorf("seek: %w", err)
	}

	type seen struct {
		offset int64
		human  bool
		paneFP string
	}
	var (
		res      Result
		reader   = bufio.NewReaderSize(f, 1<<20)
		pos      = offset
		cur      window // activity since the last human turn
		pending  *pendingTurn
		finished []Turn
		events   []seen
	)
	if carry != nil {
		cur.lastText = carry.Before.AgentText
		cur.actions = append(cur.actions, carry.Before.Actions...)
	}

	finalize := func(p *pendingTurn, after window) {
		p.turn.After = domain.LearnWindow{
			AgentText: redactClip(opts.Redactor, after.firstText, MaxAfterTextBytes, &p.turn.Redactions, false),
			Actions:   redactActions(opts.Redactor, headActions(after.actions), &p.turn.Redactions),
		}
		finished = append(finished, p.turn)
	}

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] != '\n' {
			// A line still being written. Leave it for the next pass.
			break
		}
		start := pos
		pos += int64(len(line))
		if len(line) > 0 {
			rec, ok := decode(line)
			if ok {
				res.CWD = firstNonEmpty(rec.CWD, res.CWD)
				switch rec.Type {
				case "assistant":
					text, _, blocks := contentOf(rec.Message.Content)
					cur.addText(text)
					for _, b := range blocks {
						if b.Type == "tool_use" {
							cur.addAction(actionLabel(b.Name, b.Input))
						}
					}
				case "user":
					c := classify(rec, opts.Delivered)
					switch c.class {
					case classHuman:
						if pending != nil {
							finalize(pending, cur)
						}
						t := Turn{
							UUID:       rec.UUID,
							At:         parseTime(rec.Timestamp),
							Source:     c.source,
							CWD:        rec.CWD,
							GitBranch:  rec.GitBranch,
							Redactions: redact.Counts{},
						}
						t.Before = domain.LearnWindow{
							AgentText: redactClip(opts.Redactor, cur.lastText, MaxBeforeTextBytes, &t.Redactions, true),
							Actions:   redactActions(opts.Redactor, tailActions(cur.actions), &t.Redactions),
						}
						t.Text = redactHuman(opts.Redactor, c.text, &t.Redactions)
						pending = &pendingTurn{offset: start, turn: t}
						cur = window{}
						events = append(events, seen{offset: start, human: true, paneFP: c.paneFP})
					case classMachine:
						cur.addAction(c.marker)
						events = append(events, seen{offset: start, paneFP: c.paneFP})
					case classNone:
					}
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return Result{}, fmt.Errorf("read: %w", err)
		}
	}

	res.NextOffset = pos
	if pending != nil {
		if opts.FileQuiet {
			finalize(pending, cur)
		} else {
			// The agent may still be answering this turn. Stop at it, and hand
			// its "before" to the pass that resumes there.
			res.NextOffset = pending.offset
			res.Carry = &Carry{Before: pending.turn.Before}
		}
	}
	res.Turns = finished
	for _, e := range events {
		if e.offset >= res.NextOffset {
			continue
		}
		if e.human {
			res.HumanTurns++
		} else {
			res.MachineTurns++
		}
		if e.paneFP != "" {
			res.PaneFingerprints = append(res.PaneFingerprints, e.paneFP)
		}
	}
	return res, nil
}

// decode parses one line when it can be a turn. Lines of other record types
// (attachments, snapshots, titles) are skipped before paying for a decode.
func decode(line []byte) (rawRecord, bool) {
	if !bytes.Contains(line, []byte(`"type":"user"`)) && !bytes.Contains(line, []byte(`"type":"assistant"`)) {
		return rawRecord{}, false
	}
	var rec rawRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		return rawRecord{}, false
	}
	return rec, rec.Type == "user" || rec.Type == "assistant"
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func redactHuman(r *redact.Redactor, text string, counts *redact.Counts) string {
	stripped, blobs := redact.StripBlobs(text)
	if blobs > 0 {
		(*counts)["blob"] += blobs
	}
	out, c := r.Redact(stripped)
	counts.Add(c)
	return redact.Clip(out, MaxHumanBytes)
}

// redactClip redacts BEFORE clipping, so a clip can never cut a secret in half
// and leave the half that no rule recognizes.
func redactClip(r *redact.Redactor, text string, maxBytes int, counts *redact.Counts, tail bool) string {
	if text == "" {
		return ""
	}
	stripped, blobs := redact.StripFences(text)
	if blobs > 0 {
		(*counts)["blob"] += blobs
	}
	out, c := r.Redact(stripped)
	counts.Add(c)
	if tail {
		return redact.ClipTail(out, maxBytes)
	}
	return redact.Clip(out, maxBytes)
}

// redactActions runs every action label through the redactor too: a Bash
// description is the agent's own prose and can name a value it was handed.
func redactActions(r *redact.Redactor, actions []string, counts *redact.Counts) []string {
	for i, a := range actions {
		out, c := r.Redact(a)
		counts.Add(c)
		actions[i] = out
	}
	return actions
}

func tailActions(a []string) []string {
	if len(a) > MaxActions {
		a = a[len(a)-MaxActions:]
	}
	return append([]string(nil), a...)
}

func headActions(a []string) []string {
	if len(a) > MaxActions {
		a = a[:MaxActions]
	}
	return append([]string(nil), a...)
}

// safeToolName accepts a tool name worth showing as-is (MCP tools included);
// anything else is reported only as "a tool".
var safeToolName = regexp.MustCompile(`^[A-Za-z0-9_:.-]{1,80}$`)

// actionLabel names one tool call the way the activity feed would: the tool and
// a single whitelisted target, never its command line or body. Skills are named,
// because which skill an agent reached for is exactly the kind of fact a lesson
// is about, and a skill name carries no payload.
func actionLabel(name string, input json.RawMessage) string {
	if strings.EqualFold(name, "Skill") {
		var in struct {
			Skill string `json:"skill"`
		}
		_ = json.Unmarshal(input, &in)
		if safeToolName.MatchString(in.Skill) {
			return "Skill(" + in.Skill + ")"
		}
		return "Skill"
	}
	d := toolcurate.Curate(domain.ActivityEventToolStart, name, input)
	if d.Tool != "" {
		target := firstNonEmpty(d.Text, d.Target)
		if target == "" {
			return d.Tool
		}
		return redact.Clip(d.Tool+"("+target+")", maxActionBytes)
	}
	if safeToolName.MatchString(name) {
		return name
	}
	return "a tool"
}

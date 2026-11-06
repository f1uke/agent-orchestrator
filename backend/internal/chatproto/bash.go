package chatproto

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// BashStream names the stream a piece of "!" command output came from.
type BashStream string

// Bash streams.
const (
	BashStdout BashStream = "stdout"
	BashStderr BashStream = "stderr"
)

// BashModelCap is how many bytes of a "!" command's stdout and stderr together
// the model reads. Past it the middle of the output is cut.
const BashModelCap = 30000

// BashOutput collects one stream of a "!" command for the model's copy. It
// keeps only the first and the last BashModelCap bytes, so a command that
// prints without end costs bounded memory. The zero value is ready to use.
type BashOutput struct {
	head  []byte
	tail  []byte // the last bytes after head, at most BashModelCap of them
	total int
}

// Write adds p to the stream. It never fails.
func (o *BashOutput) Write(p []byte) (int, error) {
	n := len(p)
	o.total += n
	if room := BashModelCap - len(o.head); room > 0 {
		take := min(room, len(p))
		o.head = append(o.head, p[:take]...)
		p = p[take:]
	}
	o.tail = append(o.tail, p...)
	if over := len(o.tail) - BashModelCap; over > 0 {
		o.tail = append(o.tail[:0], o.tail[over:]...)
	}
	return n, nil
}

// clip returns the stream in at most budget bytes: all of it when it fits,
// else its head and tail around a marker that says how much was cut. budget is
// at most BashModelCap.
func (o *BashOutput) clip(budget int) string {
	if o.total <= budget {
		return string(o.head) + string(o.tail)
	}
	head := o.head[:runeFloor(o.head, budget/2)]
	keep := budget - budget/2
	last := o.tail
	if len(last) < keep {
		// The tail never overflowed, so head and tail are the whole stream.
		last = append(slices.Clone(o.head), o.tail...)
	}
	last = last[len(last)-keep:]
	for len(last) > 0 && !utf8.RuneStart(last[0]) {
		last = last[1:]
	}
	cut := o.total - len(head) - len(last)
	return string(head) + fmt.Sprintf("\n... [%d characters cut] ...\n", cut) + string(last)
}

// runeFloor is the largest n <= limit at which b can be cut without splitting
// a UTF-8 sequence.
func runeFloor(b []byte, limit int) int {
	n := min(limit, len(b))
	for n > 0 && n < len(b) && !utf8.RuneStart(b[n]) {
		n--
	}
	return n
}

// bashModelStreams clips stdout and stderr to BashModelCap together. The
// shorter stream keeps up to half the cap and the longer one takes the rest,
// so a short error message survives a long log beside it.
func bashModelStreams(stdout, stderr *BashOutput) (string, string) {
	if stdout.total+stderr.total <= BashModelCap {
		return stdout.clip(BashModelCap), stderr.clip(BashModelCap)
	}
	short, long := stdout, stderr
	if long.total < short.total {
		short, long = long, short
	}
	shortBudget := min(short.total, BashModelCap/2)
	a, b := short.clip(shortBudget), long.clip(BashModelCap-shortBudget)
	if short == stdout {
		return a, b
	}
	return b, a
}

// BashResult adds a finished "!" command and its output to the conversation
// without starting a turn: a user message with shouldQuery false that carries
// the <bash-input> and <bash-stdout> tags the CLI uses for its own "!" rows.
// UUID is the run's id. ExitCode is the process's code, or 128 plus the signal
// number when a signal ended it.
type BashResult struct {
	UUID     string
	Command  string
	Stdout   BashOutput
	Stderr   BashOutput
	ExitCode int
	// Stopped means the person stopped the command: the model reads exit code
	// 130 and stderr ending in [stopped].
	Stopped bool
}

func (BashResult) isOutbound() {}

// bashStoppedCode is the exit code the model reads for a stopped command, as
// for the CLI's own interrupted "!" command.
const bashStoppedCode = 130

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func encodeBashResult(m BashResult) ([]byte, error) {
	if m.UUID == "" {
		return nil, errors.New("chatproto: bash result needs a uuid")
	}
	if m.Command == "" {
		return nil, errors.New("chatproto: bash result needs a command")
	}
	stdout, stderr := bashModelStreams(&m.Stdout, &m.Stderr)
	code := m.ExitCode
	if m.Stopped {
		code = bashStoppedCode
		if stderr != "" && !strings.HasSuffix(stderr, "\n") {
			stderr += "\n"
		}
		stderr += "[stopped]"
	}
	type text struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type message struct {
		Role    string `json:"role"`
		Content []text `json:"content"`
	}
	return json.Marshal(struct {
		Type        string  `json:"type"`
		UUID        string  `json:"uuid"`
		ShouldQuery bool    `json:"shouldQuery"`
		Message     message `json:"message"`
	}{Type: "user", UUID: m.UUID, ShouldQuery: false, Message: message{Role: "user", Content: []text{
		{"text", "<bash-input>" + xmlEscaper.Replace(m.Command) + "</bash-input>"},
		{"text", "<bash-stdout>" + xmlEscaper.Replace(stdout) + "</bash-stdout><bash-stderr>" + xmlEscaper.Replace(stderr) +
			"</bash-stderr><bash-exit-code>" + strconv.Itoa(code) + "</bash-exit-code>"},
	}}})
}

// BashPhase is where a "!" command run stands. A session is busy with the run
// from BashRunning until BashSettled.
type BashPhase string

// Bash phases, in order.
const (
	// BashRunning: the command's process runs.
	BashRunning BashPhase = "running"
	// BashEnded: the process ended; its result is not yet in the conversation.
	BashEnded BashPhase = "ended"
	// BashInjected: the result was sent to claude, which has not acknowledged it.
	BashInjected BashPhase = "injected"
	// BashSettled: claude acknowledged the result, or the claude process the
	// run was for ended.
	BashSettled BashPhase = "settled"
)

// BashChunk is one logged piece of a "!" command's output.
type BashChunk struct {
	Stream BashStream
	Text   string
}

// BashEnd is how a "!" command ended. Code is nil when a signal ended it, and
// Signal then names it. A run whose host went away before it ended reads as
// stopped with no code.
type BashEnd struct {
	Code      *int
	Signal    string
	Stopped   bool
	Truncated bool
}

// BashItem is a "!" command the person ran, with the output logged for it.
// End is nil while Phase is BashRunning and set after.
type BashItem struct {
	ID      string
	Command string
	CWD     string
	Output  []BashChunk
	Phase   BashPhase
	End     *BashEnd
}

func (BashItem) isItem() {}

func (f *Folder) applyBash(n HostNote) {
	b := n.Bash
	switch n.Kind {
	case NoteBashStart:
		if b.ID == "" || f.bashes[b.ID] != nil {
			return
		}
		run := &BashItem{ID: b.ID, Command: b.Command, CWD: b.CWD, Phase: BashRunning}
		f.bashes[b.ID], f.bash = run, run
		f.items = append(f.items, run)
	case NoteBashOutput:
		if run := f.bashes[b.ID]; run != nil && run.Phase == BashRunning {
			run.Output = append(run.Output, BashChunk{Stream: b.Stream, Text: b.Text})
		}
	case NoteBashEnd:
		if run := f.bashes[b.ID]; run != nil && run.Phase == BashRunning {
			end := &BashEnd{Code: n.Code, Stopped: b.Stopped, Truncated: b.Truncated}
			if b.Signal != nil {
				end.Signal = *b.Signal
			}
			run.Phase, run.End = BashEnded, end
		}
	}
}

// isBashResult reports whether u is the message that carried a run's result
// to claude, which the transcript shows as the run's row and not as a user
// row. The latest run's result moves it to BashInjected.
func (f *Folder) isBashResult(u User) bool {
	if !u.NoQuery || f.bashes[u.UUID] == nil {
		return false
	}
	if run := f.bash; run.ID == u.UUID && run.Phase == BashEnded {
		run.Phase = BashInjected
	}
	return true
}

// settleBash reports whether a result is claude's empty answer to an injected
// run, and settles the run. That result ends no turn.
func (f *Folder) settleBash() bool {
	if f.bash == nil || f.bash.Phase != BashInjected {
		return false
	}
	f.bash.Phase = BashSettled
	return true
}

// endBash settles the latest run when its claude process ends, since no
// result can reach claude, or be acknowledged, after that.
func (f *Folder) endBash() {
	run := f.bash
	if run == nil || run.Phase == BashSettled {
		return
	}
	if run.End == nil {
		run.End = &BashEnd{Stopped: true}
	}
	run.Phase = BashSettled
}

// activeBash returns a copy of the run the session is busy with, or nil.
func (f *Folder) activeBash() *BashItem {
	if f.bash == nil || f.bash.Phase == BashSettled {
		return nil
	}
	c := cloneBash(f.bash)
	return &c
}

func cloneBash(run *BashItem) BashItem {
	c := *run
	c.Output = slices.Clone(run.Output)
	if run.End != nil {
		e := *run.End
		if run.End.Code != nil {
			code := *run.End.Code
			e.Code = &code
		}
		c.End = &e
	}
	return c
}

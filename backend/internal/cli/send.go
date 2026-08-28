package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

type sendOptions struct {
	session      string
	crew         string
	about        string
	message      string
	messageFile  string
	stillWorking bool
}

// sendAPIRequest mirrors the daemon's SendSessionMessageRequest body for
// POST /api/v1/sessions/{id}/send. The CLI keeps its own copy so it need not
// import httpd.
type sendAPIRequest struct {
	Message string `json:"message"`
	// From is the sender's own session id, so the daemon can recognise - and cap -
	// a message between two members of one crew. Empty when a human runs this.
	From string `json:"from,omitempty"`
	// About is the commit SHA or smoke case id the message concerns. Required
	// between crewmates.
	About string `json:"about,omitempty"`
}

// crewSendAPIRequest mirrors the daemon's CrewSendRequest for
// POST /api/v1/sessions/{id}/crew/send, where {id} is the SENDER.
type crewSendAPIRequest struct {
	Role    string `json:"role"`
	Message string `json:"message"`
	About   string `json:"about,omitempty"`
	// StillWorking says this message is a mid-run update rather than the end of
	// qa's run, which is what exempts it from the handback check.
	StillWorking bool `json:"stillWorking,omitempty"`
}

// sendAPIResponse mirrors the daemon's SendSessionMessageResponse.
type sendAPIResponse struct {
	SessionID       string `json:"sessionId"`
	Queued          bool   `json:"queued"`
	PendingMessages int    `json:"pendingMessages"`
	// Handback is present when the daemon checked this message as the end of qa's
	// run; see reportHandback.
	Handback *handbackAPIView `json:"handback,omitempty"`
}

// handbackAPIView mirrors the daemon's HandbackCompletenessView.
type handbackAPIView struct {
	Cases     int      `json:"cases"`
	NotDriven []string `json:"notDriven"`
}

func newSendCommand(ctx *commandContext) *cobra.Command {
	var opts sendOptions
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send a message to a running agent session",
		Long: "Sends a message to an agent. A session that is not listening right now has it\n" +
			"HELD and delivered when it is, so a message is never silently lost.\n\n" +
			"MESSAGING YOUR CREWMATE. On a task worked by two agents, address the other one\n" +
			"by ROLE - `--crew qa` or `--crew dev` - never by id: the crew is formed after\n" +
			"dev is already running, so dev's environment cannot carry qa's id. Those\n" +
			"messages are CAPPED, and the caps are the only thing standing between two\n" +
			"agents that can each answer the other and a bill nobody is watching:\n\n" +
			"  --about is required   name the commit SHA or smoke case id it is about\n" +
			"  3 per subject         per direction; the 4th is refused and the task goes\n" +
			"                        to NEEDS YOU for a human\n" +
			"  20 per hour per crew  a backstop against a loop that keeps inventing new\n" +
			"                        subjects\n\n" +
			"There is NO obligation to reply, and that is deliberate: the ARTIFACT is the\n" +
			"reply. dev answers a finding by committing; qa answers a handoff by recording a\n" +
			"result. The one message that is not a reply and IS required is qa telling dev a\n" +
			"run has finished - the end of qa's run is the start of dev's.\n\n" +
			"THE HANDBACK IS CHECKED. A qa->dev message is read as the END of qa's run, so\n" +
			"AO looks at the task's smoke checklist and says how many cases carry nothing\n" +
			"from any machine. It does NOT refuse - a handback that never lands is worse than\n" +
			"an incomplete one - it says so, to you and to dev, and names them. Every case\n" +
			"should be in one of two states: DRIVEN (`ao smoke record`, a verdict or\n" +
			"evidence-only) or declared UNDRIVEABLE (`--verdict skip --note \"<why you could\n" +
			"not run it>\"`, and the why has to come from an attempt). If you are not finished,\n" +
			"say so with --still-working rather than skipping cases to quiet the count.",
		Example: `  ao send --session agent-orchestrator-59 --message "CI is green"
  ao send --crew dev --about 1185d0b4 --message "tests pass on this commit; 2 cases recorded"
  ao send --crew qa --about tab-stays-live --message "fixed and pushed"`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.sendMessage(cmd.Context(), opts, cmd.InOrStdin())
		},
	}
	cmd.Flags().StringVar(&opts.session, "session", "", "Session id (required unless --crew)")
	cmd.Flags().StringVar(&opts.crew, "crew", "", "Message your crewmate by ROLE (`dev` or `qa`) instead of by id. The only address that cannot go stale: a crew is formed after dev is already running, so dev never learns qa's id from its environment.")
	cmd.Flags().StringVar(&opts.about, "about", "", "The commit SHA or smoke case id this message is ABOUT. Required when messaging your crewmate: every message between the two agents on a task names a durable artifact, and a subject is allowed only 3 messages in one direction before the next is refused and the task goes to NEEDS YOU.")
	cmd.Flags().StringVar(&opts.message, "message", "", "Message body (required unless --message-file)")
	cmd.Flags().BoolVar(&opts.stillWorking, "still-working", false, "qa only: this message is a mid-run update, NOT the end of your run. Without it a message to dev is read as your handback and AO reports which checklist cases carry nothing from any machine. Use it when you mean it; declaring cases undriveable to quiet the count is the one thing that makes the check worthless.")
	cmd.Flags().StringVar(&opts.messageFile, "message-file", "", "Read the message from a file, or '-' for stdin; mutually exclusive with --message. Use for large messages that would be awkward to quote on the command line.")
	return cmd
}

func (c *commandContext) sendMessage(ctx context.Context, opts sendOptions, stdin io.Reader) error {
	// Validate --session first: it is a cheap synchronous check, and running it
	// before resolving the message means `--message-file -` on an incomplete
	// invocation exits immediately instead of blocking on stdin.
	session := strings.TrimSpace(opts.session)
	role := strings.TrimSpace(opts.crew)
	if session == "" && role == "" {
		return usageError{errors.New("usage: --session or --crew is required")}
	}
	if session != "" && role != "" {
		return usageError{errors.New("--session and --crew are mutually exclusive; pass only one")}
	}
	if opts.stillWorking && role == "" {
		return usageError{errors.New("--still-working is about your own crew run, so it only means something with --crew")}
	}
	message, err := resolveMessage(opts.message, opts.messageFile, stdin)
	if err != nil {
		return err
	}
	// Tag the message with the sender's canonical session id under the `@`
	// reference sigil (`[from @<project>-<num>]`), so the recipient's in-app
	// terminal linkifies it and can navigate back to the sender. AO_SESSION_ID is
	// the canonical `<project>-<num>`; the `@` is the human/agent-facing sigil.
	sender := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sender != "" {
		message = "[from @" + sender + "] " + message
	}

	var res sendAPIResponse
	if role != "" {
		if sender == "" {
			return usageError{errors.New("--crew names your crewmate, so it only works from inside a session (AO_SESSION_ID is unset here); use --session instead")}
		}
		// The path names the SENDER: the daemon resolves the role to a session id,
		// because the sender cannot.
		path := "sessions/" + url.PathEscape(sender) + "/crew/send"
		if err := c.postJSON(ctx, path, crewSendAPIRequest{
			Role: role, Message: message, About: opts.about, StillWorking: opts.stillWorking,
		}, &res); err != nil {
			return err
		}
		if err := reportHandback(c.deps.Out, res); err != nil {
			return err
		}
		return reportSend(c.deps.Out, orFallback(res.SessionID, role), res)
	}

	// PathEscape: session ids are already "-"/digit safe, but may later come
	// from sanitized issue refs; keep the URL well-formed regardless.
	path := "sessions/" + url.PathEscape(session) + "/send"
	if err := c.postJSON(ctx, path, sendAPIRequest{Message: message, From: sender, About: opts.about}, &res); err != nil {
		return err
	}
	return reportSend(c.deps.Out, session, res)
}

// reportHandback says what the task's checklist looked like at the moment this
// message ended qa's run. It prints only when something was left undone, because
// a complete handback needs no commentary - and it names the cases, because "3
// cases" sends the reader back to the list to work out which three.
//
// The message has already been delivered by the time this prints. That is the
// design and not a race: refusing the handback would recreate the silent stall
// the handback obligation exists to prevent, and it is the version of this check
// that is easiest to satisfy by declaring the remaining cases undriveable.
func reportHandback(out io.Writer, res sendAPIResponse) error {
	if res.Handback == nil || len(res.Handback.NotDriven) == 0 {
		return nil
	}
	n := len(res.Handback.NotDriven)
	subject := "cases carry"
	if n == 1 {
		subject = "case carries"
	}
	_, err := fmt.Fprintf(out,
		"sent - and AO told dev this too: %d of %d checklist %s nothing from any machine.\n"+
			"  %s\n"+
			"Each one is either yours to DRIVE (`ao smoke record --case <id>` with a verdict, or with --evidence\n"+
			"and no verdict) or one you must declare UNDRIVEABLE (`--verdict skip --note \"<why>\"`) - and the why\n"+
			"has to come from an ATTEMPT, not an assumption. If your run is not actually over, say `--still-working`.\n",
		n, res.Handback.Cases, subject, strings.Join(res.Handback.NotDriven, ", "))
	return err
}

// reportSend says what happened to a message the daemon accepted. A HELD message
// is a success the agent has not seen yet, and saying nothing about it would
// leave the sender believing it was delivered.
func reportSend(out io.Writer, recipient string, res sendAPIResponse) error {
	if !res.Queued {
		return nil
	}
	_, err := fmt.Fprintf(out,
		"queued for %s: the agent is not listening right now, so the message is held and will be delivered once it is (%d waiting)\n",
		recipient, res.PendingMessages)
	return err
}

func orFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// resolveMessage returns the effective message body from --message /
// --message-file. The two are mutually exclusive. --message-file "-" reads
// stdin; any other value reads that file. Loading from a file (or stdin) lets a
// large message skip the shell's quoting and ARG_MAX entirely. Mirrors
// `ao spawn --prompt-file`.
//
// The body is forwarded verbatim — only the blank check is trimmed — because
// leading/trailing whitespace is part of a message the agent will read.
func resolveMessage(message, messageFile string, stdin io.Reader) (string, error) {
	file := strings.TrimSpace(messageFile)
	if file == "" {
		if strings.TrimSpace(message) == "" {
			return "", usageError{errors.New("usage: --message is required")}
		}
		return message, nil
	}
	if message != "" {
		return "", usageError{errors.New("--message and --message-file are mutually exclusive; pass only one")}
	}
	var (
		raw []byte
		err error
	)
	if file == "-" {
		raw, err = io.ReadAll(stdin)
	} else {
		raw, err = os.ReadFile(file)
	}
	if err != nil {
		return "", usageError{fmt.Errorf("read message file %q: %w", file, err)}
	}
	if strings.TrimSpace(string(raw)) == "" {
		return "", usageError{fmt.Errorf("message file %q is empty", file)}
	}
	return string(raw), nil
}

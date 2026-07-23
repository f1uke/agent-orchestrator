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
	session     string
	message     string
	messageFile string
}

// sendAPIRequest mirrors the daemon's SendSessionMessageRequest body for
// POST /api/v1/sessions/{id}/send. The CLI keeps its own copy so it need not
// import httpd.
type sendAPIRequest struct {
	Message string `json:"message"`
}

func newSendCommand(ctx *commandContext) *cobra.Command {
	var opts sendOptions
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send a message to a running agent session",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.sendMessage(cmd.Context(), opts, cmd.InOrStdin())
		},
	}
	cmd.Flags().StringVar(&opts.session, "session", "", "Session id (required)")
	cmd.Flags().StringVar(&opts.message, "message", "", "Message body (required unless --message-file)")
	cmd.Flags().StringVar(&opts.messageFile, "message-file", "", "Read the message from a file, or '-' for stdin; mutually exclusive with --message. Use for large messages that would be awkward to quote on the command line.")
	return cmd
}

func (c *commandContext) sendMessage(ctx context.Context, opts sendOptions, stdin io.Reader) error {
	// Validate --session first: it is a cheap synchronous check, and running it
	// before resolving the message means `--message-file -` on an incomplete
	// invocation exits immediately instead of blocking on stdin.
	session := strings.TrimSpace(opts.session)
	if session == "" {
		return usageError{errors.New("usage: --session is required")}
	}
	message, err := resolveMessage(opts.message, opts.messageFile, stdin)
	if err != nil {
		return err
	}
	// Tag the message with the sender's canonical session id under the `@`
	// reference sigil (`[from @<project>-<num>]`), so the recipient's in-app
	// terminal linkifies it and can navigate back to the sender. AO_SESSION_ID is
	// the canonical `<project>-<num>`; the `@` is the human/agent-facing sigil.
	if sender := strings.TrimSpace(os.Getenv("AO_SESSION_ID")); sender != "" {
		message = "[from @" + sender + "] " + message
	}

	// PathEscape: session ids are already "-"/digit safe, but may later come
	// from sanitized issue refs; keep the URL well-formed regardless.
	path := "sessions/" + url.PathEscape(session) + "/send"
	return c.postJSON(ctx, path, sendAPIRequest{Message: message}, nil)
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

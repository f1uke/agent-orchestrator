package tmux

import "fmt"

// newSessionArgs builds args for `tmux new-session -d -s <id> -x 220 -y 50
// -c <cwd> <shell> -c <launchCmd>`. The shell -c form runs the launch command
// inside the configured shell so exported env vars and quoting work correctly.
func newSessionArgs(id, cwd, shellPath, launchCmd string) []string {
	return []string{
		"new-session", "-d",
		"-s", id,
		"-x", "220",
		"-y", "50",
		"-c", cwd,
		shellPath, "-c", launchCmd,
	}
}

// newSessionScriptArgs is newSessionArgs for a launch delivered as a SCRIPT FILE
// (`<shell> <scriptPath>`) instead of an inline `<shell> -c <cmd>`. Used when the
// launch command exceeds tmux's inline command-length limit (see launchInvocation).
func newSessionScriptArgs(id, cwd, shellPath, scriptPath string) []string {
	return []string{
		"new-session", "-d",
		"-s", id,
		"-x", "220",
		"-y", "50",
		"-c", cwd,
		shellPath, scriptPath,
	}
}

// setStatusOffArgs hides the tmux status bar for the given session.
// set-option uses pane-targeting syntax which does not accept the `=` prefix,
// so we pass the session name directly.
func setStatusOffArgs(id string) []string {
	return []string{"set-option", "-t", id, "status", "off"}
}

// setMouseOnArgs enables tmux mouse mode so the terminal's SGR mouse-wheel
// reports scroll the pane via copy-mode; without it, wheel scrolling no-ops.
// Pane-targeting, so no `=` prefix (see setStatusOffArgs).
func setMouseOnArgs(id string) []string {
	return []string{"set-option", "-t", id, "mouse", "on"}
}

// killSessionArgs builds args for `tmux kill-session -t =<id>`. The `=` prefix
// requests exact-name matching so a session "foo" does not accidentally match
// "foobar" (tmux otherwise does unique-prefix matching).
func killSessionArgs(id string) []string {
	return []string{"kill-session", "-t", exactSessionTarget(id)}
}

// hasSessionArgs builds args for `tmux has-session -t =<id>`. The `=` prefix
// requests exact-name matching (see killSessionArgs).
func hasSessionArgs(id string) []string {
	return []string{"has-session", "-t", exactSessionTarget(id)}
}

// exactSessionTarget wraps id in tmux's exact-match prefix `=` so session-
// selection commands (-t) target only the session with that precise name.
// Only kill-session and has-session support this prefix; pane-targeting
// commands (send-keys, capture-pane, set-option) use a plain session name.
func exactSessionTarget(id string) string {
	return "=" + id
}

// sendKeysLiteralArgs builds args for `tmux send-keys -t <id> -l <chunk>`.
// The -l flag stops tmux interpreting words like "Enter" as key names so the
// text is sent verbatim.
func sendKeysLiteralArgs(id, chunk string) []string {
	return []string{"send-keys", "-t", id, "-l", chunk}
}

// tmuxCommandArgvBudget is how many bytes of packed argv fit in one tmux
// command. The tmux CLI ships a command to the tmux server as a single libimsg
// message, so this is a hard protocol ceiling, not a tunable: MAX_IMSGSIZE
// (16384) minus IMSG_HEADER_SIZE (16) minus the `int argc` of struct
// msg_command (4). Exceed it and tmux refuses the command outright with
// "command too long" — it does not truncate.
const tmuxCommandArgvBudget = 16384 - 16 - 4

// packedArgvBytes reports how much of the budget an argv consumes. tmux's
// cmd_pack_argv writes each argument followed by a NUL, so every argument costs
// its length plus one.
func packedArgvBytes(args []string) int {
	n := 0
	for _, a := range args {
		n += len(a) + 1
	}
	return n
}

// sendKeysLiteralBudget returns the largest literal payload that
// `tmux send-keys -t <id> -l <chunk>` can carry for this target. The overhead is
// derived from sendKeysLiteralArgs itself rather than hardcoded, so it cannot
// drift if those flags ever change. Note the target id is charged against the
// same budget: a longer session id leaves less room for the message.
//
// Verified against tmux 3.6a — the returned size is accepted and one byte more
// is rejected, for both short and long session ids.
func sendKeysLiteralBudget(id string) int {
	return tmuxCommandArgvBudget - packedArgvBytes(sendKeysLiteralArgs(id, ""))
}

// sendEnterArgs builds args for `tmux send-keys -t <id> Enter` to submit the
// queued input.
func sendEnterArgs(id string) []string {
	return []string{"send-keys", "-t", id, "Enter"}
}

// listPanePIDArgs builds args for `tmux list-panes -t <id> -F '#{pane_pid}'`,
// which prints the pid of each pane's leader process. AgentAlive reads the first
// line to find the pane leader whose children reveal whether the agent is live.
// Pane-targeting, so a plain session name (no `=` prefix; see exactSessionTarget).
func listPanePIDArgs(id string) []string {
	return []string{"list-panes", "-t", id, "-F", "#{pane_pid}"}
}

// capturePaneArgs builds args for `tmux capture-pane -t <id> -p -S -<lines>`.
// -p prints to stdout; -S -<n> starts n lines back in history.
func capturePaneArgs(id string, lines int) []string {
	return []string{"capture-pane", "-t", id, "-p", "-S", fmt.Sprintf("-%d", lines)}
}

package domain

// ClaudeProfileRestart is what a Claude profile switch did about the session's
// running agent.
type ClaudeProfileRestart string

const (
	// ClaudeProfileRestarted means the agent was idle and was restarted on the
	// new profile.
	ClaudeProfileRestarted ClaudeProfileRestart = "restarted"
	// ClaudeProfileRestartPending means the agent was mid-turn; the daemon
	// restarts it once it is idle.
	ClaudeProfileRestartPending ClaudeProfileRestart = "pending"
	// ClaudeProfileNextLaunch means nothing was restarted; the session's next
	// launch, restart or restore applies the profile.
	ClaudeProfileNextLaunch ClaudeProfileRestart = "next_launch"
)

package sessionhost

import (
	"slices"
	"testing"
)

func TestClaudeArgv(t *testing.T) {
	cases := []struct {
		name    string
		command []string
		resume  string
		model   string
		want    []string
	}{
		{
			name:    "fresh keeps the pinned session id",
			command: []string{"claude", "--session-id", "pin", "--model", "opus"},
			want:    append([]string{"claude", "--session-id", "pin", "--model", "opus"}, protocolFlags...),
		},
		{
			name:    "resume drops the session id claude refuses beside --resume",
			command: []string{"claude", "--session-id", "pin", "--model", "opus"},
			resume:  "pin",
			want:    append(append([]string{"claude", "--model", "opus"}, protocolFlags...), "--resume", "pin"),
		},
		{
			name:    "resume drops the joined form too",
			command: []string{"claude", "--session-id=pin", "--model", "opus"},
			resume:  "other",
			want:    append(append([]string{"claude", "--model", "opus"}, protocolFlags...), "--resume", "other"),
		},
		{
			name:    "a chosen model replaces the launch model",
			command: []string{"claude", "--model", "opus", "--effort", "high"},
			model:   "claude-sonnet-5-5",
			want:    append([]string{"claude", "--effort", "high", "--model", "claude-sonnet-5-5"}, protocolFlags...),
		},
		{
			name:    "a chosen model replaces the joined form too",
			command: []string{"claude", "--model=opus", "--", "--model", "x"},
			model:   "claude-sonnet-5-5",
			want:    append(append([]string{"claude", "--model", "claude-sonnet-5-5"}, protocolFlags...), "--", "--model", "x"),
		},
		{
			name:    "a session id after -- is prompt text, not a flag",
			command: []string{"claude", "--model", "opus", "--", "--session-id", "x"},
			resume:  "pin",
			want:    append(append([]string{"claude", "--model", "opus"}, protocolFlags...), "--resume", "pin", "--", "--session-id", "x"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := claudeArgv(c.command, c.resume, c.model); !slices.Equal(got, c.want) {
				t.Errorf("claudeArgv(%v, %q, %q) =\n %v\nwant\n %v", c.command, c.resume, c.model, got, c.want)
			}
		})
	}
}

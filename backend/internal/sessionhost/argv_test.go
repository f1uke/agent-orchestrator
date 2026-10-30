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
			name:    "a session id after -- is prompt text, not a flag",
			command: []string{"claude", "--model", "opus", "--", "--session-id", "x"},
			resume:  "pin",
			want:    append(append([]string{"claude", "--model", "opus"}, protocolFlags...), "--resume", "pin", "--", "--session-id", "x"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := claudeArgv(c.command, c.resume); !slices.Equal(got, c.want) {
				t.Errorf("claudeArgv(%v, %q) =\n %v\nwant\n %v", c.command, c.resume, got, c.want)
			}
		})
	}
}

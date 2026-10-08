package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// cobra prints the first `backticked` text in a flag's usage as the flag's
// value name, so a usage that quotes a command in backticks renders as
// "--mobile-platform ao sim flow run" in --help. The skill pages send agents to
// --help for flags, so a garbled flag line is a garbled reference.
func TestHelp_FlagValueNamesAreSingleWords(t *testing.T) {
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if name, _ := pflag.UnquoteUsage(f); strings.ContainsAny(name, " -") {
				t.Errorf("`%s --%s` shows %q as its value name: drop the backticks from its usage", cmd.CommandPath(), f.Name, name)
			}
		})
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(NewRootCommand(Deps{}))
}

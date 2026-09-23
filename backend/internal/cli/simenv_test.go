package cli

import "os"

// A worker's pane carries the simulator the daemon gave its session. The sim
// tests assume none and opt in with t.Setenv, so running the suite inside an AO
// session that holds a simulator would otherwise hand them that session's
// device. Cleared once for the whole test binary: a TestMain cannot be used,
// the e2e suite in this directory already defines one.
func init() {
	for _, k := range []string{"AO_SIM_UDID", "AO_SIM_DESTINATION", "AO_SIM_APP"} {
		_ = os.Unsetenv(k)
	}
}

package session

import (
	"os"
	"testing"
)

// TestMain keeps git from maintaining the fixture repositories in the
// background. Since git 2.47, "git commit" may start "git maintenance run
// --auto" detached; on CI (git 2.55) it was still writing into .git when the
// test ended, so t.TempDir's cleanup failed with "directory not empty". The
// settings reach every git process these tests start, through git's own
// GIT_CONFIG_COUNT environment.
func TestMain(m *testing.M) {
	for k, v := range map[string]string{
		"GIT_CONFIG_COUNT":   "3",
		"GIT_CONFIG_KEY_0":   "maintenance.auto",
		"GIT_CONFIG_VALUE_0": "false",
		"GIT_CONFIG_KEY_1":   "maintenance.autoDetach",
		"GIT_CONFIG_VALUE_1": "false",
		"GIT_CONFIG_KEY_2":   "gc.auto",
		"GIT_CONFIG_VALUE_2": "0",
	} {
		_ = os.Setenv(k, v)
	}
	os.Exit(m.Run())
}

package tmux

import (
	"reflect"
	"testing"
)

func TestStripEnvKeys(t *testing.T) {
	t.Run("drops daemon-only AO keys so they do not propagate into worker panes", func(t *testing.T) {
		in := []string{
			"PATH=/usr/bin",
			"AO_SESSION_IDLE_CLOSE=3h",
			"AO_OWNER=app",
			"AO_DATA_DIR=/Users/me/.ao/data",
			"AO_SESSION_ID=agent-orchestrator-51",
		}
		got := stripEnvKeys(in, daemonOnlyEnvKeys)
		want := []string{
			"PATH=/usr/bin",
			"AO_DATA_DIR=/Users/me/.ao/data",
			"AO_SESSION_ID=agent-orchestrator-51",
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("stripEnvKeys = %v, want %v", got, want)
		}
	})

	t.Run("does not mutate the input slice", func(t *testing.T) {
		in := []string{"AO_OWNER=app", "PATH=/usr/bin"}
		_ = stripEnvKeys(in, daemonOnlyEnvKeys)
		if in[0] != "AO_OWNER=app" {
			t.Errorf("input mutated: %v", in)
		}
	})

	t.Run("returns the same content when no keys match", func(t *testing.T) {
		in := []string{"PATH=/usr/bin", "HOME=/Users/me"}
		got := stripEnvKeys(in, daemonOnlyEnvKeys)
		if !reflect.DeepEqual(got, in) {
			t.Errorf("stripEnvKeys = %v, want %v", got, in)
		}
	})

	t.Run("a bare key with no '=' matches by its whole string", func(t *testing.T) {
		// os.Environ() always yields KEY=VALUE, but be explicit: "AO_OWNER" with no
		// '=' has key "AO_OWNER" and is dropped just like "AO_OWNER=app".
		in := []string{"AO_OWNER", "AO_OWNER=app", "PATH=/usr/bin"}
		got := stripEnvKeys(in, daemonOnlyEnvKeys)
		want := []string{"PATH=/usr/bin"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("stripEnvKeys = %v, want %v", got, want)
		}
	})
}

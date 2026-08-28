package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The device a session was given has to reach the AGENT, not just the database.
// An assignment nothing exports is a fact only AO knows, and the failure this
// change exists to fix was an agent that had no way to know which device was
// its own.
func TestRuntimeEnv_ExportsTheSessionsOwnSimulator(t *testing.T) {
	m := layeredManager(newFakeStore(), nil)
	m.SetSimDeviceAssigner(func(_ context.Context, id domain.SessionID) (string, error) {
		if id != "mer-1" {
			t.Errorf("assigner asked about %q", id)
		}
		return "087df306-1fc9-4e5a-b9ed-ad36d6a1a0f1", nil
	})

	env := m.runtimeEnv(context.Background(), "mer-1", "mer", "", domain.KindWorker, "", "", "/work", nil)

	// Normalized, because simctl reports udids upper-cased and half the tools
	// an agent pastes this into compare them as strings.
	if got := env[EnvSimUDID]; got != "087DF306-1FC9-4E5A-B9ED-AD36D6A1A0F1" {
		t.Fatalf("%s = %q", EnvSimUDID, got)
	}
	// Ready to paste: `xcodebuild -destination "$AO_SIM_DESTINATION"` has to
	// work without the agent knowing xcodebuild's syntax, or it will reach for
	// the booted device instead - which is how this started.
	if got := env[EnvSimDestination]; got != "id=087DF306-1FC9-4E5A-B9ED-AD36D6A1A0F1" {
		t.Fatalf("%s = %q", EnvSimDestination, got)
	}
}

// A machine with no device to spare must leave the environment exactly as it
// was. An exported empty AO_SIM_UDID would read to `ao sim` as "the caller
// named a device" and to xcodebuild as a destination it rejects.
func TestRuntimeEnv_NoDeviceExportsNothing(t *testing.T) {
	for name, assigner := range map[string]func(context.Context, domain.SessionID) (string, error){
		"unwired":   nil,
		"no device": func(context.Context, domain.SessionID) (string, error) { return "", nil },
		"assignment failed": func(context.Context, domain.SessionID) (string, error) {
			return "", errors.New("the store is unhappy")
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := layeredManager(newFakeStore(), nil)
			if assigner != nil {
				m.SetSimDeviceAssigner(assigner)
			}
			env := m.runtimeEnv(context.Background(), "mer-1", "mer", "", domain.KindWorker, "", "", "/work", nil)
			if _, ok := env[EnvSimUDID]; ok {
				t.Fatalf("%s was exported as %q", EnvSimUDID, env[EnvSimUDID])
			}
			if _, ok := env[EnvSimDestination]; ok {
				t.Fatalf("%s was exported as %q", EnvSimDestination, env[EnvSimDestination])
			}
			// And the rest of the environment is untouched.
			if env[EnvSessionID] != "mer-1" {
				t.Fatalf("%s = %q", EnvSessionID, env[EnvSessionID])
			}
		})
	}
}

// A project cannot hand its sessions a device that is not theirs: the AO vars
// are written last, exactly as AO_SESSION_ID is.
func TestRuntimeEnv_ProjectCannotOverrideTheAssignedDevice(t *testing.T) {
	m := layeredManager(newFakeStore(), nil)
	m.SetSimDeviceAssigner(func(context.Context, domain.SessionID) (string, error) {
		return "087DF306-1FC9-4E5A-B9ED-AD36D6A1A0F1", nil
	})

	env := m.runtimeEnv(context.Background(), "mer-1", "mer", "", domain.KindWorker, "", "", "/work",
		map[string]string{EnvSimUDID: "SOMEBODY-ELSES-DEVICE"})

	if got := env[EnvSimUDID]; got != "087DF306-1FC9-4E5A-B9ED-AD36D6A1A0F1" {
		t.Fatalf("%s = %q; a project override took another session's device", EnvSimUDID, got)
	}
}

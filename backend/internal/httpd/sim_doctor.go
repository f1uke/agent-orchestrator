package httpd

import (
	"context"
	"os/exec"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simhealth"
)

// SimAssignments reads the simulator a session was given at spawn (the udid
// its AO_SIM_UDID carries). *sqlite.Store satisfies it.
type SimAssignments interface {
	GetSimDeviceAssignment(ctx context.Context, id domain.SessionID) (domain.SimDeviceAssignment, bool, error)
}

// simDoctorReaders wires `ao sim doctor` to the daemon's own state. nil, and
// so a 501, when there is no lease service to ask.
//
// The device listing is read fresh from simctl rather than through the Device
// tab's cached screen: that cache may be half a minute old, and a doctor that
// called a device booted seconds after it was shut down would be wrong about
// the one thing it is asked.
func simDoctorReaders(deps APIDeps, trust controllers.SimTrustResolver) *simhealth.Readers {
	if deps.Sim == nil {
		return nil
	}
	return &simhealth.Readers{
		Devices: func(ctx context.Context) ([]simctl.Device, error) {
			return simctl.List(ctx, exec.LookPath, commandOutput)
		},
		Assigned: func(ctx context.Context, id domain.SessionID) (string, error) {
			if deps.SimAssignments == nil {
				return "", nil
			}
			assignment, _, err := deps.SimAssignments.GetSimDeviceAssignment(ctx, id)
			return assignment.UDID, err
		},
		Leases: deps.Sim.List,
		CAFiles: func(ctx context.Context, id domain.SessionID) ([]string, error) {
			if trust == nil {
				return nil, nil
			}
			return trust.SimTrustFor(ctx, id)
		},
		Run:     commandOutput,
		Trusted: simhealth.TrustStoreHas,
	}
}

// commandOutput runs a command and returns its combined output, the shape
// simctl and simbuild read.
func commandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

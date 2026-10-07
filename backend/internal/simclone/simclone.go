// Package simclone is the one place in AO that creates or deletes a local iOS
// Simulator.
//
// It is its own door for the reason internal/simpower is: internal/simctl is
// read-only against the machine, and every caller of a device listing relies
// on that. Making and removing devices is the opposite kind of operation.
//
// Nothing here decides WHICH device may be deleted. That is the sim service's
// job, which deletes only devices it has a record of cloning; this package
// just runs the command.
package simclone

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
)

// ErrBooted is simctl's refusal to clone a device that is running.
var ErrBooted = errors.New("simclone: a booted device cannot be cloned")

// Clone copies a shut-down device under a new name and returns the copy's
// udid. Measured at 4.5-6.5 s for an iOS 26.3 device on an APFS volume.
func Clone(ctx context.Context, lookPath simctl.LookPath, run simctl.Runner, sourceUDID, name string) (string, error) {
	if _, err := lookPath(simctl.Binary); err != nil {
		return "", fmt.Errorf("%w: %s not found on PATH", simctl.ErrUnavailable, simctl.Binary)
	}
	out, err := run(ctx, simctl.Binary, "simctl", "clone", sourceUDID, name)
	if err != nil {
		// CoreSimulator's own words for it (SimError 405).
		if strings.Contains(string(out), "current state: Booted") {
			return "", fmt.Errorf("%w: %s", ErrBooted, simctl.Output(out))
		}
		return "", fmt.Errorf("`simctl clone %s` failed: %w: %s", sourceUDID, err, simctl.Output(out))
	}
	udid := domain.NormalizeSimUDID(lastLine(string(out)))
	if udid == "" {
		return "", fmt.Errorf("`simctl clone %s` printed no udid: %s", sourceUDID, simctl.Output(out))
	}
	return udid, nil
}

// Delete removes a device, shutting it down first if it is running (simctl
// does that itself). A device that no longer exists is already deleted, so it
// is not an error: the sweep that calls this must converge however often it
// runs.
func Delete(ctx context.Context, lookPath simctl.LookPath, run simctl.Runner, udid string) error {
	if _, err := lookPath(simctl.Binary); err != nil {
		return fmt.Errorf("%w: %s not found on PATH", simctl.ErrUnavailable, simctl.Binary)
	}
	out, err := run(ctx, simctl.Binary, "simctl", "delete", udid)
	if err != nil {
		if strings.Contains(string(out), "Invalid device") {
			return nil
		}
		return fmt.Errorf("`simctl delete %s` failed: %w: %s", udid, err, simctl.Output(out))
	}
	return nil
}

func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The lease half of `ao sim`. A lease is bookkeeping held by the daemon, never
// an operation on a device: claiming one changes nothing about the simulator.
//
// What a lease can and cannot do is stated everywhere it is reported, because
// the difference matters: it keeps other AO sessions off a device, and it can
// do nothing about a human driving the same simulator from Xcode (Xcode takes
// its own exclusive lock, which AO has no way to see).

const (
	// The two reasons a device reads as unknown live in domain because the
	// desktop app's Simulator tab reports the same lease state from the daemon
	// side and must not word it differently.
	simLeaseUnknownReason  = domain.SimLeaseUnknownReason
	simLeaseNoDaemonReason = domain.SimLeaseNoDaemonReason
	// simLeaseScopeNote is the honest limit of what a claim buys.
	simLeaseScopeNote = "A lease keeps other AO sessions off this device. It cannot stop a human " +
		"driving the same simulator from Xcode - Xcode takes its own exclusive lock that AO cannot see."
)

// simLeaseClient mirrors domain.SimLease on the wire.
type simLeaseClient struct {
	UDID       string    `json:"udid"`
	SessionID  string    `json:"sessionId"`
	AcquiredAt time.Time `json:"acquiredAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// acquireSimLeaseRequest mirrors controllers.AcquireSimLeaseInput.
type acquireSimLeaseRequest struct {
	UDID       string `json:"udid"`
	TTLSeconds int    `json:"ttlSeconds,omitempty"`
}

// simLeaseResponse mirrors controllers.SimLeaseResponse.
type simLeaseResponse struct {
	Lease simLeaseClient `json:"lease"`
}

// listSimLeasesResponse mirrors controllers.ListSimLeasesResponse.
type listSimLeasesResponse struct {
	Leases []simLeaseClient `json:"leases"`
}

// simLeaseView is the per-device lease state `ao sim list` and `ao sim shot`
// report. State is only ever "held" or "unknown" - see simLeaseUnknownReason.
type simLeaseView struct {
	State      domain.SimLeaseState `json:"state"`
	Holder     string               `json:"holder,omitempty"`
	AcquiredAt *time.Time           `json:"acquiredAt,omitempty"`
	ExpiresAt  *time.Time           `json:"expiresAt,omitempty"`
	Reason     string               `json:"reason,omitempty"`
}

// simClaimResult is the `ao sim claim --json` payload.
type simClaimResult struct {
	UDID              string    `json:"udid"`
	Name              string    `json:"name"`
	Runtime           string    `json:"runtime"`
	RuntimeIdentifier string    `json:"runtimeIdentifier"`
	Holder            string    `json:"holder"`
	AcquiredAt        time.Time `json:"acquiredAt"`
	ExpiresAt         time.Time `json:"expiresAt"`
	Note              string    `json:"note"`
}

// simReleaseResult is the `ao sim release --json` payload.
type simReleaseResult struct {
	UDID     string `json:"udid"`
	Released bool   `json:"released"`
}

func newSimClaimCommand(ctx *commandContext) *cobra.Command {
	var opts struct {
		udid string
		ttl  string
		json bool
	}
	cmd := &cobra.Command{
		Use:   "claim",
		Short: "Claim a booted simulator for this session so other AO sessions keep off it",
		Long: "Claim an iOS Simulator for the current session, or renew a claim it already holds.\n\n" +
			"Two AO sessions driving one simulator interleave into a single touch: the " +
			"device has one finger and no per-caller state, so one session's release " +
			"lifts the other's, and a lost release wedges input until the device is " +
			"rebooted. A claim is what keeps that from happening.\n\n" +
			"The claim lapses on its own after --ttl (10 minutes by default) and is " +
			"released automatically when this session ends, so a crashed holder can " +
			"never keep a device forever. Claiming again renews it. " + simNeverBootsNote,
		Example: `  ao sim claim
  ao sim claim --ttl 30m
  ao sim claim --udid 00000000-0000-0000-0000-000000000000 --json`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := ctx.claimSimDevice(cmd.Context(), opts.udid, opts.ttl)
			if err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			return writeSimClaim(cmd.OutOrStdout(), result)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.udid, "udid", "", "Claim this simulator instead of the booted one")
	f.StringVar(&opts.ttl, "ttl", "", "How long to hold it (e.g. 30s, 10m, 1h). Default 10m")
	f.BoolVar(&opts.json, "json", false, "Output the claim as JSON")
	return cmd
}

func newSimReleaseCommand(ctx *commandContext) *cobra.Command {
	var opts struct {
		udid string
		json bool
	}
	cmd := &cobra.Command{
		Use:   "release",
		Short: "Release this session's claim on a simulator",
		Long: "Release the simulator this session holds, handing it back immediately.\n\n" +
			"With no --udid it releases the one device this session holds. It never " +
			"touches the simulator itself, and it cannot release someone else's claim.",
		Example: `  ao sim release
  ao sim release --udid 00000000-0000-0000-0000-000000000000`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := ctx.releaseSimDevice(cmd.Context(), opts.udid)
			if err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Released the lease on %s.\n", result.UDID)
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.udid, "udid", "", "Release this simulator instead of the one this session holds")
	f.BoolVar(&opts.json, "json", false, "Output the release as JSON")
	return cmd
}

func (c *commandContext) claimSimDevice(ctx context.Context, udid, rawTTL string) (simClaimResult, error) {
	sessionID, err := simSessionID("ao sim claim")
	if err != nil {
		return simClaimResult{}, err
	}
	ttl, err := parseSimTTL(rawTTL)
	if err != nil {
		return simClaimResult{}, err
	}
	devices, err := c.listSimDevices(ctx)
	if err != nil {
		return simClaimResult{}, err
	}
	device, err := resolveSimDevice(devices, udid)
	if err != nil {
		return simClaimResult{}, err
	}

	var res simLeaseResponse
	path := "sessions/" + url.PathEscape(sessionID) + "/sim-leases"
	body := acquireSimLeaseRequest{UDID: device.UDID, TTLSeconds: int(ttl.Seconds())}
	if err := c.postJSON(ctx, path, body, &res); err != nil {
		return simClaimResult{}, c.explainSimContention(device, err)
	}
	return simClaimResult{
		UDID:              res.Lease.UDID,
		Name:              device.Name,
		Runtime:           device.Runtime,
		RuntimeIdentifier: device.RuntimeIdentifier,
		Holder:            res.Lease.SessionID,
		AcquiredAt:        res.Lease.AcquiredAt.UTC(),
		ExpiresAt:         res.Lease.ExpiresAt.UTC(),
		Note:              simLeaseScopeNote,
	}, nil
}

func (c *commandContext) releaseSimDevice(ctx context.Context, udid string) (simReleaseResult, error) {
	sessionID, err := simSessionID("ao sim release")
	if err != nil {
		return simReleaseResult{}, err
	}
	key := domain.NormalizeSimUDID(udid)
	if key == "" {
		// No udid given: release the one device this session holds. This path
		// deliberately never calls simctl - a lease outlives the device being
		// listed, bootable or even present, and handing it back must not depend
		// on any of that.
		key, err = c.sessionHeldSimUDID(ctx, sessionID)
		if err != nil {
			return simReleaseResult{}, err
		}
	}
	path := "sessions/" + url.PathEscape(sessionID) + "/sim-leases/" + url.PathEscape(key)
	if err := c.deleteJSON(ctx, path, nil); err != nil {
		return simReleaseResult{}, err
	}
	return simReleaseResult{UDID: key, Released: true}, nil
}

// sessionHeldSimUDID finds the single device this session holds, and refuses to
// guess when there is not exactly one.
func (c *commandContext) sessionHeldSimUDID(ctx context.Context, sessionID string) (string, error) {
	leases, err := c.fetchSimLeases(ctx)
	if err != nil {
		return "", err
	}
	mine := []simLeaseClient{}
	for _, lease := range leases {
		if lease.SessionID == sessionID {
			mine = append(mine, lease)
		}
	}
	switch len(mine) {
	case 1:
		return mine[0].UDID, nil
	case 0:
		return "", errors.New("this session holds no simulator lease; run `ao sim list` to see who holds what")
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "this session holds %d simulators, so there is nothing to release by default. Re-run with one of:", len(mine))
		for _, lease := range mine {
			fmt.Fprintf(&b, "\n  ao sim release --udid %s", lease.UDID)
		}
		return "", errors.New(b.String())
	}
}

// fetchSimLeases reads every live lease from the daemon, keyed by udid.
func (c *commandContext) fetchSimLeases(ctx context.Context) (map[string]simLeaseClient, error) {
	var res listSimLeasesResponse
	if err := c.getJSON(ctx, "sim/leases", &res); err != nil {
		return nil, err
	}
	leases := make(map[string]simLeaseClient, len(res.Leases))
	for _, lease := range res.Leases {
		leases[domain.NormalizeSimUDID(lease.UDID)] = lease
	}
	return leases, nil
}

// simLeaseViews reads lease state for the read-only commands. Slice 1 shipped
// `ao sim list` and `ao sim shot` as a pure CLI with no daemon involvement, so
// an unreachable daemon must degrade to "unknown" rather than fail the command.
// It reports reachable=false when the daemon could not be asked, so callers can
// say WHY a device's state is unknown.
func (c *commandContext) simLeaseViews(ctx context.Context) (map[string]simLeaseView, bool) {
	leases, err := c.fetchSimLeases(ctx)
	if err != nil {
		return nil, false
	}
	views := make(map[string]simLeaseView, len(leases))
	for udid, lease := range leases {
		acquired, expires := lease.AcquiredAt.UTC(), lease.ExpiresAt.UTC()
		views[udid] = simLeaseView{
			State:      domain.SimLeaseHeld,
			Holder:     lease.SessionID,
			AcquiredAt: &acquired,
			ExpiresAt:  &expires,
		}
	}
	return views, true
}

// simLeaseFor answers for one device. A device absent from the map is not free,
// only unclaimed by AO - which is all AO can honestly say.
func simLeaseFor(views map[string]simLeaseView, udid string, daemonReachable bool) simLeaseView {
	if view, ok := views[domain.NormalizeSimUDID(udid)]; ok {
		return view
	}
	reason := simLeaseUnknownReason
	if !daemonReachable {
		reason = simLeaseNoDaemonReason
	}
	return simLeaseView{State: domain.SimLeaseUnknown, Reason: reason}
}

// simLeaseColumn is the LEASE cell in `ao sim list`.
func (v simLeaseView) column(now time.Time) string {
	if v.State != domain.SimLeaseHeld {
		return string(domain.SimLeaseUnknown)
	}
	return fmt.Sprintf("@%s (%s left)", v.Holder, simRemaining(v.ExpiresAt, now))
}

// captureLine is the lease line the read-only commands print (`ao sim shot` and
// `ao sim ax`). A read is read-only, so the point is never to block it: it is to
// stop an agent reading a screen and then assuming the device is its to drive.
func (v simLeaseView) captureLine(sessionID string) string {
	switch {
	case v.State != domain.SimLeaseHeld:
		return v.Reason + ". Claim it with `ao sim claim` before driving it"
	case sessionID != "" && v.Holder == sessionID:
		return fmt.Sprintf("You hold this device until %s. Release it with `ao sim release` when you are done",
			expiresLabel(v.ExpiresAt))
	default:
		return fmt.Sprintf("@%s holds this device until %s. Reading the device is fine; do NOT drive it",
			v.Holder, expiresLabel(v.ExpiresAt))
	}
}

func expiresLabel(expiresAt *time.Time) string {
	if expiresAt == nil {
		return "an unknown time"
	}
	return expiresAt.Format(time.RFC3339)
}

// simRemaining renders how long a lease has left, the way a person reads it.
func simRemaining(expiresAt *time.Time, now time.Time) string {
	if expiresAt == nil {
		return "unknown"
	}
	left := expiresAt.Sub(now)
	if left < time.Second {
		return "moments"
	}
	return left.Round(time.Second).String()
}

// parseSimTTL turns --ttl into a duration. A malformed value is CLI misuse
// (exit 2); the daemon owns the allowed range, and an empty value means "let
// the daemon apply its default" so the default lives in exactly one place.
func parseSimTTL(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil {
		return 0, usageError{fmt.Errorf("invalid --ttl %q: use a duration like 30s, 10m or 1h", raw)}
	}
	if ttl <= 0 {
		return 0, usageError{fmt.Errorf("invalid --ttl %q: it must be positive", raw)}
	}
	return ttl, nil
}

func simSessionID(command string) (string, error) {
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sessionID == "" {
		return "", usageError{fmt.Errorf("%s must run inside an AO session (AO_SESSION_ID is not set): a lease belongs to a session so it can be released when that session ends", command)}
	}
	return sessionID, nil
}

// explainSimContention turns the daemon's 409 into a refusal that names the
// device (which only the CLI knows), the holder and the time left, plus the two
// things that can actually unblock the caller. Anything else passes through.
func (c *commandContext) explainSimContention(device simDevice, err error) error {
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) || apiErr.ErrorBody.Code != "SIM_DEVICE_LEASED" {
		return err
	}
	holder, _ := apiErr.ErrorBody.Details["holder"].(string)
	if holder == "" {
		return err
	}
	left := ""
	if raw, ok := apiErr.ErrorBody.Details["expiresAt"].(string); ok {
		if expiresAt, parseErr := time.Parse(time.RFC3339, raw); parseErr == nil {
			expiresAt = expiresAt.UTC()
			left = fmt.Sprintf(" for another %s", simRemaining(&expiresAt, c.deps.Now().UTC()))
		}
	}
	return fmt.Errorf("%s is leased by @%s%s, so nothing was claimed.\n"+
		"`ao sim shot` is read-only and still works. Wait for the lease to lapse, or ask @%s to run `ao sim release`",
		device.Label(), holder, left, holder)
}

func writeSimClaim(out io.Writer, result simClaimResult) error {
	if _, err := fmt.Fprintf(out, "Claimed %s (%s, %s) for @%s until %s.\n",
		result.Name, result.Runtime, result.UDID, result.Holder, result.ExpiresAt.Format(time.RFC3339)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Note: %s\n", result.Note); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out, "It is released automatically when this session ends, or when the lease lapses. Run `ao sim release` when you are done.")
	return err
}

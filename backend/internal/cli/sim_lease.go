package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
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
	UDID        string            `json:"udid"`
	SessionID   string            `json:"sessionId"`
	AcquiredAt  time.Time         `json:"acquiredAt"`
	ExpiresAt   time.Time         `json:"expiresAt"`
	OtherDaemon *domain.SimDaemon `json:"otherDaemon,omitempty"`
}

// acquireSimLeaseRequest mirrors controllers.AcquireSimLeaseInput.
type acquireSimLeaseRequest struct {
	UDID       string `json:"udid"`
	TTLSeconds int    `json:"ttlSeconds,omitempty"`
}

// simLeaseResponse mirrors controllers.SimLeaseResponse.
type simLeaseResponse struct {
	Lease simLeaseClient  `json:"lease"`
	Trust *simTrustClient `json:"trust,omitempty"`
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
	// OtherDaemon is set when the holder is a session of another AO daemon on
	// this machine - a sandbox daemon with its own AO_DATA_DIR. Holder is then
	// that daemon's session id, which can equal one here and still be
	// somebody else.
	OtherDaemon *domain.SimDaemon `json:"otherDaemon,omitempty"`
}

// heldBy reports whether this daemon's session sessionID holds the device. A
// lease held through another daemon never counts, whatever its id says.
func (v simLeaseView) heldBy(sessionID string) bool {
	return v.State == domain.SimLeaseHeld && v.OtherDaemon == nil && sessionID != "" && v.Holder == sessionID
}

// holderLabel names the holder, and the daemon it holds through when that is
// not this one - which is where it has to be released.
func (v simLeaseView) holderLabel() string {
	return simHolderLabel(v.Holder, v.OtherDaemon)
}

func simHolderLabel(holder string, other *domain.SimDaemon) string {
	if other == nil {
		return "@" + holder
	}
	return fmt.Sprintf("@%s through %s", holder, other.Describe())
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
	// Trust is what claiming did about root CAs: a claim makes the device
	// trust this Mac's debugging-proxy CA, because a device booted from Xcode
	// never went through `ao sim boot`.
	Trust *simTrustClient `json:"trust,omitempty"`
	// Clone is the session's own device that was claimed, when it was one.
	Clone *simCloneClient `json:"clone,omitempty"`
	// State is the device's simctl state when it was claimed.
	State string `json:"state,omitempty"`
}

// simReleaseResult is the `ao sim release --json` payload.
type simReleaseResult struct {
	UDID     string `json:"udid"`
	Released bool   `json:"released"`
	// Deleted says the device itself is gone: an extra device released by
	// label.
	Deleted bool `json:"deleted,omitempty"`
}

func newSimClaimCommand(ctx *commandContext) *cobra.Command {
	var opts struct {
		udid  string
		model string
		ttl   string
		json  bool
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
			"never keep a device forever. Claiming again renews it. " + simPowerNote + "\n\n" +
			"With no --udid it claims one of this session's own devices, which AO clones " +
			"from a base the first time it is asked for: the primary one ($AO_SIM_UDID), " +
			"or with --model another model (its label defaults to the model's, e.g. " +
			"iphone-se), or with --device another device under that label - a second " +
			"iPhone for the other side of a chat. `ao sim release --device <label>` deletes " +
			"an extra device; every one is deleted when the session ends.",
		Example: `  ao sim claim
  ao sim claim --model "iPhone SE"
  ao sim claim --model "iPad Pro 11-inch" --device tablet
  ao sim claim --device advisor
  ao sim claim --ttl 30m
  ao sim claim --udid 00000000-0000-0000-0000-000000000000 --json`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			label, _ := cmd.Flags().GetString(simLabelFlag)
			if opts.udid != "" && (label != "" || opts.model != "") {
				return usageError{errors.New("--udid names a device, so it takes neither --device nor --model")}
			}
			result, err := ctx.claimSimDevice(cmd.Context(), opts.udid, label, opts.model, opts.ttl)
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
	f.StringVar(&opts.udid, "udid", "", "Claim this simulator instead of one of this session's own")
	f.StringVar(&opts.model, "model", "", `Model of the device to clone for --device, e.g. "iPhone SE" or "iPad Pro 11-inch"`)
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
			"touches the simulator itself, and it cannot release someone else's claim.\n\n" +
			"With --device it DELETES that extra device of this session now, rather than " +
			"when the session ends. The primary device is never deleted this way.",
		Example: `  ao sim release
  ao sim release --device iphone-se
  ao sim release --udid 00000000-0000-0000-0000-000000000000`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			label, _ := cmd.Flags().GetString(simLabelFlag)
			label = strings.ToLower(strings.TrimSpace(label))
			if label != "" && opts.udid != "" {
				return usageError{errors.New("--device and --udid both name a device; pass one")}
			}
			if label != "" && label != domain.SimPrimaryLabel {
				return ctx.deleteSimDevice(cmd, label, opts.json)
			}
			udid := opts.udid
			if label == domain.SimPrimaryLabel {
				primary, err := ctx.simLabelUDID(cmd.Context(), label)
				if err != nil {
					return err
				}
				udid = primary
			}
			result, err := ctx.releaseSimDevice(cmd.Context(), udid)
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

func (c *commandContext) claimSimDevice(ctx context.Context, udid, label, model, rawTTL string) (simClaimResult, error) {
	sessionID, err := simSessionID("ao sim claim")
	if err != nil {
		return simClaimResult{}, err
	}
	ttl, err := parseSimTTL(rawTTL)
	if err != nil {
		return simClaimResult{}, err
	}
	var clone *simCloneClient
	if strings.TrimSpace(udid) == "" {
		// The session's own device, made now if it has none: this is also how
		// a session spawned while a base was missing gets its primary device
		// once the base exists - or learns exactly what is missing.
		made, err := c.claimSimClone(ctx, sessionID, label, model)
		switch {
		case err == nil:
			clone, udid = &made, made.UDID
		case label != "" || model != "" || !daemonLacksSimClones(err):
			return simClaimResult{}, err
		}
	}
	devices, err := c.listSimDevices(ctx)
	if err != nil {
		return simClaimResult{}, err
	}
	var device simDevice
	if clone != nil {
		// The session's own device is claimed whether or not it is up: a
		// clone made a moment ago is always shut down, and the lease is
		// bookkeeping that does not need it running.
		device, err = ownSimDevice(devices, clone.UDID)
	} else {
		if err := c.refuseSimBase(ctx, udid); err != nil {
			return simClaimResult{}, err
		}
		device, err = resolveSimDevice(devices, udid)
	}
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
		Trust:             res.Trust,
		Clone:             clone,
		State:             device.State,
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

// refuseSimBase says a named device is a base before anything else is said
// about it - "it is not booted, boot it" would send the caller to a boot that
// is refused too. A daemon that cannot be asked leaves the lease to refuse it.
func (c *commandContext) refuseSimBase(ctx context.Context, udid string) error {
	if strings.TrimSpace(udid) == "" {
		return nil
	}
	clones, err := c.fetchSimClones(ctx)
	if err != nil {
		return nil //nolint:nilerr // the daemon's own lease check refuses a base too
	}
	for _, base := range clones.Bases {
		if base.UDID != "" && domain.NormalizeSimUDID(base.UDID) == domain.NormalizeSimUDID(udid) {
			return fmt.Errorf("%s (%s) is a base AO clones devices from, and is never driven: claim your own device with `ao sim claim`, or another model with `ao sim claim --model %q`", base.Name, base.UDID, base.Name)
		}
	}
	return nil
}

// ownSimDevice finds one of this session's devices in a listing.
func ownSimDevice(devices []simDevice, udid string) (simDevice, error) {
	for _, d := range devices {
		if domain.NormalizeSimUDID(d.UDID) == domain.NormalizeSimUDID(udid) {
			return d, nil
		}
	}
	return simDevice{}, fmt.Errorf("simulator %s is not on this machine", udid)
}

// daemonLacksSimClones is a daemon that cannot clone simulators - no Xcode on
// its machine, or a daemon older than this CLI: a plain claim then falls back
// to the booted device, as it always did. A 404 the clone route itself raised
// (an unknown session) carries its own code and is not this.
func daemonLacksSimClones(err error) bool {
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusNotImplemented ||
		apiErr.StatusCode == http.StatusNotFound && !strings.HasPrefix(apiErr.ErrorBody.Code, "SIM_")
}

// deleteSimDevice deletes one of this session's extra devices.
func (c *commandContext) deleteSimDevice(cmd *cobra.Command, label string, asJSON bool) error {
	sessionID, err := simSessionID("ao sim release --device")
	if err != nil {
		return err
	}
	removed, err := c.removeSimClone(cmd.Context(), sessionID, label)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(cmd.OutOrStdout(), simReleaseResult{UDID: removed.UDID, Released: true, Deleted: true})
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s (%s), this session's device labelled %s.\n", removed.Name, removed.UDID, removed.Label)
	return err
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
		// Another daemon's session can share this id; it is never ours.
		if lease.SessionID == sessionID && lease.OtherDaemon == nil {
			mine = append(mine, lease)
		}
	}
	if len(mine) == 1 {
		return mine[0].UDID, nil
	}
	// Holding several is ordinary now that a session has more than one
	// device; with no flag the command means the primary one, as every other
	// command does.
	if primary := domain.NormalizeSimUDID(assignedSimUDID()); primary != "" {
		for _, lease := range mine {
			if domain.NormalizeSimUDID(lease.UDID) == primary {
				return primary, nil
			}
		}
	}
	switch len(mine) {
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
			State:       domain.SimLeaseHeld,
			Holder:      lease.SessionID,
			AcquiredAt:  &acquired,
			ExpiresAt:   &expires,
			OtherDaemon: lease.OtherDaemon,
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
	if v.OtherDaemon != nil {
		where := fmt.Sprintf("data dir %s", v.OtherDaemon.DataDir)
		if v.OtherDaemon.Port > 0 {
			where = fmt.Sprintf("port %d", v.OtherDaemon.Port)
		}
		return fmt.Sprintf("@%s via other AO daemon, %s (%s left)", v.Holder, where, simRemaining(v.ExpiresAt, now))
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
	case v.heldBy(sessionID):
		return fmt.Sprintf("You hold this device until %s. Release it with `ao sim release` when you are done",
			expiresLabel(v.ExpiresAt))
	default:
		return fmt.Sprintf("%s holds this device until %s. Reading the device is fine; do NOT drive it",
			v.holderLabel(), expiresLabel(v.ExpiresAt))
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
	if other := simContentionDaemon(apiErr.ErrorBody.Details["otherDaemon"]); other != nil {
		// Held through another daemon - a sandbox daemon a worker runs with
		// its own AO_DATA_DIR. Its session is not one this daemon can name or
		// reach, so say where it is and the ways its lease ends.
		return fmt.Errorf("%s is leased by %s%s, so nothing was claimed.\n"+
			"`ao sim shot` is read-only and still works. The lease ends when @%s runs `ao sim release` in that daemon, "+
			"when it lapses, or as soon as that daemon (pid %d) exits",
			device.Label(), simHolderLabel(holder, other), left, holder, other.PID)
	}
	return fmt.Errorf("%s is leased by @%s%s, so nothing was claimed.\n"+
		"`ao sim shot` is read-only and still works. Wait for the lease to lapse, or ask @%s to run `ao sim release`",
		device.Label(), holder, left, holder)
}

// simContentionDaemon reads the 409's otherDaemon detail, or nil when the
// holder is a session of this daemon.
func simContentionDaemon(raw any) *domain.SimDaemon {
	fields, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	dataDir, _ := fields["dataDir"].(string)
	if dataDir == "" {
		return nil
	}
	pid, _ := fields["pid"].(float64)
	port, _ := fields["port"].(float64)
	return &domain.SimDaemon{DataDir: dataDir, PID: int(pid), Port: int(port)}
}

func writeSimClaim(out io.Writer, result simClaimResult) error {
	if _, err := fmt.Fprintf(out, "Claimed %s (%s, %s) for @%s until %s.\n",
		result.Name, result.Runtime, result.UDID, result.Holder, result.ExpiresAt.Format(time.RFC3339)); err != nil {
		return err
	}
	if result.State != simctl.BootedState && result.State != "" {
		boot := "ao sim boot"
		if result.Clone != nil && !result.Clone.Primary {
			boot += " --device " + result.Clone.Label
		}
		if _, err := fmt.Fprintf(out, "It is not booted (%s): `%s` powers it on.\n", result.State, boot); err != nil {
			return err
		}
	}
	if result.Clone != nil && !result.Clone.Primary {
		if _, err := fmt.Fprintf(out, "It is this session's device labelled %s, a clone of %s: pass --device %s to any `ao sim` command, or its udid to other tools.\n",
			result.Clone.Label, result.Clone.Base, result.Clone.Label); err != nil {
			return err
		}
	}
	if err := writeSimTrust(out, result.Trust); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "Note: %s\n", result.Note); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out, "It is released automatically when this session ends, or when the lease lapses. Run `ao sim release` when you are done.")
	return err
}

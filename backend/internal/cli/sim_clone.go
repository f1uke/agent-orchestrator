package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// simCloneClient mirrors controllers.SimCloneView.
type simCloneClient struct {
	UDID      string    `json:"udid"`
	SessionID string    `json:"sessionId"`
	Label     string    `json:"label"`
	Primary   bool      `json:"primary"`
	Base      string    `json:"base"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

// simBaseClient mirrors controllers.SimBaseView.
type simBaseClient struct {
	Name       string `json:"name"`
	Key        string `json:"key"`
	DeviceType string `json:"deviceType"`
	UDID       string `json:"udid,omitempty"`
	Problem    string `json:"problem,omitempty"`
}

// listSimClonesResponse mirrors controllers.ListSimClonesResponse.
type listSimClonesResponse struct {
	Clones []simCloneClient `json:"clones"`
	Bases  []simBaseClient  `json:"bases"`
}

type claimSimCloneRequest struct {
	Label string `json:"label,omitempty"`
	Model string `json:"model,omitempty"`
}

type simCloneResponse struct {
	Clone simCloneClient `json:"clone"`
}

// simLabelFlag is the persistent flag every `ao sim` command takes to name one
// of the session's devices by its label. It is --device rather than --label
// because `ao sim tap --label` already names an on-screen element.
const simLabelFlag = "device"

// simLabelOwnCommands interpret --device themselves: claim makes the device it
// names, release deletes it, udid prints it, and the debugging commands
// (sim_debug.go) act only on this session's own devices, so they have no
// --udid to be handed. Every other command is handed the labelled device's
// udid as its --udid.
var simLabelOwnCommands = map[string]bool{
	"claim": true, "release": true, "udid": true,
	"pid": true, "lldb": true, "console": true, "crashes": true,
}

// applySimLabel turns `--device X` into `--udid <X's udid>` on the command
// being run, so one lookup serves every subcommand without any of them having
// to know labels exist.
func (c *commandContext) applySimLabel(cmd *cobra.Command) error {
	label, _ := cmd.Flags().GetString(simLabelFlag)
	label = strings.TrimSpace(label)
	if label == "" || simLabelOwnCommands[cmd.Name()] {
		return nil
	}
	udidFlag := cmd.Flags().Lookup("udid")
	if udidFlag == nil {
		return usageError{fmt.Errorf("--device does not apply to `%s`: it acts on no device", cmd.CommandPath())}
	}
	if udidFlag.Changed {
		return usageError{errors.New("--device and --udid both name a device; pass one")}
	}
	udid, err := c.simLabelUDID(cmd.Context(), label)
	if err != nil {
		return err
	}
	return cmd.Flags().Set("udid", udid)
}

// simLabelUDID is the udid of one of this session's devices.
func (c *commandContext) simLabelUDID(ctx context.Context, label string) (string, error) {
	sessionID, err := simSessionID("--device")
	if err != nil {
		return "", err
	}
	label = strings.ToLower(strings.TrimSpace(label))
	clones, err := c.fetchSimClones(ctx)
	if err != nil {
		return "", err
	}
	var have []string
	for _, clone := range clones.Clones {
		if clone.SessionID != sessionID {
			continue
		}
		if clone.Label == label {
			return clone.UDID, nil
		}
		have = append(have, clone.Label)
	}
	msg := fmt.Sprintf("this session has no device labelled %q; make one with `ao sim claim --device %s` (add --model to pick the model)", label, label)
	if len(have) > 0 {
		msg += "; its devices are: " + strings.Join(have, ", ")
	}
	return "", errors.New(msg)
}

func (c *commandContext) fetchSimClones(ctx context.Context) (listSimClonesResponse, error) {
	var res listSimClonesResponse
	err := c.getJSON(ctx, "sim/clones", &res)
	return res, err
}

// claimSimClone asks the daemon for one of this session's devices, cloning a
// base for it when the session has none under that label.
func (c *commandContext) claimSimClone(ctx context.Context, sessionID, label, model string) (simCloneClient, error) {
	var res simCloneResponse
	path := "sessions/" + url.PathEscape(sessionID) + "/sim-clones"
	if err := c.postJSON(ctx, path, claimSimCloneRequest{Label: label, Model: model}, &res); err != nil {
		return simCloneClient{}, err
	}
	return res.Clone, nil
}

func (c *commandContext) removeSimClone(ctx context.Context, sessionID, label string) (simCloneClient, error) {
	var res simCloneResponse
	path := "sessions/" + url.PathEscape(sessionID) + "/sim-clones/" + url.PathEscape(label)
	if err := c.deleteJSON(ctx, path, &res); err != nil {
		return simCloneClient{}, err
	}
	return res.Clone, nil
}

func newSimUDIDCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "udid",
		Short: "Print the udid of one of this session's simulators",
		Long: "Print the udid of this session's primary simulator ($AO_SIM_UDID), or with " +
			"--device of another of its devices - for the tools that need a udid rather than " +
			"a label: `bin/flow run ... --device`, `maestro --device`, `xcodebuild -destination id=`.",
		Example: `  ao sim udid
  ao sim udid --device iphone-se
  xcodebuild -destination "id=$(ao sim udid --device advisor)" ...`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			label, _ := cmd.Flags().GetString(simLabelFlag)
			if strings.TrimSpace(label) == "" {
				if assigned := assignedSimUDID(); assigned != "" {
					_, err := fmt.Fprintln(cmd.OutOrStdout(), domain.NormalizeSimUDID(assigned))
					return err
				}
				label = domain.SimPrimaryLabel
			}
			udid, err := ctx.simLabelUDID(cmd.Context(), label)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), udid)
			return err
		},
	}
}

// simRole is the ROLE cell of `ao sim list`: what AO uses the device for.
func simRole(device simDevice, self string) string {
	switch {
	case device.Base:
		return "base"
	case device.Clone != nil && device.Clone.SessionID == self:
		return "yours: " + device.Clone.Label
	case device.Clone != nil:
		return "@" + device.Clone.SessionID + ": " + device.Clone.Label
	default:
		return "-"
	}
}

// attachClones marks bases and clones in a listing. A daemon that cannot be
// asked leaves every device unmarked, the way an unreachable daemon leaves
// leases unknown.
func (r *simListResult) attachClones(res listSimClonesResponse, ok bool) {
	if !ok {
		return
	}
	bases := map[string]bool{}
	for _, b := range res.Bases {
		if b.UDID != "" {
			bases[domain.NormalizeSimUDID(b.UDID)] = true
		}
		if b.Problem != "" {
			r.BaseProblems = append(r.BaseProblems, b.Problem)
		}
	}
	clones := map[string]simCloneClient{}
	for _, clone := range res.Clones {
		clones[domain.NormalizeSimUDID(clone.UDID)] = clone
	}
	for i := range r.Devices {
		key := domain.NormalizeSimUDID(r.Devices[i].UDID)
		r.Devices[i].Base = bases[key]
		if clone, ok := clones[key]; ok {
			r.Devices[i].Clone = &clone
		}
	}
}

package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// simDoctorCheck mirrors controllers.SimDoctorCheckView.
type simDoctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// simDoctorReport mirrors controllers.SimDoctorResponse, and is what
// `ao sim doctor --json` prints.
type simDoctorReport struct {
	Checks []simDoctorCheck `json:"checks"`
	OK     bool             `json:"ok"`
}

func newSimDoctorCommand(ctx *commandContext) *cobra.Command {
	var opts struct {
		udid   string
		app    string
		expect string
		json   bool
	}
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check, without changing anything, whether your simulator is ready to drive",
		Long: "Report whether a simulator is worth driving right now, one line per check:\n\n" +
			"  device    it exists and is booted (and is this session's own, $AO_SIM_UDID)\n" +
			"  lease     this session holds it, nobody does, or someone else does\n" +
			"  app       which build of --app is installed, and with --expect whether it is that .app\n" +
			"  proxy CA  the device trusts every root CA this project makes simulators trust\n\n" +
			"It only reads: it never boots, claims, installs, launches or trusts anything, " +
			"and each failing line names the command that fixes it. WARN is something a run " +
			"fixes by itself or that was not checked; it exits 1 only when a line is FAIL.",
		Example: `  ao sim doctor
  ao sim doctor --app com.example.MyApp
  ao sim doctor --app com.example.MyApp --expect build/Build/Products/Debug-iphonesimulator/MyApp.app
  ao sim doctor --udid 00000000-0000-0000-0000-000000000000 --json`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			app := simAppOrEnv(opts.app)
			expect := strings.TrimSpace(opts.expect)
			if expect != "" && app == "" {
				return usageError{errors.New("--expect names a build of an app, so pass --app <bundle id> (or set $AO_SIM_APP) too")}
			}
			if expect != "" {
				abs, err := filepath.Abs(expect)
				if err != nil {
					return usageError{fmt.Errorf("--expect %s: %w", expect, err)}
				}
				expect = abs
			}
			sessionID, err := simSessionID("ao sim doctor")
			if err != nil {
				return err
			}
			q := url.Values{}
			for key, value := range map[string]string{"udid": strings.TrimSpace(opts.udid), "app": app, "expect": expect} {
				if value != "" {
					q.Set(key, value)
				}
			}
			path := "sessions/" + url.PathEscape(sessionID) + "/sim-doctor"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			var report simDoctorReport
			if err := ctx.getJSON(cmd.Context(), path, &report); err != nil {
				return err
			}
			if opts.json {
				err = writeJSON(cmd.OutOrStdout(), report)
			} else {
				err = writeSimDoctor(cmd.OutOrStdout(), report)
			}
			if err != nil {
				return err
			}
			if !report.OK {
				return fmt.Errorf("sim doctor: %d check(s) failed", report.failures())
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.udid, "udid", "", "Check this simulator instead of this session's own")
	f.StringVar(&opts.app, "app", "", "Check the installed build of this bundle id ($AO_SIM_APP pins it)")
	f.StringVar(&opts.expect, "expect", "", "Path to the .app the installed build must be (needs --app)")
	f.BoolVar(&opts.json, "json", false, "Output the report as JSON")
	return cmd
}

func (r simDoctorReport) failures() int {
	n := 0
	for _, c := range r.Checks {
		if c.Status == "FAIL" {
			n++
		}
	}
	return n
}

// writeSimDoctor prints one line per check in the shape the scripts store's
// `bin/flow doctor` prints its own, so the two read as one report.
func writeSimDoctor(w io.Writer, r simDoctorReport) error {
	for _, c := range r.Checks {
		if _, err := fmt.Fprintf(w, "%-4s %s: %s\n", c.Status, c.Name, c.Message); err != nil {
			return err
		}
	}
	return nil
}

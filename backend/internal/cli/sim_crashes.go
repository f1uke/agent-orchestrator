package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/simcrash"
)

const defaultSimCrashLimit = 10

// simCrashRow is one report in `ao sim crashes --json`.
type simCrashRow struct {
	Index       int    `json:"index"`
	Time        string `json:"time"`
	App         string `json:"app"`
	BundleID    string `json:"bundleId"`
	Version     string `json:"version"`
	Build       string `json:"build"`
	Exception   string `json:"exception"`
	Signal      string `json:"signal"`
	Termination string `json:"termination"`
	Path        string `json:"path"`
}

// simCrashList is the `ao sim crashes --json` payload.
type simCrashList struct {
	UDID     string        `json:"udid"`
	Name     string        `json:"name"`
	BundleID string        `json:"bundleId,omitempty"`
	Dir      string        `json:"dir"`
	Total    int           `json:"total"`
	Reports  []simCrashRow `json:"reports"`
}

const simCrashTimeLayout = "2006-01-02 15:04:05 -0700"

func newSimCrashesCommand(ctx *commandContext) *cobra.Command {
	var opts struct {
		show  int
		limit int
		json  bool
	}
	cmd := &cobra.Command{
		Use:   "crashes [bundle-id]",
		Short: "List and read crash reports of apps on this session's own simulator",
		Long: "List the crash reports apps on this session's own simulator left on this Mac - its primary " +
			"one, or with --device one it claimed - newest first.\n\n" +
			"A simulator app's crash lands in the Mac's ~/Library/Logs/DiagnosticReports among every other " +
			"app's and every other simulator's; only reports from this device are listed. With a bundle id, " +
			"or $AO_SIM_APP, only that app's; otherwise every app on the device.\n\n" +
			"--show N prints report N (1 is the newest) readably: the exception, the termination, the " +
			"app-specific information, the last exception " +
			"backtrace for an NSException, and the crashed thread's frames - then the .ips path for the " +
			"full report.\n\n" +
			"Two things measured on a simulator: a report is written about 30 s after the app dies, and it " +
			"does not carry a Swift fatalError's message - `ao sim log --grep \"Fatal error\"`, or " +
			"`ao sim console` after `ao sim launch --console`, has that.",
		Example: `  ao sim crashes
  ao sim crashes com.example.MyApp --show 1
  ao sim crashes --limit 3 --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.limit <= 0 {
				return usageError{fmt.Errorf("--limit must be positive, got %d", opts.limit)}
			}
			if opts.show < 0 {
				return usageError{fmt.Errorf("--show takes a report number from the list (1 is the newest), got %d", opts.show)}
			}
			device, err := ctx.ownSimDevice(cmd.Context(), cmd, "`ao sim crashes`")
			if err != nil {
				return err
			}
			bundleID := simAppOrEnv(firstArg(args))
			dir, err := simcrash.DefaultDir()
			if err != nil {
				return err
			}
			reports, err := simcrash.List(dir, device.UDID, bundleID)
			if err != nil {
				return fmt.Errorf("read crash reports in %s: %w", dir, err)
			}
			out := cmd.OutOrStdout()
			if opts.show > 0 {
				if opts.show > len(reports) {
					return usageError{fmt.Errorf("--show %d: there are %d crash reports of %s on %s", opts.show, len(reports), crashScope(bundleID), device.Name)}
				}
				report := reports[opts.show-1]
				if opts.json {
					return writeJSON(out, report)
				}
				return writeSimCrash(out, opts.show, report)
			}
			list := simCrashList{UDID: device.UDID, Name: device.Name, BundleID: bundleID, Dir: dir, Total: len(reports), Reports: []simCrashRow{}}
			for i, r := range reports {
				if i == opts.limit {
					break
				}
				list.Reports = append(list.Reports, simCrashRowOf(i+1, r))
			}
			if opts.json {
				return writeJSON(out, list)
			}
			return writeSimCrashList(out, list)
		},
	}
	f := cmd.Flags()
	f.IntVar(&opts.show, "show", 0, "Print report N from the list (1 is the newest) readably")
	f.IntVar(&opts.limit, "limit", defaultSimCrashLimit, "List at most this many of the newest reports")
	f.BoolVar(&opts.json, "json", false, "Output the list, or with --show the report, as JSON")
	return cmd
}

func crashScope(bundleID string) string {
	if bundleID == "" {
		return "any app"
	}
	return bundleID
}

func simCrashRowOf(index int, r simcrash.Report) simCrashRow {
	return simCrashRow{
		Index: index, Time: r.Time.Format(simCrashTimeLayout), App: r.Header.AppName, BundleID: r.Header.BundleID,
		Version: r.Header.AppVersion, Build: r.Header.BuildVersion,
		Exception: r.Body.Exception.Type, Signal: r.Body.Exception.Signal, Termination: r.Body.Termination.Indicator,
		Path: r.Path,
	}
}

const (
	// simCrashLateNote and simCrashMessageNote are what a real simulator crash
	// taught: the report arrived about 30 s after the app died, and it held no
	// app-specific information, while the fatalError message was in the log.
	simCrashLateNote    = "A report is written about 30 s after the app dies: if it just crashed, run this again in a minute."
	simCrashMessageNote = "a simulator report does not carry a Swift fatalError or precondition message; " +
		"`ao sim log --grep \"Fatal error\"` (or `ao sim console` after `ao sim launch --console`) has it"
)

func writeSimCrashList(out io.Writer, list simCrashList) error {
	if list.Total == 0 {
		_, err := fmt.Fprintf(out, "No crash reports of %s on %s (%s) in %s.\n%s\n", crashScope(list.BundleID), list.Name, list.UDID, list.Dir, simCrashLateNote)
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "#\tTIME\tAPP\tEXCEPTION\tTERMINATION\tREPORT"); err != nil {
		return err
	}
	for _, r := range list.Reports {
		if _, err := fmt.Fprintf(tw, "%d\t%s\t%s %s (%s)\t%s\t%s\t%s\n", r.Index, r.Time, r.App, r.Version, r.Build,
			strings.TrimSpace(r.Exception+" ("+r.Signal+")"), r.Termination, r.Path); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	shown := fmt.Sprintf("%d crash report%s", list.Total, pluralS(list.Total))
	if len(list.Reports) < list.Total {
		shown = fmt.Sprintf("%d of %d crash reports (the newest; --limit shows more)", len(list.Reports), list.Total)
	}
	_, err := fmt.Fprintf(out, "\n%s of %s on %s. `ao sim crashes --show N` reads one.\n", shown, crashScope(list.BundleID), list.Name)
	return err
}

func writeSimCrash(out io.Writer, index int, r simcrash.Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "#%d  %s  %s %s (%s)  %s\n", index, r.Time.Format(simCrashTimeLayout), r.Header.AppName,
		r.Header.AppVersion, r.Header.BuildVersion, r.Header.BundleID)
	if r.Unreadable != "" {
		fmt.Fprintf(&b, "The report body could not be read (%s); open the file for what it holds.\n", r.Unreadable)
	} else {
		fmt.Fprintf(&b, "Exception:   %s (%s)\n", r.Body.Exception.Type, r.Body.Exception.Signal)
		if r.Body.Termination.Indicator != "" {
			fmt.Fprintf(&b, "Termination: %s (by %s)\n", r.Body.Termination.Indicator, r.Body.Termination.ByProc)
		}
		asi := r.ASILines()
		for _, line := range asi {
			fmt.Fprintf(&b, "App info:    %s\n", line)
		}
		if len(asi) == 0 {
			fmt.Fprintf(&b, "App info:    none - %s\n", simCrashMessageNote)
		}
		if len(r.Body.LastExceptionBacktrace) > 0 {
			b.WriteString("\nLast exception backtrace:\n")
			for i, f := range r.Body.LastExceptionBacktrace {
				fmt.Fprintf(&b, "  %s\n", r.FrameLine(i, f))
			}
		}
		if thread, ok := r.CrashedThread(); ok {
			name := thread.Name
			if name == "" {
				name = thread.Queue
			}
			fmt.Fprintf(&b, "\nCrashed thread %d", r.Body.FaultingThread)
			if name != "" {
				fmt.Fprintf(&b, " (%s)", name)
			}
			b.WriteString(":\n")
			for i, f := range thread.Frames {
				fmt.Fprintf(&b, "  %s\n", r.FrameLine(i, f))
			}
		}
	}
	fmt.Fprintf(&b, "\nFull report: %s\n", r.Path)
	_, err := io.WriteString(out, b.String())
	return err
}

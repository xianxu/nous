// nous service audit: read-only inspection of the substrate's audit
// logs (charon proxy requests + brain-sync operations). Tails or filters
// over existing log files; never collects new state.
//
// Today: simple tail/filter over the two log files. Future: structured
// query (per-peer-PID, per-error-class) once nous#17 lands the
// last-errors view.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// Log paths follow the launchd convention used by lib/service and
// lib/brainsync: ~/Library/Logs/<service>.log. Hardcoded here rather
// than exported as lib API — paths are stable and one-line strings
// don't earn a public surface.
func charonLogPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "charon.log")
}

func brainSyncLogPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "brain-sync.log")
}

func newServiceAuditCmd() *cobra.Command {
	var n int
	var grep string
	var which string

	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Tail/filter the substrate's audit logs",
		Long: `Read-only view of the launchd-managed log files for charon (proxy
requests) and brain-sync (sync operations). The default prints the
last 50 lines from each, interleaved by service header.

Flags:
  -n N         lines per log (default 50)
  --grep TEXT  substring filter (case-sensitive)
  --which W    'all' | 'charon' | 'brain-sync' (default 'all')

Read-only: audit never modifies the logs. The log files are the
substrate's source of truth — same path the launchd plists write to,
nothing aggregated or duplicated. See ~/Library/Logs/{charon,brain-sync}.log.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			switch which {
			case "all", "":
				if err := dumpLog(out, "charon", charonLogPath(), n, grep); err != nil {
					fmt.Fprintf(out, "  (charon log: %v)\n", err)
				}
				fmt.Fprintln(out)
				if err := dumpLog(out, "brain-sync", brainSyncLogPath(), n, grep); err != nil {
					fmt.Fprintf(out, "  (brain-sync log: %v)\n", err)
				}
			case "charon":
				return dumpLog(out, "charon", charonLogPath(), n, grep)
			case "brain-sync":
				return dumpLog(out, "brain-sync", brainSyncLogPath(), n, grep)
			default:
				return fmt.Errorf("--which must be 'all', 'charon', or 'brain-sync' (got %q)", which)
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&n, "lines", "n", 50, "lines per log")
	cmd.Flags().StringVar(&grep, "grep", "", "substring filter (case-sensitive)")
	cmd.Flags().StringVar(&which, "which", "all", "'all' | 'charon' | 'brain-sync'")
	return cmd
}

// dumpLog tails the last n lines of path, optionally filtered by
// substring. Implemented as full-file scan with a sliding window
// because audit logs are small (kilobytes per day) and the fast path
// of tail-from-EOF would be a premature optimization.
func dumpLog(w io.Writer, label, path string, n int, grep string) error {
	fmt.Fprintf(w, "── %s (%s) ──\n", label, path)
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Sliding-window tail. For a 50-line tail of a 100k-line log we
	// scan the whole file once — that's 5MB at typical line widths,
	// trivial. If the logs ever balloon (unlikely; substrate runs
	// quiet) reading from EOF backwards is the upgrade.
	window := make([]string, 0, n)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if grep != "" && !strings.Contains(line, grep) {
			continue
		}
		if len(window) < n {
			window = append(window, line)
			continue
		}
		copy(window, window[1:])
		window[n-1] = line
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(window) == 0 {
		fmt.Fprintln(w, "  (no matching lines)")
		return nil
	}
	for _, line := range window {
		fmt.Fprintln(w, line)
	}
	return nil
}

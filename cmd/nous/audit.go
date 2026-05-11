// nous service audit: read-only tail/filter over the substrate's
// audit log (~/Library/Logs/nous.log). Both proxy and brain-sync
// goroutines write to a single stderr stream that launchd captures
// to that file (see lib/service/service.go::NewUnified).
//
// Future: structured query (per-peer-PID, per-error-class) once
// nous#17 lands the last-errors view.
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

// nousLogPath follows the launchd convention used by lib/service:
// ~/Library/Logs/<service>.log. Hardcoded here rather than exported
// as lib API — a one-line path doesn't earn a public surface.
func nousLogPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "nous.log")
}

func newServiceAuditCmd() *cobra.Command {
	var n int
	var grep string

	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Tail/filter the substrate's audit log",
		Long: `Read-only view of the launchd-managed log file for the unified
com.42shots.nous service (both proxy requests and brain-sync
operations write to one stderr stream captured at the path below).
Default prints the last 50 lines.

Flags:
  -n N         lines (default 50)
  --grep TEXT  substring filter (case-sensitive)

Read-only: audit never modifies the log. The log file is the
substrate's source of truth — same path the launchd plist writes
to, nothing aggregated or duplicated. See ~/Library/Logs/nous.log.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			if err := dumpLog(out, "nous", nousLogPath(), n, grep); err != nil {
				fmt.Fprintf(out, "  (nous log: %v)\n", err)
				return nil
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&n, "lines", "n", 50, "lines")
	cmd.Flags().StringVar(&grep, "grep", "", "substring filter (case-sensitive)")
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

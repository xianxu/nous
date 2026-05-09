package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/xianxu/nous/internal/charon/proxy"
)

// whoCmd: list recent connections, grouped by caller exe.
//
// `charon who` (no flag): defaults to last 5 minutes, focused on
// "what's on right now."
// `charon who --since 1h`: replay the last hour for forensics.
//
// Source of truth: the proxy's in-memory ring (#16 F) — wiped on
// `charon serve` restart. Persistent forensics is the file-backed
// audit log (--audit-log on serve), not this command.
func whoCmd() *cobra.Command {
	var since time.Duration
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "who",
		Short: "Show recent connections, grouped by caller process",
		Long: `Lists recent proxied requests in the last --since window
(default 5 minutes), grouped by the caller's executable path.

Source: the proxy's in-memory ring — entries since 'charon serve'
last restarted. For longer-term forensics use --audit-log on serve
and grep the file directly.

Examples:
  charon who                       # last 5 min, default human format
  charon who --since 1h            # last hour
  charon who --json | jq           # raw entries`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := fetchAuditRecent(resolveAddr(cmd), since)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if jsonOut {
				return writeJSON(out, entries)
			}
			renderWho(out, entries, since)
			return nil
		},
	}
	cmd.Flags().DurationVar(&since, "since", 5*time.Minute, "lookback window (e.g. 1m, 1h)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit raw audit entries as JSON")
	return cmd
}

// statsCmd: aggregate (caller exe, host) tuples to call counts +
// items + bytes. Useful for "what's been talking to my Gmail today."
func statsCmd() *cobra.Command {
	var since time.Duration
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Aggregate proxy traffic by (caller exe, host) over a window",
		Long: `Reads the proxy's in-memory audit ring and aggregates by
(caller_exe, host) tuple: number of requests, total items returned
(when the response was JSON), bytes in/out.

Examples:
  charon stats --since 1h
  charon stats --since 24h --json | jq`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := fetchAuditRecent(resolveAddr(cmd), since)
			if err != nil {
				return err
			}
			rows := aggregateStats(entries)
			out := cmd.OutOrStdout()
			if jsonOut {
				return writeJSON(out, rows)
			}
			renderStats(out, rows, since)
			return nil
		},
	}
	cmd.Flags().DurationVar(&since, "since", 1*time.Hour, "lookback window (e.g. 1h, 24h)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit aggregated rows as JSON")
	return cmd
}

func fetchAuditRecent(addr string, since time.Duration) ([]proxy.AuditEntry, error) {
	q := url.Values{}
	q.Set("since", since.String())
	u := fmt.Sprintf("http://%s/audit/recent?%s", addr, q.Encode())
	resp, err := http.Get(u)
	if err != nil {
		return nil, fmt.Errorf("proxy not reachable at %s — is 'charon serve' running?", addr)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusServiceUnavailable {
		return nil, fmt.Errorf("audit endpoint not configured on this charon — older binary?")
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("audit endpoint returned %d: %s", resp.StatusCode, body)
	}
	var entries []proxy.AuditEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode audit entries: %w", err)
	}
	return entries, nil
}

func renderWho(w io.Writer, entries []proxy.AuditEntry, since time.Duration) {
	if len(entries) == 0 {
		fmt.Fprintf(w, "No proxied requests in the last %s.\n", since)
		return
	}
	// Group by caller exe; "(unknown)" for entries with no peer info.
	byExe := map[string][]proxy.AuditEntry{}
	for _, e := range entries {
		key := e.PeerExe
		if key == "" {
			key = "(unknown)"
		}
		byExe[key] = append(byExe[key], e)
	}
	exes := make([]string, 0, len(byExe))
	for k := range byExe {
		exes = append(exes, k)
	}
	sort.Slice(exes, func(i, j int) bool { return len(byExe[exes[i]]) > len(byExe[exes[j]]) })

	fmt.Fprintf(w, "%s of activity, %d requests, %d caller(s):\n\n", since, len(entries), len(exes))
	for _, exe := range exes {
		group := byExe[exe]
		// Distinct hosts for the line.
		hosts := map[string]int{}
		errCount := 0
		statusCounts := map[int]int{}
		pids := map[int]struct{}{}
		for _, e := range group {
			hosts[e.Host]++
			statusCounts[e.StatusCode]++
			if e.PeerPID != 0 {
				pids[e.PeerPID] = struct{}{}
			}
			if e.Error != "" {
				errCount++
			}
		}
		hostList := topHostsLine(hosts, 3)
		fmt.Fprintf(w, "  %d req  %s%s  →  %s",
			len(group), shortExe(exe), pidLabel(pids), hostList)
		if errCount > 0 {
			fmt.Fprintf(w, "  [%d errors]", errCount)
		}
		// Surface non-200 statuses so the user knows requests
		// aren't reaching upstream cleanly.
		if non2xx := nonSuccessSummary(statusCounts); non2xx != "" {
			fmt.Fprintf(w, "  %s", non2xx)
		}
		fmt.Fprintln(w)
		// Show one example error message per group so the user
		// can act on it without grepping the full audit log.
		for _, e := range group {
			if e.Error != "" {
				fmt.Fprintf(w, "      ↳ %s\n", e.Error)
				break
			}
		}
	}
}

// pidLabel renders " (pid N)" for a single pid, " (N pids)" for many,
// "" when the group has no peer info. Avoids the previous misleading
// "shows pid of first entry" behavior.
func pidLabel(pids map[int]struct{}) string {
	switch len(pids) {
	case 0:
		return ""
	case 1:
		for p := range pids {
			return fmt.Sprintf(" (pid %d)", p)
		}
	}
	return fmt.Sprintf(" (%d pids)", len(pids))
}

// nonSuccessSummary returns "(2 status=407, 1 status=429)" when any
// statuses are non-2xx; "" otherwise. status=0 is treated as "no
// upstream" — typically charon's own short-circuits.
func nonSuccessSummary(counts map[int]int) string {
	type kv struct {
		k, v int
	}
	var bad []kv
	for k, v := range counts {
		if k == 0 || k >= 400 {
			bad = append(bad, kv{k, v})
		}
	}
	if len(bad) == 0 {
		return ""
	}
	sort.Slice(bad, func(i, j int) bool { return bad[i].v > bad[j].v })
	parts := make([]string, len(bad))
	for i, b := range bad {
		if b.k == 0 {
			parts[i] = fmt.Sprintf("%d charon-blocked", b.v)
		} else {
			parts[i] = fmt.Sprintf("%d status=%d", b.v, b.k)
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// statRow is the per-(exe, host) aggregate in stats output.
type statRow struct {
	Exe       string `json:"exe"`
	Host      string `json:"host"`
	Calls     int    `json:"calls"`
	Items     int    `json:"items"`
	ReqBytes  int64  `json:"req_bytes"`
	RespBytes int64  `json:"resp_bytes"`
}

func aggregateStats(entries []proxy.AuditEntry) []statRow {
	type key struct{ exe, host string }
	m := map[key]*statRow{}
	for _, e := range entries {
		exe := e.PeerExe
		if exe == "" {
			exe = "(unknown)"
		}
		k := key{exe, e.Host}
		row, ok := m[k]
		if !ok {
			row = &statRow{Exe: exe, Host: e.Host}
			m[k] = row
		}
		row.Calls++
		if e.ItemsReturned != nil {
			row.Items += *e.ItemsReturned
		}
		row.ReqBytes += e.ReqBytes
		row.RespBytes += e.RespBytes
	}
	rows := make([]statRow, 0, len(m))
	for _, r := range m {
		rows = append(rows, *r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Calls != rows[j].Calls {
			return rows[i].Calls > rows[j].Calls
		}
		return rows[i].Exe < rows[j].Exe
	})
	return rows
}

func renderStats(w io.Writer, rows []statRow, since time.Duration) {
	if len(rows) == 0 {
		fmt.Fprintf(w, "No proxied requests in the last %s.\n", since)
		return
	}
	fmt.Fprintf(w, "Aggregated over %s:\n\n", since)
	fmt.Fprintf(w, "  %-8s %-10s %-12s %-12s %-50s %s\n", "calls", "items", "req-bytes", "resp-bytes", "exe", "host")
	for _, r := range rows {
		fmt.Fprintf(w, "  %-8d %-10d %-12s %-12s %-50s %s\n",
			r.Calls, r.Items,
			humanBytes(r.ReqBytes), humanBytes(r.RespBytes),
			truncate(shortExe(r.Exe), 50), r.Host)
	}
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// shortExe returns the basename of an exe path; full path on the
// "(unknown)" case so the marker is readable.
func shortExe(exe string) string {
	if exe == "" || exe == "(unknown)" {
		return "(unknown)"
	}
	if i := strings.LastIndex(exe, "/"); i >= 0 {
		return exe[i+1:]
	}
	return exe
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

// topHostsLine renders "host (n), host (n), …" capped at maxHosts.
func topHostsLine(hosts map[string]int, maxHosts int) string {
	type kv struct {
		k string
		v int
	}
	pairs := make([]kv, 0, len(hosts))
	for k, v := range hosts {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].v > pairs[j].v })
	if len(pairs) > maxHosts {
		pairs = pairs[:maxHosts]
	}
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = fmt.Sprintf("%s (%d)", p.k, p.v)
	}
	return strings.Join(parts, ", ")
}

// humanBytes renders a byte count with K/M/G suffix. Approximate;
// human-readable beats exact for stats summaries.
func humanBytes(n int64) string {
	const k, m, g = 1 << 10, 1 << 20, 1 << 30
	switch {
	case n >= g:
		return fmt.Sprintf("%.1fG", float64(n)/float64(g))
	case n >= m:
		return fmt.Sprintf("%.1fM", float64(n)/float64(m))
	case n >= k:
		return fmt.Sprintf("%.1fK", float64(n)/float64(k))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

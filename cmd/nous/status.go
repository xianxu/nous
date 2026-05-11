// nous status — aggregated runtime view across the daemon's surfaces.
//
// Three lines today (M2 of nous#20):
//   - Service: launchd state of com.42shots.nous (installed / running)
//   - Proxy:   reachable on /healthz
//   - Session: armed / disarmed via /session/status
//
// Future expansion (separate issue): brain repo health, identity (gpg
// agent), sync activity recency. Kept lean here so this command is
// the first thing an operator runs and immediately understands.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/xianxu/nous/lib/charoncli"
	"github.com/xianxu/nous/lib/service"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show runtime status: service + proxy + session (h, a)",
		Long: `Aggregated runtime view. Three lines:

  Service: <launchd state of com.42shots.nous>
  Proxy:   <ok | unreachable> on <addr>
  Session: <armed | disarmed | unknown>

For deeper introspection of the launchd plist alone (legacy plists,
install path, etc.) see ` + "`nous service status`" + `.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			mgr, err := service.NewUnified()
			if err != nil {
				fmt.Fprintf(out, "Service: error (%v)\n", err)
			} else {
				svcStatus, svcErr := mgr.Status()
				line := strings.SplitN(strings.TrimSpace(svcStatus), "\n", 2)[0]
				if svcErr != nil {
					fmt.Fprintf(out, "Service: %s (query error: %v)\n", line, svcErr)
				} else {
					fmt.Fprintf(out, "Service: %s\n", line)
				}
			}

			addr := defaultProxyAddr
			proxyURL := fmt.Sprintf("http://%s/healthz", addr)
			resp, err := http.Get(proxyURL)
			if err != nil {
				fmt.Fprintf(out, "Proxy:   unreachable on %s\n", addr)
				fmt.Fprintf(out, "Session: unknown (proxy unreachable)\n")
				return nil
			}
			defer resp.Body.Close()
			var health struct {
				Status string `json:"status"`
				Addr   string `json:"addr"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&health)
			if health.Addr != "" {
				addr = health.Addr
			}
			fmt.Fprintf(out, "Proxy:   %s on %s\n", health.Status, addr)
			fmt.Fprintln(out, charoncli.RenderSessionStatus(addr))
			return nil
		},
	}
}

// defaultProxyAddr is the compile-time default the daemon listens on.
// Mirrors lib/charoncli's listenAddr default; not flag-overridable here
// since `nous status` is a quick check, not a flexible proxy client.
// If the operator ran the daemon on a custom --addr, `nous service
// status` + curl is the path.
const defaultProxyAddr = "127.0.0.1:8230"

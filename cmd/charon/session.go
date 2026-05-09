package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// armCmd: POST /session/arm with an optional --ttl. Today the path is
// CLI-only (#16 A); #16 D will route the same shape through Charon
// Security.app's menubar over the unix socket added by #16 C.
func armCmd() *cobra.Command {
	var ttl time.Duration
	cmd := &cobra.Command{
		Use:   "arm",
		Short: "Arm the proxy: allow CONNECT requests for the configured TTL",
		Long: `Arms the proxy session so HTTPS_PROXY traffic flows. Until armed,
charon refuses CONNECTs with a 407 — the runtime-consent gate that
prevents an idle agent from quietly using long-lived OAuth grants
while you're not at the keyboard.

Default TTL: 1 hour. Maximum: 8 hours regardless of --ttl. After 30
minutes of zero traffic the session auto-disarms regardless of the
absolute cap.

Examples:
  charon arm                  # 1 hour
  charon arm --ttl 30m
  charon arm --ttl 8h         # max`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]int64{"ttl_seconds": int64(ttl.Seconds())}
			out, status, err := postSessionEndpoint(resolveAddr(cmd), "/session/arm", body)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), formatSessionResp(out, status, "armed"))
			return nil
		},
	}
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "session lifetime (e.g. 30m, 1h, 8h). zero uses default 1h")
	return cmd
}

func disarmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disarm",
		Short: "Disarm the proxy: refuse new CONNECTs immediately",
		Long: `Drops the proxy's armed state. New CONNECT requests are refused
with 407. In-flight tunnels drain (agents handle TCP RST poorly,
and once a tunnel is up nothing prevents the next request anyway).

To re-allow traffic, run 'charon arm' or use Charon Security.app's
menubar.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, status, err := postSessionEndpoint(resolveAddr(cmd), "/session/disarm", nil)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), formatSessionResp(out, status, "disarmed"))
			return nil
		},
	}
}

// postSessionEndpoint POSTs JSON to /session/<endpoint> on the proxy
// and returns the response body + status. Wraps the unreachable-proxy
// case as a friendly error.
func postSessionEndpoint(addr, path string, body any) (map[string]any, int, error) {
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("encode request: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}
	url := fmt.Sprintf("http://%s%s", addr, path)
	resp, err := http.Post(url, "application/json", bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("proxy not reachable at %s — is 'charon serve' running?", addr)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("non-JSON response (status %d): %s", resp.StatusCode, raw)
	}
	return parsed, resp.StatusCode, nil
}

// formatSessionResp renders the proxy's arm/disarm response into a
// short human-readable line. Falls back to the raw JSON on any
// shape surprise rather than crashing.
func formatSessionResp(out map[string]any, status int, verb string) string {
	if status >= 400 {
		raw, _ := json.Marshal(out)
		return fmt.Sprintf("error (status %d): %s", status, raw)
	}
	statusBlock, _ := out["status"].(map[string]any)
	armed, _ := statusBlock["armed"].(bool)
	if verb == "armed" && armed {
		expires, _ := statusBlock["expires_at"].(string)
		reason, _ := statusBlock["expires_reason"].(string)
		if expires != "" {
			return fmt.Sprintf("✓ armed; expires %s (%s)", expires, reason)
		}
		return "✓ armed"
	}
	if verb == "disarmed" && !armed {
		return "✓ disarmed"
	}
	raw, _ := json.Marshal(out)
	return string(raw)
}

// renderSessionStatus is used by the extended `charon status` to
// surface armed/disarmed + expiry. Reads from /session/status; the
// healthz call still happens separately.
func renderSessionStatus(addr string) string {
	url := fmt.Sprintf("http://%s/session/status", addr)
	resp, err := http.Get(url)
	if err != nil {
		return "Session: unknown (proxy unreachable)"
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusServiceUnavailable {
		return "Session: not configured (proxy lacks runtime-consent state — older binary?)"
	}
	if resp.StatusCode != 200 {
		return fmt.Sprintf("Session: error (status %d)", resp.StatusCode)
	}
	var st struct {
		Armed         bool      `json:"armed"`
		ExpiresAt     time.Time `json:"expires_at"`
		ExpiresReason string    `json:"expires_reason"`
		TTLRemaining  int64     `json:"ttl_remaining_ns"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return "Session: unparseable response"
	}
	if !st.Armed {
		return "Session: disarmed (run 'charon arm' or click Charon Security.app's menubar dot)"
	}
	rem := time.Duration(st.TTLRemaining).Truncate(time.Second)
	return fmt.Sprintf("Session: armed; %s remaining (%s timer expires %s)",
		rem, st.ExpiresReason, st.ExpiresAt.Format(time.RFC3339))
}

// extendStatusOutput is exported so statusCmd can append session info
// to its existing output without growing main.go further.
func extendStatusOutput(addr string) string {
	return strings.TrimRight(renderSessionStatus(addr), "\n")
}

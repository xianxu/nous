package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/xianxu/nous/internal/charon/oauth"
	"github.com/xianxu/nous/internal/charon/providers/catalog"
	"github.com/xianxu/nous/internal/charon/providers/gcp"
	"github.com/xianxu/nous/internal/charon/providers/openai"
	"github.com/xianxu/nous/internal/charon/proxy"
	charonruntime "github.com/xianxu/nous/internal/charon/runtime"
	"github.com/xianxu/nous/internal/charon/service"
	"github.com/xianxu/nous/internal/charon/tui"
	"github.com/xianxu/nous/internal/charon/vault"
	"github.com/xianxu/nous/internal/charon/vault/keychain"
)

// defaultListenAddr is the compile-time default proxy listen address —
// the value cobra falls back to when --addr isn't given. Surfaced in
// `charon manifest` as proxy.default so agents can see "this is where
// charon would listen if started cleanly" regardless of current state.
const defaultListenAddr = "127.0.0.1:8230"

var (
	listenAddr string
	auditPath  string
	verbose    bool
)

func main() {
	root := &cobra.Command{
		Use:   "charon",
		Short: "Credential proxy for AI agents",
		Long:  "Charon is a credential proxy that injects OAuth tokens into HTTPS requests, keeping tokens invisible to AI agents.",
	}

	root.PersistentFlags().StringVar(&listenAddr, "addr", defaultListenAddr, "proxy listen address")

	root.AddCommand(serveCmd())
	root.AddCommand(runCmd())
	root.AddCommand(authCmd())
	root.AddCommand(manifestCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(serviceCmd())
	root.AddCommand(vaultCmd())
	root.AddCommand(scopesCmd())
	root.AddCommand(gcpCmd())
	root.AddCommand(instructionsCmd())
	root.AddCommand(armCmd())
	root.AddCommand(disarmCmd())
	root.AddCommand(whoCmd())
	root.AddCommand(statsCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func newVault() vault.Store {
	return keychain.New()
}

func serveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTPS credential proxy",
		RunE: func(cmd *cobra.Command, args []string) error {
			cat, err := catalog.Load()
			if err != nil {
				return fmt.Errorf("failed to load provider catalog: %w", err)
			}
			catalog.Register(cat)
			log.Printf("catalog: registered %d Tier-3 provider(s)", len(cat.Entries))

			ca, err := proxy.LoadOrCreateCA()
			if err != nil {
				return fmt.Errorf("failed to init CA: %w", err)
			}
			log.Printf("CA loaded from keychain")

			bundlePath, cleanup, err := proxy.BuildCABundle(ca.CertPEM)
			if err != nil {
				return fmt.Errorf("failed to build CA bundle: %w", err)
			}
			defer cleanup()
			log.Printf("CA bundle: %s", bundlePath)

			audit, err := proxy.NewAuditLog(auditPath)
			if err != nil {
				return fmt.Errorf("failed to init audit log: %w", err)
			}
			defer audit.Close()

			refreshers := make(map[string]proxy.Refresher)
			if gp, err := oauth.NewGoogleProvider(); err == nil {
				refreshers["google"] = gp
			} else {
				log.Printf("warning: Google OAuth not available: %v", err)
			}

			srv := &proxy.Server{
				Vault:        newVault(),
				Audit:        audit,
				Addr:         listenAddr,
				CA:           ca,
				Refreshers:   refreshers,
				Verbose:      verbose,
				ScopeTracker: proxy.NewScopeTracker(100, 24*time.Hour),
				// Boots disarmed (#16 A spec). User must `charon arm`
				// or click Charon Security.app's menubar to enable
				// CONNECTs.
				Session: proxy.NewSession(),
			}

			// Publish runtime info so other CLI invocations can find
			// us without --addr. Best-effort: write failure logs but
			// doesn't abort serve. Removed on graceful shutdown
			// (signal trap below); stale files from a crash are
			// tolerated since the next serve overwrites and
			// `manifest`'s healthz probe surfaces "running: false".
			if err := charonruntime.Write(listenAddr); err != nil {
				log.Printf("warning: runtime file write failed: %v", err)
			} else {
				log.Printf("runtime file: %s", charonruntime.Path())
			}
			// Bring up the runtime-consent unix socket (#16 C). DR-
			// pinned to com.charon.security so only Charon
			// Security.app can drive arm/disarm. Best-effort: bind
			// failure logs but doesn't abort serve — the HTTP
			// /session/* endpoints still work as a fallback (and
			// are the only path until #16 D's menubar lands).
			runtimeSock, sockErr := proxy.StartRuntimeSocket(srv)
			if sockErr != nil {
				log.Printf("warning: runtime socket bind failed: %v", sockErr)
			}
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				_ = charonruntime.Remove()
				if runtimeSock != nil {
					_ = runtimeSock.Close()
				}
				os.Exit(0)
			}()
			defer charonruntime.Remove()
			defer func() {
				if runtimeSock != nil {
					_ = runtimeSock.Close()
				}
			}()

			return srv.ListenAndServe()
		},
	}
	cmd.Flags().StringVar(&auditPath, "audit-log", "", "audit log file path (default: stderr)")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "enable debug logging")
	return cmd
}

func runCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run -- <command> [args...]",
		Short: "Run a command with proxy environment configured",
		Long: `Launches a child process with HTTPS_PROXY and CA trust environment
variables set so that all HTTPS traffic is routed through Charon.

The proxy must already be running (charon serve).

Example:
  charon run -- python my_agent.py
  charon run -- curl https://gmail.googleapis.com/gmail/v1/users/me/profile

Without arguments, prints the proxy environment variables for debugging.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Strip leading "--" if present.
			if len(args) > 0 && args[0] == "--" {
				args = args[1:]
			}

			// No args: print proxy info for debugging.
			if len(args) == 0 {
				return printProxyInfo(cmd)
			}

			// Check proxy is running and fetch CA cert.
			addr := resolveAddr(cmd)
			proxyURL := fmt.Sprintf("http://%s", addr)
			resp, err := http.Get(proxyURL + "/ca.pem")
			if err != nil {
				return fmt.Errorf("proxy not reachable at %s — is 'charon serve' running?\n  %w", addr, err)
			}
			caPEM, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 || len(caPEM) == 0 {
				return fmt.Errorf("could not fetch CA cert from proxy")
			}

			// Build ephemeral CA bundle.
			bundlePath, _, err := proxy.BuildCABundle(caPEM)
			if err != nil {
				return fmt.Errorf("failed to build CA bundle: %w", err)
			}
			// No cleanup — syscall.Exec replaces this process, OS cleans up on exit.
			// The temp dir will be cleaned on next boot or by OS temp cleanup.
			caPath := proxy.CAPathFromBundle(bundlePath)

			// Resolve command path.
			binary, err := exec.LookPath(args[0])
			if err != nil {
				return fmt.Errorf("command not found: %s", args[0])
			}

			// Build environment with proxy and CA trust vars.
			env := os.Environ()
			// Set BOTH uppercase and lowercase proxy env vars. Go
			// net/http, curl, Python urllib/requests, and many other
			// clients check the lowercase variant — sometimes prefer
			// it. Setting only uppercase leaves a stale ambient
			// `https_proxy=...` from the parent shell intact, and
			// requests silently route to the wrong proxy with no
			// auth injection. Belt-and-suspenders: set all four.
			env = setEnv(env, "HTTPS_PROXY", proxyURL)
			env = setEnv(env, "HTTP_PROXY", proxyURL)
			env = setEnv(env, "https_proxy", proxyURL)
			env = setEnv(env, "http_proxy", proxyURL)
			env = setEnv(env, "SSL_CERT_FILE", bundlePath)
			env = setEnv(env, "REQUESTS_CA_BUNDLE", bundlePath)                // Python requests
			env = setEnv(env, "CURL_CA_BUNDLE", bundlePath)                    // curl
			env = setEnv(env, "NODE_EXTRA_CA_CERTS", caPath)                   // Node.js (additive)
			env = setEnv(env, "GRPC_DEFAULT_SSL_ROOTS_FILE_PATH", bundlePath)  // gRPC

			fmt.Fprintf(os.Stderr, "charon: proxying through %s\n", addr)

			// Exec replaces this process with the child.
			return syscall.Exec(binary, args, env)
		},
	}
	return cmd
}

func printProxyInfo(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	addr := resolveAddr(cmd)
	proxyURL := fmt.Sprintf("http://%s", addr)

	// Check if proxy is running.
	resp, err := http.Get(proxyURL + "/healthz")
	if err != nil {
		fmt.Fprintf(out, "Proxy: not running (cannot reach %s)\n", addr)
		return nil
	}
	resp.Body.Close()

	fmt.Fprintf(out, "Proxy: %s\n", proxyURL)
	fmt.Fprintf(out, "\nEnvironment variables set by 'charon run':\n")
	fmt.Fprintf(out, "  HTTPS_PROXY=%s    (also lowercase https_proxy)\n", proxyURL)
	fmt.Fprintf(out, "  HTTP_PROXY=%s     (also lowercase http_proxy)\n", proxyURL)
	fmt.Fprintf(out, "  SSL_CERT_FILE=<temp>/ca-bundle.pem\n")
	fmt.Fprintf(out, "  REQUESTS_CA_BUNDLE=<temp>/ca-bundle.pem\n")
	fmt.Fprintf(out, "  CURL_CA_BUNDLE=<temp>/ca-bundle.pem\n")
	fmt.Fprintf(out, "  NODE_EXTRA_CA_CERTS=<temp>/ca.pem\n")
	fmt.Fprintf(out, "  GRPC_DEFAULT_SSL_ROOTS_FILE_PATH=<temp>/ca-bundle.pem\n")
	fmt.Fprintf(out, "\nUsage:\n")
	fmt.Fprintf(out, "  charon run -- <command> [args...]\n")
	fmt.Fprintf(out, "\nExamples:\n")
	fmt.Fprintf(out, "  charon run -- curl -s https://gmail.googleapis.com/gmail/v1/users/me/profile\n")
	fmt.Fprintf(out, "  charon run -- python my_agent.py\n")
	fmt.Fprintf(out, "  charon run -- gmail search \"from:alice subject:invoice\"\n")
	return nil
}

func serviceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage charon as an OS service",
	}
	cmd.AddCommand(serviceInstallCmd())
	cmd.AddCommand(serviceUninstallCmd())
	cmd.AddCommand(serviceStartCmd())
	cmd.AddCommand(serviceStopCmd())
	cmd.AddCommand(serviceStatusCmd())
	return cmd
}

func serviceInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install charon as a system service (starts on login)",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := service.New()
			if err != nil {
				return err
			}
			binary, err := os.Executable()
			if err != nil {
				return fmt.Errorf("could not determine binary path: %w", err)
			}
			serveArgs := []string{"serve"}
			if listenAddr != "127.0.0.1:8230" {
				serveArgs = append(serveArgs, "--addr", listenAddr)
			}
			if err := mgr.Install(binary, serveArgs); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Service installed and started.\n")
			return nil
		},
	}
}

func serviceUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall charon system service",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := service.New()
			if err != nil {
				return err
			}
			if err := mgr.Uninstall(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Service uninstalled.\n")
			return nil
		},
	}
}

func serviceStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the charon service",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := service.New()
			if err != nil {
				return err
			}
			if err := mgr.Start(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Service started.\n")
			return nil
		},
	}
}

func serviceStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the charon service",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := service.New()
			if err != nil {
				return err
			}
			if err := mgr.Stop(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Service stopped.\n")
			return nil
		},
	}
}

func serviceStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show charon service status",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := service.New()
			if err != nil {
				return err
			}
			status, err := mgr.Status()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Service: %s\n", status)
			return nil
		},
	}
}

func authCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "auth",
		Short: "Manage credentials via the scope-management TUI",
		Long: `Launches an interactive TUI for managing OAuth credentials.

The TUI shows existing accounts, lets you grant or revoke individual
scopes, and walks you through OAuth for new accounts. Replaces the
older 'auth google ...' subcommands (scopes, grant, fix).

Headless removal: 'charon vault delete --provider X --account Y'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			gp, err := oauth.NewGoogleProvider()
			if err != nil {
				return fmt.Errorf("init google provider: %w", err)
			}
			gp.Output = io.Discard // suppress oauth status prints inside TUI
			// Admin-key providers wired here. Currently only OpenAI —
			// Anthropic was demoted to the catalog (Tier 3) flow because
			// their Admin API can't create new keys programmatically
			// (only list / deactivate / update). The internal/providers/
			// anthropic package stays in the tree for future use by the
			// catalog flow's optional revoke pathway. See charon#13 Log.
			openaiProv := openai.New()
			v := newVault()
			gcpFactory := func(account string) (tui.GCPSetupClient, error) {
				supplier := tokenSupplierFromVault(v, gp, "google", account)
				return gcp.New(supplier), nil
			}
			return tui.Run(v, "", resolveAddr(cmd), gp, gcpFactory, openaiProv)
		},
	}
}

// setEnv sets a key=value in an env slice, replacing if exists.
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// notifyProxyCacheClear tells a running proxy to clear its credential cache.
// Best-effort — if proxy isn't running, silently ignored. addr is the
// resolved proxy address; pass empty to use listenAddr verbatim (callers
// without a *cobra.Command in scope).
func notifyProxyCacheClear(addr string) {
	if addr == "" {
		addr = listenAddr
	}
	resp, err := http.Post(fmt.Sprintf("http://%s/cache/clear", addr), "", nil)
	if err != nil {
		return // proxy not running, fine
	}
	resp.Body.Close()
}

// resolveAddr decides which proxy address to talk to. Order:
//  1. Explicit --addr flag wins.
//  2. Otherwise, read the runtime discovery file (written by
//     `charon serve` on startup) and use whatever's there.
//  3. Otherwise fall back to the compile-time default (which is
//     also the value of listenAddr when the flag wasn't passed).
//
// Step 2 means a `charon serve --addr 127.0.0.1:9000` followed by
// a plain `charon manifest` works without the user specifying
// --addr twice.
func resolveAddr(cmd *cobra.Command) string {
	if cmd.Flags().Changed("addr") {
		return listenAddr
	}
	info, err := charonruntime.Read()
	if err == nil && info != nil && info.Addr != "" {
		return info.Addr
	}
	return listenAddr
}

// manifestCmd returns everything an agent needs to use charon: where the
// proxy listens, where to fetch the CA cert, and the set of accounts plus
// each account's granted scopes. Single-shot snapshot, JSON-shaped.
func manifestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "manifest",
		Short: "Print full manifest: proxy address + accounts with granted scopes (JSON)",
		Long: `Outputs everything an agent needs to use charon as one JSON object:

  {
    "proxy": {
      "default":    "127.0.0.1:8230",
      "running":    true,
      "addr":       "127.0.0.1:8230",
      "url":        "http://127.0.0.1:8230",
      "ca_pem_url": "http://127.0.0.1:8230/ca.pem"
    },
    "permissions": {
      "google": {
        "user@gmail.com": {
          "scopes":    ["openid", "https://...userinfo.email", ...],
          "vertex":    {"project_id": "...", "region": "us-central1"},
          "ai-studio": {}
        }
      }
    }
  }

Strict agent-facing shape: only fields the caller needs to make
calls. Internal metadata (project display names, billing flags,
key UIDs, timestamps) is not surfaced. Presence/absence of vertex
and ai-studio keys signals which Gemini paths are available for
the account; ai-studio is empty by design because the proxy
attaches the key transparently.

Loading per-credential data triggers one keychain access per account,
which may prompt for permission on the first access.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload, err := manifestPayload(newVault(), resolveAddr(cmd))
			if err != nil {
				return err
			}
			b, err := json.MarshalIndent(payload, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		},
	}
}

// manifestPayload composes the manifest JSON object. Vault read for
// permissions; HTTP probe for proxy reachability.
//
// When the proxy is reachable, the proxy section includes
// {default, running, addr, url, ca_pem_url} so the caller has
// everything to make requests. When the proxy is down, only
// {default, running} are emitted — the addr/url/ca_pem_url would
// point at nothing, so surfacing them would be actively misleading.
// `default` is always present as a hint for "this is where charon
// would listen if started cleanly".
func manifestPayload(v vault.Store, addr string) (map[string]any, error) {
	perms, err := permissionsPayload(v)
	if err != nil {
		return nil, err
	}
	proxy := map[string]any{
		"default": defaultListenAddr,
		"running": false,
	}
	if proxyReachable(addr) {
		url := fmt.Sprintf("http://%s", addr)
		proxy["running"] = true
		proxy["addr"] = addr
		proxy["url"] = url
		proxy["ca_pem_url"] = url + "/ca.pem"
	}
	return map[string]any{
		"proxy":       proxy,
		"permissions": perms,
	}, nil
}

// proxyReachable does a quick HTTP GET to the proxy's healthz endpoint
// and returns true on a 200 response, false otherwise. Short timeout
// because charon-on-localhost should respond in milliseconds; if it
// doesn't, it's effectively down. Errors are squashed — for an agent
// reading manifest, "running: false" is the actionable signal, not
// the error class.
func proxyReachable(addr string) bool {
	client := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://%s/healthz", addr))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// AccountPermissions is the per-account value in the manifest's
// permissions section. Strict agent-facing shape: only fields a
// caller needs to make an API call.
//
// - Scopes: always present (empty slice when none granted).
//   Drives X-Charon-Scope declaration.
// - Vertex: present only when the account has a GCP project
//   configured. Just project_id + region — exactly what's needed
//   to construct a Vertex URL.
// - AIStudio: present (as an empty object) when an AI Studio key
//   is minted. Empty by design: charon's proxy attaches the key
//   transparently, so agents need no metadata. The presence of
//   the field is the signal that the path is available.
//
// Fields that exist in the vault but agents don't need (project
// display names, billing flags, key UIDs, timestamps, etc.) are
// deliberately not surfaced.
type AccountPermissions struct {
	Scopes   []string             `json:"scopes"`
	Vertex   *VertexManifestRef   `json:"vertex,omitempty"`
	AIStudio *AIStudioManifestRef `json:"ai-studio,omitempty"`
}

// VertexManifestRef is the agent-facing shape of GCP project info.
// Just the two fields needed to construct a Vertex URL:
//   https://{region}-aiplatform.googleapis.com
//     /v1/projects/{project_id}/locations/{region}/...
type VertexManifestRef struct {
	ProjectID string `json:"project_id"`
	Region    string `json:"region"`
}

// AIStudioManifestRef carries the bare minimum an agent needs to
// reason about AI Studio calls: which project the key was minted
// under (drives quota/billing inquiries when calls hit
// RESOURCE_EXHAUSTED or BILLING_DISABLED). The actual KeyMaterial
// is intentionally omitted — the proxy attaches it transparently;
// agents must not see secrets. Internal-only fields (uid,
// display_name, created_at) are also omitted to keep the surface
// minimal.
type AIStudioManifestRef struct {
	ProjectID string `json:"project_id"`
}

func vertexManifestRef(d *vault.GCPData) *VertexManifestRef {
	if d == nil || d.ProjectID == "" {
		return nil
	}
	return &VertexManifestRef{
		ProjectID: d.ProjectID,
		Region:    d.VertexRegion,
	}
}

func aiStudioManifestRef(d *vault.AIStudioData) *AIStudioManifestRef {
	if d == nil {
		return nil
	}
	return &AIStudioManifestRef{ProjectID: d.ProjectID}
}

// permissionsPayload returns granted scopes (and GCP metadata when
// present) keyed by provider then account — the shape used in
// `charon manifest`'s permissions section. nil-scope credentials
// normalize to an empty slice so JSON renders [] not null.
// Per-credential read failures (e.g. keychain ACL denied for one
// entry) are skipped so a partial snapshot is still returned.
func permissionsPayload(v vault.Store) (map[string]map[string]AccountPermissions, error) {
	summaries, err := v.List()
	if err != nil {
		return nil, err
	}
	byProvider := map[string]map[string]AccountPermissions{}
	for _, c := range summaries {
		cred, err := v.Get(c.Provider, c.Account)
		if err != nil {
			continue
		}
		if _, ok := byProvider[c.Provider]; !ok {
			byProvider[c.Provider] = map[string]AccountPermissions{}
		}
		scopes := cred.Scopes
		if scopes == nil {
			scopes = []string{}
		}
		byProvider[c.Provider][c.Account] = AccountPermissions{
			Scopes:   scopes,
			Vertex:   vertexManifestRef(cred.GCP),
			AIStudio: aiStudioManifestRef(cred.AIStudio),
		}
	}
	return byProvider, nil
}

// providerCatalogs maps each supported OAuth provider to its scope
// catalog. Update when a new provider is wired up.
var providerCatalogs = map[string][]oauth.ScopeInfo{
	"google": oauth.GoogleScopeCatalog,
}

func scopesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scopes",
		Short: "Print scope catalogs for all supported providers (JSON)",
		Long: `Outputs a JSON object keyed by provider name, with each provider's
full scope catalog (short names, full URLs, descriptions,
required-flag).

Intended for agent introspection. The output is just the catalog
(what's possible) — not what any specific account has granted.
Agents declare intent via X-Charon-Scope and let charon's 407 response
signal what's missing for the user to grant.

Examples:
  charon scopes                              # full snapshot, all providers
  charon scopes | jq 'keys'                  # list providers
  charon scopes | jq '.google[] | select(.short | startswith("gmail"))'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := json.MarshalIndent(providerCatalogs, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show proxy status",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			addr := resolveAddr(cmd)
			proxyURL := fmt.Sprintf("http://%s/healthz", addr)
			resp, err := http.Get(proxyURL)
			if err != nil {
				fmt.Fprintf(out, "Proxy: not running (cannot reach %s)\n", addr)
				return nil
			}
			defer resp.Body.Close()

			var health struct {
				Status string `json:"status"`
				Addr   string `json:"addr"`
			}
			json.NewDecoder(resp.Body).Decode(&health)
			fmt.Fprintf(out, "Proxy: %s on %s\n", health.Status, health.Addr)
			fmt.Fprintf(out, "CA: stored in keychain (service: charon)\n")
			fmt.Fprintln(out, extendStatusOutput(addr))
			return nil
		},
	}
}

func vaultCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vault",
		Short: "Manage credentials in the vault",
	}
	cmd.AddCommand(vaultSetCmd())
	cmd.AddCommand(vaultDeleteCmd())
	return cmd
}

func vaultSetCmd() *cobra.Command {
	var provider, account, token, ttl, credType string
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Manually store a credential (for testing)",
		Long: `Store a credential directly in the vault. Used for testing
and bootstrap; the production path for catalog providers is the TUI
add-account flow (#15 M4).

  --type oauth   (default) reads --token; stores as flat AccessToken
  --type catalog reads --token; stores as cred.Catalog.KeyMaterial
                 (the format used by Tier-3 paste-and-revoke providers)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if provider == "" || account == "" || token == "" {
				return fmt.Errorf("--provider, --account, and --token are required")
			}
			cred := &vault.Credential{
				Provider: provider,
				Account:  account,
			}
			switch credType {
			case "", "oauth":
				cred.AccessToken = token
				if ttl != "" {
					d, err := time.ParseDuration(ttl)
					if err != nil {
						return fmt.Errorf("invalid --ttl: %w", err)
					}
					cred.Expiry = time.Now().Add(d)
				}
			case "catalog":
				if ttl != "" {
					return fmt.Errorf("--ttl not supported for --type catalog (catalog keys are static)")
				}
				cred.Type = vault.TypeCatalog
				cred.Catalog = &vault.CatalogData{
					KeyMaterial: token,
					AddedAt:     time.Now(),
				}
			default:
				return fmt.Errorf("--type %q must be one of {oauth, catalog}", credType)
			}
			v := newVault()
			if err := v.Set(cred); err != nil {
				return err
			}
			notifyProxyCacheClear(resolveAddr(cmd))
			fmt.Fprintf(cmd.OutOrStdout(), "Stored %s credential for %s/%s\n", cred.CredType(), provider, account)
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "credential provider (e.g. google, anthropic)")
	cmd.Flags().StringVar(&account, "account", "", "account identifier (e.g. user@gmail.com, personal)")
	cmd.Flags().StringVar(&token, "token", "", "token / API key value")
	cmd.Flags().StringVar(&ttl, "ttl", "", "token time-to-live (e.g. 1h, 30m, 3600s). omit for no expiry. oauth only")
	cmd.Flags().StringVar(&credType, "type", "oauth", "credential type: oauth or catalog")
	return cmd
}

func vaultDeleteCmd() *cobra.Command {
	var provider, account string
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Remove a credential from the vault",
		RunE: func(cmd *cobra.Command, args []string) error {
			if provider == "" || account == "" {
				return fmt.Errorf("--provider and --account are required")
			}
			v := newVault()
			if err := v.Delete(provider, account); err != nil {
				return err
			}
			notifyProxyCacheClear(resolveAddr(cmd))
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted credential for %s/%s\n", provider, account)
			return nil
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "credential provider")
	cmd.Flags().StringVar(&account, "account", "", "account identifier")
	return cmd
}


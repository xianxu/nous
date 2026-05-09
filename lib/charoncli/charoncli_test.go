package charoncli

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/xianxu/nous/lib/provider/proxy"
)

// executeCmd runs a cobra command with args and returns stdout, stderr, and error.
func executeCmd(root *cobra.Command, args ...string) (stdout, stderr string, err error) {
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	root.SetOut(outBuf)
	root.SetErr(errBuf)
	root.SetArgs(args)
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

// buildRoot creates a fresh root command for testing (avoids global state).
func buildRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "charon",
		Short: "Credential proxy for AI agents",
	}
	root.PersistentFlags().StringVar(&listenAddr, "addr", "127.0.0.1:8230", "proxy listen address")
	root.AddCommand(ServeCmd())
	root.AddCommand(RunCmd())
	root.AddCommand(ManifestCmd())
	root.AddCommand(StatusCmd())
	root.AddCommand(VaultCmd())
	return root
}

// startTestProxy starts a proxy server on a dynamic port and returns its address.
// The proxy is stopped when the test completes.
func startTestProxy(t *testing.T) (addr string, bundlePath string) {
	t.Helper()

	// Generate a test CA (don't hit real keychain).
	ca, err := proxy.NewTestCA()
	if err != nil {
		t.Fatalf("failed to create CA: %v", err)
	}
	bundlePath, cleanup, err := proxy.BuildCABundle(ca.CertPEM)
	if err != nil {
		t.Fatalf("failed to build CA bundle: %v", err)
	}
	t.Cleanup(cleanup)

	audit := proxy.NopAuditLog()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	addr = ln.Addr().String()

	srv := &proxy.Server{
		Vault: newVault(),
		Audit: audit,
		Addr:  addr,
		CA:    ca,
	}
	httpSrv := &http.Server{Handler: srv}
	go httpSrv.Serve(ln)
	t.Cleanup(func() { httpSrv.Close() })

	return addr, bundlePath
}

func TestRootHelp(t *testing.T) {
	root := buildRoot()
	stdout, _, err := executeCmd(root, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"serve", "run", "manifest", "status", "vault"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("root help missing subcommand %q", want)
		}
	}
}

func TestServeHelp(t *testing.T) {
	root := buildRoot()
	stdout, _, err := executeCmd(root, "serve", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--audit-log", "--addr", "--verbose"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("serve help missing flag %q", want)
		}
	}
}

func TestServeDefaultAddr(t *testing.T) {
	root := buildRoot()
	stdout, _, err := executeCmd(root, "serve", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "127.0.0.1:8230") {
		t.Error("serve help should show default addr 127.0.0.1:8230")
	}
}

func TestRunHelp(t *testing.T) {
	root := buildRoot()
	stdout, _, err := executeCmd(root, "run", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"HTTPS_PROXY", "charon serve", "python", "curl"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("run help missing %q", want)
		}
	}
}

func TestRunNoArgsPrintsProxyInfo(t *testing.T) {
	addr, _ := startTestProxy(t)
	root := buildRoot()
	stdout, _, err := executeCmd(root, "--addr", addr, "run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "HTTPS_PROXY") {
		t.Errorf("expected proxy info output, got: %q", stdout)
	}
}

// `charon run` must override BOTH uppercase and lowercase proxy
// env vars from the parent. Tools like Go net/http and Python
// urllib check lowercase too — leaving an ambient `https_proxy`
// from the parent shell intact silently routes traffic to the
// wrong proxy with no auth injection. Regression for the
// localhost:58767 ambient-proxy debug session.
func TestRunSetsBothCasesOfProxyVars(t *testing.T) {
	parent := []string{
		"HOME=/tmp",
		"HTTPS_PROXY=http://stale-uppercase:1111",
		"https_proxy=http://stale-lowercase:2222",
		"HTTP_PROXY=http://stale-uppercase-http:3333",
		"http_proxy=http://stale-lowercase-http:4444",
	}
	const proxyURL = "http://127.0.0.1:8230"
	env := setEnv(parent, "HTTPS_PROXY", proxyURL)
	env = setEnv(env, "HTTP_PROXY", proxyURL)
	env = setEnv(env, "https_proxy", proxyURL)
	env = setEnv(env, "http_proxy", proxyURL)

	want := map[string]string{
		"HTTPS_PROXY":  proxyURL,
		"https_proxy":  proxyURL,
		"HTTP_PROXY":   proxyURL,
		"http_proxy":   proxyURL,
	}
	got := map[string]string{}
	for _, kv := range env {
		eq := strings.Index(kv, "=")
		if eq < 0 {
			continue
		}
		got[kv[:eq]] = kv[eq+1:]
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("env[%q] = %q, want %q (ambient parent value should be overridden)", k, got[k], v)
		}
	}
}

func TestRunHelp_DocumentsLowercaseProxyVars(t *testing.T) {
	addr, _ := startTestProxy(t)
	root := buildRoot()
	stdout, _, err := executeCmd(root, "--addr", addr, "run")
	if err != nil {
		t.Fatal(err)
	}
	// The 'charon run' info output should name both cases so users
	// know to look for ambient lowercase mismatches.
	for _, want := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("run info missing %q\n%s", want, stdout)
		}
	}
}

func TestRunRequiresProxy(t *testing.T) {
	root := buildRoot()
	_, _, err := executeCmd(root, "--addr", "127.0.0.1:19999", "run", "--", "echo", "hi")
	if err == nil {
		t.Error("expected error when proxy not running")
	}
	if err != nil && !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("expected 'not reachable' error, got: %v", err)
	}
}


func TestVaultSetHelp(t *testing.T) {
	root := buildRoot()
	stdout, _, err := executeCmd(root, "vault", "set", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--provider", "--account", "--token", "--ttl"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("vault set help missing flag %q", want)
		}
	}
}

func TestVaultSetRequiresAllFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"missing all", []string{"vault", "set"}},
		{"missing account and token", []string{"vault", "set", "--provider", "google"}},
		{"missing token", []string{"vault", "set", "--provider", "google", "--account", "user@gmail.com"}},
		{"missing provider", []string{"vault", "set", "--account", "user@gmail.com", "--token", "tok"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := buildRoot()
			_, _, err := executeCmd(root, tt.args...)
			if err == nil {
				t.Error("expected error for missing flags")
			}
		})
	}
}

func TestVaultDeleteHelp(t *testing.T) {
	root := buildRoot()
	stdout, _, err := executeCmd(root, "vault", "delete", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--provider", "--account"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("vault delete help missing flag %q", want)
		}
	}
}

func TestVaultDeleteRequiresFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"missing all", []string{"vault", "delete"}},
		{"missing account", []string{"vault", "delete", "--provider", "google"}},
		{"missing provider", []string{"vault", "delete", "--account", "user@gmail.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := buildRoot()
			_, _, err := executeCmd(root, tt.args...)
			if err == nil {
				t.Error("expected error for missing flags")
			}
		})
	}
}

func TestStatusWhenProxyNotRunning(t *testing.T) {
	root := buildRoot()
	stdout, _, err := executeCmd(root, "--addr", "127.0.0.1:19998", "status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "not running") {
		t.Errorf("expected 'not running' message, got: %q", stdout)
	}
}

func TestStatusWhenProxyRunning(t *testing.T) {
	addr, _ := startTestProxy(t)

	root := buildRoot()
	stdout, _, err := executeCmd(root, "--addr", addr, "status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "ok") {
		t.Errorf("expected 'ok' in status, got: %q", stdout)
	}
}

func TestServeCreatesCABundle(t *testing.T) {
	_, bundlePath := startTestProxy(t)

	// Bundle should exist and contain PEM data.
	info, err := os.Stat(bundlePath)
	if err != nil {
		t.Fatalf("expected CA bundle at %s: %v", bundlePath, err)
	}
	if info.Size() == 0 {
		t.Error("CA bundle is empty")
	}

	// ca.pem should also be in the same temp dir.
	caPath := proxy.CAPathFromBundle(bundlePath)
	if _, err := os.Stat(caPath); err != nil {
		t.Errorf("expected ca.pem at %s: %v", caPath, err)
	}
}

func TestServeHealthEndpointJSON(t *testing.T) {
	addr, _ := startTestProxy(t)

	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var health struct {
		Status string `json:"status"`
		Addr   string `json:"addr"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if health.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", health.Status)
	}
	if health.Addr != addr {
		t.Errorf("expected addr %q, got %q", addr, health.Addr)
	}
}


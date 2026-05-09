package charoncli

import (
	"encoding/json"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/xianxu/nous/lib/provider/vault"
	"github.com/xianxu/nous/lib/provider/vault/memory"
)

// proxySection extracts the proxy block as map[string]any. Manifest
// is heterogeneous (string fields + a bool `running`), so we can't
// type-assert to map[string]string anymore.
func proxySection(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	p, ok := m["proxy"].(map[string]any)
	if !ok {
		t.Fatalf("proxy section wrong type: %T", m["proxy"])
	}
	return p
}

// Helper: spin up a real httptest server bound to 127.0.0.1 with a
// /healthz route, return its addr (host:port) and a cleanup. Lets us
// test "running: true" without needing a full proxy stack.
func liveProxyAddr(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

func TestManifestPayload_RunningTrue_FullConnectionInfo(t *testing.T) {
	addr := liveProxyAddr(t)
	got, err := manifestPayload(fixtureVault(t), addr)
	if err != nil {
		t.Fatalf("manifestPayload: %v", err)
	}
	proxy := proxySection(t, got)
	if proxy["running"] != true {
		t.Fatalf("running = %v, want true", proxy["running"])
	}
	for _, field := range []string{"default", "addr", "url", "ca_pem_url"} {
		if proxy[field] == nil {
			t.Errorf("expected %q present when running, missing", field)
		}
	}
	if proxy["addr"] != addr {
		t.Errorf("addr = %v, want %v", proxy["addr"], addr)
	}
	if proxy["default"] != DefaultListenAddr {
		t.Errorf("default = %v, want %s", proxy["default"], DefaultListenAddr)
	}
}

// When the proxy is down, the manifest must not surface addr/url/
// ca_pem_url for it — those would point at nothing and mislead the
// caller. Only `default` and `running:false` are emitted.
func TestManifestPayload_RunningFalse_OmitsConnectionInfo(t *testing.T) {
	// 127.0.0.1:1 is reserved (tcpmux) and almost never bound. If
	// it ever is, this test would falsely pass with running=true —
	// acceptable risk.
	got, err := manifestPayload(fixtureVault(t), "127.0.0.1:1")
	if err != nil {
		t.Fatalf("manifestPayload: %v", err)
	}
	proxy := proxySection(t, got)
	if proxy["running"] != false {
		t.Fatalf("running = %v, want false", proxy["running"])
	}
	if proxy["default"] != DefaultListenAddr {
		t.Errorf("default should still be present when down, got %v", proxy["default"])
	}
	for _, field := range []string{"addr", "url", "ca_pem_url"} {
		if _, ok := proxy[field]; ok {
			t.Errorf("expected %q absent when running=false, got %v", field, proxy[field])
		}
	}
}

// Manifest's permissions section must equal what permissionsPayload returns —
// the manifest is a wrapper, not a re-implementation.
func TestManifestPayload_PermissionsMatchesHelper(t *testing.T) {
	v := fixtureVault(t)
	manifest, err := manifestPayload(v, "127.0.0.1:8230")
	if err != nil {
		t.Fatalf("manifestPayload: %v", err)
	}
	perms, err := permissionsPayload(v)
	if err != nil {
		t.Fatalf("permissionsPayload: %v", err)
	}
	if !reflect.DeepEqual(manifest["permissions"], perms) {
		t.Errorf("manifest.permissions != permissionsPayload\n manifest=%v\n helper=%v",
			manifest["permissions"], perms)
	}
}

func TestManifestPayload_RoundTripsThroughJSON(t *testing.T) {
	addr := liveProxyAddr(t)
	got, err := manifestPayload(fixtureVault(t), addr)
	if err != nil {
		t.Fatalf("manifestPayload: %v", err)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		`"default":"127.0.0.1:8230"`,
		`"running":true`,
		`"alice@gmail.com"`,
		`"scopes":[`,
		`"https://www.googleapis.com/auth/gmail.readonly"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest JSON missing %q\n%s", want, s)
		}
	}
}

// JSON shape with vertex set: agent reads "scopes" and "vertex"
// siblings under each account; vertex is omitted when absent.
func TestManifestPayload_VertexInJSON(t *testing.T) {
	v := memory.New()
	v.Set(&vault.Credential{
		Provider: "google",
		Account:  "alice@gmail.com",
		Scopes:   []string{"https://www.googleapis.com/auth/cloud-platform"},
		GCP: &vault.GCPData{
			ProjectID:    "alice-charon",
			VertexRegion: "us-central1",
		},
	})
	v.Set(&vault.Credential{
		Provider: "google",
		Account:  "no-gcp@gmail.com",
		Scopes:   []string{"openid"},
	})
	got, err := manifestPayload(v, "127.0.0.1:8230")
	if err != nil {
		t.Fatalf("manifestPayload: %v", err)
	}
	b, _ := json.Marshal(got)
	s := string(b)

	for _, want := range []string{
		`"vertex":{"project_id":"alice-charon","region":"us-central1"}`,
		`"scopes":["https://www.googleapis.com/auth/cloud-platform"]`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("alice manifest fragment missing %q\n%s", want, s)
		}
	}
	// no-gcp account must omit the vertex key entirely (omitempty).
	if !strings.Contains(s, `"no-gcp@gmail.com":{"scopes":["openid"]}`) {
		t.Errorf("no-gcp account should not include vertex key:\n%s", s)
	}
}

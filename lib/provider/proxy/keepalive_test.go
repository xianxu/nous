package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/xianxu/nous/lib/provider/vault"
	"github.com/xianxu/nous/lib/provider/vault/memory"
)

// TestKeepAlive_MultipleRequestsOneHandshake verifies that multiple HTTPS requests
// to the same host reuse a single CONNECT tunnel (one TLS handshake).
//
// Why this matters: an agent fetching 100 emails should not pay the TLS handshake
// cost 100 times. The CONNECT handler's for-loop must keep reading requests after
// writing each response, not return early.
//
// Common ways to break this:
//   - Adding a bare `return` after resp.Write in handleConnect
//   - Setting `Connection: close` on the proxied response unconditionally
//   - Breaking the response framing (missing Content-Length or chunked encoding)
//     so the client can't tell where the body ends and waits for connection close
func TestKeepAlive_MultipleRequestsOneHandshake(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("Authorization"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	hostname := upstreamURL.Hostname()
	HostToProvider[hostname] = &Provider{Name: "test", Auth: AuthBearer}
	defer delete(HostToProvider, hostname)

	store := memory.New()
	_ = store.Set(&vault.Credential{Provider: "test", Account: "user", AccessToken: "tok"})

	ca := testCA(t)
	srv := &Server{
		Vault: store,
		Audit: NopAuditLog(),
		CA:    ca,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	caPool := x509.NewCertPool()
	caPool.AddCert(ca.Cert)

	var connectCount atomic.Int64
	wrapper := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			connectCount.Add(1)
		}
		srv.ServeHTTP(w, r)
	})

	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxyLn.Close()
	go http.Serve(proxyLn, wrapper)

	proxyURL, _ := url.Parse("http://" + proxyLn.Addr().String())
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{
				RootCAs: caPool,
			},
		},
	}

	// Make 5 requests to the same host.
	for i := 0; i < 5; i++ {
		resp, err := client.Get("https://" + upstreamURL.Host + "/test")
		if err != nil {
			t.Fatalf("request %d failed: %v", i+1, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "Bearer tok" {
			t.Fatalf("request %d: got %q, want 'Bearer tok'", i+1, body)
		}
	}

	if c := connectCount.Load(); c != 1 {
		t.Errorf("expected 1 CONNECT (keep-alive) for 5 requests, got %d", c)
	}
}

// TestKeepAlive_DifferentHostsSeparateConnects verifies that requests to different
// hosts open separate CONNECT tunnels (one per host). This is the expected behavior —
// each host needs its own TLS interception certificate.
//
// This is the counterpart to TestKeepAlive_MultipleRequestsOneHandshake: it ensures
// we're not accidentally sharing tunnels across hosts, which would be a security bug
// (wrong credential injected).
func TestKeepAlive_DifferentHostsSeparateConnects(t *testing.T) {
	upstream1 := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "s1")
	}))
	defer upstream1.Close()

	upstream2 := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "s2")
	}))
	defer upstream2.Close()

	u1, _ := url.Parse(upstream1.URL)
	u2, _ := url.Parse(upstream2.URL)
	HostToProvider[u1.Hostname()] = &Provider{Name: "test", Auth: AuthBearer}
	HostToProvider[u2.Hostname()] = &Provider{Name: "test", Auth: AuthBearer}
	defer delete(HostToProvider, u1.Hostname())
	defer delete(HostToProvider, u2.Hostname())

	store := memory.New()
	_ = store.Set(&vault.Credential{Provider: "test", Account: "user", AccessToken: "tok"})

	ca := testCA(t)
	srv := &Server{
		Vault: store,
		Audit: NopAuditLog(),
		CA:    ca,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	var connectCount atomic.Int64
	wrapper := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			connectCount.Add(1)
		}
		srv.ServeHTTP(w, r)
	})

	proxyLn, _ := net.Listen("tcp", "127.0.0.1:0")
	defer proxyLn.Close()
	go http.Serve(proxyLn, wrapper)

	caPool := x509.NewCertPool()
	caPool.AddCert(ca.Cert)

	proxyURL, _ := url.Parse("http://" + proxyLn.Addr().String())
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{
				RootCAs: caPool,
			},
		},
	}

	// Request to host 1.
	resp1, err := client.Get("https://" + u1.Host + "/test")
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp1.Body)
	resp1.Body.Close()

	// Request to host 2 — different host, needs new CONNECT.
	resp2, err := client.Get("https://" + u2.Host + "/test")
	if err != nil {
		t.Fatal(err)
	}
	io.ReadAll(resp2.Body)
	resp2.Body.Close()

	if c := connectCount.Load(); c != 2 {
		t.Errorf("expected 2 CONNECTs for 2 different hosts, got %d", c)
	}
}

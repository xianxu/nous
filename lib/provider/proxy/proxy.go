package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/xianxu/nous/lib/provider/oauth"
	"github.com/xianxu/nous/lib/provider/vault"
	"golang.org/x/sync/singleflight"
)

// scopeNormalizer returns the per-provider canonicalization function used
// for scope comparison. Without it, an agent declaring "gmail.readonly"
// (short name) would never match a credential granted with the full URL
// form Google issues, producing spurious 407s.
func scopeNormalizer(providerName string) func(string) string {
	switch providerName {
	case "google":
		return oauth.ResolveGoogleScope
	}
	return nil
}

const charonAccountHeader = "X-Charon-Account"

// Refresher can refresh expired credentials.
type Refresher interface {
	Refresh(cred *vault.Credential) (*vault.Credential, error)
}

// Server is the Charon HTTPS forward proxy.
type Server struct {
	Vault      vault.Store
	Audit      *AuditLog
	Addr       string // listen address, e.g. "127.0.0.1:8230"
	CA         *CA
	Transport  http.RoundTripper
	Refreshers map[string]Refresher // provider name → refresher (e.g. "google" → GoogleProvider)
	Verbose    bool                 // enable debug logging
	// Now returns the current time. Defaults to time.Now. Override in tests.
	Now func() time.Time
	// ScopeTracker tracks scope denials for the fix command. Nil disables tracking.
	ScopeTracker *ScopeTracker
	// Session is the proxy's runtime-consent state. nil disables the
	// gate (legacy permanently-armed behavior — useful for tests
	// that pre-date the consent work). `charon serve` always wires
	// a non-nil Session that boots disarmed (#16 A).
	Session *Session

	// tokenCache caches access tokens in memory keyed by "provider:account".
	tokenCache sync.Map
	// accountCache caches provider→account resolution for single-account providers.
	accountCache sync.Map
	// refreshGroup deduplicates concurrent refresh calls for the same provider:account.
	refreshGroup singleflight.Group
}

type cachedToken struct {
	token  string
	expiry time.Time
	scopes []string
}

// ClearCache invalidates all cached tokens and account resolutions.
func (s *Server) ClearCache() {
	s.tokenCache.Range(func(k, v any) bool { s.tokenCache.Delete(k); return true })
	s.accountCache.Range(func(k, v any) bool { s.accountCache.Delete(k); return true })
}

func (s *Server) debug(format string, args ...any) {
	if s.Verbose {
		log.Printf(format, args...)
	}
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// http1Transport is used for CONNECT interception — forces HTTP/1.1 upstream
// since our client-side MITM connection is HTTP/1.1.
var http1Transport = &http.Transport{
	TLSNextProto: make(map[string]func(string, *tls.Conn) http.RoundTripper), // disable HTTP/2
}

func (s *Server) transport() http.RoundTripper {
	if s.Transport != nil {
		return s.Transport
	}
	return http1Transport
}

// ListenAndServe starts the proxy server.
func (s *Server) ListenAndServe() error {
	srv := &http.Server{
		Addr:    s.Addr,
		Handler: s,
		// Stash the underlying net.Conn into the request context so
		// connFromResponseWriter can recover it pre-hijack. The stdlib
		// http.response struct does not expose Conn(), so without this
		// hook the disarmed-gate audit path (#16) can't resolve the
		// peer's PID and the user loses visibility into who knocked
		// while the session was off.
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, connCtxKey{}, c)
		},
	}
	log.Printf("charon proxy listening on %s", s.Addr)
	return srv.ListenAndServe()
}

// connCtxKey is the context-key type for the per-conn net.Conn
// stashed by ConnContext. Unexported empty struct so it can't
// collide with other packages' keys.
type connCtxKey struct{}

// ServeHTTP handles both CONNECT (HTTPS) and plain HTTP requests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		s.handleConnect(w, r)
		return
	}
	// Direct requests to the proxy itself (not forwarded).
	if r.URL.Host == "" {
		s.handleDirect(w, r)
		return
	}
	s.handleHTTP(w, r)
}

// handleDirect handles requests sent directly to the proxy (health, CA cert).
func (s *Server) handleDirect(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/cache/clear":
		s.ClearCache()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"cleared":true}`)
	case "/session/arm":
		s.handleSessionArm(w, r)
	case "/session/disarm":
		s.handleSessionDisarm(w, r)
	case "/session/status":
		s.handleSessionStatus(w, r)
	case "/audit/recent":
		s.handleAuditRecent(w, r)
	case "/healthz":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","addr":%q}`, s.Addr)
	case "/ca.pem":
		if s.CA == nil {
			http.Error(w, "no CA configured", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Write(s.CA.CertPEM)
	case "/scopes/denied":
		if s.ScopeTracker != nil {
			s.ScopeTracker.HandleDeniedScopes(w, r)
		} else {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, "[]")
		}
	default:
		http.Error(w, "charon proxy — use HTTPS_PROXY to route traffic", http.StatusOK)
	}
}

// resolvePeerFromConn extracts the remote (peer) port from a hijacked
// TCP connection and runs the lsof+ps lookup. Returns nil on any
// failure path (nil conn, non-TCP addr, lookup failure); callers
// tolerate nil and audit "unknown".
func resolvePeerFromConn(c net.Conn) *PeerInfo {
	if c == nil {
		return nil
	}
	tcp, ok := c.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return nil
	}
	return ResolvePeer(tcp.Port)
}

// handleConnect handles HTTPS CONNECT tunneling with token injection.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	// Runtime-consent gate: refuse new tunnels while disarmed.
	// Per spec, the gate fires once at CONNECT — once a tunnel is
	// up, requests inside drain even if the session expires
	// mid-flight (agents handle TCP RST poorly, and the next
	// request inside an open tunnel doesn't ask CONNECT again so
	// RST mid-tunnel doesn't actually defend more).
	if s.Session != nil && !s.Session.IsArmed() {
		s.logDisarmedDenial(w, r, "CONNECT", strings.Split(r.Host, ":")[0], "")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusProxyAuthRequired)
		fmt.Fprint(w, `{"error":"session_disarmed","fix":"charon arm   # or click the menubar dot in Charon Security.app"}`)
		return
	}

	host := r.Host
	if !strings.Contains(host, ":") {
		host += ":443"
	}
	hostname := strings.Split(host, ":")[0]

	provider := ProviderForHost(hostname)
	if provider == nil {
		s.tunnelPassthrough(w, r, host)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer clientConn.Close()

	// Caller identification (#16 B): resolve once per CONNECT and
	// reuse for every request inside the tunnel. The peer's local
	// source port = our RemoteAddr port; passing that to ResolvePeer
	// finds which process owns the socket.
	peer := resolvePeerFromConn(clientConn)

	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	tlsClientConn := tls.Server(clientConn, &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := hello.ServerName
			if name == "" {
				name = hostname
			}
			return s.CA.GenerateCert(name)
		},
	})
	defer tlsClientConn.Close()
	if err := tlsClientConn.Handshake(); err != nil {
		log.Printf("TLS handshake with client failed: %v", err)
		return
	}
	s.debug("CONNECT %s: TLS handshake complete", hostname)

	clientReader := bufio.NewReader(tlsClientConn)
	reqNum := 0
	for {
		req, err := http.ReadRequest(clientReader)
		if err != nil {
			if err != io.EOF {
				log.Printf("error reading request: %v", err)
			}
			return
		}

		reqNum++
		s.debug("CONNECT %s: request #%d %s %s", hostname, reqNum, req.Method, req.URL.Path)

		start := s.now()
		account := req.Header.Get(charonAccountHeader)
		req.Header.Del(charonAccountHeader)
		requestedScopes := req.Header.Get(charonScopeHeader)
		req.Header.Del(charonScopeHeader)

		token, resolvedAccount, grantedScopes, err := s.resolveToken(provider, account)
		entry := AuditEntry{
			Timestamp: start,
			Method:    req.Method,
			Host:      hostname,
			Path:      req.URL.Path,
			Provider:  provider.Name,
			Account:   resolvedAccount,
		}
		// Stamp peer info onto every request entry inside this tunnel.
		// Lookup happened once at CONNECT (above); cheap to copy.
		if peer != nil {
			entry.PeerPID = peer.PID
			entry.PeerExe = peer.Exe
			entry.PeerArgv0 = peer.Argv0
			entry.PeerParentChain = peer.ParentChain
		}

		if err != nil {
			log.Printf("credential error for %s/%s: %v", provider.Name, account, err)
			entry.Error = err.Error()
			entry.StatusCode = http.StatusProxyAuthRequired
			s.Audit.Log(entry)
			resp := &http.Response{
				StatusCode: http.StatusProxyAuthRequired,
				Status:     "407 Proxy Authentication Required",
				Proto:      "HTTP/1.1",
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     http.Header{"Content-Type": {"text/plain"}},
				Body:       io.NopCloser(strings.NewReader("charon: credential required\n")),
			}
			_ = resp.Write(tlsClientConn)
			return
		}

		// Check requested scopes against granted scopes. Admin-key
		// providers have no scope concept — silently ignore the
		// header on their routes per the agent-protocol contract.
		if provider.HasScopes && requestedScopes != "" {
			requested := strings.Split(requestedScopes, ",")
			for i := range requested {
				requested[i] = strings.TrimSpace(requested[i])
			}
			missing := findMissingScopes(requested, grantedScopes, scopeNormalizer(provider.Name))
			if len(missing) > 0 {
				if s.ScopeTracker != nil {
					s.ScopeTracker.Track(provider.Name, resolvedAccount, missing)
				}
				errBody := scopeErrorJSON(provider.Name, resolvedAccount, missing)
				entry.Error = "scope_missing: " + strings.Join(missing, ",")
				entry.StatusCode = http.StatusProxyAuthRequired
				s.Audit.Log(entry)
				resp := &http.Response{
					StatusCode: http.StatusProxyAuthRequired,
					Status:     "407 Proxy Authentication Required",
					Proto:      "HTTP/1.1",
					ProtoMajor: 1,
					ProtoMinor: 1,
					Header:     http.Header{"Content-Type": {"application/json"}},
					Body:       io.NopCloser(strings.NewReader(errBody)),
				}
				_ = resp.Write(tlsClientConn)
				return
			}
		}

		if err := provider.InjectAuth(req, token); err != nil {
			entry.Error = err.Error()
			entry.StatusCode = http.StatusInternalServerError
			s.Audit.Log(entry)
			// Tell the client what happened — without this the agent
			// sees a TLS handshake then silent EOF.
			errResp := &http.Response{
				StatusCode: http.StatusInternalServerError,
				Status:     "500 Internal Server Error",
				Proto:      "HTTP/1.1",
				ProtoMajor: 1, ProtoMinor: 1,
				Header: http.Header{"Content-Type": {"application/json"}},
				Body:   io.NopCloser(strings.NewReader(`{"error":"inject_auth_failed"}`)),
			}
			_ = errResp.Write(tlsClientConn)
			return
		}

		req.URL.Scheme = "https"
		req.URL.Host = host
		req.RequestURI = ""

		// Tier 1 stats: req_bytes from request Content-Length (when
		// known; -1 from Go's transport for chunked uploads).
		if req.ContentLength > 0 {
			entry.ReqBytes = req.ContentLength
		}

		resp, err := s.transport().RoundTrip(req)
		if err != nil {
			entry.Error = err.Error()
			entry.StatusCode = http.StatusBadGateway
			entry.LatencyMs = time.Since(start).Milliseconds()
			s.Audit.Log(entry)
			log.Printf("upstream error: %v", err)
			// 502 with the upstream error message so charon stats
			// can distinguish upstream failures from gate refusals.
			errResp := &http.Response{
				StatusCode: http.StatusBadGateway,
				Status:     "502 Bad Gateway",
				Proto:      "HTTP/1.1",
				ProtoMajor: 1, ProtoMinor: 1,
				Header: http.Header{"Content-Type": {"text/plain"}},
				Body:   io.NopCloser(strings.NewReader("charon: upstream error: " + err.Error() + "\n")),
			}
			_ = errResp.Write(tlsClientConn)
			return
		}

		entry.StatusCode = resp.StatusCode
		entry.RespContentType = resp.Header.Get("Content-Type")

		// Wrap the body so we can count total bytes streamed (Tier 1)
		// and sample the first statsBodyCap for Tier 2 array counting.
		// Sampling is a non-destructive observation — the body still
		// streams unaltered to the downstream client.
		tap := newBodyTap(resp.Body, statsBodyCap)
		resp.Body = tap

		// Ensure response has proper framing so the client knows where the body ends.
		// Go's transport strips Transfer-Encoding and dechunks the body, leaving
		// ContentLength=-1. Without framing, the client waits for connection close.
		if resp.ContentLength < 0 && len(resp.TransferEncoding) == 0 {
			resp.TransferEncoding = []string{"chunked"}
		}

		_ = resp.Write(tlsClientConn)
		_ = resp.Body.Close()

		// Stats finalization (post-write): byte count is exact;
		// item count only if Content-Type is JSON-ish AND the body
		// fit within the cap.
		entry.RespBytes = tap.Total
		if !tap.Capped && isJSONContentType(entry.RespContentType) {
			if n, ok := countTopLevelItems(tap.sample.Bytes()); ok {
				entry.ItemsReturned = &n
			}
		}
		entry.LatencyMs = time.Since(start).Milliseconds()
		s.Audit.Log(entry)

		// Honor Connection: close from either client or upstream.
		if req.Close || resp.Close {
			s.debug("CONNECT %s: closing (req.Close=%v, resp.Close=%v)", hostname, req.Close, resp.Close)
			return
		}
	}
}

// tunnelPassthrough creates a raw TCP tunnel for unknown hosts. The
// gate at the top of handleConnect already refused this if the
// session was disarmed; we still audit so `charon who`/`stats` see
// passthrough activity (per the user-mental-model promise of
// "see everything that goes through charon").
func (s *Server) tunnelPassthrough(w http.ResponseWriter, r *http.Request, host string) {
	hostname := host
	if i := strings.LastIndex(host, ":"); i >= 0 {
		hostname = host[:i]
	}
	start := s.now()
	entry := AuditEntry{
		Timestamp: start,
		Method:    r.Method,
		Host:      hostname,
		Path:      "(passthrough)",
		Provider:  "passthrough",
	}
	// Best-effort peer attribution; same logic as the credentialed
	// CONNECT path above.
	if peer := resolvePeerFromConn(peerConnForRequest(w, r)); peer != nil {
		entry.PeerPID = peer.PID
		entry.PeerExe = peer.Exe
		entry.PeerArgv0 = peer.Argv0
		entry.PeerParentChain = peer.ParentChain
	}

	upstream, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		entry.Error = err.Error()
		entry.StatusCode = http.StatusServiceUnavailable
		entry.LatencyMs = time.Since(start).Milliseconds()
		s.Audit.Log(entry)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		upstream.Close()
		entry.Error = "hijacking not supported"
		entry.StatusCode = http.StatusInternalServerError
		s.Audit.Log(entry)
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		upstream.Close()
		entry.Error = err.Error()
		s.Audit.Log(entry)
		return
	}

	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	entry.StatusCode = 200

	// Re-resolve peer if the pre-hijack attempt couldn't get a *net.Conn
	// (the http.ResponseWriter shim path). Post-hijack we always have one.
	if entry.PeerPID == 0 {
		if peer := resolvePeerFromConn(clientConn); peer != nil {
			entry.PeerPID = peer.PID
			entry.PeerExe = peer.Exe
			entry.PeerArgv0 = peer.Argv0
			entry.PeerParentChain = peer.ParentChain
		}
	}

	// Copy with byte counters so passthrough traffic shows up in
	// stats. ReqBytes = client→upstream, RespBytes = upstream→client.
	done := make(chan struct{})
	var upBytes, downBytes int64
	go func() {
		upBytes, _ = io.Copy(upstream, clientConn)
		close(done)
	}()
	downBytes, _ = io.Copy(clientConn, upstream)
	clientConn.Close()
	<-done
	upstream.Close()

	entry.ReqBytes = upBytes
	entry.RespBytes = downBytes
	entry.LatencyMs = time.Since(start).Milliseconds()
	s.Audit.Log(entry)
}

// logDisarmedDenial records an audit entry for a request that was
// rejected by the runtime-consent gate. The point is visibility:
// background processes hammering the proxy while the user is away
// should still appear in `charon who` so the user can see who and
// where, even though no traffic actually flowed. Peer attribution
// is best-effort (same as the credentialed paths); pre-hijack we
// recover the conn via peerConnForRequest, which works for both
// production traffic (Server.ConnContext) and test shims.
func (s *Server) logDisarmedDenial(w http.ResponseWriter, r *http.Request, method, hostname, path string) {
	if s.Audit == nil {
		return
	}
	entry := AuditEntry{
		Timestamp:  s.now(),
		Method:     method,
		Host:       hostname,
		Path:       path,
		StatusCode: http.StatusProxyAuthRequired,
		Error:      "session_disarmed",
	}
	if peer := resolvePeerFromConn(peerConnForRequest(w, r)); peer != nil {
		entry.PeerPID = peer.PID
		entry.PeerExe = peer.Exe
		entry.PeerArgv0 = peer.Argv0
		entry.PeerParentChain = peer.ParentChain
	}
	s.Audit.Log(entry)
}

// connFromResponseWriter best-effort extracts the underlying
// net.Conn from an http.ResponseWriter for peer resolution
// before any hijack. The stdlib http.response struct doesn't
// expose Conn(), so for production traffic we recover it from
// the request context populated by Server.ConnContext (see
// ListenAndServe). The interface fallback is kept for tests
// that supply a custom ResponseWriter shim.
func connFromResponseWriter(w http.ResponseWriter) net.Conn {
	type connHolder interface{ Conn() net.Conn }
	if h, ok := w.(connHolder); ok {
		if c := h.Conn(); c != nil {
			return c
		}
	}
	return nil
}

// connFromRequest looks up the per-conn net.Conn stashed by the
// Server.ConnContext hook. Returns nil if the request didn't go
// through that hook (e.g. fully synthetic httptest requests).
func connFromRequest(r *http.Request) net.Conn {
	if r == nil || r.Context() == nil {
		return nil
	}
	if c, ok := r.Context().Value(connCtxKey{}).(net.Conn); ok {
		return c
	}
	return nil
}

// peerConnForRequest returns whichever conn-source yields a real
// net.Conn first: the response-writer interface (hit by tests with
// custom shims) or the request's stashed ConnContext value (hit
// by production traffic). Pre-hijack callers use this instead of
// connFromResponseWriter directly.
func peerConnForRequest(w http.ResponseWriter, r *http.Request) net.Conn {
	if c := connFromResponseWriter(w); c != nil {
		return c
	}
	return connFromRequest(r)
}

// handleHTTP handles plain HTTP requests (non-CONNECT). Gated on
// the same Session.IsArmed check as CONNECT so the runtime-consent
// bit applies symmetrically across protocols. Today most agent
// traffic is HTTPS-via-CONNECT; this path mostly handles legacy
// http:// URLs and proxy introspection.
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if s.Session != nil && !s.Session.IsArmed() {
		s.logDisarmedDenial(w, r, r.Method, r.URL.Hostname(), r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusProxyAuthRequired)
		fmt.Fprint(w, `{"error":"session_disarmed","fix":"charon arm   # or click the menubar dot in Charon Security.app"}`)
		return
	}
	start := s.now()
	hostname := r.URL.Hostname()
	provider := ProviderForHost(hostname)

	entry := AuditEntry{
		Timestamp: start,
		Method:    r.Method,
		Host:      hostname,
		Path:      r.URL.Path,
	}

	if provider != nil {
		entry.Provider = provider.Name
		account := r.Header.Get(charonAccountHeader)
		r.Header.Del(charonAccountHeader)
		requestedScopes := r.Header.Get(charonScopeHeader)
		r.Header.Del(charonScopeHeader)

		token, resolvedAccount, grantedScopes, err := s.resolveToken(provider, account)
		entry.Account = resolvedAccount
		if err != nil {
			entry.Error = err.Error()
			s.Audit.Log(entry)
			http.Error(w, "charon: credential required", http.StatusProxyAuthRequired)
			return
		}

		// Check requested scopes against granted scopes. Admin-key
		// providers have no scope concept — silently ignore the
		// header on their routes per the agent-protocol contract.
		if provider.HasScopes && requestedScopes != "" {
			requested := strings.Split(requestedScopes, ",")
			for i := range requested {
				requested[i] = strings.TrimSpace(requested[i])
			}
			missing := findMissingScopes(requested, grantedScopes, scopeNormalizer(provider.Name))
			if len(missing) > 0 {
				if s.ScopeTracker != nil {
					s.ScopeTracker.Track(provider.Name, resolvedAccount, missing)
				}
				entry.Error = "scope_missing: " + strings.Join(missing, ",")
				s.Audit.Log(entry)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusProxyAuthRequired)
				fmt.Fprint(w, scopeErrorJSON(provider.Name, resolvedAccount, missing))
				return
			}
		}

		if err := provider.InjectAuth(r, token); err != nil {
			entry.Error = err.Error()
			s.Audit.Log(entry)
			http.Error(w, "charon: unsupported auth method", http.StatusInternalServerError)
			return
		}
	}

	r.RequestURI = ""
	resp, err := s.transport().RoundTrip(r)
	if err != nil {
		entry.Error = err.Error()
		s.Audit.Log(entry)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	entry.StatusCode = resp.StatusCode
	entry.LatencyMs = time.Since(start).Milliseconds()
	s.Audit.Log(entry)

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// resolveToken gets a credential token and granted scopes for the
// given provider/account. The provider's VaultName() determines
// where to look up the credential, and Auth determines which field
// of the credential to use as the token (AccessToken, AdminKey
// material, or AIStudio key).
func (s *Server) resolveToken(p *Provider, account string) (token, resolvedAccount string, scopes []string, err error) {
	vaultName := p.VaultName()
	if account == "" {
		// Check account cache first to avoid calling security dump-keychain.
		if cached, ok := s.accountCache.Load(vaultName); ok {
			account = cached.(string)
		} else {
			creds, err := s.Vault.List()
			if err != nil {
				return "", "", nil, fmt.Errorf("failed to list credentials: %w", err)
			}
			var matches []*vault.Credential
			for _, c := range creds {
				if c.Provider == vaultName {
					matches = append(matches, c)
				}
			}
			switch len(matches) {
			case 0:
				return "", "", nil, fmt.Errorf("no credentials for provider %q", vaultName)
			case 1:
				account = matches[0].Account
				s.accountCache.Store(vaultName, account)
			default:
				return "", "", nil, fmt.Errorf("multiple accounts for provider %q, set %s header", vaultName, charonAccountHeader)
			}
		}
	}

	// Cache key is keyed on the routing provider name so two routes
	// onto the same vault entry (Google OAuth bearer vs. Google AI
	// Studio key) get separate cache slots.
	cacheKey := p.Name + ":" + account
	now := s.now()
	if cached, ok := s.tokenCache.Load(cacheKey); ok {
		ct := cached.(*cachedToken)
		if ct.expiry.IsZero() || now.Before(ct.expiry.Add(-vault.GracePeriod)) {
			return ct.token, account, ct.scopes, nil
		}
	}

	cred, err := s.Vault.Get(vaultName, account)
	if err != nil {
		return "", account, nil, err
	}

	// AI Studio: AuthQuery routes whose credential is stored under
	// "google" alongside the OAuth tokens; the key material lives
	// in cred.AIStudio. Distinct from generic catalog AuthQuery use
	// because the AI Studio key piggybacks on an OAuth credential
	// rather than being its own TypeCatalog entry. No scopes, no
	// refresh — the key is static until rotated/revoked.
	if p.Auth == AuthQuery && cred.CredType() == vault.TypeOAuth {
		if cred.AIStudio == nil || cred.AIStudio.KeyMaterial == "" {
			return "", account, nil, fmt.Errorf("no AI Studio key for %s/%s — run 'charon auth' and complete cloud-platform setup", vaultName, account)
		}
		s.tokenCache.Store(cacheKey, &cachedToken{token: cred.AIStudio.KeyMaterial})
		return cred.AIStudio.KeyMaterial, account, nil, nil
	}

	// Catalog (Tier 3) credentials: pasted key in cred.Catalog.
	// No scopes, no refresh — static until the user rotates locally
	// or invokes the catalog revoke pathway (#15 M4b).
	if cred.CredType() == vault.TypeCatalog {
		if cred.Catalog == nil || cred.Catalog.KeyMaterial == "" {
			return "", account, nil, fmt.Errorf("catalog credential %s/%s has no key material", vaultName, account)
		}
		s.tokenCache.Store(cacheKey, &cachedToken{token: cred.Catalog.KeyMaterial})
		return cred.Catalog.KeyMaterial, account, nil, nil
	}

	// Admin-key credentials (OpenAI service-account keys, future
	// catalog Tier 3 paste keys): KeyMaterial is the token directly.
	// No refresh path — admin-key tokens are static until revoked.
	// No scopes — admin-key providers don't expose scope semantics.
	// Cache with zero expiry so the existing cache-hit branch above
	// short-circuits subsequent reads cheaply.
	if cred.CredType() == vault.TypeAdminKey {
		if cred.AdminKey == nil || cred.AdminKey.KeyMaterial == "" {
			return "", account, nil, fmt.Errorf("admin-key credential %s/%s has no key material — re-mint", vaultName, account)
		}
		s.tokenCache.Store(cacheKey, &cachedToken{token: cred.AdminKey.KeyMaterial})
		return cred.AdminKey.KeyMaterial, account, nil, nil
	}

	if cred.AccessToken != "" && !cred.IsExpiredAt(now) {
		s.tokenCache.Store(cacheKey, &cachedToken{
			token:  cred.AccessToken,
			expiry: cred.Expiry,
			scopes: cred.Scopes,
		})
		return cred.AccessToken, account, cred.Scopes, nil
	}

	// Token expired or missing — try to refresh.
	// Use singleflight to prevent concurrent refreshes for the same account
	// (thundering herd when multiple requests arrive with an expired token).
	if cred.RefreshToken != "" && s.Refreshers != nil {
		if refresher, ok := s.Refreshers[vaultName]; ok {
			type refreshResult struct {
				token  string
				scopes []string
			}
			result, err, _ := s.refreshGroup.Do(cacheKey, func() (any, error) {
				// Double-check cache — another goroutine may have refreshed while we waited.
				if cached, ok := s.tokenCache.Load(cacheKey); ok {
					ct := cached.(*cachedToken)
					if ct.expiry.IsZero() || now.Before(ct.expiry.Add(-vault.GracePeriod)) {
						return &refreshResult{ct.token, ct.scopes}, nil
					}
				}
				refreshed, err := refresher.Refresh(cred)
				if err != nil {
					return nil, err
				}
				if storeErr := s.Vault.Set(refreshed); storeErr != nil {
					log.Printf("failed to store refreshed token for %s/%s: %v", vaultName, account, storeErr)
				}
				s.tokenCache.Store(cacheKey, &cachedToken{
					token:  refreshed.AccessToken,
					expiry: refreshed.Expiry,
					scopes: refreshed.Scopes,
				})
				return &refreshResult{refreshed.AccessToken, refreshed.Scopes}, nil
			})
			if err != nil {
				log.Printf("token refresh failed for %s/%s: %v", vaultName, account, err)
			} else {
				rr := result.(*refreshResult)
				return rr.token, account, rr.scopes, nil
			}
		}
	}

	// Fallback: return whatever token we have (may be expired).
	if cred.AccessToken != "" {
		return cred.AccessToken, account, cred.Scopes, nil
	}

	return "", account, nil, fmt.Errorf("no access token for %s/%s and refresh not available", vaultName, account)
}

package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/xianxu/nous/lib/provider/vault"
)

// dialect captures the per-provider variations behind the Provider port — the
// seam Microsoft forced into existence (nous#48). Everything else (the loopback
// callback server, the token HTTP exchange, Refresh, applyRefresh, the
// oauth-credential-lifecycle S machine) is shared across providers in
// OIDCProvider below. This is NOT a cross-service framework (ariadne#71): it
// lives inside package oauth, varies only OIDC providers, and each provider is
// still built from its own Conf.
//
//   - providerID/requiredScopes/authParams/extractID/mintID are shared by the
//     real adapter AND the fake (so the fake models the same wire/identity).
//   - usePKCE and revoke are real-adapter-only: the fake does no PKCE wire and
//     has its own provider-neutral Revoke (deleting the live grant).
type dialect struct {
	providerID     string                                           // vault.Credential.Provider ("google"/"microsoft")
	requiredScopes []string                                         // structural scopes always merged into a request
	usePKCE        bool                                             // real Auth adds code_challenge + sends code_verifier
	authParams     func(forceFresh bool) url.Values                 // provider-specific authorization-URL params
	extractID      func(idToken string) (account string, e error)   // identity-claim extraction
	mintID         func(account string, verified bool) string       // fake-side: mint a token extractID can read
	revoke         func(g *OIDCProvider, refreshToken string) error // real-adapter revoke mechanism
}

// OIDCProvider is the generic real adapter — the only thing that talks to an
// OIDC issuer (HTTP token/refresh/revoke + browser-open + loopback callback
// server). One implementation serves every provider; the per-provider variation
// is the injected dialect. It implements the Provider port; construct it with a
// per-provider constructor (NewGoogleProvider / NewMicrosoftProvider) or, for
// tests pointing at a fake endpoint, New(Conf) (Google dialect).
type OIDCProvider struct {
	clientID      string
	clientSecret  string // empty for a public client (Microsoft PKCE) — then no client_secret is sent
	authURL       string
	tokenURL      string
	revokeURL     string
	defaultScopes []string
	dialect       dialect
	// Output receives status messages emitted during Auth (e.g. "Opening
	// browser..."). Defaults to os.Stderr. Set to io.Discard from a TUI
	// to keep these from corrupting the rendered screen.
	Output io.Writer
}

var _ Provider = (*OIDCProvider)(nil)

// newProvider builds the generic real adapter from a Conf + a per-provider
// dialect. The per-provider constructors (and the Google-dialect New) wrap it.
func newProvider(conf Conf, d dialect) *OIDCProvider {
	out := conf.Output
	if out == nil {
		out = os.Stderr
	}
	return &OIDCProvider{
		clientID:      conf.ClientID,
		clientSecret:  conf.ClientSecret,
		authURL:       conf.AuthURL,
		tokenURL:      conf.TokenURL,
		revokeURL:     conf.RevokeURL,
		defaultScopes: conf.DefaultScopes,
		dialect:       d,
		Output:        out,
	}
}

// out returns the writer for status messages, falling back to io.Discard if
// Output isn't set (defensive against a zero-value OIDCProvider).
func (g *OIDCProvider) out() io.Writer {
	if g.Output == nil {
		return io.Discard
	}
	return g.Output
}

// Auth runs the OAuth authorization flow: opens browser, waits for callback,
// exchanges code for tokens. (NoGrant→Active in the S machine.)
//
// If account is provided, it's used as a login_hint to pre-select the account.
// The actual authenticated identity is extracted from the ID token (the
// dialect's extractID) and set as the credential's Account.
//
// forceFresh controls whether the issued token covers the union of all
// previously-granted scopes (false, additive/incremental) or only the requested
// set (true, reductive). Use forceFresh=true when narrowing scopes.
//
// When the dialect requests PKCE (Microsoft public client), a per-call
// code_verifier is generated; its S256 challenge rides on the authorization URL
// and the verifier is sent on the code exchange.
func (g *OIDCProvider) Auth(account string, scopes []string, existingScopes []string, forceFresh bool) (*vault.Credential, error) {
	if len(scopes) == 0 {
		scopes = g.defaultScopes
	}
	var allScopes []string
	if forceFresh {
		// Reductive: request only the desired scope set + structural required.
		allScopes = mergeScopes(scopes, g.dialect.requiredScopes)
	} else {
		// Additive: merge desired + existing + required.
		allScopes = mergeScopes(mergeScopes(scopes, existingScopes), g.dialect.requiredScopes)
	}

	// Start local callback server (a random loopback port — Microsoft's
	// "Mobile and desktop" platform and Google both wildcard the loopback port).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to start callback server: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d", port)

	// Provider-specific authorization params + (for public clients) PKCE.
	extra := g.dialect.authParams(forceFresh)
	var verifier string
	if g.dialect.usePKCE {
		verifier = codeVerifier()
		extra.Set("code_challenge", codeChallenge(verifier))
		extra.Set("code_challenge_method", "S256")
	}
	authURL := buildAuthURL(g.authURL, g.clientID, redirectURI, allScopes, account, extra)

	fmt.Fprintf(g.out(), "Opening browser for %s OAuth...\n", g.dialect.providerID)
	fmt.Fprintf(g.out(), "If browser doesn't open, visit:\n%s\n\n", authURL)
	openBrowser(authURL)

	code, err := waitForCallback(ln)
	if err != nil {
		return nil, fmt.Errorf("OAuth callback failed: %w", err)
	}

	cred, err := g.exchangeCode(code, redirectURI, verifier)
	if err != nil {
		return nil, err
	}

	if account != "" && cred.Account != account {
		fmt.Fprintf(g.out(), "Note: requested %s but authenticated as %s\n", account, cred.Account)
	}
	return cred, nil
}

// Refresh uses a refresh token to get a new access token. (Expired→Active; the
// refresh token may rotate — Microsoft always rotates, Google sometimes.)
func (g *OIDCProvider) Refresh(cred *vault.Credential) (*vault.Credential, error) {
	if cred.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token for %s/%s", cred.Provider, cred.Account)
	}

	data := url.Values{
		"client_id":     {g.clientID},
		"refresh_token": {cred.RefreshToken},
		"grant_type":    {"refresh_token"},
	}
	if g.clientSecret != "" {
		data.Set("client_secret", g.clientSecret) // omitted for a public client (PKCE)
	}

	resp, err := http.PostForm(g.tokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("token refresh failed: %w", err)
	}
	defer resp.Body.Close()

	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}
	if tok.Error != "" {
		return nil, fmt.Errorf("token refresh error: %s: %s", tok.Error, tok.ErrorDesc)
	}

	// Rotation + sidecar/identity preservation is the shared pure core, so the
	// real and fake adapters can't drift on this contract.
	return applyRefresh(cred, tok, time.Now()), nil
}

// Revoke delegates to the dialect's revoke mechanism (Google: RFC-7009 token
// revoke; Microsoft: ErrRevokeUnsupported — no per-token revoke endpoint).
func (g *OIDCProvider) Revoke(refreshToken string) error {
	if refreshToken == "" {
		return fmt.Errorf("no refresh token to revoke")
	}
	return g.dialect.revoke(g, refreshToken)
}

// exchangeCode trades an authorization code for tokens. For a public client
// (no client secret) it omits client_secret; for a PKCE flow it sends the
// per-call code_verifier. Shapes the credential via the shared pure core
// (the dialect's identity extractor + providerID).
func (g *OIDCProvider) exchangeCode(code, redirectURI, verifier string) (*vault.Credential, error) {
	data := url.Values{
		"client_id":    {g.clientID},
		"code":         {code},
		"redirect_uri": {redirectURI},
		"grant_type":   {"authorization_code"},
	}
	if g.clientSecret != "" {
		data.Set("client_secret", g.clientSecret)
	}
	if verifier != "" {
		data.Set("code_verifier", verifier)
	}

	resp, err := http.PostForm(g.tokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	defer resp.Body.Close()

	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}
	if tok.Error != "" {
		return nil, fmt.Errorf("token exchange error: %s: %s", tok.Error, tok.ErrorDesc)
	}

	return credentialFromToken(tok, g.dialect.providerID, g.dialect.extractID, time.Now())
}

// waitForCallback starts an HTTP server, waits for the OAuth callback, extracts the code.
func waitForCallback(ln net.Listener) (string, error) {
	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			code := r.URL.Query().Get("code")
			if code == "" {
				errMsg := r.URL.Query().Get("error")
				if errMsg == "" {
					errMsg = "no authorization code received"
				}
				fmt.Fprintf(w, "<html><body><h1>Authorization Failed</h1><p>%s</p><p>You can close this tab.</p></body></html>", html.EscapeString(errMsg))
				errCh <- fmt.Errorf("OAuth error: %s", errMsg)
				return
			}
			fmt.Fprint(w, "<html><body><h1>Authorization Successful</h1><p>You can close this tab and return to the terminal.</p></body></html>")
			codeCh <- code
		}),
	}

	go srv.Serve(ln)

	select {
	case code := <-codeCh:
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		return code, nil
	case err := <-errCh:
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		return "", err
	case <-time.After(5 * time.Minute):
		srv.Close()
		return "", fmt.Errorf("OAuth callback timed out (5 minutes)")
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		log.Printf("Please open this URL manually: %s", url)
		return
	}
	if err := cmd.Start(); err == nil {
		go cmd.Wait() // reap child process
	}
}

package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	"strings"
	"time"

	"github.com/xianxu/nous/lib/provider/vault"
)

const (
	obKey          = "charon-credential-proxy-obfuscation"
	obClientID     = "5a51594157591a504a55575643150c0f1a481a1b5c4a4e431d05121d4007111c0a59065250061f1c041a5a41014a041e041a4f0b421f15031d0c5e0a10051a1d17041a1d410d0c05"
	obClientSecret = "242722213f360028202410033d440c145f6821021755007837072210322757232d000d"

	googleAuthURL = "https://accounts.google.com/o/oauth2/auth"
)

// googleTokenURL is a var (not const) so tests can swap in an
// httptest server. Production callers must not mutate this.
var googleTokenURL = "https://oauth2.googleapis.com/token"

// DefaultGoogleScopes are requested if none specified.
//
// Empty by default — the TUI is the canonical UX for choosing scopes.
// Headless callers (legacy code paths) get only the required openid+email
// for ID-token email extraction; data scopes must be opted into explicitly.
var DefaultGoogleScopes = []string{}

// requiredGoogleScopes are always included to enable email extraction from ID token.
//
// Note: we use the full userinfo.email URL form rather than the OIDC short
// name "email" because Google rewrites the short form to this URL on the
// way back, and we want request and response to use the same string so
// that round-tripping (request → token endpoint → keychain → catalog
// lookup) matches.
var requiredGoogleScopes = []string{
	"openid",
	"https://www.googleapis.com/auth/userinfo.email",
}

// GoogleProvider implements the OAuth flow for Google accounts.
type GoogleProvider struct {
	clientID     string
	clientSecret string
	// Output receives status messages emitted during Auth (e.g. "Opening
	// browser..."). Defaults to os.Stderr. Set to io.Discard from a TUI
	// to keep these from corrupting the rendered screen.
	Output io.Writer
}

func NewGoogleProvider() (*GoogleProvider, error) {
	cid, err := XORDecode(obClientID, obKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode client_id: %w", err)
	}
	cs, err := XORDecode(obClientSecret, obKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode client_secret: %w", err)
	}
	return &GoogleProvider{clientID: cid, clientSecret: cs, Output: os.Stderr}, nil
}

// out returns the writer for status messages, falling back to io.Discard if
// Output isn't set (defensive against zero-value GoogleProvider).
func (g *GoogleProvider) out() io.Writer {
	if g.Output == nil {
		return io.Discard
	}
	return g.Output
}

// tokenResponse is the JSON response from Google's token endpoint.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// Auth runs the OAuth authorization flow: opens browser, waits for callback, exchanges code for tokens.
//
// If account is provided, it's used as a login_hint to pre-select the Google account.
// The actual authenticated email is extracted from the ID token and set as the credential's Account.
//
// forceFresh controls whether the issued token covers the union of all
// previously-granted scopes (false, additive/incremental — Google returns the
// union via include_granted_scopes=true) or only the requested set (true,
// reductive — Google returns exactly what's asked, ignoring older grants).
// Use forceFresh=true when narrowing scopes for an existing account.
func (g *GoogleProvider) Auth(account string, scopes []string, existingScopes []string, forceFresh bool) (*vault.Credential, error) {
	if len(scopes) == 0 {
		scopes = DefaultGoogleScopes
	}
	var allScopes []string
	if forceFresh {
		// Reductive: request only the desired scope set + structural required.
		// Don't merge existingScopes (those are what we're trying to drop).
		allScopes = mergeScopes(scopes, requiredGoogleScopes)
	} else {
		// Additive: merge desired + existing + required. Google's
		// include_granted_scopes=true returns a token covering the union.
		allScopes = mergeScopes(mergeScopes(scopes, existingScopes), requiredGoogleScopes)
	}

	// Start local callback server.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to start callback server: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d", port)

	// Build authorization URL with optional login hint.
	authURL := g.buildAuthURL(redirectURI, allScopes, account, forceFresh)

	// Open browser.
	fmt.Fprintf(g.out(), "Opening browser for Google OAuth...\n")
	fmt.Fprintf(g.out(), "If browser doesn't open, visit:\n%s\n\n", authURL)
	openBrowser(authURL)

	// Wait for callback with authorization code.
	code, err := waitForCallback(ln)
	if err != nil {
		return nil, fmt.Errorf("OAuth callback failed: %w", err)
	}

	// Exchange code for tokens — email extracted from ID token.
	cred, err := g.exchangeCode(code, redirectURI)
	if err != nil {
		return nil, err
	}

	// Warn if authenticated account doesn't match the requested one.
	if account != "" && cred.Account != account {
		fmt.Fprintf(g.out(), "Note: requested %s but authenticated as %s\n", account, cred.Account)
	}

	return cred, nil
}

// Refresh uses a refresh token to get a new access token.
func (g *GoogleProvider) Refresh(cred *vault.Credential) (*vault.Credential, error) {
	if cred.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token for %s/%s", cred.Provider, cred.Account)
	}

	data := url.Values{
		"client_id":     {g.clientID},
		"client_secret": {g.clientSecret},
		"refresh_token": {cred.RefreshToken},
		"grant_type":    {"refresh_token"},
	}

	resp, err := http.PostForm(googleTokenURL, data)
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

	updated := &vault.Credential{
		Type:         cred.Type,
		Provider:     cred.Provider,
		Account:      cred.Account,
		AccessToken:  tok.AccessToken,
		RefreshToken: cred.RefreshToken,
		Expiry:       time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
		Scopes:       cred.Scopes,
		// Preserve sidecars across refresh. These describe the
		// account's setup (GCP project metadata, AI Studio key)
		// and are independent of the OAuth token rotation. Earlier
		// versions dropped them on every refresh, silently wiping
		// the user's configured project + minted key.
		GCP:      cred.GCP,
		AIStudio: cred.AIStudio,
		AdminKey: cred.AdminKey,
		Catalog:  cred.Catalog,
	}

	// Handle refresh token rotation — Google may return a new refresh token.
	if tok.RefreshToken != "" {
		updated.RefreshToken = tok.RefreshToken
	}

	// Update scopes if changed.
	if tok.Scope != "" {
		updated.Scopes = strings.Split(tok.Scope, " ")
	}

	return updated, nil
}

func (g *GoogleProvider) buildAuthURL(redirectURI string, scopes []string, loginHint string, forceFresh bool) string {
	params := url.Values{
		"client_id":     {g.clientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {strings.Join(scopes, " ")},
		"access_type":   {"offline"}, // request refresh token
		"prompt":        {"consent"}, // force consent to get refresh token
	}
	if forceFresh {
		// Token will cover only the requested scope set, not the union of
		// existing grants. Required for the reductive flow.
		params.Set("include_granted_scopes", "false")
	} else {
		params.Set("include_granted_scopes", "true") // incremental authorization
	}
	if loginHint != "" {
		params.Set("login_hint", loginHint)
	}
	return googleAuthURL + "?" + params.Encode()
}

// ErrAlreadyRevoked indicates Google considers the token already invalid
// (revoked, expired, or otherwise unusable). Callers can treat this as
// success for local cleanup purposes — the upstream side is already in
// the desired state. Distinguished from real network or protocol errors
// so genuine failures still surface.
var ErrAlreadyRevoked = errors.New("token already revoked or invalid on Google's side")

// Revoke calls Google's revoke endpoint, invalidating the refresh token (and
// the underlying authorization grant). After this, neither this token nor any
// access tokens minted from it are usable. Use for "I'm done with this app
// entirely" — not the routine scope-reduction flow.
//
// Returns ErrAlreadyRevoked when Google responds with HTTP 400 and a body
// indicating the token is already invalid (`{"error":"invalid_token"}`).
// Callers that just want the token gone may treat this as success.
func (g *GoogleProvider) Revoke(refreshToken string) error {
	if refreshToken == "" {
		return fmt.Errorf("no refresh token to revoke")
	}
	resp, err := http.PostForm("https://oauth2.googleapis.com/revoke", url.Values{
		"token": {refreshToken},
	})
	if err != nil {
		return fmt.Errorf("revoke request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	// HTTP != 200. Try to parse Google's standard OAuth error envelope
	// {"error": "...", "error_description": "..."}. Google's revoke
	// endpoint returns 400 with `error=invalid_token` for tokens that
	// are already revoked or never were valid; treat that as success.
	body, _ := io.ReadAll(resp.Body)
	var oauthErr struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &oauthErr)
	if resp.StatusCode == http.StatusBadRequest && oauthErr.Error == "invalid_token" {
		return ErrAlreadyRevoked
	}
	if oauthErr.Error != "" {
		return fmt.Errorf("revoke returned HTTP %d (%s): %s", resp.StatusCode, oauthErr.Error, oauthErr.ErrorDescription)
	}
	return fmt.Errorf("revoke returned HTTP %d", resp.StatusCode)
}

func (g *GoogleProvider) exchangeCode(code, redirectURI string) (*vault.Credential, error) {
	data := url.Values{
		"client_id":     {g.clientID},
		"client_secret": {g.clientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
	}

	resp, err := http.PostForm(googleTokenURL, data)
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

	// Extract authenticated email from ID token.
	account, err := parseIDTokenEmail(tok.IDToken)
	if err != nil {
		return nil, fmt.Errorf("failed to identify account: %w", err)
	}

	var scopes []string
	if tok.Scope != "" {
		scopes = strings.Split(tok.Scope, " ")
	}

	return &vault.Credential{
		Provider:     "google",
		Account:      account,
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		Expiry:       time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
		Scopes:       scopes,
	}, nil
}

// parseIDTokenEmail extracts the email claim from a Google ID token (JWT).
// No signature verification needed — token comes directly from Google's token endpoint over HTTPS.
func parseIDTokenEmail(idToken string) (string, error) {
	if idToken == "" {
		return "", fmt.Errorf("no ID token in response (openid scope may not be granted)")
	}
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid ID token format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("failed to decode ID token payload: %w", err)
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("failed to parse ID token claims: %w", err)
	}
	if claims.Email == "" {
		return "", fmt.Errorf("no email claim in ID token")
	}
	return claims.Email, nil
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

func mergeScopes(requested, existing []string) []string {
	seen := make(map[string]bool)
	for _, s := range existing {
		seen[s] = true
	}
	for _, s := range requested {
		seen[s] = true
	}
	var merged []string
	for s := range seen {
		if s != "" {
			merged = append(merged, s)
		}
	}
	return merged
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

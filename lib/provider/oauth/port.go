package oauth

import (
	"fmt"
	"io"
	"os"

	"github.com/xianxu/nous/lib/provider/vault"
)

// Provider is the provider-neutral OAuth port: the surface charon actually
// uses, = the union of its three consumer interfaces (the TUI's Authenticator
// = Auth+Revoke, the proxy's Refresher = Refresh, charoncli's CheckHealth).
// Not a verbatim copy of Google's API — surface = what consumers use.
//
// The real adapter (google.go, the only thing that talks to Google) and the
// in-memory fake (fake.go) both implement it; consumers depend only on this
// interface so tests can inject the fake. This is instance #2 of the
// ariadne#71 shim(X)/shim'(X) pattern (reference: lib/gh).
type Provider interface {
	Auth(account string, scopes, existingScopes []string, forceFresh bool) (*vault.Credential, error)
	Refresh(cred *vault.Credential) (*vault.Credential, error)
	Revoke(refreshToken string) error
	CheckHealth(cred *vault.Credential) HealthState
}

var _ Provider = (*GoogleProvider)(nil)

// ConformanceKeychainService is the macOS Keychain service holding the
// throwaway-account Google refresh token the grounding test
// (contract_real_test.go) reads. cmd/oauth-conformance-provision *writes* this
// same entry — they must agree by construction, so the name lives here once.
const ConformanceKeychainService = "nous-oauth-conformance-google"

// Conf is the opaque, service-specific construction config. The one
// cross-service convention is the shape New(Conf)/NewFake(Conf), not these
// fields. Endpoints are injectable so tests (and a future non-Google OIDC
// provider) can point the adapter elsewhere.
type Conf struct {
	ClientID      string
	ClientSecret  string
	AuthURL       string // authorization endpoint
	TokenURL      string // token + refresh endpoint
	RevokeURL     string // revocation endpoint
	DefaultScopes []string
	// Output receives Auth status messages. nil → os.Stderr (set io.Discard
	// from a TUI to avoid corrupting the rendered screen).
	Output io.Writer
}

// New builds the real Google adapter from Conf.
func New(conf Conf) *GoogleProvider {
	out := conf.Output
	if out == nil {
		out = os.Stderr
	}
	return &GoogleProvider{
		clientID:      conf.ClientID,
		clientSecret:  conf.ClientSecret,
		authURL:       conf.AuthURL,
		tokenURL:      conf.TokenURL,
		revokeURL:     conf.RevokeURL,
		defaultScopes: conf.DefaultScopes,
		Output:        out,
	}
}

// defaultGoogleConf decodes the obfuscated client credentials and sets
// Google's production endpoints + default scopes. NewGoogleProvider wraps it.
func defaultGoogleConf() (Conf, error) {
	cid, err := XORDecode(obClientID, obKey)
	if err != nil {
		return Conf{}, fmt.Errorf("failed to decode client_id: %w", err)
	}
	cs, err := XORDecode(obClientSecret, obKey)
	if err != nil {
		return Conf{}, fmt.Errorf("failed to decode client_secret: %w", err)
	}
	return Conf{
		ClientID:      cid,
		ClientSecret:  cs,
		AuthURL:       googleAuthURL,
		TokenURL:      googleTokenURL,
		RevokeURL:     googleRevokeURL,
		DefaultScopes: DefaultGoogleScopes,
		Output:        os.Stderr,
	}, nil
}

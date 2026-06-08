package oauth

import (
	"io"

	"github.com/xianxu/nous/lib/provider/vault"
)

// Provider is the provider-neutral OAuth port: the surface charon actually
// uses, = the union of its three consumer interfaces (the TUI's Authenticator
// = Auth+Revoke, the proxy's Refresher = Refresh, charoncli's CheckHealth).
// Not a verbatim copy of any one provider's API — surface = what consumers use.
//
// The real adapter (OIDCProvider in oidc.go — the only thing that talks to a
// real issuer) and the in-memory fake (fake.go) both implement it; consumers
// depend only on this interface so tests can inject the fake. This is instance
// #2 of the ariadne#71 shim(X)/shim'(X) pattern (reference: lib/gh). Google and
// Microsoft are two dialects of the one OIDCProvider, not two adapters.
type Provider interface {
	Auth(account string, scopes, existingScopes []string, forceFresh bool) (*vault.Credential, error)
	Refresh(cred *vault.Credential) (*vault.Credential, error)
	Revoke(refreshToken string) error
	CheckHealth(cred *vault.Credential) HealthState
}

// ConformanceKeychainService is the macOS Keychain service holding the
// throwaway-account Google refresh token the grounding test
// (contract_real_test.go) reads. cmd/oauth-conformance-provision *writes* this
// same entry — they must agree by construction, so the name lives here once.
const ConformanceKeychainService = "nous-oauth-conformance-google"

// ConformanceKeychainServiceMicrosoft is the Microsoft counterpart — the
// throwaway Entra-account refresh token the real-Microsoft grounding test reads
// (and the provisioner's -provider microsoft path writes).
const ConformanceKeychainServiceMicrosoft = "nous-oauth-conformance-microsoft"

// Conf is the opaque, service-specific construction config. The one
// cross-service convention is the shape New(Conf)/NewFake(Conf), not these
// fields. Endpoints are injectable so tests (and the Microsoft adapter) point
// the OIDCProvider elsewhere. A public client (Microsoft PKCE) leaves
// ClientSecret empty — then no client_secret is sent on token requests.
type Conf struct {
	ClientID      string
	ClientSecret  string
	AuthURL       string // authorization endpoint
	TokenURL      string // token + refresh endpoint
	RevokeURL     string // revocation endpoint (empty when the provider has none)
	DefaultScopes []string
	// Output receives Auth status messages. nil → os.Stderr (set io.Discard
	// from a TUI to avoid corrupting the rendered screen).
	Output io.Writer
}

// New builds an OIDCProvider against the given Conf with the Google dialect.
// It is the implicit-Google constructor used by tests pointing at a fake token
// endpoint; production code uses NewGoogleProvider / NewMicrosoftProvider.
func New(conf Conf) *OIDCProvider {
	return newProvider(conf, googleDialect)
}

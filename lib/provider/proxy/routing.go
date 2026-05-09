// Package proxy implements the Charon HTTPS forward proxy.
package proxy

import (
	"fmt"
	"net/http"
	"strings"
)

// AuthMethod defines how credentials are injected into requests.
type AuthMethod string

const (
	AuthBearer AuthMethod = "bearer" // Authorization: <prefix><token> (default prefix "Bearer ")
	AuthHeader AuthMethod = "header" // <HeaderName>: <prefix><token> (custom header, e.g. x-api-key)
	AuthQuery  AuthMethod = "query"  // append ?<HeaderName>=<token> (default param "key")
)

// Provider describes a credential provider and how to inject auth.
//
// HasScopes controls whether X-Charon-Scope is honored for routes
// to this provider. OAuth providers (Google) have scope semantics
// and consume the header. Admin-key providers (OpenAI) and catalog
// providers have no scope concept — the header is silently ignored
// on their routes per the agent-protocol contract. Charon strips
// the header from outbound requests in either case.
//
// VaultProvider is the provider name to look up credentials under in
// the vault. Empty means use Name (the typical case). Set explicitly
// when a routing provider piggybacks on another provider's credential
// — e.g. the AI Studio route ("google-aistudio") looks up its key
// from the underlying Google credential ("google").
//
// HeaderName, HeaderPrefix, and ExtraHeaders configure auth dispatch:
//
//   - AuthBearer: HeaderPrefix overrides "Bearer " (used by providers
//     like Replicate that expect "Token <key>"). HeaderName ignored.
//   - AuthHeader: HeaderName is required (e.g. "x-api-key").
//     HeaderPrefix optional (empty = no prefix).
//   - AuthQuery:  HeaderName is the query param name; defaults to "key"
//     when empty (Google AI Studio's convention). HeaderPrefix ignored.
//
// ExtraHeaders are static headers attached to every proxied request
// regardless of auth style (e.g. anthropic-version: 2023-06-01).
type Provider struct {
	Name          string
	Auth          AuthMethod
	HasScopes     bool
	VaultProvider string
	HeaderName    string
	HeaderPrefix  string
	ExtraHeaders  map[string]string
}

// VaultName returns the provider name to use for vault lookups,
// defaulting to Name when VaultProvider is unset.
func (p *Provider) VaultName() string {
	if p.VaultProvider != "" {
		return p.VaultProvider
	}
	return p.Name
}

// InjectAuth attaches credentials to req per the provider's AuthMethod
// and applies any static ExtraHeaders. The proxy calls this once per
// request after resolving the credential.
func (p *Provider) InjectAuth(req *http.Request, token string) error {
	switch p.Auth {
	case AuthBearer, "": // default to bearer
		prefix := p.HeaderPrefix
		if prefix == "" {
			prefix = "Bearer "
		}
		req.Header.Set("Authorization", prefix+token)
	case AuthHeader:
		if p.HeaderName == "" {
			return fmt.Errorf("AuthHeader requires HeaderName")
		}
		req.Header.Set(p.HeaderName, p.HeaderPrefix+token)
	case AuthQuery:
		param := p.HeaderName
		if param == "" {
			param = "key"
		}
		q := req.URL.Query()
		q.Set(param, token)
		req.URL.RawQuery = q.Encode()
	default:
		return fmt.Errorf("unsupported auth method: %s", p.Auth)
	}
	for k, v := range p.ExtraHeaders {
		req.Header.Set(k, v)
	}
	return nil
}

// HostToProvider maps exact API hosts to credential providers.
//
// OpenAI's data plane is api.openai.com (chat, embeddings, image
// generation, etc.). The admin API (api.openai.com/v1/organization/…)
// shares the host but doesn't transit through the agent's runtime
// flow — admin calls go through internal/providers/openai during
// the TUI mint/revoke flows, not via this proxy.
//
// Catalog (Tier 3) entries are registered into this map at startup
// by catalog.Register(); compiled-provider entries declared here
// take precedence on any hostname overlap.
var HostToProvider = map[string]*Provider{
	"api.openai.com": {Name: "openai", Auth: AuthBearer, HasScopes: false},
	// AI Studio runs on its own host with API-key URL-param auth,
	// distinct from the rest of the Google universe (which uses
	// OAuth bearer). Exact-match takes precedence over the
	// .googleapis.com suffix rule below. Credentials live under
	// the "google" namespace; the URL-param-attached key comes from
	// cred.AIStudio.KeyMaterial.
	"generativelanguage.googleapis.com": {
		Name:          "google-aistudio",
		Auth:          AuthQuery,
		HasScopes:     false,
		VaultProvider: "google",
	},
}

// SuffixToProvider maps host suffixes (e.g. ".googleapis.com") to providers.
// Checked when no exact match is found in HostToProvider.
// suffixRule pairs a host suffix with a provider.
type suffixRule struct {
	Suffix   string
	Provider *Provider
}

// SuffixToProvider maps host suffixes (e.g. ".googleapis.com") to providers.
// Checked when no exact match is found in HostToProvider.
var SuffixToProvider = []suffixRule{
	{".googleapis.com", &Provider{Name: "google", Auth: AuthBearer, HasScopes: true}},
}

// ProviderForHost returns the provider config for a given host, or nil if unknown.
func ProviderForHost(host string) *Provider {
	// Exact match first.
	if p, ok := HostToProvider[host]; ok {
		return p
	}
	if _, p := MatchingSuffix(host); p != nil {
		return p
	}
	return nil
}

// MatchingSuffix returns the SuffixToProvider rule that would catch
// host (suffix string + provider), or ("", nil) if none matches.
// Exposed so callers (e.g. catalog.Register) can detect when a
// proposed exact-match rule would collide with an existing suffix
// rule.
func MatchingSuffix(host string) (string, *Provider) {
	for _, sp := range SuffixToProvider {
		if strings.HasSuffix(host, sp.Suffix) {
			return sp.Suffix, sp.Provider
		}
	}
	return "", nil
}

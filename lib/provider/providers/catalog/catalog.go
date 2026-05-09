// Package catalog implements charon's data-driven Tier-3 provider
// catalog: a YAML-defined registry of API-key services that the
// proxy routes generically (no per-provider Go code) and that the
// TUI surfaces in its add-account flow.
//
// Catalog providers are paste-and-revoke: charon does not mint
// their keys, only attaches them to outbound requests and (when
// the catalog declares a revoke endpoint) deactivates them on
// deletion. See workshop/issues/000015-provider-catalog.md and
// the plan doc for design context.
package catalog

// Catalog is the parsed registry of Tier-3 providers loaded from
// the embedded YAML at startup. Entries are validated up-front;
// the loader rejects malformed catalogs so misconfiguration is a
// startup-time failure, not a request-time one.
type Catalog struct {
	Entries []Entry
}

// Entry is one Tier-3 provider in the catalog.
type Entry struct {
	ID               string   `yaml:"id"`                          // e.g. "anthropic" — stable, lowercase, [a-z0-9_-]+
	Name             string   `yaml:"name"`                        // e.g. "Anthropic" — display name
	SignupURL        string   `yaml:"signup_url"`                  // dashboard signup
	KeyURL           string   `yaml:"key_url"`                     // dashboard "create API key" page
	HostnamePatterns []string `yaml:"hostname_patterns"`           // exact hostnames the proxy routes
	Auth             Auth     `yaml:"auth"`                        // how the pasted key is attached to requests
	Revoke           *Revoke  `yaml:"revoke,omitempty"`            // nil → local-delete only; set → best-effort deactivate
	ConsoleURL       string   `yaml:"console_url,omitempty"`       // shown to user on local-delete when Revoke is nil
	VerifyURL        string   `yaml:"verify_url,omitempty"`        // GET to confirm key works (M5 --verify)
	Notes            string   `yaml:"notes,omitempty"`             // surfaced in TUI / docs
}

// Auth describes how to attach the pasted key to a proxied request.
type Auth struct {
	// Style is one of:
	//   "bearer" — Authorization: <prefix><key> (default prefix "Bearer ")
	//   "header" — <header>: <prefix><key> (custom header name; e.g. x-api-key)
	//   "query"  — append ?<param>=<key> to the URL (e.g. ?key= for AI Studio)
	Style string `yaml:"style"`

	Header       string            `yaml:"header,omitempty"`        // required for style=header
	Prefix       string            `yaml:"prefix,omitempty"`        // optional; defaults per style
	Param        string            `yaml:"param,omitempty"`         // optional for style=query; defaults to "key"
	ExtraHeaders map[string]string `yaml:"extra_headers,omitempty"` // static headers (e.g. anthropic-version)
}

// Revoke describes how to deactivate a pasted key on deletion.
//
// Two shapes are supported:
//
//  1. Direct revoke: Method + URL (with optional {key_id}
//     placeholder filled from the pasted key itself).
//  2. List-then-revoke: ListEndpoint discovers the provider's
//     internal id for a pasted key (matching by partial-key hint),
//     then Method + URL is called with that id.
//
// Anthropic uses shape (2): the org-level keys list returns each
// key with a partial_key_hint suffix; the deactivate endpoint
// requires the discovered key_id, not the pasted material.
//
// Auth for both list and revoke calls is taken from the parent
// Entry's Auth shape with the pasted key as the secret. There's
// no auth_source switch because no current provider needs one
// (all of Anthropic's org-level admin endpoints accept the same
// x-api-key the data plane uses, provided the key has admin
// scope). When a provider arrives that needs a *separate* admin
// credential to revoke a pasted regular key, grow the schema
// with an explicit auth_source: pasted_key | admin_key_ref enum
// and a dispatcher branch — see chunk-2 review's load-bearing
// recommendation in the issue's design notes.
type Revoke struct {
	ListEndpoint *ListEndpoint `yaml:"list_endpoint,omitempty"`

	Method string `yaml:"method"`         // "POST" | "DELETE"
	URL    string `yaml:"url"`            // may contain "{key_id}"
	Body   string `yaml:"body,omitempty"` // JSON body for POST
}

// ListEndpoint configures discovery of a provider-internal key id
// from a pasted key when the revoke URL needs that id.
type ListEndpoint struct {
	URL        string `yaml:"url"`         // GET this with the parent Entry.Auth applied to the pasted key
	KeyMatch   string `yaml:"key_match"`   // "partial_key_hint" — match suffix of pasted key
	ResultPath string `yaml:"result_path"` // path into JSON response, e.g. "data[].id"
}

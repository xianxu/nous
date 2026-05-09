package catalog

import (
	_ "embed"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed catalog.yaml
var seedYAML []byte

// Load parses and validates the embedded catalog. It is the only
// entry point callers should use; the binary's startup wires it
// once and shares the result. Validation failures are returned as
// errors so misconfiguration surfaces at boot, not on first
// request.
func Load() (*Catalog, error) {
	return parse(seedYAML)
}

func parse(data []byte) (*Catalog, error) {
	var entries []Entry
	if err := yaml.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("catalog: yaml unmarshal: %w", err)
	}
	if err := validate(entries); err != nil {
		return nil, err
	}
	return &Catalog{Entries: entries}, nil
}

var (
	idRE = regexp.MustCompile(`^[a-z0-9_-]+$`)
)

func validate(entries []Entry) error {
	if len(entries) == 0 {
		return fmt.Errorf("catalog: must contain at least one entry")
	}
	seenIDs := make(map[string]struct{}, len(entries))
	// Hostname → first id that claimed it, so the error names both the
	// duplicate's source and the previous owner. Catalog YAML is grown
	// via PR; the error makes the conflict obvious to the second PR.
	seenHosts := make(map[string]string, len(entries))
	for i, e := range entries {
		if err := validateEntry(e); err != nil {
			return fmt.Errorf("catalog: entry %d (%q): %w", i, e.ID, err)
		}
		if _, dup := seenIDs[e.ID]; dup {
			return fmt.Errorf("catalog: entry %d: duplicate id %q", i, e.ID)
		}
		seenIDs[e.ID] = struct{}{}
		for _, h := range e.HostnamePatterns {
			if owner, dup := seenHosts[h]; dup {
				return fmt.Errorf("catalog: entry %q claims hostname %q already owned by entry %q", e.ID, h, owner)
			}
			seenHosts[h] = e.ID
		}
	}
	return nil
}

func validateEntry(e Entry) error {
	if e.ID == "" {
		return fmt.Errorf("missing id")
	}
	if !idRE.MatchString(e.ID) {
		return fmt.Errorf("id %q must match [a-z0-9_-]+", e.ID)
	}
	if e.Name == "" {
		return fmt.Errorf("missing name")
	}
	if err := validateHTTPSURL("signup_url", e.SignupURL); err != nil {
		return err
	}
	if err := validateHTTPSURL("key_url", e.KeyURL); err != nil {
		return err
	}
	if e.ConsoleURL != "" {
		if err := validateHTTPSURL("console_url", e.ConsoleURL); err != nil {
			return err
		}
	}
	if e.VerifyURL != "" {
		if err := validateHTTPSURL("verify_url", e.VerifyURL); err != nil {
			return err
		}
	}
	if len(e.HostnamePatterns) == 0 {
		return fmt.Errorf("hostname_patterns must be non-empty")
	}
	for _, h := range e.HostnamePatterns {
		if err := validateHostname(h); err != nil {
			return err
		}
	}
	if err := validateAuth(e.Auth); err != nil {
		return err
	}
	if e.Revoke != nil {
		if err := validateRevoke(*e.Revoke); err != nil {
			return err
		}
	}
	return nil
}

func validateAuth(a Auth) error {
	switch a.Style {
	case "bearer":
		// Prefix optional (defaults to "Bearer "). No header required.
	case "header":
		if a.Header == "" {
			return fmt.Errorf("auth.style=header requires auth.header")
		}
	case "query":
		// Param optional (defaults to "key"). Nothing else required.
	case "":
		return fmt.Errorf("auth.style is required")
	default:
		return fmt.Errorf("auth.style %q not in {bearer,header,query}", a.Style)
	}
	return nil
}

func validateRevoke(r Revoke) error {
	switch r.Method {
	case "POST", "DELETE":
	default:
		return fmt.Errorf("revoke.method %q not in {POST,DELETE}", r.Method)
	}
	if err := validateHTTPSURL("revoke.url", r.URL); err != nil {
		return err
	}
	// {key_id} placeholder in the revoke URL only makes sense when a
	// list_endpoint is configured to discover the id from the pasted
	// key. Without one, M4b's dispatcher would have nothing to fill it
	// with — fail at load time rather than confuse a future operator.
	if strings.Contains(r.URL, "{key_id}") && r.ListEndpoint == nil {
		return fmt.Errorf("revoke.url contains {key_id} but revoke.list_endpoint is unset")
	}
	if r.ListEndpoint != nil {
		if err := validateHTTPSURL("revoke.list_endpoint.url", r.ListEndpoint.URL); err != nil {
			return err
		}
		if r.ListEndpoint.KeyMatch != "partial_key_hint" {
			return fmt.Errorf("revoke.list_endpoint.key_match %q must be \"partial_key_hint\"", r.ListEndpoint.KeyMatch)
		}
		if r.ListEndpoint.ResultPath == "" {
			return fmt.Errorf("revoke.list_endpoint.result_path required when list_endpoint set")
		}
	}
	return nil
}

func validateHTTPSURL(field, raw string) error {
	if raw == "" {
		return fmt.Errorf("%s is required", field)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s parse: %w", field, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%s must be https://", field)
	}
	if u.Host == "" {
		return fmt.Errorf("%s missing host", field)
	}
	return nil
}

var hostnameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

func validateHostname(h string) error {
	if !hostnameRE.MatchString(h) {
		return fmt.Errorf("hostname %q is not a valid lowercase DNS name", h)
	}
	return nil
}

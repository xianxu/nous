package catalog

import (
	"net/http"
	"testing"

	"github.com/xianxu/nous/internal/charon/proxy"
)

func TestEntryToProvider_Bearer(t *testing.T) {
	e := Entry{
		ID:               "groq",
		Auth:             Auth{Style: "bearer"},
		HostnamePatterns: []string{"api.groq.com"},
	}
	p := EntryToProvider(e)
	if p.Auth != proxy.AuthBearer {
		t.Errorf("Auth = %q, want bearer", p.Auth)
	}
	if p.Name != "groq" || p.VaultName() != "groq" {
		t.Errorf("Name/VaultName = %q/%q, want groq/groq", p.Name, p.VaultName())
	}
	if p.HasScopes {
		t.Error("catalog providers must not have HasScopes=true")
	}
}

func TestEntryToProvider_BearerCustomPrefix(t *testing.T) {
	e := Entry{
		ID:   "replicate",
		Auth: Auth{Style: "bearer", Prefix: "Token "},
	}
	p := EntryToProvider(e)
	if p.HeaderPrefix != "Token " {
		t.Errorf("HeaderPrefix = %q, want %q", p.HeaderPrefix, "Token ")
	}
}

func TestEntryToProvider_Header(t *testing.T) {
	e := Entry{
		ID: "anthropic",
		Auth: Auth{
			Style:        "header",
			Header:       "x-api-key",
			ExtraHeaders: map[string]string{"anthropic-version": "2023-06-01"},
		},
	}
	p := EntryToProvider(e)
	if p.Auth != proxy.AuthHeader {
		t.Errorf("Auth = %q, want header", p.Auth)
	}
	if p.HeaderName != "x-api-key" {
		t.Errorf("HeaderName = %q, want x-api-key", p.HeaderName)
	}
	if p.ExtraHeaders["anthropic-version"] != "2023-06-01" {
		t.Errorf("ExtraHeaders[anthropic-version] = %q, want 2023-06-01", p.ExtraHeaders["anthropic-version"])
	}
}

func TestEntryToProvider_Query(t *testing.T) {
	e := Entry{
		ID:   "x",
		Auth: Auth{Style: "query", Param: "api_key"},
	}
	p := EntryToProvider(e)
	if p.Auth != proxy.AuthQuery {
		t.Errorf("Auth = %q, want query", p.Auth)
	}
	if p.HeaderName != "api_key" {
		t.Errorf("HeaderName = %q, want api_key", p.HeaderName)
	}
}

// TestRegister_AddsCatalogHostsAndAttachesAuth exercises the full
// catalog → routing path: register an entry, look it up via
// proxy.ProviderForHost, and confirm InjectAuth applies the right
// header + extras. Restores HostToProvider on cleanup.
func TestRegister_AddsCatalogHostsAndAttachesAuth(t *testing.T) {
	const host = "api.example-catalog-test.com"
	t.Cleanup(func() { delete(proxy.HostToProvider, host) })

	c := &Catalog{Entries: []Entry{
		{
			ID:               "test-cat",
			Name:             "TestCat",
			HostnamePatterns: []string{host},
			Auth: Auth{
				Style:        "header",
				Header:       "x-api-key",
				ExtraHeaders: map[string]string{"x-version": "1"},
			},
		},
	}}
	Register(c)

	p := proxy.ProviderForHost(host)
	if p == nil {
		t.Fatalf("ProviderForHost(%q) = nil after Register", host)
	}
	if p.Name != "test-cat" {
		t.Errorf("Name = %q, want test-cat", p.Name)
	}

	req, _ := http.NewRequest("POST", "https://"+host+"/v1/x", nil)
	if err := p.InjectAuth(req, "tok-123"); err != nil {
		t.Fatalf("InjectAuth: %v", err)
	}
	if got := req.Header.Get("x-api-key"); got != "tok-123" {
		t.Errorf("x-api-key = %q, want tok-123", got)
	}
	if got := req.Header.Get("x-version"); got != "1" {
		t.Errorf("x-version = %q, want 1", got)
	}
}

// TestRegister_DoesNotOverrideCompiledHosts ensures the static
// HostToProvider entries (api.openai.com, etc.) take precedence
// over any catalog entry that names the same host.
func TestRegister_DoesNotOverrideCompiledHosts(t *testing.T) {
	const host = "api.openai.com"
	want := proxy.HostToProvider[host]
	if want == nil {
		t.Fatalf("expected api.openai.com pre-registered")
	}
	// Defensive: snapshot+restore so prior or later tests that mutate
	// this entry can't make this assertion silently meaningless.
	t.Cleanup(func() { proxy.HostToProvider[host] = want })

	c := &Catalog{Entries: []Entry{
		{
			ID:               "fake-openai",
			Name:             "Imposter",
			HostnamePatterns: []string{host},
			Auth:             Auth{Style: "bearer"},
		},
	}}
	Register(c)

	got := proxy.HostToProvider[host]
	if got != want {
		t.Errorf("Register overwrote compiled api.openai.com entry: got %+v", got)
	}
}

// TestRegister_SkipsSuffixCollision ensures a catalog entry whose
// hostname falls under a compiled SuffixToProvider rule (e.g.
// .googleapis.com → google) is silently skipped (with a log line)
// rather than registered as a misleading exact-match shadow.
func TestRegister_SkipsSuffixCollision(t *testing.T) {
	const host = "anything.googleapis.com"
	// Snapshot in case any prior test added an exact-match entry.
	prev, hadPrev := proxy.HostToProvider[host]
	t.Cleanup(func() {
		if hadPrev {
			proxy.HostToProvider[host] = prev
		} else {
			delete(proxy.HostToProvider, host)
		}
	})

	c := &Catalog{Entries: []Entry{
		{
			ID:               "google-shadow",
			Name:             "Imposter",
			HostnamePatterns: []string{host},
			Auth:             Auth{Style: "bearer"},
		},
	}}
	Register(c)

	if got, ok := proxy.HostToProvider[host]; ok && got != prev {
		t.Errorf("Register added exact-match entry %q despite suffix collision: %+v", host, got)
	}
	// Suffix-routed lookup still resolves to google.
	if p := proxy.ProviderForHost(host); p == nil || p.Name != "google" {
		t.Errorf("ProviderForHost(%q) = %+v, want google via suffix", host, p)
	}
}

// TestRegister_RealSeedYAML loads the embedded catalog and confirms
// Anthropic's hostname routes correctly with the right header shape.
func TestRegister_RealSeedYAML(t *testing.T) {
	const host = "api.anthropic.com"
	t.Cleanup(func() { delete(proxy.HostToProvider, host) })

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	Register(c)

	p := proxy.ProviderForHost(host)
	if p == nil {
		t.Fatalf("api.anthropic.com unrouted after Register")
	}
	if p.Name != "anthropic" || p.VaultName() != "anthropic" {
		t.Errorf("Name/Vault = %s/%s, want anthropic/anthropic", p.Name, p.VaultName())
	}

	req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", nil)
	if err := p.InjectAuth(req, "sk-ant-FAKE"); err != nil {
		t.Fatalf("InjectAuth: %v", err)
	}
	if got := req.Header.Get("x-api-key"); got != "sk-ant-FAKE" {
		t.Errorf("x-api-key = %q, want sk-ant-FAKE", got)
	}
	if got := req.Header.Get("anthropic-version"); got != "2023-06-01" {
		t.Errorf("anthropic-version = %q, want 2023-06-01", got)
	}
}

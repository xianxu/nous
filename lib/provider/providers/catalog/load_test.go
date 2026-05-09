package catalog

import (
	"strings"
	"testing"
)

func TestLoad_EmbeddedSeed(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Entries) == 0 {
		t.Fatalf("expected at least one entry, got 0")
	}

	var found *Entry
	for i := range c.Entries {
		if c.Entries[i].ID == "anthropic" {
			found = &c.Entries[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("anthropic entry missing from seed")
	}

	if got, want := found.Auth.Style, "header"; got != want {
		t.Errorf("anthropic auth.style = %q, want %q", got, want)
	}
	if got, want := found.Auth.Header, "x-api-key"; got != want {
		t.Errorf("anthropic auth.header = %q, want %q", got, want)
	}
	if got := found.Auth.ExtraHeaders["anthropic-version"]; got == "" {
		t.Errorf("anthropic-version extra header missing")
	}
	if found.Revoke == nil {
		t.Fatalf("anthropic revoke unset")
	}
	if found.Revoke.ListEndpoint == nil {
		t.Errorf("anthropic revoke.list_endpoint unset")
	}
	if found.VerifyURL == "" {
		t.Errorf("anthropic verify_url unset")
	}
}

func TestParse_RejectsMalformed(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "empty",
			yaml:    "",
			wantErr: "at least one entry",
		},
		{
			name: "missing id",
			yaml: `
- name: Test
  signup_url: https://example.com
  key_url: https://example.com/keys
  hostname_patterns: [api.example.com]
  auth: { style: bearer }
`,
			wantErr: "missing id",
		},
		{
			name: "id with uppercase",
			yaml: `
- id: BadID
  name: x
  signup_url: https://example.com
  key_url: https://example.com/keys
  hostname_patterns: [api.example.com]
  auth: { style: bearer }
`,
			wantErr: "[a-z0-9_-]+",
		},
		{
			name: "duplicate id",
			yaml: `
- id: a
  name: A
  signup_url: https://example.com
  key_url: https://example.com/keys
  hostname_patterns: [api.example.com]
  auth: { style: bearer }
- id: a
  name: A2
  signup_url: https://example.com
  key_url: https://example.com/keys
  hostname_patterns: [api2.example.com]
  auth: { style: bearer }
`,
			wantErr: "duplicate id",
		},
		{
			name: "unknown auth style",
			yaml: `
- id: a
  name: A
  signup_url: https://example.com
  key_url: https://example.com/keys
  hostname_patterns: [api.example.com]
  auth: { style: weird }
`,
			wantErr: "auth.style",
		},
		{
			name: "header style missing header",
			yaml: `
- id: a
  name: A
  signup_url: https://example.com
  key_url: https://example.com/keys
  hostname_patterns: [api.example.com]
  auth: { style: header }
`,
			wantErr: "auth.style=header requires auth.header",
		},
		{
			name: "non-https signup_url",
			yaml: `
- id: a
  name: A
  signup_url: http://example.com
  key_url: https://example.com/keys
  hostname_patterns: [api.example.com]
  auth: { style: bearer }
`,
			wantErr: "must be https",
		},
		{
			name: "empty hostname_patterns",
			yaml: `
- id: a
  name: A
  signup_url: https://example.com
  key_url: https://example.com/keys
  hostname_patterns: []
  auth: { style: bearer }
`,
			wantErr: "hostname_patterns",
		},
		{
			name: "invalid hostname",
			yaml: `
- id: a
  name: A
  signup_url: https://example.com
  key_url: https://example.com/keys
  hostname_patterns: ["NOT A HOST"]
  auth: { style: bearer }
`,
			wantErr: "valid lowercase DNS name",
		},
		{
			name: "revoke method bad",
			yaml: `
- id: a
  name: A
  signup_url: https://example.com
  key_url: https://example.com/keys
  hostname_patterns: [api.example.com]
  auth: { style: bearer }
  revoke:
    method: PUT
    url: https://api.example.com/revoke
`,
			wantErr: "revoke.method",
		},
		{
			name: "duplicate hostname across entries",
			yaml: `
- id: a
  name: A
  signup_url: https://example.com
  key_url: https://example.com/keys
  hostname_patterns: [api.shared.test]
  auth: { style: bearer }
- id: b
  name: B
  signup_url: https://example.com
  key_url: https://example.com/keys
  hostname_patterns: [api.shared.test]
  auth: { style: bearer }
`,
			wantErr: "already owned by entry",
		},
		{
			name: "key_id placeholder without list_endpoint",
			yaml: `
- id: a
  name: A
  signup_url: https://example.com
  key_url: https://example.com/keys
  hostname_patterns: [api.example.com]
  auth: { style: bearer }
  revoke:
    method: POST
    url: https://api.example.com/keys/{key_id}
`,
			wantErr: "{key_id}",
		},
		{
			name: "list_endpoint missing result_path",
			yaml: `
- id: a
  name: A
  signup_url: https://example.com
  key_url: https://example.com/keys
  hostname_patterns: [api.example.com]
  auth: { style: bearer }
  revoke:
    list_endpoint:
      url: https://api.example.com/keys
      key_match: partial_key_hint
    method: POST
    url: https://api.example.com/keys/{key_id}
`,
			wantErr: "result_path",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parse([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestParse_AcceptsMinimalBearer(t *testing.T) {
	yamlSrc := `
- id: minimal
  name: Minimal
  signup_url: https://example.com
  key_url: https://example.com/keys
  hostname_patterns: [api.example.com]
  auth: { style: bearer }
`
	c, err := parse([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(c.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(c.Entries))
	}
	if c.Entries[0].Revoke != nil {
		t.Errorf("revoke = %+v, want nil for minimal entry", c.Entries[0].Revoke)
	}
}

func TestParse_AcceptsQueryStyleDefaults(t *testing.T) {
	yamlSrc := `
- id: q
  name: Q
  signup_url: https://example.com
  key_url: https://example.com/keys
  hostname_patterns: [api.example.com]
  auth: { style: query }
`
	c, err := parse([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Entries[0].Auth.Style != "query" {
		t.Errorf("style = %q, want query", c.Entries[0].Auth.Style)
	}
}

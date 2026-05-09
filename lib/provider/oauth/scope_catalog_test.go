package oauth

import "testing"

func TestResolveGoogleScope(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"gmail.readonly", "https://www.googleapis.com/auth/gmail.readonly"},
		{"calendar.readonly", "https://www.googleapis.com/auth/calendar.readonly"},
		{"drive.readonly", "https://www.googleapis.com/auth/drive.readonly"},
		{"cloud-platform", "https://www.googleapis.com/auth/cloud-platform"},
		{"openid", "openid"},
		{"email", "https://www.googleapis.com/auth/userinfo.email"},
		// Unknown scope passed through.
		{"https://custom.scope/foo", "https://custom.scope/foo"},
		{"unknown.scope", "unknown.scope"},
	}
	for _, tt := range tests {
		got := ResolveGoogleScope(tt.input)
		if got != tt.want {
			t.Errorf("ResolveGoogleScope(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLookupGoogleScope(t *testing.T) {
	// Lookup by short name.
	info := LookupGoogleScope("gmail.readonly")
	if info == nil {
		t.Fatal("expected to find gmail.readonly")
	}
	if info.Description == "" {
		t.Error("expected non-empty description")
	}

	// Lookup by full URL.
	info2 := LookupGoogleScope("https://www.googleapis.com/auth/gmail.readonly")
	if info2 == nil {
		t.Fatal("expected to find by full URL")
	}
	if info.Short != info2.Short {
		t.Error("short name and full URL should resolve to same entry")
	}

	// Unknown returns nil.
	if LookupGoogleScope("nonexistent") != nil {
		t.Error("expected nil for unknown scope")
	}
}

func TestGoogleScopeCatalog_NoDuplicateShortNames(t *testing.T) {
	seen := make(map[string]bool)
	for _, s := range GoogleScopeCatalog {
		if seen[s.Short] {
			t.Errorf("duplicate short name: %q", s.Short)
		}
		seen[s.Short] = true
	}
}

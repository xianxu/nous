package vault

import (
	"encoding/json"
	"testing"
	"time"
)

var baseTime = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func TestIsExpiredAt_EmptyToken(t *testing.T) {
	c := &Credential{AccessToken: ""}
	if !c.IsExpiredAt(baseTime) {
		t.Error("empty access token should be expired")
	}
}

func TestIsExpiredAt_ZeroExpiry(t *testing.T) {
	c := &Credential{AccessToken: "tok"}
	if c.IsExpiredAt(baseTime) {
		t.Error("zero expiry should mean never expires")
	}
	// Still not expired 100 years later.
	if c.IsExpiredAt(baseTime.Add(100 * 365 * 24 * time.Hour)) {
		t.Error("zero expiry should mean never expires, even far in the future")
	}
}

func TestIsExpiredAt_FutureExpiry(t *testing.T) {
	c := &Credential{
		AccessToken: "tok",
		Expiry:      baseTime.Add(10 * time.Minute),
	}
	if c.IsExpiredAt(baseTime) {
		t.Error("token with 10min remaining should not be expired")
	}
}

func TestIsExpiredAt_PastExpiry(t *testing.T) {
	c := &Credential{
		AccessToken: "tok",
		Expiry:      baseTime.Add(-1 * time.Minute),
	}
	if !c.IsExpiredAt(baseTime) {
		t.Error("token expired 1min ago should be expired")
	}
}

func TestIsExpiredAt_ExactlyAtExpiry(t *testing.T) {
	c := &Credential{
		AccessToken: "tok",
		Expiry:      baseTime,
	}
	// At expiry time, the token is within the grace period, so expired.
	if !c.IsExpiredAt(baseTime) {
		t.Error("token at exact expiry should be expired (within grace period)")
	}
}

func TestIsExpiredAt_WithinGracePeriod(t *testing.T) {
	c := &Credential{
		AccessToken: "tok",
		Expiry:      baseTime.Add(20 * time.Second),
	}
	// 20s remaining < 30s grace → expired.
	if !c.IsExpiredAt(baseTime) {
		t.Error("token within 30s grace period should be expired")
	}
}

func TestIsExpiredAt_JustOutsideGracePeriod(t *testing.T) {
	c := &Credential{
		AccessToken: "tok",
		Expiry:      baseTime.Add(31 * time.Second),
	}
	// 31s remaining > 30s grace → not expired.
	if c.IsExpiredAt(baseTime) {
		t.Error("token with 31s remaining should not be expired")
	}
}

func TestCredType_DefaultsToOAuth(t *testing.T) {
	cases := []struct {
		typeField string
		want      string
	}{
		{"", TypeOAuth},
		{TypeOAuth, TypeOAuth},
		{TypeAdminKey, TypeAdminKey},
		{TypeCatalog, TypeCatalog},
	}
	for _, tc := range cases {
		c := &Credential{Type: tc.typeField}
		if got := c.CredType(); got != tc.want {
			t.Errorf("CredType() with Type=%q = %q, want %q", tc.typeField, got, tc.want)
		}
	}
}

// Pre-#13 keychain entries lack the Type discriminator and the
// AdminKey/Catalog payloads. They MUST deserialize unchanged into the
// new shape — anything else breaks rollback safety for users who already
// have OAuth credentials in their keychain.
func TestCredential_LegacyJSON_Deserializes(t *testing.T) {
	legacy := `{
		"provider": "google",
		"account": "user@gmail.com",
		"access_token": "ya29.tok",
		"refresh_token": "1//rfsh",
		"expiry": "2026-01-01T12:00:00Z",
		"scopes": ["https://www.googleapis.com/auth/gmail.readonly"]
	}`
	var c Credential
	if err := json.Unmarshal([]byte(legacy), &c); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if c.Type != "" {
		t.Errorf("legacy Type should be empty, got %q", c.Type)
	}
	if c.CredType() != TypeOAuth {
		t.Errorf("legacy CredType() = %q, want %q", c.CredType(), TypeOAuth)
	}
	if c.RefreshToken != "1//rfsh" || c.AccessToken != "ya29.tok" {
		t.Errorf("legacy oauth fields not preserved: %+v", c)
	}
	if c.AdminKey != nil || c.Catalog != nil {
		t.Errorf("legacy entry should have nil typed payloads, got AdminKey=%v Catalog=%v", c.AdminKey, c.Catalog)
	}
}

func TestCredential_AdminKey_RoundTrip(t *testing.T) {
	c := Credential{
		Type:     TypeAdminKey,
		Provider: "openai",
		Account:  "work-project",
		AdminKey: &AdminKeyData{
			OrgID:       "org-aB3cD4eF5gH6iJ7kL8mN9oP0",
			OrgLabel:    "xianxu@gmail.com",
			OrgName:     "acme-inc",
			ProjectID:   "proj_aB3xY9z",
			ProjectName: "work-project",
			KeyID:       "key_QqW123",
			KeyMaterial: "sk-test-secret",
			CreatedAt:   baseTime,
		},
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Credential
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.CredType() != TypeAdminKey {
		t.Errorf("CredType = %q, want %q", got.CredType(), TypeAdminKey)
	}
	if got.AdminKey == nil {
		t.Fatal("AdminKey should round-trip non-nil")
	}
	if *got.AdminKey != *c.AdminKey {
		t.Errorf("AdminKey diverged after round-trip:\nbefore: %+v\nafter:  %+v", *c.AdminKey, *got.AdminKey)
	}
	// Wrong-payload guard: admin-key creds shouldn't ship OAuth fields.
	if got.RefreshToken != "" || len(got.Scopes) > 0 {
		t.Errorf("admin-key cred leaked OAuth fields: %+v", got)
	}
}

func TestCredential_Catalog_RoundTrip(t *testing.T) {
	c := Credential{
		Type:     TypeCatalog,
		Provider: "groq",
		Account:  "default",
		Catalog: &CatalogData{
			KeyMaterial: "gsk_secret",
			AddedAt:     baseTime,
		},
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Credential
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.CredType() != TypeCatalog {
		t.Errorf("CredType = %q, want %q", got.CredType(), TypeCatalog)
	}
	if got.Catalog == nil || *got.Catalog != *c.Catalog {
		t.Errorf("Catalog diverged after round-trip: %+v vs %+v", got.Catalog, c.Catalog)
	}
}

// New OAuth credentials write Type explicitly so future readers can
// branch on the discriminator without falling back to the empty-string
// legacy path.
func TestCredential_OAuth_RoundTrip(t *testing.T) {
	c := Credential{
		Type:         TypeOAuth,
		Provider:     "google",
		Account:      "user@gmail.com",
		AccessToken:  "ya29.tok",
		RefreshToken: "1//rfsh",
		Expiry:       baseTime,
		Scopes:       []string{"https://www.googleapis.com/auth/gmail.readonly"},
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Credential
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Type != TypeOAuth {
		t.Errorf("Type = %q, want %q", got.Type, TypeOAuth)
	}
	if got.RefreshToken != c.RefreshToken {
		t.Errorf("RefreshToken diverged: %q vs %q", got.RefreshToken, c.RefreshToken)
	}
	if got.AdminKey != nil || got.Catalog != nil {
		t.Errorf("oauth cred should have nil typed payloads, got AdminKey=%v Catalog=%v", got.AdminKey, got.Catalog)
	}
}

func TestIsExpiredAt_TimePasses(t *testing.T) {
	c := &Credential{
		AccessToken: "tok",
		Expiry:      baseTime.Add(5 * time.Minute),
	}

	// t=0: 5min remaining → valid
	if c.IsExpiredAt(baseTime) {
		t.Error("t=0: should be valid")
	}

	// t=+4m: 1min remaining → valid (outside grace)
	if c.IsExpiredAt(baseTime.Add(4 * time.Minute)) {
		t.Error("t=+4m: should be valid")
	}

	// t=+4m31s: 29s remaining → expired (within grace)
	if !c.IsExpiredAt(baseTime.Add(4*time.Minute + 31*time.Second)) {
		t.Error("t=+4m31s: should be expired (within grace)")
	}

	// t=+5m: 0s remaining → expired
	if !c.IsExpiredAt(baseTime.Add(5 * time.Minute)) {
		t.Error("t=+5m: should be expired")
	}

	// t=+6m: -1min → definitely expired
	if !c.IsExpiredAt(baseTime.Add(6 * time.Minute)) {
		t.Error("t=+6m: should be expired")
	}
}

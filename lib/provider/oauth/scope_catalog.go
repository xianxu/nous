package oauth

// ScopeInfo describes a known OAuth scope.
type ScopeInfo struct {
	Scope       string `json:"full"`        // full scope URL (or OIDC short name like "openid")
	Short       string `json:"short"`       // short name for display
	Description string `json:"description"`
	Required    bool   `json:"required"` // structurally required by charon (always granted)
}

// GoogleScopeCatalog lists known Google OAuth scopes.
//
// Note on `email`: Google rewrites the OIDC short scope `email` to its full
// URL form `https://www.googleapis.com/auth/userinfo.email` in token
// responses. We use the full URL here so that round-tripping (request →
// response → keychain → row lookup) matches.
var GoogleScopeCatalog = []ScopeInfo{
	{Scope: "openid", Short: "openid", Description: "OpenID Connect authentication (required)", Required: true},
	{Scope: "https://www.googleapis.com/auth/userinfo.email", Short: "email", Description: "View email address (required)", Required: true},
	{Scope: "https://www.googleapis.com/auth/gmail.readonly", Short: "gmail.readonly", Description: "Read Gmail messages"},
	{Scope: "https://www.googleapis.com/auth/gmail.send", Short: "gmail.send", Description: "Send Gmail messages"},
	{Scope: "https://www.googleapis.com/auth/gmail.modify", Short: "gmail.modify", Description: "Read, send, and manage Gmail"},
	{Scope: "https://www.googleapis.com/auth/calendar.readonly", Short: "calendar.readonly", Description: "Read Google Calendar events"},
	{Scope: "https://www.googleapis.com/auth/calendar", Short: "calendar", Description: "Read and write Google Calendar"},
	{Scope: "https://www.googleapis.com/auth/drive.readonly", Short: "drive.readonly", Description: "Read Google Drive files"},
	{Scope: "https://www.googleapis.com/auth/drive", Short: "drive", Description: "Read and write Google Drive files"},
	{Scope: "https://www.googleapis.com/auth/drive.file", Short: "drive.file", Description: "Access files created by this app"},
	{Scope: "https://www.googleapis.com/auth/spreadsheets.readonly", Short: "spreadsheets.readonly", Description: "Read Google Sheets"},
	{Scope: "https://www.googleapis.com/auth/spreadsheets", Short: "spreadsheets", Description: "Read and write Google Sheets"},
	{Scope: "https://www.googleapis.com/auth/documents.readonly", Short: "docs.readonly", Description: "Read Google Docs"},
	{Scope: "https://www.googleapis.com/auth/documents", Short: "docs", Description: "Read and write Google Docs"},
	{Scope: "https://www.googleapis.com/auth/presentations.readonly", Short: "slides.readonly", Description: "Read Google Slides"},
	{Scope: "https://www.googleapis.com/auth/presentations", Short: "slides", Description: "Read and write Google Slides"},
	{Scope: "https://www.googleapis.com/auth/tasks.readonly", Short: "tasks.readonly", Description: "Read Google Tasks"},
	{Scope: "https://www.googleapis.com/auth/tasks", Short: "tasks", Description: "Read and write Google Tasks"},
	{Scope: "https://www.googleapis.com/auth/contacts.readonly", Short: "contacts.readonly", Description: "Read Google Contacts"},
	{Scope: "https://www.googleapis.com/auth/youtube.readonly", Short: "youtube.readonly", Description: "Read YouTube account"},
	// cloud-platform is broad (covers most GCP APIs). Used here to enable Gemini access via
	// AI Studio (mint API key via apikeys.googleapis.com) and Vertex AI (OAuth bearer to
	// {region}-aiplatform.googleapis.com). Google does not publish narrower scopes for these.
	{Scope: "https://www.googleapis.com/auth/cloud-platform", Short: "cloud-platform", Description: "Gemini API access via AI Studio + Vertex AI (broad: full GCP)"},
}

// googleScopeIndex maps full scope URLs and short names to ScopeInfo.
var googleScopeIndex map[string]*ScopeInfo

func init() {
	googleScopeIndex = make(map[string]*ScopeInfo, len(GoogleScopeCatalog)*2)
	for i := range GoogleScopeCatalog {
		s := &GoogleScopeCatalog[i]
		googleScopeIndex[s.Scope] = s
		googleScopeIndex[s.Short] = s
	}
}

// ResolveGoogleScope resolves a scope string (short name or full URL) to its full URL.
// Returns the input unchanged if not found in the catalog.
func ResolveGoogleScope(s string) string {
	if info, ok := googleScopeIndex[s]; ok {
		return info.Scope
	}
	return s
}

// LookupGoogleScope returns ScopeInfo for a scope (by short name or full URL), or nil.
func LookupGoogleScope(s string) *ScopeInfo {
	return googleScopeIndex[s]
}

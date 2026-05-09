package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

const charonScopeHeader = "X-Charon-Scope"

// ScopeDenial records a scope that was requested but not granted.
type ScopeDenial struct {
	Provider string    `json:"provider"`
	Account  string    `json:"account"`
	Scope    string    `json:"scope"`
	Count    int       `json:"count"`
	LastSeen time.Time `json:"last_seen"`
}

// ScopeTracker tracks scope denials from proxy requests.
// Thread-safe. Entries are bounded and expire after maxAge.
type ScopeTracker struct {
	mu      sync.Mutex
	entries map[string]*ScopeDenial // key: "provider:account:scope"
	maxSize int
	maxAge  time.Duration
	now     func() time.Time
}

// NewScopeTracker creates a tracker with bounded size and expiry.
func NewScopeTracker(maxSize int, maxAge time.Duration) *ScopeTracker {
	return &ScopeTracker{
		entries: make(map[string]*ScopeDenial),
		maxSize: maxSize,
		maxAge:  maxAge,
		now:     time.Now,
	}
}

func denialKey(provider, account, scope string) string {
	return provider + ":" + account + ":" + scope
}

// Track records one or more scope denials.
func (st *ScopeTracker) Track(provider, account string, scopes []string) {
	st.mu.Lock()
	defer st.mu.Unlock()

	now := st.now()
	st.expireLocked(now)

	for _, scope := range scopes {
		key := denialKey(provider, account, scope)
		if d, ok := st.entries[key]; ok {
			d.Count++
			d.LastSeen = now
		} else {
			if len(st.entries) >= st.maxSize {
				st.evictOldestLocked()
			}
			st.entries[key] = &ScopeDenial{
				Provider: provider,
				Account:  account,
				Scope:    scope,
				Count:    1,
				LastSeen: now,
			}
		}
	}
}

// Denials returns current scope denials, optionally filtered by provider and account.
func (st *ScopeTracker) Denials(provider, account string) []ScopeDenial {
	st.mu.Lock()
	defer st.mu.Unlock()

	now := st.now()
	st.expireLocked(now)

	var result []ScopeDenial
	for _, d := range st.entries {
		if provider != "" && d.Provider != provider {
			continue
		}
		if account != "" && d.Account != account {
			continue
		}
		result = append(result, *d)
	}
	return result
}

func (st *ScopeTracker) expireLocked(now time.Time) {
	for key, d := range st.entries {
		if now.Sub(d.LastSeen) > st.maxAge {
			delete(st.entries, key)
		}
	}
}

func (st *ScopeTracker) evictOldestLocked() {
	var oldestKey string
	var oldestTime time.Time
	for key, d := range st.entries {
		if oldestKey == "" || d.LastSeen.Before(oldestTime) {
			oldestKey = key
			oldestTime = d.LastSeen
		}
	}
	if oldestKey != "" {
		delete(st.entries, oldestKey)
	}
}

// findMissingScopes returns scopes in requested that are not in granted,
// after both sides are normalized via `normalize` (typically
// oauth.ResolveGoogleScope, which maps short names like "gmail.readonly"
// to the full URL Google actually issues tokens against). Without
// normalization, an agent declaring a short name would never match a
// credential granted with the full-URL form, returning spurious 407s
// for scopes the user has actually granted.
//
// Returned strings are in their original (un-normalized) form from
// `requested`, so the 407 response shows the agent what it asked for.
// nil normalize means direct string equality.
func findMissingScopes(requested, granted []string, normalize func(string) string) []string {
	if normalize == nil {
		normalize = func(s string) string { return s }
	}
	grantedSet := make(map[string]bool, len(granted))
	for _, s := range granted {
		grantedSet[normalize(s)] = true
	}
	var missing []string
	for _, s := range requested {
		if !grantedSet[normalize(s)] {
			missing = append(missing, s)
		}
	}
	return missing
}

// scopeErrorJSON builds the JSON body for a scope mismatch 407 response.
func scopeErrorJSON(provider, account string, missing []string) string {
	body := struct {
		Error    string   `json:"error"`
		Missing  []string `json:"missing"`
		Account  string   `json:"account"`
		Provider string   `json:"provider"`
		Fix      string   `json:"fix"`
	}{
		Error:    "scope_missing",
		Missing:  missing,
		Account:  account,
		Provider: provider,
		Fix:      "charon auth google grant " + account + " " + strings.Join(missing, " "),
	}
	data, _ := json.Marshal(body)
	return string(data)
}

// HandleDeniedScopes serves the /scopes/denied endpoint for the fix command.
func (st *ScopeTracker) HandleDeniedScopes(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	account := r.URL.Query().Get("account")
	denials := st.Denials(provider, account)
	if denials == nil {
		denials = []ScopeDenial{} // return [] not null
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(denials)
}

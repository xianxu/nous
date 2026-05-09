package tui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/lib/provider/oauth"
	"github.com/xianxu/nous/lib/provider/providers"
	"github.com/xianxu/nous/lib/provider/providers/catalog"
	"github.com/xianxu/nous/lib/provider/providers/gcp"
	"github.com/xianxu/nous/lib/provider/vault"
)

// newAccountAuthedMsg is the result of an OAuth flow kicked off by
// "+ new account" in the picker. cred.Account is the email Google told us
// the user authenticated as.
type newAccountAuthedMsg struct {
	cred *vault.Credential
	err  error
}

type screen int

const (
	screenProvider       screen = iota // top-level provider picker (post-#13 entry point)
	screenPicker                        // OAuth account picker (Google)
	screenScopes                        // OAuth scope view
	screenAuthing                       // OAuth in flight from the picker; ignore picker keys
	screenAdminKeyList                  // admin-key entity list (admin row + projects)
	screenAdminKeyPaste                 // admin-key first-time setup or replace flow
	screenAdminMint                     // mint a new project key (+ optional create-project step)
	screenAdminRevoke                   // revoke confirmation modal (project or admin-key cascade)
	screenAdminKeyDetail                // per-key drill-in (Screen 3b)
	screenGCPSetup                      // Google Cloud project setup (#14 M3)
	screenCatalogPicker                 // Tier-3 catalog provider picker (#15 M2)
	screenCatalogPaste                  // Tier-3 catalog add-account flow (#15 M4)
	screenCatalogAccountList            // Tier-3 catalog per-provider account list (#15 M4b)
	screenCatalogRevoke                 // Tier-3 catalog revoke confirm modal (#15 M4b)
)

// model is the top-level bubbletea model.
type model struct {
	current        screen
	providerPicker providerPickerModel
	picker         pickerModel
	scopes         scopesModel
	adminList      adminKeyListModel
	adminPaste     adminKeyPasteModel
	adminMint      adminMintModel
	adminRevoke    adminRevokeModel
	adminDetail    adminKeyDetailModel
	gcpSetup       gcpSetupModel
	catalogPicker      catalogPickerModel
	catalogPaste       catalogPasteModel
	catalogAccountList catalogAccountListModel
	catalogRevoke      catalogRevokeModel
	catalog            *catalog.Catalog
	// catalogPasteOrigin is the screen that launched the active paste
	// flow — used by catalogPasteCancelMsg to return to either the
	// catalog picker (first-time add) or the per-provider account list
	// (existing-provider "+ add account").
	catalogPasteOrigin screen
	// activeCatalogEntry is set when current==screenCatalogAccountList
	// or screenCatalogRevoke; used to rebuild the account list after
	// a revoke without re-deriving from a stale message.
	activeCatalogEntry catalog.Entry

	vault       vault.Store
	auth        Authenticator
	fetchDenied denialFetcher
	proxyAddr   string // for cache-clear notify after vault writes; "" disables

	// adminProviders is keyed by provider name ("openai", "anthropic").
	// Empty map means no admin-key providers registered — the provider
	// picker still renders, just without those rows.
	adminProviders map[string]providers.Provider
	adminStores    map[string]*providers.AdminKeyStore

	// gcpClientFactory builds a GCP setup client for the given Google
	// account. nil means GCP setup is unavailable (`charon auth`
	// invocations from older code paths that haven't wired the
	// factory) — enter on cloud-platform falls through to a status
	// hint instead of launching the flow.
	gcpClientFactory func(account string) (GCPSetupClient, error)

	// healthCheck probes per-account refresh-token validity for badge
	// surfacing. nil → no checks (older callers, tests). Production
	// wires an adapter over oauth.GoogleProvider.CheckHealth in
	// lib/charoncli's AuthCmd. See nous#15 for the design rationale
	// (active health check at session boundary; can't prevent token
	// death, only detect it early).
	healthCheck AccountHealthChecker

	// activeAdminProvider is the provider whose entity list is on
	// screen when current==screenAdminKeyList. Used for re-rendering
	// the list after a vault/store mutation lands.
	activeAdminProvider string

	// In-session cursor memory across drill-out → drill-in. Saved on
	// back-nav (or other drill-out) and restored when the user re-
	// enters the same screen, so the TUI remembers where they were
	// last time without persisting anything across CLI invocations.
	// Per-key for screens that have multiple instances (admin lists
	// keyed by provider name; catalog account lists keyed by entry
	// id); single int for screens with a unique instance (OAuth
	// picker, since only Google has an OAuth flow today). Maps are
	// init'd in newModel to keep save sites nil-check-free.
	oauthCursor           int
	adminCursors          map[string]int // provider name → cursor
	catalogAccountCursors map[string]int // catalog entry id → cursor

	width, height int

	// exit signals
	exitNote string
	err      error
}

type Option func(*model)

func WithDenialFetcher(f denialFetcher) Option {
	return func(m *model) { m.fetchDenied = f }
}

// WithAuthenticator wires the OAuth dispatch used for apply. Required before
// the scope view can apply changes; without it, apply is a no-op.
func WithAuthenticator(a Authenticator) Option {
	return func(m *model) { m.auth = a }
}

// WithProxyAddr lets the model notify the running charon proxy to flush
// its credential cache after vault writes (apply, revoke). Without this,
// the proxy continues serving stale tokens whose scope set predates the
// most recent OAuth dance, and agents see 407s for scopes the user just
// granted. Empty addr is fine — caller may not be running the proxy.
func WithProxyAddr(addr string) Option {
	return func(m *model) { m.proxyAddr = addr }
}

// WithGCPClientFactory wires the per-account GCP setup client builder
// used when the user triggers Google Cloud project setup from the
// scope view. The factory takes the Google account email and returns
// a client backed by a token supplier that refreshes the OAuth
// credential as needed. Without this option, "manage project" hints
// from the scope view are inert — set it from cmd/charon's authCmd.
func WithGCPClientFactory(f func(account string) (GCPSetupClient, error)) Option {
	return func(m *model) { m.gcpClientFactory = f }
}

// WithAccountHealthChecker wires a per-account refresh-token health
// probe. The TUI calls it during picker construction to surface
// "needs reauth" badges in the provider list and account list.
// Without this option, no badges render — same UX as before nous#15.
//
// Adapter typically lives in cmd/nous or lib/charoncli where
// oauth.GoogleProvider is in scope; the adapter maps oauth's
// HealthState enum to tui.AccountHealth strings.
func WithAccountHealthChecker(f AccountHealthChecker) Option {
	return func(m *model) { m.healthCheck = f }
}

// WithAdminKeyProvider registers an admin-key provider (OpenAI,
// Anthropic). The provider's Name() determines the keychain namespace
// and the picker label; all admin-key providers are auto-paired with
// an AdminKeyStore for the same name. Multiple calls are additive —
// register one provider per call.
func WithAdminKeyProvider(p providers.Provider) Option {
	return func(m *model) {
		if m.adminProviders == nil {
			m.adminProviders = make(map[string]providers.Provider)
			m.adminStores = make(map[string]*providers.AdminKeyStore)
		}
		m.adminProviders[p.Name()] = p
		m.adminStores[p.Name()] = providers.NewAdminKeyStore(p.Name())
	}
}

// notifyProxyCacheClear pings the proxy at proxyAddr to flush its
// in-memory token + account cache. Best-effort; failure means the proxy
// isn't running locally, which is fine.
func (m model) notifyProxyCacheClear() {
	if m.proxyAddr == "" {
		return
	}
	url := fmt.Sprintf("http://%s/cache/clear", m.proxyAddr)
	resp, err := http.Post(url, "", nil)
	if err == nil {
		resp.Body.Close()
	}
}

func newModel(v vault.Store, initialAccount string, opts ...Option) (model, error) {
	m := model{
		vault:                 v,
		adminCursors:          map[string]int{},
		catalogAccountCursors: map[string]int{},
	}
	for _, opt := range opts {
		opt(&m)
	}

	// initialAccount short-circuits the provider picker: it's a
	// pre-#13 escape hatch for "open scope view directly for this
	// google account" used by Run(v, "user@gmail.com", …). Implies
	// the OAuth/Google flow.
	if initialAccount != "" {
		rows, err := loadScopeRows(v, initialAccount, m.fetchDenied)
		if err != nil {
			return model{}, err
		}
		m.scopes = newScopesModel(initialAccount, rows, m.auth)
		m.current = screenScopes
		return m, nil
	}

	// Eager-load the catalog so the "+ add provider" path is instant.
	// Failure here is a build-time bug in the embedded YAML (caught by
	// catalog tests), not a runtime concern — surface as an error so
	// the user notices instead of silently dropping the catalog.
	cat, err := catalog.Load()
	if err != nil {
		return model{}, fmt.Errorf("load catalog: %w", err)
	}
	m.catalog = cat
	m.catalogPicker = newCatalogPickerModel(cat)

	pp, err := newProviderPickerModel(v, m.adminStores, cat)
	if err != nil {
		return model{}, err
	}
	pp.AnnotateHealth(v, m.healthCheck)
	m.providerPicker = pp

	m.current = screenProvider
	return m, nil
}

func (m model) Init() tea.Cmd { return nil }

// Update is a thin wrapper around updateInner that handles two cross-cutting
// concerns:
//   - WindowSizeMsg dimensions are cached on the parent so they can be replayed
//     on screen transitions (see seedSizeCmd).
//   - When updateInner changes m.current, a synthetic tea.WindowSizeMsg is
//     queued so the new screen sees the latest dimensions on its first frame
//     instead of zero. Without this, sub-models that lay out against width or
//     height (today: scopesModel; tomorrow: anything else) render blank/clipped
//     until the user resizes the terminal.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	prev := m.current
	out, cmd := m.updateInner(msg)
	mm := out.(model)
	if mm.current != prev {
		if seed := mm.seedSizeCmd(); seed != nil {
			if cmd == nil {
				return mm, seed
			}
			return mm, tea.Batch(cmd, seed)
		}
	}
	return mm, cmd
}

// seedSizeCmd returns a Cmd that delivers the parent's cached dimensions as a
// tea.WindowSizeMsg on the next tick. Returns nil before the first real
// WindowSizeMsg arrives (m.width or m.height still zero) — there's nothing
// useful to seed yet, and the real message will arrive shortly.
func (m model) seedSizeCmd() tea.Cmd {
	if m.width <= 0 || m.height <= 0 {
		return nil
	}
	w, h := m.width, m.height
	return func() tea.Msg { return tea.WindowSizeMsg{Width: w, Height: h} }
}

func (m model) updateInner(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Cache dimensions then fall through to the current-screen
		// dispatch at the bottom so the active sub-model gets the
		// resize too. Previously only scopes was forwarded, leaving
		// every other screen unable to react to SIGWINCH.

	case providerSelectedMsg:
		return m.handleProviderSelected(msg)

	case addProviderMsg:
		// Re-use the existing catalog picker so cursor is preserved
		// across re-entries — same in-session "remember where I was"
		// principle the rest of the screens follow. The catalog
		// itself is embedded at compile time, so no rebuild needed.
		m.current = screenCatalogPicker
		return m, nil

	case catalogBackMsg:
		return m.refreshProviderPicker()

	case catalogSelectedMsg:
		m.catalogPaste = newCatalogPasteModel(msg.entry, m.vault)
		m.catalogPasteOrigin = screenCatalogPicker
		m.current = screenCatalogPaste
		return m, nil

	case catalogAccountAddMsg:
		// "+ add account" inside an existing catalog provider's
		// account list — re-enter paste flow with the entry pre-set,
		// so the user doesn't have to traverse the catalog picker
		// when they already know the provider.
		m.catalogPaste = newCatalogPasteModel(msg.entry, m.vault)
		m.catalogPasteOrigin = screenCatalogAccountList
		m.current = screenCatalogPaste
		return m, nil

	case catalogPasteCancelMsg:
		// User backed out of the paste flow before storing — return
		// to whichever screen launched the flow. From the catalog
		// picker, that's the picker; from the account list, that's
		// the account list (so an esc-to-rethink doesn't kick the
		// user back to the top-level picker).
		switch m.catalogPasteOrigin {
		case screenCatalogAccountList:
			return m.refreshCatalogAccountList()
		default:
			m.current = screenCatalogPicker
			return m, nil
		}

	case catalogPasteDoneMsg:
		// Successfully stored. Notify the proxy to invalidate any
		// cached lookup (e.g. a stale 407 cache for this provider/
		// account from before the paste). Then bounce to whichever
		// screen launched the flow with a precise "ready to use" hint:
		// from the catalog picker that's the provider picker; from
		// the account list that's the account list (so the user lands
		// on the row they just added).
		m.notifyProxyCacheClear()
		// The verify note (if any) goes inside parentheses right
		// after the stored key, so the eye reads "Stored X/Y
		// (verified) — try: ..." or "Stored X/Y (verify
		// inconclusive: ...) — try: ..." without breaking the
		// command-line example downstream.
		storedFragment := fmt.Sprintf("Stored %s/%s", msg.provider, msg.account)
		if msg.verifyNote != "" {
			storedFragment = fmt.Sprintf("Stored %s/%s (%s)", msg.provider, msg.account, msg.verifyNote)
		}
		hint := fmt.Sprintf(
			"%s — try: charon run -- curl -H \"X-Charon-Account: %s\" https://%s/...",
			storedFragment, msg.account,
			catalogFirstHost(m.catalog, msg.provider),
		)
		if m.catalogPasteOrigin == screenCatalogAccountList {
			updated, cmd := m.refreshCatalogAccountList()
			mm := updated.(model)
			mm.catalogAccountList.statusMsg = hint
			return mm, cmd
		}
		return m.refreshProviderPickerWithStatus(hint)

	case catalogAccountListBackMsg:
		if m.activeCatalogEntry.ID != "" {
			if m.catalogAccountCursors == nil {
				m.catalogAccountCursors = map[string]int{}
			}
			m.catalogAccountCursors[m.activeCatalogEntry.ID] = m.catalogAccountList.cursor
		}
		return m.refreshProviderPicker()

	case catalogRevokeRequestMsg:
		rm, err := newCatalogRevokeModel(msg.entry, msg.account, m.vault)
		if err != nil {
			m.err = err
			return m, tea.Quit
		}
		m.catalogRevoke = rm
		m.activeCatalogEntry = msg.entry
		m.current = screenCatalogRevoke
		return m, nil

	case catalogRevokeDoneMsg:
		// Vault is consistent. Flush proxy cache so any in-flight
		// request for this account gets a fresh 407 instead of
		// trying to inject a now-deleted credential.
		m.notifyProxyCacheClear()
		updated, cmd := m.refreshCatalogAccountList()
		mm := updated.(model)
		// If the list now has only the trailing "+ add account" row,
		// the account that was just revoked was the last one — pop
		// back to the provider picker so the now-empty catalog
		// provider's row disappears.
		if len(mm.catalogAccountList.rows) == 1 && mm.catalogAccountList.rows[0].isAddNew {
			return mm.refreshProviderPickerWithStatus(msg.statusNote)
		}
		mm.catalogAccountList.statusMsg = msg.statusNote
		return mm, cmd

	case catalogRevokeCancelMsg:
		return m.refreshCatalogAccountList()

	case adminKeyListBackMsg:
		// Save cursor for this provider before drilling out so re-
		// entry lands the user back where they were. Lazy-init for
		// tests that construct model{} directly without newModel.
		if m.activeAdminProvider != "" {
			if m.adminCursors == nil {
				m.adminCursors = map[string]int{}
			}
			m.adminCursors[m.activeAdminProvider] = m.adminList.cursor
		}
		return m.refreshProviderPicker()

	case pickerBackMsg:
		m.oauthCursor = m.picker.cursor
		return m.refreshProviderPicker()

	case adminKeyPasteRequestMsg:
		return m.openAdminKeyPaste(msg)

	case adminKeyPasteDoneMsg:
		// Admin key was written — rebuild the entity list so the
		// admin row flips to ●, then return to that screen.
		return m.refreshAdminKeyList()

	case adminKeyPasteCancelMsg:
		// User cancelled the paste flow — return to the entity list
		// without rebuilding (state didn't change).
		m.current = screenAdminKeyList
		return m, nil

	case adminMintRequestMsg:
		return m.openAdminMint(msg)

	case adminMintDoneMsg:
		// New credential was minted + stored — rebuild the entity
		// list so the new project row appears.
		return m.refreshAdminKeyList()

	case adminMintCancelMsg:
		// Cancelled mint — return to the entity list. The cancel msg's
		// StatusNote names any partial-success upstream state (orphan
		// project from CreateProject success + MintKey fail, or orphan
		// minted key from MintKey success + vault.Set fail) so the
		// user can clean up at the provider's dashboard. Empty note
		// means clean cancel before any side effects.
		updated, cmd := m.refreshAdminKeyList()
		mm := updated.(model)
		if msg.StatusNote != "" {
			mm.adminList.statusMsg = msg.StatusNote
		}
		return mm, cmd

	case adminRevokeRequestMsg:
		return m.openAdminRevoke(msg)

	case adminRevokeDoneMsg:
		return m.refreshAdminKeyList()

	case adminRevokeCancelMsg:
		// Coming back from revoke-cancel: if we were on the detail
		// screen before, return there; otherwise the entity list.
		// activeAdminProvider is set in either case.
		m.current = screenAdminKeyList
		return m, nil

	case adminKeyDetailRequestMsg:
		return m.openAdminKeyDetail(msg)

	case adminKeyDetailBackMsg:
		// State could have changed (e.g., a revoke happened from the
		// detail screen and was cancelled — entity list is unchanged
		// but cheaper to just refresh than reason about it).
		return m.refreshAdminKeyList()

	case accountSelectedMsg:
		rows, err := loadScopeRows(m.vault, msg.email, m.fetchDenied)
		if err != nil {
			m.err = err
			return m, tea.Quit
		}
		m.scopes = newScopesModel(msg.email, rows, m.auth)
		m.current = screenScopes
		return m, nil

	case reauthRequestedMsg:
		// Picker fired a reauth for an account. Dispatch auth.Auth with
		// forceFresh=true (preserves granted scope set, gets new tokens).
		// On success the result lands as reauthResultMsg → vault save
		// + picker refresh. On failure: friendly error in statusMsg.
		// nous#15 M3.
		if m.auth == nil {
			m.picker.statusMsg = "no authenticator configured"
			return m, nil
		}
		existingCred, err := m.vault.Get("google", msg.email)
		if err != nil {
			m.picker.statusMsg = fmt.Sprintf("vault lookup failed: %v", err)
			return m, nil
		}
		auth := m.auth
		email := msg.email
		scopes := existingCred.Scopes
		return m, func() tea.Msg {
			fresh, err := auth.Auth(email, scopes, scopes, true)
			return reauthResultMsg{email: email, cred: fresh, err: err}
		}

	case reauthResultMsg:
		if msg.err != nil {
			user, _ := oauth.FriendlyError(msg.err)
			m.picker.statusMsg = fmt.Sprintf("reauth %s: %s", msg.email, user)
			return m, nil
		}
		if msg.cred != nil {
			if err := m.vault.Set(msg.cred); err != nil {
				m.picker.statusMsg = fmt.Sprintf("vault.Set %s: %v", msg.email, err)
				return m, nil
			}
			m.notifyProxyCacheClear()
		}
		// Refresh the picker so the health badge updates.
		newPicker, err := newPickerModel(m.vault)
		if err != nil {
			m.picker.statusMsg = fmt.Sprintf("reauth ok but picker refresh failed: %v", err)
			return m, nil
		}
		newPicker.AnnotateHealth(m.vault, m.healthCheck)
		newPicker.cursor = m.picker.cursor
		if newPicker.cursor >= len(newPicker.items) {
			newPicker.cursor = len(newPicker.items) - 1
		}
		if newPicker.cursor < 0 {
			newPicker.cursor = 0
		}
		newPicker.statusMsg = fmt.Sprintf("reauthenticated %s", msg.email)
		m.picker = newPicker
		return m, nil

	case newAccountMsg:
		// First-time auth: empty scopes (just openid+email via
		// requiredGoogleScopes), no login_hint. The browser opens, user
		// picks the Google account they want, completes consent. Auth
		// returns a credential whose Account is the discovered email.
		if m.current == screenAuthing {
			// A second newAccountMsg can fire if the user mashes Enter
			// before the first OAuth completes — picker's Update is still
			// running. Drop the duplicate.
			return m, nil
		}
		if m.auth == nil {
			m.err = fmt.Errorf("no authenticator configured")
			return m, tea.Quit
		}
		auth := m.auth
		m.current = screenAuthing
		return m, func() tea.Msg {
			cred, err := auth.Auth("", nil, nil, false)
			return newAccountAuthedMsg{cred: cred, err: err}
		}

	case newAccountAuthedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		if err := m.vault.Set(msg.cred); err != nil {
			m.err = err
			return m, tea.Quit
		}
		rows, err := loadScopeRows(m.vault, msg.cred.Account, m.fetchDenied)
		if err != nil {
			m.err = err
			return m, tea.Quit
		}
		m.scopes = newScopesModel(msg.cred.Account, rows, m.auth)
		m.current = screenScopes
		return m, nil

	case gcpSetupRequestMsg:
		return m.openGCPSetup(msg)

	case gcpSetupDoneMsg:
		return m.handleGCPSetupDone(msg)

	case gcpSetupCancelMsg:
		// User cancelled out of GCP setup — return to scope view
		// unchanged. The scope view's row state hasn't moved.
		m.current = screenScopes
		return m, nil

	case scopesQuitMsg:
		// scopesQuitMsg used to terminate the program. With the
		// provider picker as the new top-level, exiting the scope
		// view returns to the OAuth account picker. The user has to
		// `q` from the provider picker (or chain `q`s up the stack)
		// to actually exit. Initial-account mode (skipped the picker)
		// still terminates here.
		if m.current == screenScopes && m.picker.items != nil {
			m.current = screenPicker
			return m, nil
		}
		return m, tea.Quit

	case applyResultMsg:
		// Side effect: persist the new credential before forwarding to scopes.
		// Forwarded message lets scopes update its row state.
		//
		// Guard against account drift: if the user authenticated as a
		// different Google account than what the scope view is editing,
		// surface as an error rather than silently writing a new vault
		// entry under a different key (which would leave the original
		// untouched and confusingly leak rows from the wrong account into
		// the displayed view).
		if msg.err == nil && msg.cred != nil {
			if m.current == screenScopes && m.scopes.account != "" && msg.cred.Account != m.scopes.account {
				msg.err = fmt.Errorf("authenticated as %s, expected %s — original credential left untouched",
					msg.cred.Account, m.scopes.account)
			} else if err := m.vault.Set(msg.cred); err != nil {
				msg.err = err
			} else {
				// Flush the proxy's token cache so the next request uses
				// the freshly-stored credential (with the just-granted
				// scopes). Otherwise the proxy keeps serving the cached
				// pre-grant token and agents see 407 for scopes the user
				// already granted.
				m.notifyProxyCacheClear()
			}
		}
		var cmd tea.Cmd
		m.scopes, cmd = m.scopes.Update(msg)
		return m, cmd

	case revokeAccountMsg:
		// Look up credential, then unwind in reverse order of creation:
		//   1. Revoke the AI Studio key upstream (uses OAuth bearer).
		//   2. Revoke the OAuth refresh token at Google.
		//   3. Delete the local credential entry.
		// Order matters: step 1 needs a valid OAuth token, which step
		// 2 invalidates.
		//
		// On success, rebuild the OAuth picker so the deleted account
		// disappears and route the user back there. Initial-account
		// mode has no picker to return to — falls back to exit. Errors
		// during revoke surface via applyResultMsg → existing
		// apply-error overlay.
		cred, err := m.vault.Get("google", msg.account)
		if err != nil {
			return m, func() tea.Msg {
				return applyResultMsg{err: err}
			}
		}
		// Step 1 — AI Studio key cleanup. Best-effort: a failure
		// here does not block the local delete (user wants this
		// account gone). The status note records partial-success
		// so the user knows whether to manually clean up the key
		// at console.cloud.google.com/apis/credentials.
		aistudioRevoked, aistudioFailed := false, false
		if cred.AIStudio != nil && cred.AIStudio.Name != "" && m.gcpClientFactory != nil {
			if client, ferr := m.gcpClientFactory(msg.account); ferr == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if _, err := client.DeleteAPIKey(ctx, cred.AIStudio.Name); err == nil {
					aistudioRevoked = true
				} else {
					aistudioFailed = true
				}
				cancel()
			}
		}
		// Track whether Google actually revoked the token here, vs. the
		// token being already-invalid on Google's side. Either way we
		// proceed to delete the local entry — the user wants this
		// account *gone* — but the status note is honest about what
		// happened upstream.
		alreadyRevoked := false
		if m.auth != nil {
			err := m.auth.Revoke(cred.RefreshToken)
			switch {
			case err == nil:
				// Revoked at Google.
			case errors.Is(err, oauth.ErrAlreadyRevoked):
				// Token was already revoked or never valid (e.g. user
				// revoked via myaccount.google.com/permissions before
				// reaching us). Local cleanup still wanted.
				alreadyRevoked = true
			default:
				return m, func() tea.Msg {
					return applyResultMsg{err: err}
				}
			}
		}
		if err := m.vault.Delete("google", msg.account); err != nil {
			return m, func() tea.Msg {
				return applyResultMsg{err: err}
			}
		}
		m.notifyProxyCacheClear() // proxy must drop the now-revoked token

		var note string
		switch {
		case alreadyRevoked:
			note = "Removed " + msg.account + " (already revoked on Google's side)"
		default:
			note = "Revoked and removed " + msg.account
		}
		switch {
		case aistudioRevoked:
			note += "; AI Studio key revoked"
		case aistudioFailed:
			note += "; AI Studio key may still exist upstream — clean up at console.cloud.google.com/apis/credentials"
		}

		// Initial-account short-circuit: there's no picker stack to
		// return to. Preserve the original exit-on-revoke behavior.
		if m.picker.items == nil {
			m.exitNote = note
			return m, tea.Quit
		}

		// Rebuild the OAuth picker so the revoked account is gone.
		// Preserve the cursor position (clamped to new bounds) per
		// chunk-2 review finding #5 — stay at the same row index;
		// the row may now show a different account.
		prevCursor := m.picker.cursor
		newPicker, err := newPickerModel(m.vault)
		if err != nil {
			m.err = err
			return m, tea.Quit
		}
		newPicker.AnnotateHealth(m.vault, m.healthCheck)
		if prevCursor >= len(newPicker.items) {
			prevCursor = len(newPicker.items) - 1
		}
		if prevCursor < 0 {
			prevCursor = 0
		}
		newPicker.cursor = prevCursor
		newPicker.statusMsg = note
		m.picker = newPicker
		m.current = screenPicker
		return m, nil
	}

	switch m.current {
	case screenProvider:
		var cmd tea.Cmd
		m.providerPicker, cmd = m.providerPicker.Update(msg)
		return m, cmd
	case screenCatalogPicker:
		var cmd tea.Cmd
		m.catalogPicker, cmd = m.catalogPicker.Update(msg)
		return m, cmd
	case screenCatalogPaste:
		var cmd tea.Cmd
		m.catalogPaste, cmd = m.catalogPaste.Update(msg)
		return m, cmd
	case screenCatalogAccountList:
		var cmd tea.Cmd
		m.catalogAccountList, cmd = m.catalogAccountList.Update(msg)
		return m, cmd
	case screenCatalogRevoke:
		var cmd tea.Cmd
		m.catalogRevoke, cmd = m.catalogRevoke.Update(msg)
		return m, cmd
	case screenPicker:
		var cmd tea.Cmd
		m.picker, cmd = m.picker.Update(msg)
		return m, cmd
	case screenAdminKeyList:
		var cmd tea.Cmd
		m.adminList, cmd = m.adminList.Update(msg)
		return m, cmd
	case screenAdminKeyPaste:
		var cmd tea.Cmd
		m.adminPaste, cmd = m.adminPaste.Update(msg)
		return m, cmd
	case screenAdminMint:
		var cmd tea.Cmd
		m.adminMint, cmd = m.adminMint.Update(msg)
		return m, cmd
	case screenAdminRevoke:
		var cmd tea.Cmd
		m.adminRevoke, cmd = m.adminRevoke.Update(msg)
		return m, cmd
	case screenAdminKeyDetail:
		var cmd tea.Cmd
		m.adminDetail, cmd = m.adminDetail.Update(msg)
		return m, cmd
	case screenAuthing:
		// Block all picker/scopes input while OAuth is in flight; only
		// ctrl+c reaches us here so the user can still abort the program.
		if k, ok := msg.(tea.KeyMsg); ok && k.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	case screenScopes:
		var cmd tea.Cmd
		m.scopes, cmd = m.scopes.Update(msg)
		return m, cmd
	case screenGCPSetup:
		var cmd tea.Cmd
		m.gcpSetup, cmd = m.gcpSetup.Update(msg)
		return m, cmd
	}
	return m, nil
}

// openGCPSetup constructs a GCP setup model for the requested account
// and routes the screen there. If no factory is wired (factory is
// optional), drop a status hint into the scope view and stay there.
//
// The currently-configured GCP project (cred.GCP) is passed as a pin
// so it always appears in the picker even when Google's projects.list
// hasn't propagated a recent create yet.
func (m model) openGCPSetup(req gcpSetupRequestMsg) (tea.Model, tea.Cmd) {
	if m.gcpClientFactory == nil {
		m.scopes.applyStatus = "GCP setup not wired in this build — run 'charon gcp setup " + req.account + "' from the shell."
		return m, nil
	}
	client, err := m.gcpClientFactory(req.account)
	if err != nil {
		m.scopes.applyStatus = fmt.Sprintf("GCP setup unavailable: %v", err)
		return m, nil
	}
	var pinned *gcp.Project
	hasAIStudio := false
	if cred, err := m.vault.Get("google", req.account); err == nil {
		if cred.GCP != nil {
			pinned = &gcp.Project{
				ProjectID:      cred.GCP.ProjectID,
				Name:           cred.GCP.ProjectName,
				LifecycleState: "ACTIVE",
			}
		}
		hasAIStudio = cred.AIStudio != nil
	}
	gs := newGCPSetupModel(client, req.account, pinned, hasAIStudio)
	m.gcpSetup = gs
	m.current = screenGCPSetup
	return m, gs.initCmd()
}

// handleGCPSetupDone persists the result onto the existing OAuth
// credential and returns to the scope view. Errors during persistence
// surface as a scope-view status; the upstream GCP state already
// landed (project created, APIs enabled, etc.) so the user can
// retry without losing that work.
func (m model) handleGCPSetupDone(msg gcpSetupDoneMsg) (tea.Model, tea.Cmd) {
	cred, err := m.vault.Get("google", msg.account)
	if err != nil {
		m.scopes.applyStatus = fmt.Sprintf("GCP setup completed upstream but persistence failed: %v", err)
		m.current = screenScopes
		return m, nil
	}
	cred.GCP = &vault.GCPData{
		ProjectID:       msg.projectID,
		ProjectName:     msg.projectName,
		VertexRegion:    msg.region,
		CreatedByCharon: msg.createdNew,
		BillingEnabled:  msg.billing,
		UpdatedAt:       time.Now().UTC(),
	}
	if msg.aiStudio != nil {
		cred.AIStudio = &vault.AIStudioData{
			Name:        msg.aiStudio.Name,
			UID:         msg.aiStudio.UID,
			DisplayName: msg.aiStudio.DisplayName,
			KeyMaterial: msg.aiStudio.KeyString,
			ProjectID:   msg.projectID,
			CreatedAt:   time.Now().UTC(),
		}
	}
	if err := m.vault.Set(cred); err != nil {
		m.scopes.applyStatus = fmt.Sprintf("GCP setup completed upstream but vault.Set failed: %v", err)
		m.current = screenScopes
		return m, nil
	}
	m.notifyProxyCacheClear()
	switch {
	case msg.aiStudioErr != "":
		// Mint failed but everything else landed. Surface the error
		// persistently in the scope picker so the user can act —
		// the in-flow notice flashed by too quickly to read.
		m.scopes.applyStatus = fmt.Sprintf("Stored project %s (region: %s). AI Studio mint failed: %s", msg.projectID, msg.region, msg.aiStudioErr)
	case msg.aiStudio != nil:
		m.scopes.applyStatus = fmt.Sprintf("Stored project %s (region: %s) and minted AI Studio key %s.", msg.projectID, msg.region, msg.aiStudio.UID)
	default:
		m.scopes.applyStatus = fmt.Sprintf("Stored project %s (region: %s).", msg.projectID, msg.region)
	}
	m.current = screenScopes
	return m, nil
}

// handleProviderSelected routes from the provider picker to the
// per-type screen for the chosen provider. OAuth → existing
// pickerModel; admin-key → adminKeyListModel; catalog → account
// list. Each sub-screen is rebuilt fresh from the vault (so
// vault changes since last visit are reflected) but the cursor
// is restored from the model's per-screen memory so re-entry
// lands the user where they last were.
func (m model) handleProviderSelected(msg providerSelectedMsg) (tea.Model, tea.Cmd) {
	switch msg.provType {
	case vault.TypeOAuth:
		// Today only Google has an OAuth flow registered. Build (or
		// rebuild) the OAuth account picker.
		p, err := newPickerModel(m.vault)
		if err != nil {
			m.err = err
			return m, tea.Quit
		}
		p.AnnotateHealth(m.vault, m.healthCheck)
		p.cursor = clampCursor(m.oauthCursor, len(p.items))
		m.picker = p
		m.current = screenPicker
		return m, nil
	case vault.TypeAdminKey:
		store := m.adminStores[msg.name]
		l, err := newAdminKeyListModel(msg.name, m.vault, store)
		if err != nil {
			m.err = err
			return m, tea.Quit
		}
		l.cursor = clampCursor(m.adminCursors[msg.name], len(l.rows))
		m.adminList = l
		m.activeAdminProvider = msg.name
		m.current = screenAdminKeyList
		return m, nil
	case vault.TypeCatalog:
		entry, ok := lookupCatalogEntry(m.catalog, msg.name)
		if !ok {
			m.err = fmt.Errorf("catalog entry %q not loaded — picker shouldn't have shown the row", msg.name)
			return m, tea.Quit
		}
		l, err := newCatalogAccountListModel(entry, m.vault)
		if err != nil {
			m.err = err
			return m, tea.Quit
		}
		l.cursor = clampCursor(m.catalogAccountCursors[entry.ID], len(l.rows))
		m.catalogAccountList = l
		m.activeCatalogEntry = entry
		m.current = screenCatalogAccountList
		return m, nil
	}
	return m, nil
}

// clampCursor returns desired clamped to [0, max-1]. max==0 yields
// 0; negative desired yields 0. Centralizes the boilerplate that
// would otherwise repeat at every cursor-restore site.
func clampCursor(desired, max int) int {
	if max <= 0 {
		return 0
	}
	if desired >= max {
		return max - 1
	}
	if desired < 0 {
		return 0
	}
	return desired
}

// refreshCatalogAccountList rebuilds the active catalog provider's
// account list (after a paste or revoke) and routes the screen.
func (m model) refreshCatalogAccountList() (tea.Model, tea.Cmd) {
	if m.activeCatalogEntry.ID == "" {
		// No active catalog entry — fall back to provider picker.
		// Defensive: shouldn't happen since catalog flows are only
		// reachable through the picker.
		return m.refreshProviderPicker()
	}
	prevCursor := m.catalogAccountList.cursor
	l, err := newCatalogAccountListModel(m.activeCatalogEntry, m.vault)
	if err != nil {
		m.err = err
		return m, tea.Quit
	}
	if prevCursor >= len(l.rows) {
		prevCursor = len(l.rows) - 1
	}
	if prevCursor < 0 {
		prevCursor = 0
	}
	l.cursor = prevCursor
	m.catalogAccountList = l
	m.current = screenCatalogAccountList
	return m, nil
}

// lookupCatalogEntry returns the catalog entry with the given id, or
// false when the catalog is nil or the id is unknown.
func lookupCatalogEntry(c *catalog.Catalog, id string) (catalog.Entry, bool) {
	if c == nil {
		return catalog.Entry{}, false
	}
	for _, e := range c.Entries {
		if e.ID == id {
			return e, true
		}
	}
	return catalog.Entry{}, false
}

// refreshProviderPicker rebuilds the provider picker (state may have
// changed: an OAuth account was added, an admin key was set, etc.)
// and returns to that screen. Called when navigating back from any
// per-provider sub-screen.
func (m model) refreshProviderPicker() (tea.Model, tea.Cmd) {
	return m.refreshProviderPickerWithStatus("")
}

// refreshProviderPickerWithStatus rebuilds the provider picker and
// drops a transient status note onto it (cleared by the user's next
// keystroke per the picker's existing behavior). Used to surface
// hints back from sub-screens — e.g. the M2 catalog-picked stub
// pointing at the CLI shortcut until M4 lands.
//
// Preserves the previous cursor index across rebuild (clamped to
// new bounds) so esc-back lands the user on the row they entered
// from. Same convention as refreshAdminKeyList and
// refreshCatalogAccountList.
func (m model) refreshProviderPickerWithStatus(status string) (tea.Model, tea.Cmd) {
	prevCursor := m.providerPicker.cursor
	pp, err := newProviderPickerModel(m.vault, m.adminStores, m.catalog)
	if err != nil {
		m.err = err
		return m, tea.Quit
	}
	pp.AnnotateHealth(m.vault, m.healthCheck)
	if prevCursor >= len(pp.items) {
		prevCursor = len(pp.items) - 1
	}
	if prevCursor < 0 {
		prevCursor = 0
	}
	pp.cursor = prevCursor
	pp.statusMsg = status
	m.providerPicker = pp
	m.current = screenProvider
	return m, nil
}

// openAdminKeyPaste constructs the paste flow for the active admin
// provider and routes the screen. Replace mode is determined by the
// caller (entity list) — model just plumbs.
func (m model) openAdminKeyPaste(req adminKeyPasteRequestMsg) (tea.Model, tea.Cmd) {
	provider := m.adminProviders[req.provider]
	if provider == nil {
		m.err = fmt.Errorf("no admin-key provider registered for %q", req.provider)
		return m, tea.Quit
	}
	store := m.adminStores[req.provider]
	m.adminPaste = newAdminKeyPasteModel(req.provider, provider, store, m.vault, req.isReplace, req.existingOrgID)
	m.activeAdminProvider = req.provider
	m.current = screenAdminKeyPaste
	return m, nil
}

// openAdminMint constructs the mint flow. The entity list only emits
// adminMintRequestMsg when the admin key is set, so we don't have to
// re-validate that here — but newAdminMintModel reads the admin key
// from the store and would surface a hard error if it disappeared in
// the gap.
func (m model) openAdminMint(req adminMintRequestMsg) (tea.Model, tea.Cmd) {
	provider := m.adminProviders[req.provider]
	store := m.adminStores[req.provider]
	if provider == nil || store == nil {
		m.err = fmt.Errorf("no admin-key provider registered for %q", req.provider)
		return m, tea.Quit
	}
	mm, err := newAdminMintModel(req.provider, provider, store, m.vault)
	if err != nil {
		m.err = err
		return m, tea.Quit
	}
	m.adminMint = mm
	m.activeAdminProvider = req.provider
	m.current = screenAdminMint
	return m, nil
}

// openAdminKeyDetail constructs the per-key detail screen.
// Errors at construction surface as model.err + tea.Quit because
// the entity list only emits the request for visible key rows.
func (m model) openAdminKeyDetail(req adminKeyDetailRequestMsg) (tea.Model, tea.Cmd) {
	d, err := newAdminKeyDetailModel(req.provider, m.vault, req.account)
	if err != nil {
		m.err = err
		return m, tea.Quit
	}
	m.adminDetail = d
	m.activeAdminProvider = req.provider
	m.current = screenAdminKeyDetail
	return m, nil
}

// openAdminRevoke constructs the revoke confirm modal for either a
// minted project credential or the admin key (cascade). Errors at
// construction (e.g. credential not found in vault, admin key meta
// corrupt) surface as model.err + tea.Quit, since the entity list
// only ever emits adminRevokeRequestMsg for visible rows — anything
// else is a real bug.
func (m model) openAdminRevoke(req adminRevokeRequestMsg) (tea.Model, tea.Cmd) {
	provider := m.adminProviders[req.provider]
	store := m.adminStores[req.provider]
	if store == nil {
		m.err = fmt.Errorf("no admin-key store for %q", req.provider)
		return m, tea.Quit
	}

	var rm adminRevokeModel
	var err error
	switch req.target {
	case revokeProject:
		if provider == nil {
			m.err = fmt.Errorf("no admin-key provider for %q", req.provider)
			return m, tea.Quit
		}
		rm, err = newProjectRevokeModel(req.provider, provider, store, m.vault, req.account)
	case revokeAdminKey:
		rm, err = newAdminKeyRevokeModel(req.provider, store, m.vault)
	}
	if err != nil {
		m.err = err
		return m, tea.Quit
	}
	m.adminRevoke = rm
	m.activeAdminProvider = req.provider
	m.current = screenAdminRevoke
	return m, nil
}

// refreshAdminKeyList rebuilds the admin entity list for the active
// admin provider after a state change (admin key set / replaced /
// cascade-deleted, mint, revoke) and routes the screen.
//
// All admin-key mutations route through here, so this is also the
// chokepoint for flushing the running proxy's token + account caches
// — those caches hold the now-revoked or now-replaced KeyMaterial
// keyed by `provider:account`, and without an explicit flush the
// proxy will keep injecting dead bytes until upstream 401 evicts
// them. See chunk-2 review finding #1.
func (m model) refreshAdminKeyList() (tea.Model, tea.Cmd) {
	m.notifyProxyCacheClear()

	if m.activeAdminProvider == "" {
		// No active admin provider — fall back to provider picker.
		// Defensive: shouldn't happen since the paste flow is only
		// reachable when an admin provider is active.
		return m.refreshProviderPicker()
	}
	store := m.adminStores[m.activeAdminProvider]
	prevCursor := m.adminList.cursor
	l, err := newAdminKeyListModel(m.activeAdminProvider, m.vault, store)
	if err != nil {
		m.err = err
		return m, tea.Quit
	}
	// Preserve cursor position across rebuild — clamp to new bounds.
	// "Stay at the same row index" is the conventional behavior; a
	// row may now show a different key, but the cursor doesn't jump
	// to the top. See chunk-2 review finding #5.
	if prevCursor >= len(l.rows) {
		prevCursor = len(l.rows) - 1
	}
	if prevCursor < 0 {
		prevCursor = 0
	}
	l.cursor = prevCursor
	m.adminList = l
	m.current = screenAdminKeyList
	return m, nil
}

func (m model) View() string {
	switch m.current {
	case screenProvider:
		return m.providerPicker.View()
	case screenCatalogPicker:
		return m.catalogPicker.View()
	case screenCatalogPaste:
		return m.catalogPaste.View()
	case screenCatalogAccountList:
		return m.catalogAccountList.View()
	case screenCatalogRevoke:
		return m.catalogRevoke.View()
	case screenPicker:
		return m.picker.View()
	case screenAdminKeyList:
		return m.adminList.View()
	case screenAdminKeyPaste:
		return m.adminPaste.View()
	case screenAdminMint:
		return m.adminMint.View()
	case screenAdminRevoke:
		return m.adminRevoke.View()
	case screenAdminKeyDetail:
		return m.adminDetail.View()
	case screenAuthing:
		return "\nAuthenticating with Google...\n\n" +
			"  A browser window should have opened for OAuth.\n" +
			"  Complete the consent flow there. (ctrl+c to abort)\n"
	case screenScopes:
		return m.scopes.View()
	case screenGCPSetup:
		return m.gcpSetup.View()
	}
	return ""
}

// catalogFirstHost returns the first hostname pattern declared by
// the catalog entry with the given provider id, or a placeholder
// when the catalog is unavailable. Used to craft a precise "ready
// to use" hint after a successful paste.
func catalogFirstHost(c *catalog.Catalog, provider string) string {
	if c == nil {
		return "<provider-host>"
	}
	for _, e := range c.Entries {
		if e.ID == provider && len(e.HostnamePatterns) > 0 {
			return e.HostnamePatterns[0]
		}
	}
	return "<provider-host>"
}

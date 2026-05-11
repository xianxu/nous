package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/lib/provider/oauth"
	"github.com/xianxu/nous/lib/provider/vault"
	"golang.org/x/term"
)

// cloudPlatformScope is the full URL form of the Google Cloud
// "all GCP APIs" scope. The scope view treats a realized row with
// this URL specially: enter triggers the GCP project setup flow
// (#14 M3) instead of the default apply/quit.
const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// scopeRow is one displayable row in the scope view.
type scopeRow struct {
	short           string
	full            string
	description     string
	realized        bool
	initialRealized bool // realized state when the TUI loaded; used for the session +/- marker
	target          bool
	requested       bool
	custom          bool // not in static catalog (came from keychain or proxy)
	required        bool // structurally required — target is forced true, not togglable
}

// Authenticator is the OAuth dispatch the scope view uses to apply target
// state. Production wires *oauth.GoogleProvider; tests inject stubs.
//
// forceFresh on Auth: false for additive (incremental) flows, true for
// reductive flows where the issued token must be scoped exactly to what
// was requested (not unioned with previously-granted scopes).
type Authenticator interface {
	Auth(account string, scopes, existingScopes []string, forceFresh bool) (*vault.Credential, error)
	Revoke(refreshToken string) error
}

// denialFetcher returns scopes denied for the given account. Best-effort: an
// unreachable proxy must return (nil, nil), not an error.
type denialFetcher func(account string) []string

// httpDenialFetcher queries proxy at addr for /scopes/denied.
func httpDenialFetcher(addr string) denialFetcher {
	return func(account string) []string {
		u := fmt.Sprintf("http://%s/scopes/denied?provider=google&account=%s",
			addr, url.QueryEscape(account))
		client := http.Client{Timeout: 1 * time.Second}
		resp, err := client.Get(u)
		if err != nil {
			return nil
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil
		}
		var denials []struct {
			Scope string `json:"scope"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&denials); err != nil {
			return nil
		}
		out := make([]string, 0, len(denials))
		for _, d := range denials {
			out = append(out, d.Scope)
		}
		return out
	}
}

func loadScopeRows(v vault.Store, account string, fetchDenied denialFetcher) ([]scopeRow, error) {
	cred, err := v.Get("google", account)
	granted := map[string]bool{}
	if err == nil {
		for _, s := range cred.Scopes {
			granted[s] = true
		}
	}

	rows := make([]scopeRow, 0, len(oauth.GoogleScopeCatalog))
	seen := map[string]bool{}
	for _, info := range oauth.GoogleScopeCatalog {
		realized := granted[info.Scope]
		// Required scopes are force-targeted on, since charon will always
		// include them in the next Auth request regardless of user toggles.
		target := realized
		if info.Required {
			target = true
		}
		rows = append(rows, scopeRow{
			short:           info.Short,
			full:            info.Scope,
			description:     info.Description,
			realized:        realized,
			initialRealized: realized,
			target:          target,
			required:        info.Required,
		})
		seen[info.Scope] = true
	}
	if cred != nil {
		extras := make([]string, 0)
		for _, s := range cred.Scopes {
			if !seen[s] {
				extras = append(extras, s)
				seen[s] = true
			}
		}
		sort.Strings(extras)
		for _, s := range extras {
			rows = append(rows, scopeRow{
				short:           customShortName(s),
				full:            s,
				description:     "(custom scope)",
				realized:        true,
				initialRealized: true,
				target:           true,
				custom:           true,
			})
		}
	}
	if fetchDenied != nil {
		denied := fetchDenied(account)
		// Agents may declare scopes via short name ("gmail.readonly") or
		// full URL; the proxy records denials in whatever form was
		// declared. Canonicalize through the catalog so the denial maps
		// to the same row regardless of declaration form.
		denialSet := map[string]bool{}
		for _, s := range denied {
			denialSet[oauth.ResolveGoogleScope(s)] = true
		}
		for i := range rows {
			if denialSet[rows[i].full] {
				rows[i].requested = true
				delete(denialSet, rows[i].full)
			}
		}
		extras := make([]string, 0, len(denialSet))
		for s := range denialSet {
			extras = append(extras, s)
		}
		sort.Strings(extras)
		for _, s := range extras {
			rows = append(rows, scopeRow{
				short:       customShortName(s),
				full:        s,
				description: "(requested by proxy)",
				requested:   true,
				custom:      true,
			})
		}
	}
	sortGrantedFirst(rows)
	return rows, nil
}

// sortGrantedFirst stable-sorts rows so realized (granted) scopes appear
// before non-granted ones, preserving catalog order within each group.
// Stable preserves construction order, which mirrors GoogleScopeCatalog
// for catalog rows and append-order for custom rows.
func sortGrantedFirst(rows []scopeRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].realized && !rows[j].realized
	})
}

func customShortName(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 && i < len(s)-1 {
		return s[i+1:]
	}
	return s
}

func (r scopeRow) matches(filter string) bool {
	if filter == "" {
		return true
	}
	f := strings.ToLower(filter)
	return strings.Contains(strings.ToLower(r.short), f) ||
		strings.Contains(strings.ToLower(r.description), f)
}

type scopesFocus int

const (
	focusSearch scopesFocus = iota
	focusList
)

type scopesState int

const (
	stateNormal scopesState = iota
	stateAddCustom
	stateApplying
	stateApplyError
	stateQuitConfirm
	stateReduceConfirm // confirming a reductive apply
	stateRevokeConfirm // confirming "R: revoke account entirely"
)

type scopesModel struct {
	account        string
	rows           []scopeRow
	cursor         int
	filtered       []int
	windowStart    int // first visible row index in m.filtered
	height         int // effective terminal height; 0 means render all rows
	heightOverride int // CHARON_TUI_HEIGHT; if > 0, used in place of WindowSizeMsg
	search         textinput.Model
	custom         textinput.Model
	focus          scopesFocus
	state          scopesState
	applyErr       error
	applyStatus    string // transient message shown after success
	auth           Authenticator
	health         AccountHealth // refresh-token state stamped at view-entry; "" = unchecked
}

// reservedLines is the fixed chrome around the row list:
// header (1) + separator (1) + search (1) + ↑more (1, always emitted) +
// ↓more (1, always emitted) + blank-before-help (1) + status (1, always
// emitted, blank when empty) + help (1) = 8.
// No trailing newline — that would scroll the alt-screen on writers that
// strictly observe rows.
const reservedLines = 8

func newScopesModel(account string, rows []scopeRow, auth Authenticator) scopesModel {
	search := textinput.New()
	search.Placeholder = "filter (substring)"
	search.Prompt = "/ "
	search.CharLimit = 64
	search.Width = 40
	search.Focus()

	custom := textinput.New()
	custom.Placeholder = "https://www.googleapis.com/auth/..."
	custom.Prompt = "  url> "
	custom.CharLimit = 256
	custom.Width = 60

	m := scopesModel{
		account: account,
		rows:    rows,
		search:  search,
		custom:  custom,
		focus:   focusSearch,
		auth:    auth,
	}
	// Seed height from ioctl. bubbletea's initial WindowSizeMsg arrives
	// shortly and updates this; the seed just covers the very first
	// View() call before that message lands. If the terminal multiplexer
	// (cmux) doesn't update PTY size on pane resize, use
	// CHARON_TUI_HEIGHT to override.
	for _, fd := range []uintptr{os.Stdin.Fd(), os.Stdout.Fd(), os.Stderr.Fd()} {
		if w, h, err := term.GetSize(int(fd)); err == nil && h > 0 {
			m.height = h
			debugf("newScopesModel: term.GetSize(fd=%d) -> w=%d h=%d", fd, w, h)
			break
		} else {
			debugf("newScopesModel: term.GetSize(fd=%d) failed: %v", fd, err)
		}
	}
	// Manual override: terminals (iTerm tabs, tmux panes) sometimes report
	// the parent window height rather than the actual visible area. If the
	// detected height doesn't match what the user sees, they can set
	// CHARON_TUI_HEIGHT=<rows> to override. The override is sticky — it
	// also wins over later WindowSizeMsg events.
	if env := os.Getenv("CHARON_TUI_HEIGHT"); env != "" {
		if n, err := strconv.Atoi(env); err == nil && n > 0 {
			debugf("newScopesModel: CHARON_TUI_HEIGHT override %d -> %d", m.height, n)
			m.heightOverride = n
			m.height = n
		}
	}
	debugf("newScopesModel done: height=%d, total rows=%d", m.height, len(rows))
	m.recomputeFiltered()
	return m
}

func (m *scopesModel) recomputeFiltered() {
	m.filtered = m.filtered[:0]
	q := m.search.Value()
	for i, r := range m.rows {
		if r.matches(q) {
			m.filtered = append(m.filtered, i)
		}
	}
	// Cursor + window reset on filter change. fzf-style: typing narrows the
	// list, cursor lands on the first match. Avoids the "cursor jumps to
	// end" surprise that pinning to len-1 created.
	m.cursor = 0
	m.windowStart = 0
	m.adjustWindow()
}

// visibleRowCount returns how many rows fit in the available terminal space.
// Returns len(m.filtered) when height isn't known yet (initial render before
// WindowSizeMsg) so we don't artificially clip on the first frame.
func (m *scopesModel) visibleRowCount() int {
	if m.height == 0 {
		return len(m.filtered)
	}
	v := m.height - reservedLines
	if v < 1 {
		v = 1
	}
	if v > len(m.filtered) {
		v = len(m.filtered)
	}
	return v
}

// adjustWindow scrolls the visible window so the cursor stays in view.
// Called after cursor moves, filter changes, or terminal resize.
func (m *scopesModel) adjustWindow() {
	visible := m.visibleRowCount()
	if visible <= 0 {
		m.windowStart = 0
		return
	}
	if m.cursor < m.windowStart {
		m.windowStart = m.cursor
	}
	if m.cursor >= m.windowStart+visible {
		m.windowStart = m.cursor - visible + 1
	}
	// Clamp.
	if m.windowStart < 0 {
		m.windowStart = 0
	}
	if m.windowStart+visible > len(m.filtered) {
		m.windowStart = len(m.filtered) - visible
		if m.windowStart < 0 {
			m.windowStart = 0
		}
	}
}

func (m scopesModel) pendingChanges() bool {
	for _, r := range m.rows {
		if r.target != r.realized {
			return true
		}
	}
	return false
}

// diff returns scopes added and removed by current target state.
func (m scopesModel) diff() (added, removed []string) {
	for _, r := range m.rows {
		switch {
		case r.target && !r.realized:
			added = append(added, r.full)
		case !r.target && r.realized:
			removed = append(removed, r.full)
		}
	}
	return added, removed
}

// targetScopes returns the full set the user wants after apply.
func (m scopesModel) targetScopes() []string {
	out := make([]string, 0)
	for _, r := range m.rows {
		if r.target {
			out = append(out, r.full)
		}
	}
	return out
}

// realizedScopes returns the full set currently granted.
func (m scopesModel) realizedScopes() []string {
	out := make([]string, 0)
	for _, r := range m.rows {
		if r.realized {
			out = append(out, r.full)
		}
	}
	return out
}

// applyResultMsg carries the outcome of an OAuth attempt back to the model.
// nil cred + nil err = no-op (e.g. cancelled before dispatch).
type applyResultMsg struct {
	cred *vault.Credential
	err  error
}

// applyCmd builds the tea.Cmd that runs OAuth for the current diff.
//
// Additive flow (target ⊋ realized): include_granted_scopes=true; new
// token covers the union (Google's incremental authorization).
//
// Reductive flow (target has any removal): forceFresh=true so Google
// returns a token scoped exactly to the requested set, not the union.
// The user has already confirmed via the reduction-warning modal.
func (m scopesModel) applyCmd() tea.Cmd {
	_, removed := m.diff()
	if m.auth == nil {
		return func() tea.Msg {
			return applyResultMsg{err: fmt.Errorf("no authenticator configured (use tui.WithAuthenticator)")}
		}
	}
	target := m.targetScopes()
	existing := m.realizedScopes()
	account := m.account
	auth := m.auth
	forceFresh := len(removed) > 0
	return func() tea.Msg {
		cred, err := auth.Auth(account, target, existing, forceFresh)
		return applyResultMsg{cred: cred, err: err}
	}
}

func (m scopesModel) Update(msg tea.Msg) (scopesModel, tea.Cmd) {
	// Apply results are delivered regardless of current state.
	if r, ok := msg.(applyResultMsg); ok {
		return m.handleApplyResult(r), nil
	}
	// Window size updates affect rendering regardless of state. The env
	// override (heightOverride) sticks, ignoring the OS-reported height.
	// Auto-detected heights get the same -1 safety margin as the initial
	// term.GetSize seeding.
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		debugf("WindowSizeMsg: w=%d h=%d (was h=%d, override=%d)",
			ws.Width, ws.Height, m.height, m.heightOverride)
		if m.heightOverride > 0 {
			m.height = m.heightOverride
		} else if ws.Height > 0 {
			m.height = ws.Height
		}
		m.adjustWindow()
		return m, nil
	}

	switch m.state {
	case stateAddCustom:
		return m.updateAddCustom(msg)
	case stateApplying:
		return m.updateApplying(msg)
	case stateApplyError:
		return m.updateApplyError(msg)
	case stateQuitConfirm:
		return m.updateQuitConfirm(msg)
	case stateReduceConfirm:
		return m.updateReduceConfirm(msg)
	case stateRevokeConfirm:
		return m.updateRevokeConfirm(msg)
	}

	keyMsg, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return m, nil
	}
	if m.focus == focusSearch {
		return m.updateSearch(keyMsg)
	}
	return m.updateList(keyMsg)
}

func (m scopesModel) updateSearch(msg tea.KeyMsg) (scopesModel, tea.Cmd) {
	switch msg.String() {
	case "down", "enter":
		if len(m.filtered) > 0 {
			m.focus = focusList
			m.search.Blur()
		}
		return m, nil
	case "esc":
		if m.pendingChanges() {
			m.state = stateQuitConfirm
			return m, nil
		}
		return m, func() tea.Msg { return scopesQuitMsg{} }
	case "ctrl+r":
		// ^r → reauth (was: revoke); see nous#15 polish. Same handler
		// as the row-focused branch — keep both reachable.
		account := m.account
		return m, func() tea.Msg { return reauthRequestedMsg{email: account} }
	case "ctrl+c":
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	m.recomputeFiltered()
	return m, cmd
}

func (m scopesModel) updateList(msg tea.KeyMsg) (scopesModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.adjustWindow()
			debugf("up: cursor=%d windowStart=%d", m.cursor, m.windowStart)
		} else {
			m.focus = focusSearch
			m.search.Focus()
			debugf("up at top: focus=search")
		}
	case "down", "j":
		if m.cursor < len(m.filtered)-1 {
			m.cursor++
			m.adjustWindow()
			debugf("down: cursor=%d windowStart=%d", m.cursor, m.windowStart)
		}
	case "/":
		m.focus = focusSearch
		m.search.Focus()
	case " ":
		if len(m.filtered) > 0 {
			i := m.filtered[m.cursor]
			if m.rows[i].required {
				m.applyStatus = fmt.Sprintf("%s is required for nous to identify the account.", m.rows[i].short)
			} else {
				m.rows[i].target = !m.rows[i].target
				m.applyStatus = ""
			}
		}
	case "enter":
		// Special-case: enter on a realized cloud-platform row launches
		// the Google Cloud project setup flow rather than the
		// default apply/quit. The cloud-platform scope by itself only
		// authorizes API calls; project_id + region are also required
		// for Vertex / AI Studio. Tying the two flows together at the
		// row keeps the user from getting stuck on "I granted it,
		// nothing changed". Pending changes elsewhere are not
		// dropped — they stay pending while the GCP flow runs.
		if len(m.filtered) > 0 {
			i := m.filtered[m.cursor]
			r := m.rows[i]
			if r.full == cloudPlatformScope && r.realized {
				account := m.account
				return m, func() tea.Msg { return gcpSetupRequestMsg{account: account} }
			}
		}

		if !m.pendingChanges() {
			// Operator pressed Enter with no scope diff. Previously this
			// exited to the parent (account picker) — counter-intuitive,
			// since Enter is the apply action everywhere else. Now it's
			// a no-op with a brief "no changes" hint. Use q / esc to
			// exit. nous#15 M4.
			m.applyStatus = "no changes — press q or esc to exit"
			return m, nil
		}
		_, removed := m.diff()
		if len(removed) > 0 {
			// Route through confirmation modal — reductive flow asks Google
			// for a fresh token scoped exactly to the smaller set.
			m.state = stateReduceConfirm
			return m, nil
		}
		m.state = stateApplying
		m.applyErr = nil
		return m, m.applyCmd()
	case "ctrl+r":
		// ^r → reauth (was: revoke). Emits reauthRequestedMsg which
		// the top-level model dispatches to auth.Auth with
		// forceFresh=true. After success the new credential is
		// persisted and m.scopes.health is updated to Healthy.
		//
		// Revoke moved out of the scope view entirely — operator
		// uses the picker's `R` keystroke for that. nous#15 polish.
		account := m.account
		return m, func() tea.Msg { return reauthRequestedMsg{email: account} }
	case "a":
		m.state = stateAddCustom
		m.custom.Reset()
		m.custom.Focus()
		return m, nil
	case "esc":
		if m.pendingChanges() {
			m.state = stateQuitConfirm
			return m, nil
		}
		m.focus = focusSearch
		m.search.Focus()
		return m, nil
	case "q":
		if m.pendingChanges() {
			m.state = stateQuitConfirm
			return m, nil
		}
		return m, func() tea.Msg { return scopesQuitMsg{} }
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m scopesModel) updateAddCustom(msg tea.Msg) (scopesModel, tea.Cmd) {
	keyMsg, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return m, nil
	}
	switch keyMsg.String() {
	case "enter":
		raw := strings.TrimSpace(m.custom.Value())
		if raw == "" {
			// Empty input: just exit add-custom mode.
			m.state = stateNormal
			m.custom.Blur()
			return m, nil
		}
		// Refuse if it duplicates an existing row.
		for _, r := range m.rows {
			if r.full == raw {
				m.applyErr = fmt.Errorf("scope %q is already in the list", raw)
				m.state = stateApplyError
				m.custom.Blur()
				return m, nil
			}
		}
		m.rows = append(m.rows, scopeRow{
			short:       customShortName(raw),
			full:        raw,
			description: "(custom scope)",
			target:      true,
			custom:      true,
		})
		m.recomputeFiltered()
		// Move cursor to the new row in the filtered view if it's visible.
		for i, idx := range m.filtered {
			if idx == len(m.rows)-1 {
				m.cursor = i
				break
			}
		}
		m.state = stateNormal
		m.focus = focusList
		m.custom.Blur()
		m.search.Blur()
		return m, nil
	case "esc":
		m.state = stateNormal
		m.custom.Blur()
		// Restore prior focus on list so user can continue navigating.
		m.focus = focusList
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.custom, cmd = m.custom.Update(msg)
	return m, cmd
}

func (m scopesModel) updateApplying(msg tea.Msg) (scopesModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	// applyResultMsg is handled in Update directly.
	return m, nil
}

func (m scopesModel) updateApplyError(msg tea.Msg) (scopesModel, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		// Any key dismisses the error overlay.
		m.state = stateNormal
		m.applyErr = nil
	}
	return m, nil
}

func (m scopesModel) updateQuitConfirm(msg tea.Msg) (scopesModel, tea.Cmd) {
	keyMsg, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return m, nil
	}
	switch keyMsg.String() {
	case "a":
		// Apply pending changes. Route reductions through the confirmation
		// modal first so the user sees the warning regardless of which path
		// led them here.
		_, removed := m.diff()
		if len(removed) > 0 {
			m.state = stateReduceConfirm
			return m, nil
		}
		m.state = stateApplying
		return m, m.applyCmd()
	case "d":
		// Discard pending changes; exit.
		return m, func() tea.Msg { return scopesQuitMsg{} }
	case "c", "esc":
		m.state = stateNormal
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// updateReduceConfirm handles the reductive-apply warning modal.
func (m scopesModel) updateReduceConfirm(msg tea.Msg) (scopesModel, tea.Cmd) {
	keyMsg, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return m, nil
	}
	switch keyMsg.String() {
	case "y", "enter":
		m.state = stateApplying
		m.applyErr = nil
		return m, m.applyCmd()
	case "n", "esc", "c":
		m.state = stateNormal
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// revokeAccountMsg signals the top-level model to delete the credential
// after Revoke succeeds. The vault write happens at the parent layer;
// scopesModel just orchestrates.
type revokeAccountMsg struct {
	account string
	err     error
}

// updateRevokeConfirm handles the "R: revoke entire account" modal. The
// scopesModel only emits intent; the top-level model performs the vault
// lookup, calls Revoke, deletes the credential, and signals exit.
func (m scopesModel) updateRevokeConfirm(msg tea.Msg) (scopesModel, tea.Cmd) {
	keyMsg, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return m, nil
	}
	switch keyMsg.String() {
	case "y", "enter":
		m.state = stateApplying
		account := m.account
		return m, func() tea.Msg { return revokeAccountMsg{account: account} }
	case "n", "esc", "c":
		m.state = stateNormal
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m scopesModel) handleApplyResult(r applyResultMsg) scopesModel {
	if r.err != nil {
		m.state = stateApplyError
		m.applyErr = r.err
		return m
	}
	if r.cred != nil {
		// Update rows in place: realized = whatever Google says we have now,
		// target = realized (no pending changes after apply).
		granted := map[string]bool{}
		for _, s := range r.cred.Scopes {
			granted[s] = true
		}
		// Mark rows that match the new credential.
		for i := range m.rows {
			m.rows[i].realized = granted[m.rows[i].full]
			m.rows[i].target = m.rows[i].realized
			delete(granted, m.rows[i].full)
		}
		// Any granted scope still in `granted` is brand-new — append it.
		extras := make([]string, 0, len(granted))
		for s := range granted {
			extras = append(extras, s)
		}
		sort.Strings(extras)
		for _, s := range extras {
			m.rows = append(m.rows, scopeRow{
				short:           customShortName(s),
				full:            s,
				description:     "(custom scope)",
				realized:        true,
				initialRealized: false, // new this session
				target:          true,
				custom:          true,
			})
		}
		sortGrantedFirst(m.rows)
		m.recomputeFiltered()
		m.applyStatus = "Applied successfully."
	}
	m.state = stateNormal
	return m
}

// scopesQuitMsg signals the top-level model to exit the scope view.
type scopesQuitMsg struct{}

// padToHeight ensures the rendered view occupies exactly `height` lines so
// bubbletea's diff renderer overwrites every position on every frame and
// can't leak stale rows from a previous shorter view (e.g. modal → normal,
// no-match → match). When height is 0 (initial frame before WindowSizeMsg),
// returns the view unchanged.
func padToHeight(view string, height int) string {
	if height <= 0 {
		return view
	}
	current := strings.Count(view, "\n") + 1
	if current >= height {
		return view
	}
	return view + strings.Repeat("\n", height-current)
}

func (m scopesModel) View() string {
	var v string
	switch m.state {
	case stateAddCustom:
		v = m.viewAddCustom()
	case stateApplying:
		v = m.viewApplying()
	case stateApplyError:
		v = m.viewApplyError()
	case stateQuitConfirm:
		v = m.viewQuitConfirm()
	case stateReduceConfirm:
		v = m.viewReduceConfirm()
	case stateRevokeConfirm:
		v = m.viewRevokeConfirm()
	default:
		v = m.viewNormal()
	}
	v = padToHeight(v, m.height)
	lineCount := 1
	for _, r := range v {
		if r == '\n' {
			lineCount++
		}
	}
	debugf("View: state=%d focus=%d height=%d cursor=%d windowStart=%d visible=%d filtered=%d/total=%d -> rendered_lines=%d",
		m.state, m.focus, m.height, m.cursor, m.windowStart,
		m.visibleRowCount(), len(m.filtered), len(m.rows), lineCount)
	return v
}

func (m scopesModel) viewNormal() string {
	var b strings.Builder

	granted := 0
	requested := 0
	for _, r := range m.rows {
		if r.realized {
			granted++
		}
		if r.requested && !r.realized {
			requested++
		}
	}
	header := fmt.Sprintf("google / %s — %d of %d granted", m.account, granted, len(m.rows))
	if m.pendingChanges() {
		added, removed := m.diff()
		header += fmt.Sprintf("   [%d pending: +%d -%d]", len(added)+len(removed), len(added), len(removed))
	}
	b.WriteString(titleStyle.Render(header))
	// Health badge in the title — needs-reauth rendered in the
	// destructive/red style so it stands out. Healthy accounts get a
	// subtle "✓ checked" marker in the muted style so the operator can
	// see that the probe ran (not "no probe happened, looks healthy").
	// Unknown (transient probe failure) gets "(?)" in muted style.
	// nous#15 follow-up: Level 3 surfacing the issue spec called for.
	switch m.health {
	case AccountHealthNeedsReauth:
		b.WriteString(titleStyle.Render(" - "))
		b.WriteString(rowDelStyle.Render("NEEDS REAUTH (^r)"))
	case AccountHealthHealthy:
		b.WriteString(titleStyle.Render("   "))
		b.WriteString(mutedStyle.Render("✓ checked"))
	case AccountHealthUnknown:
		b.WriteString(titleStyle.Render("   "))
		b.WriteString(mutedStyle.Render("(probe inconclusive)"))
	}
	// Color legend: when an agent has asked for a scope the user hasn't
	// granted, those rows are tinted muted yellow. Surface that in the
	// header in the same color so users learn what the tint means.
	if requested > 0 {
		b.WriteString(titleStyle.Render(" - "))
		b.WriteString(rowReqStyle.Render(fmt.Sprintf("%d requested", requested)))
	}
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n")

	b.WriteString(m.search.View())
	b.WriteString("\n")

	visible := m.visibleRowCount()
	end := m.windowStart + visible
	if end > len(m.filtered) {
		end = len(m.filtered)
	}
	// Always emit exactly one "↑" indicator line (empty when no rows above)
	// so every frame has the same line count and bubbletea's render diff
	// doesn't leave stale lines visible.
	if m.windowStart > 0 {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ↑ %d more above", m.windowStart)))
	}
	b.WriteString("\n")

	if len(m.filtered) == 0 {
		b.WriteString(mutedStyle.Render("  (no scopes match filter)"))
		b.WriteString("\n")
	}
	rendered := 0
	for visIdx := m.windowStart; visIdx < end; visIdx++ {
		rowIdx := m.filtered[visIdx]
		r := m.rows[rowIdx]
		check := "[ ]"
		if r.target {
			check = "[x]"
		}
		// Marker column shows session-level diff: + for newly granted in
		// this session, - for newly removed. The proxy "requested by
		// proxy" hint is conveyed by row color (muted yellow via
		// styleForRow) — no marker needed for it.
		marker := " "
		switch {
		case r.realized && !r.initialRealized:
			marker = "+"
		case !r.realized && r.initialRealized:
			marker = "-"
		}
		cursor := "  "
		if m.focus == focusList && visIdx == m.cursor {
			cursor = "> "
		}
		shortDisplay := r.short
		if r.required {
			shortDisplay = r.short + " (req)"
		}
		line := fmt.Sprintf("%s %s %-32s %s", check, marker, shortDisplay, r.description)
		styled := styleForRow(r, m.focus == focusList && visIdx == m.cursor).Render(line)
		b.WriteString(cursor)
		b.WriteString(styled)
		b.WriteString("\n")
		rendered++
	}
	// Pad row area to a constant `visible` lines so the frame size doesn't
	// vary as the user navigates or filters. (No filter case is the
	// exception: the "no matches" message above replaces this padding.)
	if len(m.filtered) > 0 {
		for ; rendered < visible; rendered++ {
			b.WriteString("\n")
		}
	}
	// Always emit exactly one "↓" indicator line (empty when no rows below).
	if end < len(m.filtered) {
		b.WriteString(mutedStyle.Render(fmt.Sprintf("  ↓ %d more below", len(m.filtered)-end)))
	}
	b.WriteString("\n")

	b.WriteString("\n")
	// Always emit the status slot (blank when empty) so frame size stays
	// constant regardless of whether a transient message is showing.
	if m.applyStatus != "" {
		b.WriteString(helpStyle.Render(m.applyStatus))
	}
	b.WriteString("\n")
	if m.focus == focusSearch {
		b.WriteString(helpStyle.Render("type to filter   ↓/enter: list   ^r: revoke   esc: quit"))
	} else {
		// Keep this short enough to fit on one line in narrow terminals.
		b.WriteString(helpStyle.Render("↑↓ nav   space toggle   enter apply   a add   ^r revoke   / search   q quit"))
	}
	// IMPORTANT: no trailing newline. A final \n pushes the cursor past the
	// last terminal row, which the alt-screen treats as a scroll, sliding
	// the top line (header) off-screen. Bubbletea handles the rendered
	// string as a sequence of lines without needing the trailing newline.
	return b.String()
}

func (m scopesModel) viewAddCustom() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Add custom scope URL"))
	b.WriteString("\n\n")
	b.WriteString(m.custom.View())
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("enter: add    esc: cancel"))
	b.WriteString("\n")
	return b.String()
}

func (m scopesModel) viewApplying() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Authenticating..."))
	b.WriteString("\n\n")
	b.WriteString("  A browser window should have opened for Google OAuth.\n")
	b.WriteString("  Complete the consent flow there. (ctrl+c to abort)\n")
	return b.String()
}

func (m scopesModel) viewApplyError() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Apply failed"))
	b.WriteString("\n\n")
	if m.applyErr != nil {
		// Translate raw OAuth errors into user-facing prose; raw is
		// preserved on a follow-up line for debug. nous#15 M4.
		userMsg, raw := oauth.FriendlyError(m.applyErr)
		b.WriteString("  ")
		b.WriteString(userMsg)
		b.WriteString("\n")
		// Show raw only when it differs from userMsg so we don't
		// double-print for cases FriendlyError didn't translate.
		if raw != "" && raw != userMsg && !strings.HasSuffix(userMsg, raw) {
			b.WriteString("\n  ")
			b.WriteString(helpStyle.Render("debug: " + raw))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("press any key to dismiss"))
	b.WriteString("\n")
	return b.String()
}

func (m scopesModel) viewQuitConfirm() string {
	added, removed := m.diff()
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("You have %d pending change(s)", len(added)+len(removed))))
	b.WriteString("\n\n")
	for _, s := range added {
		b.WriteString(rowAddStyle.Render("  + " + s))
		b.WriteString("\n")
	}
	for _, s := range removed {
		b.WriteString(rowDelStyle.Render("  - " + s))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("[a] apply    [d] discard    [c] cancel"))
	b.WriteString("\n")
	return b.String()
}

func (m scopesModel) viewReduceConfirm() string {
	added, removed := m.diff()
	var b strings.Builder
	b.WriteString(titleStyle.Render("Confirm scope reduction"))
	b.WriteString("\n\n")
	if len(removed) > 0 {
		b.WriteString("  Removing:\n")
		for _, s := range removed {
			b.WriteString(rowDelStyle.Render("    - " + s))
			b.WriteString("\n")
		}
	}
	if len(added) > 0 {
		b.WriteString("  Adding:\n")
		for _, s := range added {
			b.WriteString(rowAddStyle.Render("    + " + s))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString("  nous will re-authorize with only the remaining scopes.\n")
	b.WriteString("  You'll see a fresh consent screen in your browser.\n")
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("[y/enter] continue    [n/esc] cancel"))
	b.WriteString("\n")
	return b.String()
}

func (m scopesModel) viewRevokeConfirm() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("Revoke account: %s", m.account)))
	b.WriteString("\n\n")
	b.WriteString(rowDelStyle.Render("  This will revoke ALL Google scopes for this account"))
	b.WriteString("\n")
	b.WriteString(rowDelStyle.Render("  and remove the credential from nous's keychain."))
	b.WriteString("\n\n")
	b.WriteString("  Agents using this account will lose access immediately.\n")
	b.WriteString("  You'll need to run `nous provider` again to use this account.\n")
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("[y/enter] revoke    [n/esc] cancel"))
	b.WriteString("\n")
	return b.String()
}

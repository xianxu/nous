package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/lib/provider/providers/catalog"
	"github.com/xianxu/nous/lib/provider/vault"
)

// catalogPasteModel drives the Tier-3 add-account flow (#15 M4).
//
// Two-step UX:
//
//	step 1/2: account name (textinput) — also surfaces signup/key
//	          URLs and a ctrl+o shortcut to open the key URL.
//	step 2/2: paste key (masked textinput) — enter to store, esc to
//	          go back.
//
// On success: emits catalogPasteDoneMsg with provider+account so the
// parent can refresh the picker and surface a "ready to use" hint.
// On cancel (esc from step 1): catalogPasteCancelMsg, no side effect.
//
// Verify-on-paste (#15 M5): when the entry declares a verify_url,
// the flow probes it after the user types Enter on the key step.
// Provider-explicit rejection (401/403) sends the user back to
// retype the key; an inconclusive endpoint (5xx, network) stores
// the key with a degraded status note. Entries without verify_url
// skip the verifying step entirely.
type catalogPasteModel struct {
	entry catalog.Entry
	state catalogPasteState

	accountInput textinput.Model
	keyInput     textinput.Model
	vault        vault.Store

	err error
}

type catalogPasteState int

const (
	catalogPasteStateAccount catalogPasteState = iota
	catalogPasteStateKey
	catalogPasteStateVerifying
	catalogPasteStateError
)

// catalogPasteDoneMsg signals the paste flow stored a new catalog
// credential successfully. Carries provider+account so the parent
// can craft a precise "ready to use" hint without re-deriving the
// vault key shape. verifyNote is empty when no verify happened
// (entry has no verify_url) and otherwise carries a short status
// fragment ("verified" or "verify inconclusive: ...") that the
// parent can append to the success hint.
type catalogPasteDoneMsg struct {
	provider   string
	account    string
	verifyNote string
}

// catalogVerifyResultMsg carries the verify probe outcome back to
// the paste model. Driven by entry.Verify; the paste model decides
// whether to proceed to store, send the user back to retype, or
// store-with-degraded-status.
type catalogVerifyResultMsg struct {
	result catalog.VerifyResult
	err    error
}

// catalogPasteCancelMsg signals the user cancelled out of the flow
// before any persistent state change.
type catalogPasteCancelMsg struct{}

func newCatalogPasteModel(entry catalog.Entry, v vault.Store) catalogPasteModel {
	acc := textinput.New()
	acc.Placeholder = "personal"
	acc.Prompt = "  account> "
	acc.CharLimit = 64
	acc.Width = 60
	acc.Focus()

	key := textinput.New()
	key.Placeholder = catalogKeyPlaceholder(entry.ID)
	key.Prompt = "  key> "
	key.CharLimit = 256
	key.Width = 60
	key.EchoMode = textinput.EchoPassword
	key.EchoCharacter = '•'

	return catalogPasteModel{
		entry:        entry,
		state:        catalogPasteStateAccount,
		accountInput: acc,
		keyInput:     key,
		vault:        v,
	}
}

// catalogKeyPlaceholder returns a per-provider key-shape hint for
// the textinput placeholder. Falls back to a generic hint for
// unknown providers — the catalog YAML doesn't carry a sample-key
// pattern today, and that's the right call: real keys are
// secret-prefixed and any "shape" we ship gets stale.
func catalogKeyPlaceholder(id string) string {
	switch id {
	case "anthropic":
		return "sk-ant-…"
	}
	return "paste here"
}

func (m catalogPasteModel) Update(msg tea.Msg) (catalogPasteModel, tea.Cmd) {
	switch m.state {
	case catalogPasteStateAccount:
		return m.updateAccount(msg)
	case catalogPasteStateKey:
		return m.updateKey(msg)
	case catalogPasteStateVerifying:
		return m.updateVerifying(msg)
	case catalogPasteStateError:
		return m.updateError(msg)
	}
	return m, nil
}

func (m catalogPasteModel) updateAccount(msg tea.Msg) (catalogPasteModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter":
			if strings.TrimSpace(m.accountInput.Value()) == "" {
				return m, nil
			}
			m.state = catalogPasteStateKey
			m.accountInput.Blur()
			m.keyInput.Focus()
			return m, nil
		case "esc":
			return m, func() tea.Msg { return catalogPasteCancelMsg{} }
		case "ctrl+c":
			return m, tea.Quit
		case "ctrl+o":
			openURL(m.entry.KeyURL)
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.accountInput, cmd = m.accountInput.Update(msg)
	return m, cmd
}

func (m catalogPasteModel) updateKey(msg tea.Msg) (catalogPasteModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter":
			pasted := strings.TrimSpace(m.keyInput.Value())
			if pasted == "" {
				return m, nil
			}
			// Move to verifying state and fire the verify probe.
			// Entries without verify_url short-circuit inside Verify
			// (returns VerifyOK with nil error) so the brief
			// "verifying..." flash is harmless and keeps the state
			// machine uniform.
			m.state = catalogPasteStateVerifying
			entry := m.entry
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				res, err := entry.Verify(ctx, pasted)
				return catalogVerifyResultMsg{result: res, err: err}
			}
		case "esc":
			m.state = catalogPasteStateAccount
			m.keyInput.Blur()
			m.keyInput.Reset()
			m.accountInput.Focus()
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		case "ctrl+o":
			openURL(m.entry.KeyURL)
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.keyInput, cmd = m.keyInput.Update(msg)
	return m, cmd
}

// updateVerifying handles the brief "verifying..." state. The probe
// finishes via catalogVerifyResultMsg; ctrl+c remains the only way
// out mid-probe (the in-flight goroutine will time out via its 30s
// context once the program exits the foreground).
func (m catalogPasteModel) updateVerifying(msg tea.Msg) (catalogPasteModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	r, ok := msg.(catalogVerifyResultMsg)
	if !ok {
		return m, nil
	}
	pasted := strings.TrimSpace(m.keyInput.Value())
	account := strings.TrimSpace(m.accountInput.Value())

	if r.result == catalog.VerifyRejected {
		// Provider explicitly rejected the key (401/403). Don't
		// store; send the user back to step 2 to retype. The
		// existing error overlay handles "any key returns to step
		// 2"; we just funnel through it.
		m.state = catalogPasteStateError
		m.err = fmt.Errorf("provider rejected the pasted key: %v", r.err)
		return m, nil
	}

	// VerifyOK or VerifyEndpointError: store. Endpoint-error means
	// we couldn't get a verdict (5xx, network) — the key might
	// still be good, so don't block; surface the warning in the
	// status note instead.
	cred := &vault.Credential{
		Type:     vault.TypeCatalog,
		Provider: m.entry.ID,
		Account:  account,
		Catalog: &vault.CatalogData{
			KeyMaterial: pasted,
			AddedAt:     time.Now(),
		},
	}
	if err := m.vault.Set(cred); err != nil {
		m.state = catalogPasteStateError
		m.err = fmt.Errorf("store credential: %w", err)
		return m, nil
	}

	var note string
	switch r.result {
	case catalog.VerifyOK:
		// Only emit "verified" when an actual verify_url was probed;
		// a no-verify_url entry returns VerifyOK as a no-op and we
		// shouldn't claim verification we didn't do.
		if m.entry.VerifyURL != "" {
			note = "verified"
		}
	case catalog.VerifyEndpointError:
		note = fmt.Sprintf("verify inconclusive: %v", r.err)
	}
	return m, func() tea.Msg {
		return catalogPasteDoneMsg{provider: m.entry.ID, account: account, verifyNote: note}
	}
}

func (m catalogPasteModel) updateError(msg tea.Msg) (catalogPasteModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if _, ok := msg.(tea.KeyMsg); ok {
		m.state = catalogPasteStateKey
		m.err = nil
		m.keyInput.Reset()
		m.keyInput.Focus()
	}
	return m, nil
}

func (m catalogPasteModel) View() string {
	switch m.state {
	case catalogPasteStateAccount:
		return m.viewAccount()
	case catalogPasteStateKey:
		return m.viewKey()
	case catalogPasteStateVerifying:
		return m.viewVerifying()
	case catalogPasteStateError:
		return m.viewError()
	}
	return ""
}

func (m catalogPasteModel) viewAccount() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("%s › Add provider › %s", appName(), m.entry.Name)))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Step 1/2 — account name"))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n\n")

	if m.entry.SignupURL != "" {
		b.WriteString("  Sign up:  ")
		b.WriteString(hyperlink(m.entry.SignupURL, mutedStyle.Render(m.entry.SignupURL)))
		b.WriteString("\n")
	}
	if m.entry.KeyURL != "" {
		b.WriteString("  Get key:  ")
		b.WriteString(hyperlink(m.entry.KeyURL, mutedStyle.Render(m.entry.KeyURL)))
		b.WriteString("    ")
		b.WriteString(actionHintStyle.Render("(ctrl+o or click to open)"))
		b.WriteString("\n")
	}
	if len(m.entry.HostnamePatterns) > 0 {
		b.WriteString("  Host:     ")
		b.WriteString(mutedStyle.Render(m.entry.HostnamePatterns[0]))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString("  Account name (becomes X-Charon-Account header value):\n")
	b.WriteString(m.accountInput.View())
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("enter: continue   ctrl+o: open key URL   esc: back"))
	return b.String()
}

func (m catalogPasteModel) viewKey() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("%s › Add provider › %s", appName(), m.entry.Name)))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Step 2/2 — paste API key"))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  Account: %s\n\n", strings.TrimSpace(m.accountInput.Value())))
	b.WriteString("  Paste API key (input is hidden):\n")
	b.WriteString(m.keyInput.View())
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("enter: store   ctrl+o: open key URL   esc: back"))
	return b.String()
}

func (m catalogPasteModel) viewVerifying() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("%s › Add provider › %s", appName(), m.entry.Name)))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Verifying with provider..."))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  Account: %s\n\n", strings.TrimSpace(m.accountInput.Value())))
	if m.entry.VerifyURL != "" {
		b.WriteString("  Probing " + hyperlink(m.entry.VerifyURL, m.entry.VerifyURL) + "\n\n")
	}
	b.WriteString(helpStyle.Render("(ctrl+c to abort)"))
	return b.String()
}

func (m catalogPasteModel) viewError() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("%s › Add provider › %s — failed", appName(), m.entry.Name)))
	b.WriteString("\n\n")
	if m.err != nil {
		b.WriteString(rowDelStyle.Render("  " + m.err.Error()))
	}
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("press any key to retry   ctrl+c: quit"))
	return b.String()
}

// openURL launches the user's default browser at url. Best-effort —
// failures are silent (URL is also displayed inline so the user can
// copy it manually). darwin uses "open"; linux uses "xdg-open";
// other platforms no-op.
func openURL(url string) {
	if url == "" {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return
	}
	_ = cmd.Start()
}

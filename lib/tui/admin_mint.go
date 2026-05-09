package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/lib/provider/providers"
	"github.com/xianxu/nous/lib/provider/vault"
)

// adminMintModel drives the `+ new project` / `+ new workspace` flow
// for admin-key providers. State machine:
//
//	editingAccountName ──enter──▶ loadingProjects ──projects/err──▶ pickingProject
//	      │                              │                                │
//	      esc                            err ──▶ error                    │
//	      ▼                                                               │
//	    cancel                                                            │
//	                                       ┌──────────────────────────────┤
//	                                       ▼                              ▼
//	                                  pick existing                  pick "+ create new"
//	                                       │                              │
//	                                       │                              ▼
//	                                       │                       editingProjectName
//	                                       │                              │
//	                                       │                          enter│
//	                                       ▼                              ▼
//	                                    minting ◀────────── creating ──perr──▶ error
//	                                       │
//	                              ──perr──▶ error
//	                                       │
//	                                    success
//	                                       ▼
//	                                     done
type adminMintModel struct {
	providerName string
	provider     providers.Provider
	store        *providers.AdminKeyStore
	vault        vault.Store

	state adminMintState

	// adminKey + orgInfo fetched once at the start of the flow so we
	// don't re-touch keychain mid-flow.
	adminKey string
	meta     providers.AdminMeta

	accountInput     textinput.Model
	projectNameInput textinput.Model

	// Project picker state
	projects     []providers.Project
	projectCur   int

	// In-flight mint state.
	chosenProjectID   string
	chosenProjectName string

	// Upstream-state tracking for partial-failure surfacing. Set as
	// each upstream step succeeds; on failure, the cancel message's
	// StatusNote names what landed upstream so the user can clean
	// up at the provider's dashboard.
	createdProjectID    string // non-empty if CreateProject succeeded this flow
	createdProjectName  string
	mintedKeyID         string // non-empty if MintKey succeeded this flow
	mintedKeyHasVault   bool   // true once vault.Set persisted the credential

	err error
}

type adminMintState int

const (
	mintStateEditingAccount adminMintState = iota
	mintStateLoadingProjects
	mintStatePickingProject
	mintStateEditingProjectName
	mintStateCreatingProject
	mintStateMinting
	mintStateError
)

// Messages
type adminMintDoneMsg struct {
	account string // newly-stored credential's X-Charon-Account
}

// adminMintCancelMsg signals the user backed out of the flow. When
// the cancel happens after a partial-success state (e.g. CreateProject
// succeeded upstream but MintKey then failed, leaving an orphan
// project at the provider), the StatusNote describes the upstream
// state so the user can clean up via the dashboard. Empty StatusNote
// means a clean cancel before any upstream side effects.
type adminMintCancelMsg struct {
	StatusNote string
}

type adminMintProjectsLoadedMsg struct {
	projects []providers.Project
	err      error
}

type adminMintProjectCreatedMsg struct {
	project providers.Project
	err     error
}

type adminMintMintedMsg struct {
	keyID    string
	keyMat   string
	err      error
}

const createNewSentinel = "__create_new__"

// newAdminMintModel constructs a mint flow. Caller must verify the
// admin key is set before opening this — `+ new project` is muted in
// the entity list when adminKeySet is false, so this is enforced
// upstream. We do still read the admin key here (rather than have the
// caller pass it) since the paste flow may have written it
// asynchronously and the entity list's captured value could be stale.
func newAdminMintModel(
	providerName string,
	provider providers.Provider,
	store *providers.AdminKeyStore,
	v vault.Store,
) (adminMintModel, error) {
	adminKey, meta, err := store.Get()
	if err != nil {
		return adminMintModel{}, fmt.Errorf("read admin key: %w", err)
	}

	acct := textinput.New()
	acct.Placeholder = "work-key"
	acct.Prompt = "  Name> "
	acct.CharLimit = 64
	acct.Width = 40
	acct.Focus()

	pname := textinput.New()
	pname.Placeholder = entityTerm(providerName) + " name (upstream)"
	pname.Prompt = "  name> "
	pname.CharLimit = 80
	pname.Width = 40

	return adminMintModel{
		providerName:     providerName,
		provider:         provider,
		store:            store,
		vault:            v,
		state:            mintStateEditingAccount,
		adminKey:         adminKey,
		meta:             meta,
		accountInput:     acct,
		projectNameInput: pname,
	}, nil
}

func (m adminMintModel) Update(msg tea.Msg) (adminMintModel, tea.Cmd) {
	switch m.state {
	case mintStateEditingAccount:
		return m.updateEditingAccount(msg)
	case mintStateLoadingProjects:
		return m.updateLoadingProjects(msg)
	case mintStatePickingProject:
		return m.updatePickingProject(msg)
	case mintStateEditingProjectName:
		return m.updateEditingProjectName(msg)
	case mintStateCreatingProject:
		return m.updateCreatingProject(msg)
	case mintStateMinting:
		return m.updateMinting(msg)
	case mintStateError:
		return m.updateError(msg)
	}
	return m, nil
}

func (m adminMintModel) updateEditingAccount(msg tea.Msg) (adminMintModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter":
			name := strings.TrimSpace(m.accountInput.Value())
			if name == "" {
				return m, nil
			}
			// Reject duplicate names early so we don't waste an upstream
			// mint on a name that already exists in the vault.
			if _, err := m.vault.Get(m.providerName, name); err == nil {
				m.state = mintStateError
				m.err = fmt.Errorf("key name %q already exists for %s — pick a different name", name, m.providerName)
				return m, nil
			}
			m.state = mintStateLoadingProjects
			provider := m.provider
			adminKey := m.adminKey
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				ps, err := provider.ListProjects(ctx, adminKey)
				return adminMintProjectsLoadedMsg{projects: ps, err: err}
			}
		case "esc":
			return m, m.cancel()
		case "ctrl+c":
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.accountInput, cmd = m.accountInput.Update(msg)
	return m, cmd
}

// cancel emits adminMintCancelMsg with a StatusNote that names any
// upstream side effects — orphan project, orphan minted key — so
// the parent screen can surface them to the user. Clean cancel
// (no upstream work yet) emits an empty note.
func (m adminMintModel) cancel() tea.Cmd {
	note := m.partialFailureNote()
	return func() tea.Msg { return adminMintCancelMsg{StatusNote: note} }
}

// partialFailureNote builds a one-line summary of what survived
// upstream when the mint flow exits without producing a working
// credential. Empty string means clean exit (no upstream side
// effects to report).
func (m adminMintModel) partialFailureNote() string {
	switch {
	case m.mintedKeyID != "" && !m.mintedKeyHasVault:
		// Worst case: key exists at provider but charon doesn't have
		// it. The user has to revoke it manually at the dashboard.
		return fmt.Sprintf("orphan: API key %s was minted in %s upstream but charon couldn't store it — revoke at the provider's dashboard",
			m.mintedKeyID, m.chosenProjectName)
	case m.createdProjectID != "" && m.mintedKeyID == "":
		// New project sitting empty upstream.
		return fmt.Sprintf("orphan: created %s (%s) but no key was minted — re-run + new key into this %s, or archive it at the dashboard",
			m.chosenProjectName, m.createdProjectID, entityTerm(m.providerName))
	}
	return ""
}

func (m adminMintModel) updateLoadingProjects(msg tea.Msg) (adminMintModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	pl, ok := msg.(adminMintProjectsLoadedMsg)
	if !ok {
		return m, nil
	}
	if pl.err != nil {
		m.state = mintStateError
		m.err = pl.err
		return m, nil
	}
	m.projects = pl.projects
	sort.Slice(m.projects, func(i, j int) bool { return m.projects[i].Name < m.projects[j].Name })
	m.projectCur = 0
	m.state = mintStatePickingProject
	return m, nil
}

func (m adminMintModel) updatePickingProject(msg tea.Msg) (adminMintModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	// Picker has len(projects)+1 rows; last row is "+ create new".
	last := len(m.projects)
	switch k.String() {
	case "up", "k":
		if m.projectCur > 0 {
			m.projectCur--
		}
	case "down", "j":
		if m.projectCur < last {
			m.projectCur++
		}
	case "enter":
		if m.projectCur == last {
			// "+ create new"
			m.state = mintStateEditingProjectName
			m.projectNameInput.Focus()
			return m, nil
		}
		// Existing project chosen — proceed straight to mint.
		p := m.projects[m.projectCur]
		m.chosenProjectID = p.ID
		m.chosenProjectName = p.Name
		return m.kickoffMint()
	case "esc":
		// Back to account name (preserve typed name).
		m.state = mintStateEditingAccount
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m adminMintModel) updateEditingProjectName(msg tea.Msg) (adminMintModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter":
			name := strings.TrimSpace(m.projectNameInput.Value())
			if name == "" {
				return m, nil
			}
			m.chosenProjectName = name
			m.state = mintStateCreatingProject
			provider := m.provider
			adminKey := m.adminKey
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				p, err := provider.CreateProject(ctx, adminKey, name)
				return adminMintProjectCreatedMsg{project: p, err: err}
			}
		case "esc":
			// Back to picker; clear the typed name.
			m.state = mintStatePickingProject
			m.projectNameInput.Reset()
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.projectNameInput, cmd = m.projectNameInput.Update(msg)
	return m, cmd
}

func (m adminMintModel) updateCreatingProject(msg tea.Msg) (adminMintModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	pc, ok := msg.(adminMintProjectCreatedMsg)
	if !ok {
		return m, nil
	}
	if pc.err != nil {
		m.state = mintStateError
		m.err = fmt.Errorf("create %s: %w", entityTerm(m.providerName), pc.err)
		return m, nil
	}
	m.chosenProjectID = pc.project.ID
	m.createdProjectID = pc.project.ID
	if pc.project.Name != "" {
		m.chosenProjectName = pc.project.Name
		m.createdProjectName = pc.project.Name
	}
	return m.kickoffMint()
}

func (m adminMintModel) kickoffMint() (adminMintModel, tea.Cmd) {
	m.state = mintStateMinting
	provider := m.provider
	adminKey := m.adminKey
	pid := m.chosenProjectID
	keyName := strings.TrimSpace(m.accountInput.Value())
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		kid, mat, err := provider.MintKey(ctx, adminKey, pid, keyName)
		return adminMintMintedMsg{keyID: kid, keyMat: mat, err: err}
	}
}

func (m adminMintModel) updateMinting(msg tea.Msg) (adminMintModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	mm, ok := msg.(adminMintMintedMsg)
	if !ok {
		return m, nil
	}
	if mm.err != nil {
		m.state = mintStateError
		m.err = fmt.Errorf("mint: %w", mm.err)
		return m, nil
	}

	// Mint succeeded upstream — record the key id so partialFailureNote
	// can surface the orphan if the vault.Set below fails.
	m.mintedKeyID = mm.keyID

	// Persist the credential. Per the keychain layout, minted creds
	// store the OrgID + ProjectID + KeyID + KeyMaterial so cascade-
	// revoke and per-key revoke can both find the right targets.
	cred := &vault.Credential{
		Type:     vault.TypeAdminKey,
		Provider: m.providerName,
		Account:  strings.TrimSpace(m.accountInput.Value()),
		AdminKey: &vault.AdminKeyData{
			OrgID:       m.meta.OrgID,
			OrgLabel:    m.meta.OrgLabel,
			OrgName:     m.meta.OrgName,
			ProjectID:   m.chosenProjectID,
			ProjectName: m.chosenProjectName,
			KeyID:       mm.keyID,
			KeyMaterial: mm.keyMat,
			CreatedAt:   time.Now(),
		},
	}
	if err := m.vault.Set(cred); err != nil {
		// Worst case: minted upstream but couldn't store locally. The
		// user must revoke the key at the provider's dashboard since
		// charon never persisted the IDs needed to call RevokeKey.
		m.state = mintStateError
		m.err = fmt.Errorf("store credential (key was minted upstream — revoke %s manually at provider dashboard): %w", mm.keyID, err)
		return m, nil
	}
	m.mintedKeyHasVault = true
	account := cred.Account
	return m, func() tea.Msg { return adminMintDoneMsg{account: account} }
}

func (m adminMintModel) updateError(msg tea.Msg) (adminMintModel, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); ok {
		// Any key cancels the flow. Error states can leave the upstream
		// in a partial-success state (project created but mint failed,
		// or mint succeeded but vault.Set failed). The cancel msg
		// carries a status note that names the orphan so the user
		// knows what's left to clean up.
		return m, m.cancel()
	}
	return m, nil
}

func (m adminMintModel) View() string {
	switch m.state {
	case mintStateEditingAccount:
		return m.viewEditingAccount()
	case mintStateLoadingProjects:
		return m.viewSimple("Loading " + entityTermPlural(m.providerName) + "...")
	case mintStatePickingProject:
		return m.viewPickingProject()
	case mintStateEditingProjectName:
		return m.viewEditingProjectName()
	case mintStateCreatingProject:
		return m.viewSimple("Creating " + entityTerm(m.providerName) + " " + m.chosenProjectName + "...")
	case mintStateMinting:
		return m.viewSimple("Minting API key in " + m.chosenProjectName + "...")
	case mintStateError:
		return m.viewError()
	}
	return ""
}

func (m adminMintModel) header(sub string) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("%s › %s › new key",
		appName(), providerLabel(m.providerName))))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(sub))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n\n")
	return b.String()
}

func (m adminMintModel) viewEditingAccount() string {
	var b strings.Builder
	b.WriteString(m.header("Step 1/2 — name"))
	b.WriteString("  Give this key a short name. Agents identify which key\n")
	b.WriteString("  to use by referencing this name.\n\n")
	b.WriteString(m.accountInput.View())
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("enter: continue   esc: cancel"))
	return b.String()
}

func (m adminMintModel) viewPickingProject() string {
	var b strings.Builder
	sub := fmt.Sprintf("Step 2/2 — pick which %s the key should live in",
		upstreamContainerLabel(m.providerName))
	b.WriteString(m.header(sub))
	b.WriteString(fmt.Sprintf("  Name: %s\n\n", strings.TrimSpace(m.accountInput.Value())))

	if len(m.projects) == 0 {
		b.WriteString(mutedStyle.Render("  (no existing " + entityTermPlural(m.providerName) + ")"))
		b.WriteString("\n")
	} else {
		for i, p := range m.projects {
			cursor := "  "
			if i == m.projectCur {
				cursor = "> "
			}
			line := fmt.Sprintf("%s   %s", padOrTrunc(p.Name, 32), p.ID)
			if i == m.projectCur {
				line = selectedStyle.Render(line)
			}
			b.WriteString(cursor)
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	// "+ create new" affordance row (always last).
	cursor := "  "
	if m.projectCur == len(m.projects) {
		cursor = "> "
	}
	createLine := "+ create new " + entityTerm(m.providerName)
	if m.projectCur == len(m.projects) {
		createLine = selectedStyle.Render(createLine)
	} else {
		createLine = mutedStyle.Render(createLine)
	}
	b.WriteString(cursor)
	b.WriteString(createLine)
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("↑↓ nav   enter select   esc back   ctrl+c quit"))
	return b.String()
}

func (m adminMintModel) viewEditingProjectName() string {
	var b strings.Builder
	b.WriteString(m.header("Step 2b — name the new " + entityTerm(m.providerName)))
	b.WriteString(fmt.Sprintf("  Name: %s\n\n", strings.TrimSpace(m.accountInput.Value())))
	b.WriteString("  Upstream " + entityTerm(m.providerName) + " name:\n")
	b.WriteString(m.projectNameInput.View())
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("enter: create + mint   esc: back to picker"))
	return b.String()
}

func (m adminMintModel) viewSimple(line string) string {
	var b strings.Builder
	b.WriteString(m.header(""))
	b.WriteString("  " + line + "\n\n")
	b.WriteString(helpStyle.Render("(ctrl+c to abort)"))
	return b.String()
}

func (m adminMintModel) viewError() string {
	var b strings.Builder
	b.WriteString(m.header("failed"))
	if m.err != nil {
		b.WriteString(rowDelStyle.Render("  " + m.err.Error()))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("press any key to dismiss"))
	return b.String()
}

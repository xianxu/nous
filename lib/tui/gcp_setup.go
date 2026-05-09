package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/lib/provider/providers/gcp"
)

// GCPSetupClient is the subset of gcp.Client the TUI calls. Defined
// as an interface so tests can drop in a stub without standing up an
// httptest server. Production wires *gcp.Client.
type GCPSetupClient interface {
	ListProjects(ctx context.Context) ([]gcp.Project, error)
	CreateProject(ctx context.Context, projectID, displayName string, parent *gcp.Parent) (*gcp.Operation, error)
	WaitOperation(ctx context.Context, opName string, pollInterval time.Duration) error
	BatchEnableServices(ctx context.Context, projectID string, services []string) error
	GetBillingInfo(ctx context.Context, projectID string) (*gcp.BillingInfo, error)
	CreateAPIKey(ctx context.Context, projectID, displayName string, restrictedTo []string) (*gcp.Operation, error)
	WaitAPIKeyOperation(ctx context.Context, opName string) (*gcp.Operation, error)
	DeleteAPIKey(ctx context.Context, name string) (*gcp.Operation, error)
}

type gcpSetupState int

const (
	gcpStateLoading gcpSetupState = iota
	gcpStatePickingProject
	gcpStateEditingNewName
	gcpStateCreatingProject
	gcpStateEnabling
	gcpStateBillingCheck
	gcpStateBillingBlocked
	gcpStatePickingRegion
	gcpStateMintingAIStudio
	gcpStateError
)

// gcpSetupModel drives the M3 project-management flow inside the TUI.
// State machine mirrors gcp.Setup but message-driven for bubbletea.
//
//	loading ──projects──▶ pickingProject ──existing──▶ enabling
//	    │                       │
//	   err                      └──"n"──▶ editingNewName ──enter──▶ creatingProject
//	    │                                                                │
//	    ▼                                                               done
//	  error                                                              ▼
//	                                                               enabling
//	                                                                  │
//	                                                                 done
//	                                                                  ▼
//	                                                             billingCheck
//	                                                                  │
//	                                                                 done
//	                                                                  ▼
//	                                                             pickingRegion
//	                                                                  │
//	                                                                enter
//	                                                                  ▼
//	                                                             gcpSetupDoneMsg
//
// All async ops emit gcpSetup*DoneMsg; the model advances on receipt.
type gcpSetupModel struct {
	client  GCPSetupClient
	account string

	state gcpSetupState

	// pinnedProject is shown at the top of the picker even if Google's
	// projects.list doesn't return it. Set when the user already has a
	// configured project (cred.GCP). Closes the eventual-consistency
	// gap where a freshly-created project hasn't yet propagated to
	// projects.list — the user still sees their pick in the list
	// immediately on re-entry.
	pinnedProject *gcp.Project

	// hasAIStudioKey is true when the credential already has an
	// AI Studio key persisted. Skip the mint step in that case to
	// honor "one key per account" — re-running setup keeps the
	// existing key. Re-mint requires explicit revoke first.
	hasAIStudioKey bool

	// mintedAIStudio carries the freshly-minted key from the mint
	// state to the done message so the top-level model can persist
	// it onto the credential.
	mintedAIStudio *gcp.APIKey

	// Project picker state.
	projects   []gcp.Project
	projectCur int
	nameInput  textinput.Model

	// Result accumulator.
	chosenProject  gcp.Project
	createdNew     bool
	billingEnabled bool

	// Region picker state. Cursor index into gcp.SupportedVertexRegions
	// with -1 meaning "default" sentinel.
	regionCur int

	notice string // transient status (e.g. "creating project...")
	err    error
}

// Async messages
type gcpProjectsLoadedMsg struct {
	projects []gcp.Project
	err      error
}
type gcpProjectCreatedMsg struct {
	projectID   string
	projectName string
	err         error
}
type gcpServicesEnabledMsg struct {
	err error
}
type gcpBillingCheckedMsg struct {
	enabled bool
	err     error // non-fatal: surfaced as a notice, not an abort
}

// gcpSetupDoneMsg signals the top-level model to persist the result
// and return to scopes view. aiStudio is non-nil when this run
// minted a fresh key; nil means the credential already had one or
// mint failed (non-fatal). aiStudioErr carries the mint failure
// message so the scope picker can surface it persistently — the
// in-flow notice is too brief for users to read before the screen
// transitions back.
type gcpSetupDoneMsg struct {
	account      string
	projectID    string
	projectName  string
	region       string
	createdNew   bool
	billing      bool
	aiStudio     *gcp.APIKey
	aiStudioErr  string // non-empty iff mint was attempted and failed
}

// gcpAIStudioMintedMsg is the async result of the AI Studio mint
// operation. err is non-nil only on real failure; mint failure is
// surfaced as a notice but the flow still proceeds to done.
type gcpAIStudioMintedMsg struct {
	key *gcp.APIKey
	err error
}

// gcpSetupCancelMsg signals user-initiated cancel from any state.
type gcpSetupCancelMsg struct{}

// gcpSetupRequestMsg is emitted by scopesModel when the user presses
// enter on a realized cloud-platform row. The top-level model picks
// it up and opens the GCP setup screen.
type gcpSetupRequestMsg struct {
	account string
}

func newGCPSetupModel(client GCPSetupClient, account string, pinned *gcp.Project, hasAIStudioKey bool) gcpSetupModel {
	ti := textinput.New()
	ti.Placeholder = "Charon Gemini"
	ti.Prompt = "Display name: "
	ti.CharLimit = 30
	ti.Width = 40

	return gcpSetupModel{
		client:         client,
		account:        account,
		state:          gcpStateLoading,
		nameInput:      ti,
		regionCur:      0, // default to first region (us-central1)
		pinnedProject:  pinned,
		hasAIStudioKey: hasAIStudioKey,
	}
}

// initCmd kicks off the project list. Returned as the first cmd when
// transitioning into screenGCPSetup.
func (m *gcpSetupModel) initCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		projects, err := client.ListProjects(ctx)
		return gcpProjectsLoadedMsg{projects: projects, err: err}
	}
}

func (m gcpSetupModel) Update(msg tea.Msg) (gcpSetupModel, tea.Cmd) {
	// ctrl+c quits the program from any sub-state, matching the rest of
	// the TUI. esc is the softer "cancel back to scope view" path
	// handled per-state below.
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	switch msg := msg.(type) {
	case gcpProjectsLoadedMsg:
		if msg.err != nil {
			m.state = gcpStateError
			m.err = msg.err
			return m, nil
		}
		m.projects = mergePinned(msg.projects, m.pinnedProject)
		// Default cursor to the currently-configured project so the
		// user can re-pick the same one with a single Enter.
		if m.pinnedProject != nil {
			for i, p := range m.projects {
				if p.ProjectID == m.pinnedProject.ProjectID {
					m.projectCur = i
					break
				}
			}
		}
		m.state = gcpStatePickingProject
		return m, nil

	case gcpProjectCreatedMsg:
		if msg.err != nil {
			m.state = gcpStateError
			m.err = msg.err
			return m, nil
		}
		m.chosenProject = gcp.Project{
			ProjectID:      msg.projectID,
			Name:           msg.projectName,
			LifecycleState: "ACTIVE",
		}
		m.createdNew = true
		m.state = gcpStateEnabling
		m.notice = fmt.Sprintf("Enabling APIs on %s...", m.chosenProject.ProjectID)
		return m, m.enableServicesCmd()

	case gcpServicesEnabledMsg:
		if msg.err != nil {
			m.state = gcpStateError
			m.err = msg.err
			return m, nil
		}
		m.state = gcpStateBillingCheck
		m.notice = fmt.Sprintf("Checking billing on %s...", m.chosenProject.ProjectID)
		return m, m.checkBillingCmd()

	case gcpBillingCheckedMsg:
		m.billingEnabled = msg.enabled
		switch {
		case msg.err != nil:
			// Read failed (permission, network) — non-fatal.
			m.state = gcpStatePickingRegion
			m.notice = fmt.Sprintf("Couldn't read billing info (%v) — proceeding anyway.", msg.err)
		case !msg.enabled:
			// Block here so the user fixes billing before they end
			// up with calls that fail. Both Vertex and AI Studio
			// (charon-created projects) need billing.
			m.state = gcpStateBillingBlocked
			m.notice = ""
		default:
			m.state = gcpStatePickingRegion
			m.notice = ""
		}
		return m, nil

	case gcpAIStudioMintedMsg:
		// Mint failure is non-fatal: project setup still works for
		// Vertex. Carry the error string into the done msg so the
		// scope picker can surface it after this screen exits.
		if msg.err != nil {
			return m, m.emitDoneCmdWithMintErr(msg.err.Error())
		}
		m.mintedAIStudio = msg.key
		return m, m.emitDoneCmd(msg.key)
	}

	keyMsg, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return m, nil
	}

	switch m.state {
	case gcpStatePickingProject:
		return m.updatePickingProject(keyMsg)
	case gcpStateEditingNewName:
		return m.updateEditingNewName(keyMsg)
	case gcpStatePickingRegion:
		return m.updatePickingRegion(keyMsg)
	case gcpStateBillingBlocked:
		return m.updateBillingBlocked(keyMsg)
	case gcpStateError:
		// Any key dismisses the error and cancels.
		return m, func() tea.Msg { return gcpSetupCancelMsg{} }
	case gcpStateLoading, gcpStateCreatingProject, gcpStateEnabling, gcpStateBillingCheck, gcpStateMintingAIStudio:
		// Async ops in flight: only esc cancels (ctrl+c handled at top).
		if keyMsg.String() == "esc" {
			return m, func() tea.Msg { return gcpSetupCancelMsg{} }
		}
	}
	return m, nil
}

func (m gcpSetupModel) updatePickingProject(msg tea.KeyMsg) (gcpSetupModel, tea.Cmd) {
	// Cursor positions: 0..len(projects)-1 are existing projects;
	// len(projects) is the synthetic "+ new project" row.
	maxCur := len(m.projects)
	switch msg.String() {
	case "up", "k":
		if m.projectCur > 0 {
			m.projectCur--
		}
	case "down", "j":
		if m.projectCur < maxCur {
			m.projectCur++
		}
	case "enter":
		if m.projectCur == maxCur {
			// "+ new project" row.
			m.state = gcpStateEditingNewName
			m.nameInput.SetValue("")
			m.nameInput.Focus()
			return m, nil
		}
		m.chosenProject = m.projects[m.projectCur]
		m.createdNew = false
		m.state = gcpStateEnabling
		m.notice = fmt.Sprintf("Enabling APIs on %s...", m.chosenProject.ProjectID)
		return m, m.enableServicesCmd()
	case "esc":
		return m, func() tea.Msg { return gcpSetupCancelMsg{} }
	}
	return m, nil
}

func (m gcpSetupModel) updateEditingNewName(msg tea.KeyMsg) (gcpSetupModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			return m, nil
		}
		id := generateGCPProjectID()
		m.state = gcpStateCreatingProject
		m.notice = fmt.Sprintf("Creating project %q (id: %s)... 5-30s", name, id)
		return m, m.createProjectCmd(id, name)
	case "esc":
		// Back to project picker.
		m.state = gcpStatePickingProject
		m.nameInput.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	return m, cmd
}

// updateBillingBlocked drives the billing-required screen. r
// re-checks (calls cloudbilling again); c continues without
// billing; esc cancels the whole flow.
func (m gcpSetupModel) updateBillingBlocked(msg tea.KeyMsg) (gcpSetupModel, tea.Cmd) {
	switch msg.String() {
	case "r", "R":
		m.notice = "Re-checking billing..."
		m.state = gcpStateBillingCheck
		return m, m.checkBillingCmd()
	case "c", "C":
		m.state = gcpStatePickingRegion
		m.notice = "Proceeding without billing — calls may fail."
		return m, nil
	case "esc":
		return m, func() tea.Msg { return gcpSetupCancelMsg{} }
	}
	return m, nil
}

func (m gcpSetupModel) updatePickingRegion(msg tea.KeyMsg) (gcpSetupModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.regionCur > 0 {
			m.regionCur--
		}
	case "down", "j":
		if m.regionCur < len(gcp.SupportedVertexRegions)-1 {
			m.regionCur++
		}
	case "enter":
		// Region picked. If the credential already has an AI Studio
		// key, skip the mint step entirely and emit done. Otherwise
		// transition to mintingAIStudio and kick off the API call.
		if m.hasAIStudioKey {
			return m, m.emitDoneCmd(nil)
		}
		m.state = gcpStateMintingAIStudio
		m.notice = fmt.Sprintf("Minting AI Studio API key under %s (restricted to %s)...", m.chosenProject.ProjectID, gcp.AIStudioServiceTarget)
		return m, m.mintAIStudioCmd()
	case "esc":
		return m, func() tea.Msg { return gcpSetupCancelMsg{} }
	}
	return m, nil
}

// emitDoneCmd returns a cmd that emits gcpSetupDoneMsg carrying the
// orchestrator's accumulated state. Caller passes the (optional)
// freshly-minted AI Studio key.
func (m gcpSetupModel) emitDoneCmd(key *gcp.APIKey) tea.Cmd {
	return m.emitDoneCmdWith(key, "")
}

// emitDoneCmdWithMintErr is the failure-path variant that records
// the mint error so the scope picker can surface it.
func (m gcpSetupModel) emitDoneCmdWithMintErr(mintErr string) tea.Cmd {
	return m.emitDoneCmdWith(nil, mintErr)
}

func (m gcpSetupModel) emitDoneCmdWith(key *gcp.APIKey, mintErr string) tea.Cmd {
	region := gcp.SupportedVertexRegions[m.regionCur]
	account := m.account
	project := m.chosenProject
	createdNew := m.createdNew
	billing := m.billingEnabled
	return func() tea.Msg {
		return gcpSetupDoneMsg{
			account:     account,
			projectID:   project.ProjectID,
			projectName: project.Name,
			region:      region,
			createdNew:  createdNew,
			billing:     billing,
			aiStudio:    key,
			aiStudioErr: mintErr,
		}
	}
}

func (m gcpSetupModel) mintAIStudioCmd() tea.Cmd {
	client := m.client
	projectID := m.chosenProject.ProjectID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		op, err := client.CreateAPIKey(ctx, projectID, gcp.AIStudioDisplayName, []string{gcp.AIStudioServiceTarget})
		if err != nil {
			return gcpAIStudioMintedMsg{err: err}
		}
		if !op.Done {
			op, err = client.WaitAPIKeyOperation(ctx, op.Name)
			if err != nil {
				return gcpAIStudioMintedMsg{err: err}
			}
		}
		key, err := gcp.ExtractAPIKey(op)
		if err != nil {
			return gcpAIStudioMintedMsg{err: err}
		}
		if key.KeyString == "" {
			return gcpAIStudioMintedMsg{err: fmt.Errorf("minted key has empty KeyString")}
		}
		return gcpAIStudioMintedMsg{key: key}
	}
}

func (m gcpSetupModel) createProjectCmd(id, name string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		op, err := client.CreateProject(ctx, id, name, nil)
		if err != nil {
			return gcpProjectCreatedMsg{err: err}
		}
		if !op.Done {
			if err := client.WaitOperation(ctx, op.Name, 0); err != nil {
				return gcpProjectCreatedMsg{err: err}
			}
		}
		return gcpProjectCreatedMsg{projectID: id, projectName: name}
	}
}

func (m gcpSetupModel) enableServicesCmd() tea.Cmd {
	client := m.client
	projectID := m.chosenProject.ProjectID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		err := client.BatchEnableServices(ctx, projectID, gcp.RequiredServices)
		return gcpServicesEnabledMsg{err: err}
	}
}

func (m gcpSetupModel) checkBillingCmd() tea.Cmd {
	client := m.client
	projectID := m.chosenProject.ProjectID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		info, err := client.GetBillingInfo(ctx, projectID)
		if err != nil {
			return gcpBillingCheckedMsg{enabled: false, err: err}
		}
		return gcpBillingCheckedMsg{enabled: info.BillingEnabled}
	}
}

func (m gcpSetupModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("Google Cloud setup — %s", m.account)))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n\n")

	switch m.state {
	case gcpStateLoading:
		b.WriteString("  Loading your Google Cloud projects...\n")
	case gcpStatePickingProject:
		b.WriteString("  Pick a project:\n\n")
		for i, p := range m.projects {
			cursor := "  "
			if i == m.projectCur {
				cursor = "> "
			}
			marker := "  "
			if m.pinnedProject != nil && p.ProjectID == m.pinnedProject.ProjectID {
				marker = "● " // currently configured
			}
			fmt.Fprintf(&b, "  %s%s%-30s  %s\n", cursor, marker, p.ProjectID, p.Name)
		}
		// Synthetic "+ new project" row at index len(m.projects).
		newCursor := "  "
		if m.projectCur == len(m.projects) {
			newCursor = "> "
		}
		fmt.Fprintf(&b, "  %s  + new project\n", newCursor)
	case gcpStateEditingNewName:
		b.WriteString("  New Google Cloud project\n\n")
		b.WriteString("  ")
		b.WriteString(m.nameInput.View())
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  enter: create    esc: back"))
	case gcpStateCreatingProject, gcpStateEnabling, gcpStateBillingCheck, gcpStateMintingAIStudio:
		b.WriteString("  ")
		b.WriteString(m.notice)
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("  esc: cancel"))
	case gcpStateBillingBlocked:
		b.WriteString("  ")
		b.WriteString(rowDelStyle.Render("Billing setup required"))
		b.WriteString("\n\n")
		fmt.Fprintf(&b, "  Project %s has no billing account linked.\n", m.chosenProject.ProjectID)
		b.WriteString("  Vertex calls will return BILLING_DISABLED, and AI Studio's free-tier\n")
		b.WriteString("  quota is 0 for charon-created projects. Both fail until billing is linked.\n\n")
		b.WriteString("  Open this URL in a browser, link a billing account, then press [r]:\n\n")
		fmt.Fprintf(&b, "    %s\n\n", gcp.BillingFixURL(m.chosenProject.ProjectID))
		b.WriteString(helpStyle.Render("  [r] re-check    [c] continue without billing    [esc] cancel"))
	case gcpStatePickingRegion:
		b.WriteString(fmt.Sprintf("  Project: %s (%s)\n", m.chosenProject.ProjectID, m.chosenProject.Name))
		if m.notice != "" {
			b.WriteString("\n  ")
			b.WriteString(m.notice)
			b.WriteString("\n")
		}
		b.WriteString("\n  Pick a Vertex AI region:\n\n")
		for i, r := range gcp.SupportedVertexRegions {
			cursor := "  "
			if i == m.regionCur {
				cursor = "> "
			}
			marker := " "
			if r == gcp.DefaultVertexRegion {
				marker = "*"
			}
			fmt.Fprintf(&b, "  %s%s %s\n", cursor, marker, r)
		}
	case gcpStateError:
		b.WriteString(rowDelStyle.Render(fmt.Sprintf("  %v\n", m.err)))
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("  press any key to dismiss"))
	}

	b.WriteString("\n\n")
	switch m.state {
	case gcpStatePickingProject:
		b.WriteString(helpStyle.Render("  ↑↓ nav   enter pick   esc cancel"))
	case gcpStatePickingRegion:
		b.WriteString(helpStyle.Render("  ↑↓ nav   enter pick   esc cancel"))
	}
	return b.String()
}

// mergePinned ensures pinned (if non-nil) appears in the returned
// list, prepended when projects.list hasn't caught up to a recent
// create. Already-listed pinned projects are left in place — no
// duplication, original order preserved.
func mergePinned(listed []gcp.Project, pinned *gcp.Project) []gcp.Project {
	if pinned == nil {
		return listed
	}
	for _, p := range listed {
		if p.ProjectID == pinned.ProjectID {
			return listed
		}
	}
	out := make([]gcp.Project, 0, len(listed)+1)
	out = append(out, *pinned)
	out = append(out, listed...)
	return out
}

// generateGCPProjectID mirrors the orchestrator's generator. Defined
// here too so the TUI doesn't need to call into orchestrator code.
// Keep in sync with internal/providers/gcp/setup.go::generateProjectID.
func generateGCPProjectID() string {
	// Reuse the package-level helper via a tiny wrapper to avoid
	// duplicating crypto/rand boilerplate. Calling into the gcp
	// package here is fine — TUI already imports it.
	return gcp.GenerateProjectID()
}

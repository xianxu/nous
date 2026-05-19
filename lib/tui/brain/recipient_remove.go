package brain

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	libbrain "github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/brainsync"
)

// recipient_remove.go is the in-TUI revocation flow. Mirrors the CLI
// safeguards from cmd/nous/brain_recipient.go:
//
//   1. Last-recipient guard — refuses with a banner when count <= 1.
//      No way past it inside the TUI; matches CLI behavior.
//   2. Self-removal warning — when the cursored recipient has a secret
//      half on this machine, demands typing REMOVE-SELF in plain text
//      before proceeding (TUI equivalent of CLI's --force).
//   3. Revocation caveat — operator must type REVOKED-OUT-OF-BAND so
//      they internalize that gcrypt blobs already on the remote remain
//      readable to the removed recipient with their existing material.
//
// All three safeguards are enforced sequentially; esc at any stage
// emits cancelRecipientFlowMsg.

type removeStage int

const (
	removeStagePick removeStage = iota
	removeStageSelfConfirm
	removeStageCaveatConfirm
	removeStageApply
	removeStageDone
)

type removeApplyResultMsg struct {
	last8 string
	err   error
}

type recipientRemovedMsg struct {
	last8 string
	err   error
}

type recipientRemoveModel struct {
	brainPath string

	stage       removeStage
	recipients  []libbrain.RecipientInfo
	cursor      int
	selfConfirm textinput.Model

	chosen        string // canonical fingerprint after pick
	wouldLockOut  bool   // removing `chosen` leaves no local secret on the brain
	allRecipients []string
	// lockoutMarker[i] is true if removing recipients[i] would leave no
	// local-secret recipient on this brain. Computed up front so the
	// picker row can render the safeguard marker before enter is
	// pressed — so the operator sees what they're about to gate on,
	// not after.
	lockoutMarker []bool

	banner string
	final  string
	err    error
}

// Typed-phrase ceremony only for REMOVE-SELF — that stage gates the
// destruction of the operator's own decrypt path, which earns the
// friction. The revocation caveat is informational (gcrypt re-keys
// only future blobs; already-pushed blobs stay readable to the
// removed recipient), so it's an enter-to-confirm prompt rather than
// another typed-phrase ceremony.
const selfRemovePhrase = "REMOVE-SELF"

func newRecipientRemoveModel(brainPath string, recipients []libbrain.RecipientInfo) recipientRemoveModel {
	sc := textinput.New()
	sc.Prompt = "  type " + selfRemovePhrase + "> "
	sc.CharLimit = len(selfRemovePhrase) + 4
	sc.Width = 40

	all := make([]string, 0, len(recipients))
	for _, r := range recipients {
		all = append(all, r.Fingerprint)
	}
	// Pre-compute the would-lock-out marker per row. WouldLockOut is
	// fail-closed, so an outage shows the safeguard on every row
	// rather than hiding it — defensive UX matching defensive logic.
	markers := make([]bool, len(recipients))
	for i, r := range recipients {
		locked, _ := libbrain.WouldLockOut(all, r.Fingerprint)
		markers[i] = locked
	}
	return recipientRemoveModel{
		brainPath:     brainPath,
		stage:         removeStagePick,
		recipients:    recipients,
		allRecipients: all,
		lockoutMarker: markers,
		selfConfirm:   sc,
	}
}

func (m recipientRemoveModel) Init() tea.Cmd { return textinput.Blink }

func (m recipientRemoveModel) Update(msg tea.Msg) (recipientRemoveModel, tea.Cmd) {
	switch m.stage {
	case removeStagePick:
		return m.updatePick(msg)
	case removeStageSelfConfirm:
		return m.updateSelfConfirm(msg)
	case removeStageCaveatConfirm:
		return m.updateCaveatConfirm(msg)
	case removeStageApply:
		return m.updateApply(msg)
	case removeStageDone:
		return m.updateDone(msg)
	}
	return m, nil
}

func (m recipientRemoveModel) updatePick(msg tea.Msg) (recipientRemoveModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.recipients)-1 {
			m.cursor++
		}
	case "enter":
		if len(m.recipients) == 0 {
			return m, nil
		}
		// Hard-fail on last-recipient. Banner-only — operator must esc.
		if len(m.recipients) <= 1 {
			m.banner = "refusing to remove the only recipient — would orphan the brain"
			return m, nil
		}
		ri := m.recipients[m.cursor]
		m.chosen = ri.Fingerprint
		// Functional safeguard: does removing this leave the operator
		// with no decrypt path on this brain? Pre-computed at model
		// init so the picker row can flag it before enter is pressed
		// — operator sees [⚠ would lock you out] next to the row that
		// triggers the REMOVE-SELF stage.
		m.wouldLockOut = m.lockoutMarker[m.cursor]
		m.banner = ""
		if m.wouldLockOut {
			m.stage = removeStageSelfConfirm
			m.selfConfirm.Focus()
		} else {
			m.stage = removeStageCaveatConfirm
		}
		return m, nil
	case "esc":
		return m, func() tea.Msg { return cancelRecipientFlowMsg{} }
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m recipientRemoveModel) updateSelfConfirm(msg tea.Msg) (recipientRemoveModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter":
			got := strings.TrimSpace(m.selfConfirm.Value())
			if got != selfRemovePhrase {
				m.banner = "doesn't match — type " + selfRemovePhrase + " exactly (case-sensitive)"
				m.selfConfirm.SetValue("")
				return m, nil
			}
			m.banner = ""
			m.stage = removeStageCaveatConfirm
			m.selfConfirm.Blur()
			return m, nil
		case "esc":
			return m, func() tea.Msg { return cancelRecipientFlowMsg{} }
		case "ctrl+c":
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.selfConfirm, cmd = m.selfConfirm.Update(msg)
	return m, cmd
}

// updateCaveatConfirm — informational caveat, enter to proceed,
// esc to cancel. The caveat content is the value (forcing the
// operator to read it); typing a phrase here would be friction
// without a matching threat (we're not gating credential leakage,
// just making sure they internalize that already-pushed blobs stay
// readable). REMOVE-SELF still uses a typed phrase because that
// stage destroys the operator's own decrypt path.
func (m recipientRemoveModel) updateCaveatConfirm(msg tea.Msg) (recipientRemoveModel, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "enter":
		m.banner = ""
		m.stage = removeStageApply
		return m, m.applyCmd()
	case "esc":
		return m, func() tea.Msg { return cancelRecipientFlowMsg{} }
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m recipientRemoveModel) updateApply(msg tea.Msg) (recipientRemoveModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	res, ok := msg.(removeApplyResultMsg)
	if !ok {
		return m, nil
	}
	m.stage = removeStageDone
	m.err = res.err
	if res.err == nil {
		m.final = fmt.Sprintf("revoked %s; pushed.", res.last8)
	}
	return m, nil
}

func (m recipientRemoveModel) updateDone(msg tea.Msg) (recipientRemoveModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter", "esc":
			return m, func() tea.Msg {
				last8 := ""
				if len(m.chosen) >= 8 {
					last8 = strings.ToLower(m.chosen[len(m.chosen)-8:])
				}
				return recipientRemovedMsg{last8: last8, err: m.err}
			}
		case "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m recipientRemoveModel) applyCmd() tea.Cmd {
	brainPath := m.brainPath
	chosen := m.chosen
	short := chosen
	if len(short) >= 8 {
		short = strings.ToLower(short[len(short)-8:])
	}
	return func() tea.Msg {
		man, err := libbrain.Read(brainPath)
		if err != nil {
			return removeApplyResultMsg{err: fmt.Errorf("read manifest: %w", err)}
		}
		// Re-check guard inside the apply phase. The picker enforced
		// it earlier but the manifest could have changed between the
		// picker render and the apply (paranoia / race-free).
		if err := libbrain.CanRemoveRecipient(man); err != nil {
			return removeApplyResultMsg{err: err}
		}
		man.Recipients = libbrain.WithoutRecipient(man.Recipients, chosen)
		if err := libbrain.RewriteFrontmatter(brainPath, man); err != nil {
			return removeApplyResultMsg{err: fmt.Errorf("rewrite frontmatter: %w", err)}
		}
		// gcrypt-participants derives from the manifest at push time
		// (nous#24); AddCommitPush below handles the sync.
		if err := brainsync.AddCommitPush(brainPath, fmt.Sprintf("recipient: revoke %s", short)); err != nil {
			return removeApplyResultMsg{err: fmt.Errorf("push: %w", err)}
		}
		return removeApplyResultMsg{last8: short}
	}
}

func (m recipientRemoveModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("revoke recipient — %s", m.brainPath)))
	b.WriteString("\n\n")

	switch m.stage {
	case removeStagePick:
		b.WriteString("Pick a recipient to revoke:\n\n")
		if len(m.recipients) == 0 {
			b.WriteString(mutedStyle.Render("  (none)"))
			b.WriteString("\n")
		}
		for i, r := range m.recipients {
			lockMarker := ""
			if i < len(m.lockoutMarker) && m.lockoutMarker[i] {
				lockMarker = " " + warnStyle.Render("[⚠ would lock you out]")
			}
			row := fmt.Sprintf("  %s  %s%s", shortFP(r.Fingerprint), styledAnnotation(r.Annotation), lockMarker)
			if i == m.cursor {
				// Cursor row: keep the marker visible (cursorRowStyle's
				// background would otherwise blanket-tint it). Render
				// the row chrome and lockMarker as separate spans so
				// the warning still pops on the highlighted line.
				rowBody := fmt.Sprintf("▸ %s  %s", shortFP(r.Fingerprint), r.Annotation)
				row = cursorRowStyle.Render(rowBody) + lockMarker
			}
			b.WriteString(row + "\n")
		}
		if m.banner != "" {
			b.WriteString("\n")
			b.WriteString(warnStyle.Render(m.banner))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("↑↓/jk  navigate    enter  select    esc  cancel    ctrl+c  quit"))
	case removeStageSelfConfirm:
		b.WriteString(warnStyle.Render("SELF-REMOVAL"))
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("  Removing %s means you lose access to this brain after the\n  next push (gcrypt re-encrypts without your key).\n",
			shortFP(m.chosen)))
		b.WriteString("\n")
		b.WriteString(m.selfConfirm.View())
		b.WriteString("\n")
		if m.banner != "" {
			b.WriteString(warnStyle.Render(m.banner))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("enter  confirm    esc  cancel    ctrl+c  quit"))
	case removeStageCaveatConfirm:
		b.WriteString(warnStyle.Render("REVOCATION CAVEAT"))
		b.WriteString("\n")
		b.WriteString("  gcrypt re-encrypts on push, so future commits will exclude this recipient.\n")
		b.WriteString("  However: any gcrypt blob currently in the remote (or in their local clone)\n")
		b.WriteString("  remains readable to them with their existing key material. True revocation\n")
		b.WriteString("  requires re-keying the brain (rotate the operator's key + re-encrypt all\n")
		b.WriteString("  history).\n")
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("enter  proceed    esc  cancel    ctrl+c  quit"))
	case removeStageApply:
		b.WriteString(mutedStyle.Render("applying..."))
		b.WriteString("\n  manifest → gcrypt participants → commit → push")
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("ctrl+c  quit (operations in flight will complete)"))
	case removeStageDone:
		if m.err != nil {
			b.WriteString(warnStyle.Render("✗ " + m.err.Error()))
		} else {
			b.WriteString(selfAnnotStyle.Render("✓ " + m.final))
		}
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("enter/esc  back to detail    ctrl+c  quit"))
	}
	return b.String()
}

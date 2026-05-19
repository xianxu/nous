package brain

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	libbrain "github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/brainsync"
	"github.com/xianxu/nous/lib/identity"
)

// recipient_add.go ports the verify-fingerprint ceremony into a native
// bubbletea sub-model so the operator stays inside the TUI for the
// add flow. Same trust boundary as the CLI version
// (cmd/nous/brain_recipient.go) — only the rendering changes.
//
// Stages:
//
//	addStageSource  list-pick from three buckets: imported peers
//	                (in the local keyring without secret material —
//	                what `nous identity import` produces), local
//	                *.pub files in the operator's cwd, or "type a
//	                path manually". Skipped when neither imported
//	                peers nor *.pub files are around → straight to
//	                addStagePath.
//	addStagePath    textinput for a pubkey file path (or "-" stdin
//	                — but stdin is impractical inside bubbletea, so
//	                paths only here). Blocked enter without a value.
//	addStageInspect render fingerprint/UID/last-8 + a textinput for
//	                "type the last 8 hex chars to confirm". Up to 3
//	                attempts, then aborts. The verify-fingerprint
//	                ceremony runs even for already-imported peers —
//	                admitting to a brain is a distinct trust event
//	                from importing the pubkey.
//	addStageApply   async: identity.Import + brain.RewriteFrontmatter
//	                + brain.SetGcryptParticipants +
//	                brainsync.AddCommitPush. Static "applying..."
//	                while waiting on addApplyResultMsg.
//	addStageDone    success/error banner. enter/esc pops to detail.

// recipientAddedMsg signals success back to the detail model so it can
// flash a banner + reload the brain status. err is set on failure
// (banner stays red, but detail still refreshes since partial state
// — e.g. manifest updated, push failed — is worth reflecting).
type recipientAddedMsg struct {
	last8 string
	err   error
}

// cancelRecipientFlowMsg is the user-initiated abort (esc on any
// stage). Detail model handles by clearing focus + popping back to
// the read-only view, no banner.
type cancelRecipientFlowMsg struct{}

type addStage int

const (
	addStageSource addStage = iota
	addStagePath
	addStageInspect
	addStageApply
	addStageDone
)

type addApplyResultMsg struct {
	last8 string
	err   error
}

// sourceKind tags each row in the source picker (addStageSource).
type sourceKind int

const (
	sourcePeer   sourceKind = iota // already in keyring; needs identity.Export
	sourceFile                     // *.pub in cwd; needs os.ReadFile
	sourceManual                   // sentinel; transitions to addStagePath
)

// sourceItem is one row in the source picker. label is what the user
// sees; fp / path carry the data for whichever load method applies.
type sourceItem struct {
	kind  sourceKind
	label string
	fp    string // sourcePeer only
	path  string // sourceFile only
}

type recipientAddModel struct {
	brainPath string

	stage addStage

	sources []sourceItem // addStageSource list (always non-empty: manual entry sentinel at end)
	cursor  int          // addStageSource selection

	path  textinput.Model // pubkey file path (addStagePath)
	last8 textinput.Model // confirmation (addStageInspect)

	pendingKey  identity.Key // populated after inspect
	pendingFile string       // armor text held until apply
	attempts    int

	banner string // transient hint (wrong last-8, fs errors, etc.)
	final  string // populated in addStageDone
	err    error  // populated in addStageDone on failure
}

func newRecipientAddModel(brainPath string) recipientAddModel {
	p := textinput.New()
	p.Placeholder = "/path/to/peer-pubkey.asc"
	p.Prompt = "  pubkey path> "
	p.CharLimit = 1024
	p.Width = 64

	l := textinput.New()
	l.Placeholder = "8 hex chars"
	l.Prompt = "  last-8> "
	l.CharLimit = 8
	l.Width = 12

	m := recipientAddModel{
		brainPath: brainPath,
		path:      p,
		last8:     l,
		sources:   collectSources(),
	}
	// If the only source is the manual-entry sentinel (no imported
	// peers in keyring AND no *.pub in cwd), skip the picker and go
	// straight to the manual-path stage — same UX as the pre-source-
	// picker version.
	if len(m.sources) == 1 {
		m.stage = addStagePath
		m.path.Focus()
	} else {
		m.stage = addStageSource
	}
	return m
}

// collectSources builds the picker rows for addStageSource. Three
// buckets, in operator-priority order: imported peers (highest
// signal — operator already vetted the pubkey via `nous identity
// import`), local *.pub files (sneakernet just landed it), manual
// path entry (sentinel; always last).
func collectSources() []sourceItem {
	var items []sourceItem

	// Imported peers: keys whose public half is in the keyring but
	// secret half isn't (i.e., not the operator's own identity).
	if peers, err := identity.ListPublic(); err == nil {
		// Stable order so the cursor doesn't jump between launches.
		sort.Slice(peers, func(i, j int) bool {
			return peers[i].Last8() < peers[j].Last8()
		})
		for _, p := range peers {
			uid := p.UID
			if uid == "" {
				uid = "(no UID)"
			}
			items = append(items, sourceItem{
				kind:  sourcePeer,
				label: fmt.Sprintf("[peer]  %s  %s", p.Last8(), uid),
				fp:    p.Fingerprint,
			})
		}
	}

	// Local *.pub files in the current working directory. Glob fails
	// silently → no file entries appear, fine.
	if matches, err := filepath.Glob("*.pub"); err == nil {
		sort.Strings(matches)
		for _, m := range matches {
			items = append(items, sourceItem{
				kind:  sourceFile,
				label: fmt.Sprintf("[file]  %s", m),
				path:  m,
			})
		}
	}

	// Manual entry sentinel — always present so the operator can
	// reach the typed-path flow even when no other sources are
	// available, and as an escape hatch when the right key isn't
	// in either of the auto-picked buckets.
	items = append(items, sourceItem{
		kind:  sourceManual,
		label: "Enter a pubkey file path manually...",
	})
	return items
}

func (m recipientAddModel) Init() tea.Cmd { return textinput.Blink }

func (m recipientAddModel) Update(msg tea.Msg) (recipientAddModel, tea.Cmd) {
	switch m.stage {
	case addStageSource:
		return m.updateSource(msg)
	case addStagePath:
		return m.updatePath(msg)
	case addStageInspect:
		return m.updateInspect(msg)
	case addStageApply:
		return m.updateApply(msg)
	case addStageDone:
		return m.updateDone(msg)
	}
	return m, nil
}

func (m recipientAddModel) updateSource(msg tea.Msg) (recipientAddModel, tea.Cmd) {
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
		if m.cursor < len(m.sources)-1 {
			m.cursor++
		}
	case "enter":
		src := m.sources[m.cursor]
		switch src.kind {
		case sourcePeer:
			armor, err := identity.Export(src.fp)
			if err != nil {
				m.banner = "export: " + err.Error()
				return m, nil
			}
			return m.advanceToInspect(armor), nil
		case sourceFile:
			armor, err := os.ReadFile(src.path)
			if err != nil {
				m.banner = "read: " + err.Error()
				return m, nil
			}
			return m.advanceToInspect(string(armor)), nil
		case sourceManual:
			m.stage = addStagePath
			m.path.Focus()
			m.banner = ""
			return m, textinput.Blink
		}
	case "esc":
		return m, func() tea.Msg { return cancelRecipientFlowMsg{} }
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// advanceToInspect loads an armored pubkey blob, inspects it, and
// transitions to the verify-fingerprint stage. Shared by the source
// picker (peer / file branches) and the manual-path entry — every
// path through the flow lands here before the operator types the
// last-8.
func (m recipientAddModel) advanceToInspect(armor string) recipientAddModel {
	key, err := identity.Inspect(armor)
	if err != nil {
		m.banner = "inspect: " + err.Error()
		return m
	}
	m.pendingKey = key
	m.pendingFile = armor
	m.banner = ""
	m.stage = addStageInspect
	m.path.Blur()
	m.last8.Focus()
	return m
}

func (m recipientAddModel) updatePath(msg tea.Msg) (recipientAddModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter":
			p := strings.TrimSpace(m.path.Value())
			if p == "" {
				m.banner = "enter a path to the peer's pubkey (.asc / .gpg)"
				return m, nil
			}
			armor, err := os.ReadFile(p)
			if err != nil {
				m.banner = "read: " + err.Error()
				return m, nil
			}
			return m.advanceToInspect(string(armor)), nil
		case "esc":
			// If we came here via the source picker, go back there
			// rather than aborting the whole flow.
			if len(m.sources) > 1 {
				m.stage = addStageSource
				m.path.Blur()
				m.path.SetValue("")
				m.banner = ""
				return m, nil
			}
			return m, func() tea.Msg { return cancelRecipientFlowMsg{} }
		case "ctrl+c":
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.path, cmd = m.path.Update(msg)
	return m, cmd
}

func (m recipientAddModel) updateInspect(msg tea.Msg) (recipientAddModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter":
			got := strings.ToLower(strings.TrimSpace(m.last8.Value()))
			want := strings.ToLower(m.pendingKey.Last8())
			if got != want {
				m.attempts++
				if m.attempts >= 3 {
					m.stage = addStageDone
					m.err = fmt.Errorf("verify-fingerprint failed after 3 attempts — aborting (not imported)")
					return m, nil
				}
				m.banner = fmt.Sprintf("no match (attempt %d/3) — try again", m.attempts)
				m.last8.SetValue("")
				return m, nil
			}
			// Match. Kick the apply phase.
			m.banner = ""
			m.stage = addStageApply
			m.last8.Blur()
			return m, m.applyCmd()
		case "esc":
			return m, func() tea.Msg { return cancelRecipientFlowMsg{} }
		case "ctrl+c":
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.last8, cmd = m.last8.Update(msg)
	return m, cmd
}

func (m recipientAddModel) updateApply(msg tea.Msg) (recipientAddModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	res, ok := msg.(addApplyResultMsg)
	if !ok {
		return m, nil
	}
	m.stage = addStageDone
	m.err = res.err
	if res.err == nil {
		m.final = fmt.Sprintf("admitted %s; pushed.", res.last8)
	}
	return m, nil
}

func (m recipientAddModel) updateDone(msg tea.Msg) (recipientAddModel, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "enter", "esc":
			return m, func() tea.Msg {
				return recipientAddedMsg{last8: m.pendingKey.Last8(), err: m.err}
			}
		case "ctrl+c":
			return m, tea.Quit
		}
	}
	return m, nil
}

// applyCmd runs the actual import + manifest update + commit/push off
// the main event loop. Returned as a tea.Cmd so the program's Update
// loop stays responsive (the View renders "applying..." while it runs).
func (m recipientAddModel) applyCmd() tea.Cmd {
	armor := m.pendingFile
	fp := m.pendingKey.Fingerprint
	last8 := m.pendingKey.Last8()
	brainPath := m.brainPath
	return func() tea.Msg {
		if _, err := identity.Import(armor); err != nil {
			return addApplyResultMsg{err: fmt.Errorf("import: %w", err)}
		}
		man, err := libbrain.Read(brainPath)
		if err != nil {
			return addApplyResultMsg{err: fmt.Errorf("read manifest: %w", err)}
		}
		if libbrain.ContainsRecipient(man.Recipients, fp) {
			unpushed, _ := brainsync.HasUnpushedCommits(brainPath)
			if unpushed {
				if err := brainsync.Push(brainPath); err != nil {
					return addApplyResultMsg{err: fmt.Errorf("push: %w", err)}
				}
			}
			return addApplyResultMsg{last8: last8}
		}
		man.Recipients = append(man.Recipients, fp)
		if err := libbrain.RewriteFrontmatter(brainPath, man); err != nil {
			return addApplyResultMsg{err: fmt.Errorf("rewrite frontmatter: %w", err)}
		}
		if err := libbrain.SetGcryptParticipants(brainPath, man.Recipients); err != nil {
			return addApplyResultMsg{err: fmt.Errorf("gcrypt participants: %w", err)}
		}
		if err := brainsync.AddCommitPush(brainPath, fmt.Sprintf("recipient: admit %s", last8)); err != nil {
			return addApplyResultMsg{err: fmt.Errorf("push: %w", err)}
		}
		return addApplyResultMsg{last8: last8}
	}
}

func (m recipientAddModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("admit recipient — %s", m.brainPath)))
	b.WriteString("\n\n")

	switch m.stage {
	case addStageSource:
		b.WriteString("Pick the pubkey to admit:\n")
		b.WriteString(mutedStyle.Render(
			"[peer]  already in keyring (e.g. via `nous identity import`)\n" +
				"[file]  *.pub in the current directory"))
		b.WriteString("\n\n")
		for i, s := range m.sources {
			if i == m.cursor {
				b.WriteString(cursorRowStyle.Render("▸ " + s.label))
			} else {
				b.WriteString("  " + s.label)
			}
			b.WriteString("\n")
		}
		if m.banner != "" {
			b.WriteString("\n")
			b.WriteString(warnStyle.Render(m.banner))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("↑↓/jk  navigate    enter  pick    esc  cancel    ctrl+c  quit"))
	case addStagePath:
		b.WriteString("Paste or type the path to the peer's pubkey file (.asc / .gpg).\n")
		b.WriteString(mutedStyle.Render("The verify-fingerprint ceremony runs next — make sure the operator\nsent you their last-8 OUT OF BAND (voice/in-person), not the same\nchannel that delivered the pubkey itself."))
		b.WriteString("\n\n")
		b.WriteString(m.path.View())
		b.WriteString("\n")
		if m.banner != "" {
			b.WriteString(warnStyle.Render(m.banner))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("enter  load    esc  cancel    ctrl+c  quit"))
	case addStageInspect:
		k := m.pendingKey
		b.WriteString("Pubkey loaded:\n")
		b.WriteString(fmt.Sprintf("  fingerprint: %s\n", k.Fingerprint))
		b.WriteString(fmt.Sprintf("  last-8:      %s\n", k.Last8()))
		uid := k.UID
		if uid == "" {
			uid = "(no UID)"
		}
		b.WriteString(fmt.Sprintf("  uid:         %s\n", uid))
		b.WriteString("\n")
		b.WriteString(warnStyle.Render(
			"VERIFY the last 8 hex chars match what the peer sent you OUT OF BAND\n(phone, in-person, signed message — NOT the same channel as the pubkey)."))
		b.WriteString("\n\n")
		b.WriteString(m.last8.View())
		b.WriteString("\n")
		if m.banner != "" {
			b.WriteString(warnStyle.Render(m.banner))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(helpStyle.Render("enter  confirm    esc  cancel    ctrl+c  quit"))
	case addStageApply:
		b.WriteString(mutedStyle.Render("applying..."))
		b.WriteString("\n  import → manifest → gcrypt participants → commit → push")
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("ctrl+c  quit (operations in flight will complete)"))
	case addStageDone:
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

package brain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	libbrain "github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/gh"
)

// drillInMsg signals "navigate to the detail view for brain at path."
// The root model intercepts and pushes a fresh detail model.
type drillInMsg struct{ path string }

// listItem represents one row on the brain list. Either a local
// brain (manifest set, isPending false) or a pending GitHub
// invitation we haven't accepted yet (invitation set, isPending
// true). Mutually exclusive.
type listItem struct {
	manifest   libbrain.Manifest // local brain
	invitation gh.Invitation     // pending GitHub invite
	isPending  bool              // discriminator
	isOperator bool              // local-brain-only; false for invitations
}

// labelInner is the post-marker text — basename, kind, recipient
// count for local brains; full_name + [invited] for pending. The
// marker prefix (`*` for operator, ` ` otherwise) is added at
// render time so cursor-row highlighting can include or exclude it
// consistently with the rest of the row.
func (it listItem) labelInner() string {
	if it.isPending {
		return fmt.Sprintf("%-22s  (invited — press enter to join)",
			it.invitation.Repository.FullName)
	}
	kind := "private"
	if it.manifest.Shared() {
		kind = "shared"
	}
	// Display the directory basename rather than manifest.Name. The
	// manifest's `name:` field is operator-authored and can drift from
	// the on-disk location (e.g. brain `name: personal` sits at
	// ~/workspace/brain); for "which repo am I looking at?" the
	// basename is the unambiguous answer.
	return fmt.Sprintf("%-22s  (%s, %d recipients)",
		filepath.Base(it.manifest.Path), kind, len(it.manifest.Recipients))
}

type listModel struct {
	items   []listItem
	cursor  int
	err     error  // shown in View when non-nil; drill-in disabled
	myLogin string // auth'd github user; "" when gh outage
}

func newListModel() listModel {
	manifests, err := libbrain.DiscoverAll()
	if err != nil {
		return listModel{err: err}
	}
	// Resolve auth'd login once for all the IsOperator probes. Empty
	// on outage — marker just doesn't render, consistent with the CLI
	// list's behavior.
	myLogin, _ := gh.AuthLogin()

	items := make([]listItem, 0, len(manifests))
	for _, m := range manifests {
		items = append(items, listItem{
			manifest:   m,
			isOperator: libbrain.IsOperator(m.Path, myLogin),
		})
	}
	// Local brains sorted by basename for stability.
	sort.Slice(items, func(i, j int) bool {
		return filepath.Base(items[i].manifest.Path) < filepath.Base(items[j].manifest.Path)
	})

	// Append pending brain invitations after local brains. Best-
	// effort: a gh outage shouldn't block the list view — invitations
	// just don't render. Filter to brain projects via the same
	// description/topic markers nous brain join uses (lives in
	// brain_join.go in package main; we duplicate the predicate
	// here as `isBrainInvitation` to avoid a TUI → cmd/nous
	// dependency).
	if invites, ierr := gh.PendingInvitations(); ierr == nil {
		brainInvites := make([]gh.Invitation, 0, len(invites))
		for _, inv := range invites {
			if isBrainInvitation(inv) {
				brainInvites = append(brainInvites, inv)
			}
		}
		sort.Slice(brainInvites, func(i, j int) bool {
			return brainInvites[i].Repository.FullName < brainInvites[j].Repository.FullName
		})
		for _, inv := range brainInvites {
			items = append(items, listItem{invitation: inv, isPending: true})
		}
	}

	return listModel{items: items, myLogin: myLogin}
}

// isBrainInvitation mirrors the filter in cmd/nous/brain_join.go's
// filterBrainInvitations. Duplicated rather than exported across
// the package boundary because the predicate is small + stable.
// Markers: description prefix `nous-brain:`, substring
// `gcrypt-encrypted brain` (legacy new-brain.sh wording), or topic
// `nous-brain`.
func isBrainInvitation(inv gh.Invitation) bool {
	desc := strings.ToLower(inv.Repository.Description)
	if strings.HasPrefix(desc, "nous-brain:") {
		return true
	}
	if strings.Contains(desc, "gcrypt-encrypted brain") {
		return true
	}
	for _, t := range inv.Repository.Topics {
		if strings.EqualFold(t, "nous-brain") {
			return true
		}
	}
	return false
}

func (m listModel) Init() tea.Cmd { return nil }

// joinSubprocessDoneMsg signals "the join subprocess returned." Root
// handles by re-instantiating the list model (so the newly-joined
// brain disappears from the pending-invitation section and any
// new local brain shows up after auto-admit + clone).
type joinSubprocessDoneMsg struct{ err error }

func (m listModel) Update(msg tea.Msg) (listModel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch keyMsg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "enter":
		if m.err != nil || len(m.items) == 0 {
			return m, nil
		}
		it := m.items[m.cursor]
		if it.isPending {
			// Pending invitation row → delegate to the CLI's join
			// flow (which knows how to pick GPG identity, accept
			// invitation, publish pubkey, etc.). Pass the
			// owner/repo as positional so we skip the multi-pick
			// listing and go straight to "join this one."
			bin, err := os.Executable()
			if err != nil {
				bin = "nous"
			}
			cmd := exec.Command(bin, "brain", "join")
			cmd.Env = os.Environ()
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				return joinSubprocessDoneMsg{err: err}
			})
		}
		path := it.manifest.Path
		return m, func() tea.Msg { return drillInMsg{path: path} }
	case "n":
		// Launch the new-brain flow regardless of whether the list
		// is populated — `n` is a useful entry point even when there
		// are zero brains yet (first-run experience).
		return m, func() tea.Msg { return launchNewBrainMsg{} }
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m listModel) View() string {
	var b strings.Builder
	title := "Brains"
	if m.myLogin != "" {
		title = fmt.Sprintf("Brains (%s)", m.myLogin)
	}
	b.WriteString(titleStyle.Render(title))
	b.WriteString("\n\n")

	if m.err != nil {
		b.WriteString(warnStyle.Render("error: " + m.err.Error()))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("q/esc quit"))
		return b.String()
	}
	if len(m.items) == 0 {
		b.WriteString(mutedStyle.Render("(no brains found under workspace root)"))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("n  create one    q/esc  quit"))
		return b.String()
	}

	for i, it := range m.items {
		marker := " "
		if it.isOperator {
			marker = "*"
		}
		body := marker + " " + it.labelInner()
		row := "  " + body
		if i == m.cursor {
			row = cursorRowStyle.Render("▸ " + body)
		} else if it.isPending {
			// Muted style for pending invitations — visually
			// distinct from local brains so the operator's eye
			// knows "not yet mine to drill into, can join with
			// enter." Cursor row keeps the highlight color for
			// affordance.
			row = mutedStyle.Render(row)
		}
		b.WriteString(row)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if m.myLogin != "" {
		b.WriteString(mutedStyle.Render("  (* = owner)"))
		b.WriteString("\n")
	}
	b.WriteString(helpStyle.Render("↑↓/jk  navigate    enter  drill in    n  new brain    q/esc  quit"))
	return b.String()
}

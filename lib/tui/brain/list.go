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
	"github.com/xianxu/nous/lib/workspace"
)

// drillInMsg signals "navigate to the detail view for brain at path."
// The root model intercepts and pushes a fresh detail model.
type drillInMsg struct{ path string }

// listItem represents one row on the brain list. Exactly one of:
//   - local brain (manifest set, no flag)
//   - pending GitHub invitation (invitation set, isPending true)
//   - accessible-but-not-cloned (uncloned set, isUncloned true) —
//     operator is a github collaborator (or owner) on a brain repo
//     but hasn't run `nous brain clone` against it. The post-join
//     interim state; without this, brains "disappear" the moment
//     an invite is accepted until the operator manually clones.
type listItem struct {
	manifest   libbrain.Manifest // local brain
	invitation gh.Invitation     // pending GitHub invite
	uncloned   gh.UserRepo       // accessible but not cloned
	isPending  bool
	isUncloned bool
	isOperator bool // local-brain-only; false for invitations / uncloned
}

// labelInner is the post-marker text — basename + kind/count for
// local brains; full_name + status for non-local rows. The marker
// prefix (`*` for operator, ` ` otherwise) is added at render time
// so cursor-row highlighting can include or exclude it consistently
// with the rest of the row.
//
// User-facing terminology: "collaborators," not "recipients." The
// manifest schema still uses `recipients:` on disk (stable contract),
// but the UI talks about collaborators — that's the model the operator
// thinks in (GitHub-collaborator-add = trust admission per nous#26).
func (it listItem) labelInner() string {
	if it.isPending {
		return fmt.Sprintf("%-22s  (invited — press enter to join)",
			it.invitation.Repository.FullName)
	}
	if it.isUncloned {
		return fmt.Sprintf("%-22s  (collaborator — press enter to clone)",
			it.uncloned.FullName)
	}
	// Display the directory basename rather than manifest.Name. The
	// manifest's `name:` field is operator-authored and can drift from
	// the on-disk location (e.g. brain `name: personal` sits at
	// ~/workspace/brain); for "which repo am I looking at?" the
	// basename is the unambiguous answer.
	name := filepath.Base(it.manifest.Path)
	n := len(it.manifest.Recipients)
	if n <= 1 {
		return fmt.Sprintf("%-22s  (private)", name)
	}
	return fmt.Sprintf("%-22s  (shared, %d collaborators)", name, n)
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

	// Build a set of github full_names for local brains so we can
	// exclude them from the uncloned probe below. Local-brain remotes
	// look like `gcrypt::ssh://git@github.com/<owner>/<repo>.git`;
	// brain.GitHubOwnerRepo parses it.
	localFullNames := make(map[string]bool, len(manifests))
	for _, m := range manifests {
		if url := libbrain.ReadOriginURL(m.Path); url != "" {
			if owner, repo, err := libbrain.GitHubOwnerRepo(url); err == nil {
				localFullNames[strings.ToLower(owner+"/"+repo)] = true
			}
		}
	}

	// Append pending brain invitations after local brains. Best-
	// effort: a gh outage shouldn't block the list view — invitations
	// just don't render. Filter to brain projects via the same
	// description/topic markers nous brain join uses (lives in
	// brain_join.go in package main; we duplicate the predicate
	// here as `isBrainInvitation` to avoid a TUI → cmd/nous
	// dependency).
	pendingFullNames := map[string]bool{}
	if invites, ierr := gh.PendingInvitations(); ierr == nil {
		brainInvites := make([]gh.Invitation, 0, len(invites))
		for _, inv := range invites {
			if isBrainInvitation(inv) {
				brainInvites = append(brainInvites, inv)
				pendingFullNames[strings.ToLower(inv.Repository.FullName)] = true
			}
		}
		sort.Slice(brainInvites, func(i, j int) bool {
			return brainInvites[i].Repository.FullName < brainInvites[j].Repository.FullName
		})
		for _, inv := range brainInvites {
			items = append(items, listItem{invitation: inv, isPending: true})
		}
	}

	// Accessible-but-not-cloned: brain repos the operator has github
	// access to but doesn't have a local manifest for. Catches the
	// interim state between "I accepted the invitation" (consumed
	// from PendingInvitations) and "I ran nous brain clone." Without
	// this, brains visually disappear at that exact transition —
	// exactly what bit ying in nous#27's first manual repro.
	if repos, rerr := gh.UserRepos(); rerr == nil {
		brainRepos := make([]gh.UserRepo, 0, len(repos))
		for _, r := range repos {
			if !isBrainRepo(r) {
				continue
			}
			fullLower := strings.ToLower(r.FullName)
			if localFullNames[fullLower] {
				continue // already a local brain
			}
			if pendingFullNames[fullLower] {
				continue // still pending invite; rendered above
			}
			brainRepos = append(brainRepos, r)
		}
		sort.Slice(brainRepos, func(i, j int) bool {
			return brainRepos[i].FullName < brainRepos[j].FullName
		})
		for _, r := range brainRepos {
			items = append(items, listItem{uncloned: r, isUncloned: true})
		}
	}

	return listModel{items: items, myLogin: myLogin}
}

// isBrainRepo applies the same brain-marker filter to a UserRepo as
// isBrainInvitation does to an Invitation. Both endpoints use the
// same MinimalRepository shape, but the embedded struct types
// differ (gh.Invitation.Repository.*  vs gh.UserRepo.*), so we have
// two small filters rather than one polymorphic predicate.
func isBrainRepo(r gh.UserRepo) bool {
	desc := strings.ToLower(r.Description)
	if strings.HasPrefix(desc, "nous-brain:") {
		return true
	}
	if strings.Contains(desc, "gcrypt-encrypted brain") {
		return true
	}
	for _, t := range r.Topics {
		if strings.EqualFold(t, "nous-brain") {
			return true
		}
	}
	return false
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

// cloneSubprocessDoneMsg signals "the clone subprocess returned"
// (Enter on an uncloned-row). Same handling as joinSubprocessDoneMsg:
// refresh list on success, exit TUI cleanly on failure so the
// operator sees subprocess output in scrollback.
type cloneSubprocessDoneMsg struct{ err error }

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
		switch {
		case it.isPending:
			// Pending invitation row → delegate to the CLI's join
			// flow (which knows how to pick GPG identity, accept
			// invitation, publish pubkey, etc.).
			bin, err := os.Executable()
			if err != nil {
				bin = "nous"
			}
			cmd := exec.Command(bin, "brain", "join")
			cmd.Env = os.Environ()
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				return joinSubprocessDoneMsg{err: err}
			})
		case it.isUncloned:
			// Accessible-but-not-cloned row → delegate to
			// `nous brain clone <gcrypt-url> <target>`. Target is
			// always <workspace-root>/<repo-basename> — brains live
			// as peers of nous, never inside the CWD where the TUI
			// happened to launch. The repo-basename mirrors github's
			// default clone behavior; workspace.Root resolves
			// $WORKSPACE_ROOT → $NOUS_DIR's parent → $HOME/workspace.
			bin, err := os.Executable()
			if err != nil {
				bin = "nous"
			}
			target := ""
			if root, rerr := workspace.Root(); rerr == nil {
				target = filepath.Join(root, it.uncloned.Name)
			}
			gcryptURL := "gcrypt::" + it.uncloned.CloneSSHURL()
			args := []string{"brain", "clone", gcryptURL}
			if target != "" {
				args = append(args, target)
			}
			cmd := exec.Command(bin, args...)
			cmd.Env = os.Environ()
			return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
				return cloneSubprocessDoneMsg{err: err}
			})
		default:
			path := it.manifest.Path
			return m, func() tea.Msg { return drillInMsg{path: path} }
		}
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
		} else if it.isPending || it.isUncloned {
			// Muted style for non-local rows (pending invitations
			// + accessible-but-not-cloned brains). Visually
			// distinct from local brains so the operator's eye
			// knows "not yet mine to drill into, action on enter."
			// Cursor row keeps the highlight color for affordance.
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

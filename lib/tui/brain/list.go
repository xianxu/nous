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
	hasRemote  bool // local-brain-only; true once published (origin set)
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
	r := classifyRung(it.hasRemote, n)
	return fmt.Sprintf("%-22s  %s", name, rungLabel(r, n))
}

type listModel struct {
	gh gh.Client

	items   []listItem
	cursor  int
	err     error  // shown in View when non-nil; drill-in disabled
	myLogin string // auth'd github user; "" when gh outage

	// loadingRemote is true while the async fetch of gh data is in
	// flight (initial-load with no cache, or 'r' refresh). The view
	// renders a subtle "loading collaborators..." line while true so
	// the operator knows more rows may appear shortly.
	loadingRemote bool
}

// listLoadedData carries the gh-fetched data that we cache on
// rootModel so navigating back to the list (e.g. ESC from detail)
// doesn't re-run the network. Holds the raw remote data, not
// rebuilt list items — local manifests can change between
// navigations (operator created/deleted a brain), so items must be
// recomputed against the fresh local set on each list construction.
type listLoadedData struct {
	myLogin     string
	invitations []gh.Invitation
	repos       []gh.UserRepo
}

// listLoadedMsg is the result of the async load triggered by Init
// (or 'r' refresh). The list model folds the payload into its
// items; the root model captures it as the new cache.
type listLoadedMsg struct {
	data listLoadedData
}

// newListModel builds the list model. When `cache` is non-nil
// (re-entering the list via popToListMsg with a prior load in
// hand), the items are computed immediately from local manifests +
// the cached gh data + `extraRepos` and Init returns nil. When
// `cache` is nil (first entry to the TUI, or after explicit
// refresh / cache invalidation), only local-brain items plus the
// extraRepos are built synchronously; Init returns the async load
// Cmd, and listLoadedMsg later folds in the rest of the remote rows.
//
// extraRepos is the session-scoped "I just accepted these" splice
// that papers over GitHub's /user/repos lag — see rootModel's
// justAccepted field for the rationale.
//
// Splitting the model this way keeps the constructor filesystem-
// only (DiscoverAll is fast) and pushes the slow `gh` subprocess
// calls off the bubbletea event loop.
func newListModel(c gh.Client, cache *listLoadedData, extraRepos []gh.UserRepo) listModel {
	manifests, err := libbrain.DiscoverAll()
	if err != nil {
		return listModel{gh: c, err: err}
	}
	if cache == nil {
		// Render immediately with local brains and any
		// just-accepted-but-not-yet-on-/user/repos uncloned rows.
		// The async load completes via Init() and folds in
		// invitations + the rest of the uncloned set.
		data := listLoadedData{repos: extraRepos}
		items := buildAllItems(c, manifests, data)
		return listModel{gh: c, items: items, loadingRemote: true}
	}
	merged := *cache
	merged.repos = mergeUserReposDedup(cache.repos, extraRepos)
	items := buildAllItems(c, manifests, merged)
	return listModel{gh: c, items: items, myLogin: cache.myLogin}
}

// mergeUserReposDedup returns the union of a and b, deduped by
// case-insensitive FullName. Used to splice rootModel.justAccepted
// into the visible list without producing duplicates once
// /user/repos catches up.
func mergeUserReposDedup(a, b []gh.UserRepo) []gh.UserRepo {
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]gh.UserRepo, 0, len(a)+len(b))
	for _, r := range a {
		key := strings.ToLower(r.FullName)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	for _, r := range b {
		key := strings.ToLower(r.FullName)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}

// buildLocalItems builds list items for the local brains only,
// applying the operator marker if myLogin is non-empty.
func buildLocalItems(c gh.Client, manifests []libbrain.Manifest, myLogin string) []listItem {
	items := make([]listItem, 0, len(manifests))
	for _, m := range manifests {
		hasRemote := libbrain.ReadOriginURL(m.Path) != ""
		// A local brain (no remote) is trivially yours — show the
		// owner marker even with no gh auth. A published brain defers
		// to GitHub ownership/permission via IsOperator.
		isOp := !hasRemote
		if hasRemote && myLogin != "" {
			isOp = libbrain.IsOperator(c, m.Path, myLogin)
		}
		items = append(items, listItem{manifest: m, isOperator: isOp, hasRemote: hasRemote})
	}
	sort.Slice(items, func(i, j int) bool {
		return filepath.Base(items[i].manifest.Path) < filepath.Base(items[j].manifest.Path)
	})
	return items
}

// buildAllItems builds the full item list: local brains (with
// IsOperator marker), then pending invitations, then accessible-
// but-not-cloned repos. Pure function over local manifests + the
// cached remote payload — called from newListModel (cache hit
// path) and from listLoadedMsg handling (post-async-load path).
func buildAllItems(c gh.Client, manifests []libbrain.Manifest, data listLoadedData) []listItem {
	items := buildLocalItems(c, manifests, data.myLogin)

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

	// Pending brain invitations. Filter to brain projects via the
	// description/topic markers nous brain join uses.
	pendingFullNames := map[string]bool{}
	brainInvites := make([]gh.Invitation, 0, len(data.invitations))
	for _, inv := range data.invitations {
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

	// Accessible-but-not-cloned: brain repos the operator has github
	// access to but doesn't have a local manifest for. Catches the
	// interim state between "I accepted the invitation" and "I ran
	// nous brain clone." Without this, brains visually disappear at
	// that exact transition — exactly what bit ying in nous#27.
	brainRepos := make([]gh.UserRepo, 0, len(data.repos))
	for _, r := range data.repos {
		if !isBrainRepo(r) {
			continue
		}
		fullLower := strings.ToLower(r.FullName)
		if localFullNames[fullLower] {
			continue
		}
		if pendingFullNames[fullLower] {
			continue
		}
		brainRepos = append(brainRepos, r)
	}
	sort.Slice(brainRepos, func(i, j int) bool {
		return brainRepos[i].FullName < brainRepos[j].FullName
	})
	for _, r := range brainRepos {
		items = append(items, listItem{uncloned: r, isUncloned: true})
	}
	return items
}

// loadRemoteCmd is the async fetch of gh data that previously
// blocked newListModel. Returns a tea.Cmd suitable for Init() or
// for an explicit 'r' refresh. Errors are absorbed into empty
// payloads — a gh outage shouldn't break the list view, it should
// just mean invitations / uncloned rows don't render.
func loadRemoteCmd(c gh.Client) tea.Cmd {
	return func() tea.Msg {
		myLogin, _ := c.AuthLogin()
		invites, _ := c.PendingInvitations()
		repos, _ := c.UserRepos()
		return listLoadedMsg{data: listLoadedData{
			myLogin:     myLogin,
			invitations: invites,
			repos:       repos,
		}}
	}
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

func (m listModel) Init() tea.Cmd {
	if m.loadingRemote {
		return loadRemoteCmd(m.gh)
	}
	return nil
}

// cloneSubprocessDoneMsg signals "the clone subprocess returned"
// (Enter on an uncloned-row). Same handling as joinSubprocessDoneMsg:
// refresh list on success, exit TUI cleanly on failure so the
// operator sees subprocess output in scrollback.
type cloneSubprocessDoneMsg struct{ err error }

func (m listModel) Update(msg tea.Msg) (listModel, tea.Cmd) {
	if lm, ok := msg.(listLoadedMsg); ok {
		// Fold remote data into items. Re-discover local manifests
		// rather than relying on m.items so a newly-created brain
		// during the load window shows up correctly.
		manifests, derr := libbrain.DiscoverAll()
		if derr != nil {
			m.err = derr
			m.loadingRemote = false
			return m, nil
		}
		m.items = buildAllItems(m.gh, manifests, lm.data)
		m.myLogin = lm.data.myLogin
		m.loadingRemote = false
		// Clamp cursor if items shrank (unlikely on load, but cheap).
		if m.cursor >= len(m.items) {
			m.cursor = len(m.items) - 1
		}
		if m.cursor < 0 {
			m.cursor = 0
		}
		return m, nil
	}
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch keyMsg.String() {
	case "r":
		// Manual refresh: re-issue the async load. Keeps current
		// items rendered until the new data arrives — operator gets
		// a non-blank screen while the gh calls run.
		if !m.loadingRemote {
			m.loadingRemote = true
			return m, loadRemoteCmd(m.gh)
		}
		return m, nil
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
			// Pending invitation row → launch the inline accept-
			// invite TUI flow (no subprocess, no flicker). The
			// flow handles GPG identity pick, gh.AcceptInvitation,
			// and the plain-git push of <login>.asc to the keys
			// branch. No pinentry-using subprocess in the flow, so
			// safe to inline regardless of SSH session.
			return m, func() tea.Msg {
				return launchAcceptInviteMsg{invitation: it.invitation}
			}
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
			gcryptURL := "gcrypt::" + m.gh.CloneURL(it.uncloned.FullName, it.uncloned.SSHURL)
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
		if m.loadingRemote {
			b.WriteString(mutedStyle.Render("loading collaborators..."))
		} else {
			b.WriteString(mutedStyle.Render("(no brains found under workspace root)"))
		}
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
	if m.loadingRemote {
		b.WriteString(mutedStyle.Render("  loading collaborators..."))
		b.WriteString("\n")
	}
	b.WriteString(mutedStyle.Render("  (* = owner · local = this device only)"))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑↓/jk  navigate    enter  drill in    n  new brain    r  refresh    q/esc  quit"))
	return b.String()
}

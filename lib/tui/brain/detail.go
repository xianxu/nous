package brain

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	libbrain "github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/gh"
)

// statusLoadedMsg carries an async LoadStatus result back to the detail
// model. Async because LoadStatus can take 100–500ms on a large brain
// (gpg + git invocations); blocking the model's constructor would
// freeze the TUI during the transition from list → detail.
type statusLoadedMsg struct {
	status libbrain.Status
	err    error
}

// openConflictPreviewMsg / popToListMsg / launchRecipient{Add,Remove}Msg
// are emitted by the detail model for the root model to act on. Keeping
// nav as messages (rather than having the detail model own the stack)
// keeps each sub-model's responsibility local to its own screen.
type openConflictPreviewMsg struct {
	root string
	rels []string
}

type popToListMsg struct{}

// launchPublishMsg asks the root model to publish a local brain to
// GitHub (the local → private rung). The root runs `nous brain publish`
// as a foreground subprocess so gpg/ssh pinentry prompts work.
type launchPublishMsg struct {
	brainPath string
}

type launchRecipientAddMsg struct {
	brainPath string
}

type launchRecipientRemoveMsg struct {
	brainPath  string
	recipients []libbrain.RecipientInfo
}

// detailModel renders the drill-in for one brain. It owns the loading
// state machine (loading → ready / failed) and the action keystrokes.
type detailModel struct {
	gh gh.Client

	path    string
	loading bool
	status  libbrain.Status
	err     error

	// banner is a transient status line shown above the help row.
	// Used by M5b (recipient add/remove results) and the M5a "not
	// wired yet" hints for action keys.
	banner string
}

func newDetailModel(c gh.Client, path string) detailModel {
	return detailModel{gh: c, path: path, loading: true}
}

func (m detailModel) Init() tea.Cmd {
	c := m.gh
	path := m.path
	return func() tea.Msg {
		s, err := libbrain.LoadStatus(c, path)
		return statusLoadedMsg{status: s, err: err}
	}
}

func (m detailModel) Update(msg tea.Msg) (detailModel, tea.Cmd) {
	switch msg := msg.(type) {
	case statusLoadedMsg:
		m.loading = false
		m.status = msg.status
		m.err = msg.err
		return m, nil
	case tea.KeyMsg:
		// Actions are gated by the brain's rung (nous#33): a local brain
		// can only `p` publish; a published brain can `a`/`r`/`l`. We
		// don't list — or accept — actions that the current rung can't
		// perform, rather than letting them fail downstream.
		if m.loading || m.err != nil {
			// Only nav keys work while loading / on error.
			switch msg.String() {
			case "esc":
				return m, func() tea.Msg { return popToListMsg{} }
			case "q", "ctrl+c":
				return m, tea.Quit
			}
			return m, nil
		}
		r := classifyRung(m.status.OriginURL != "", len(m.status.Manifest.Recipients))
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return popToListMsg{} }
		case "q", "ctrl+c":
			return m, tea.Quit
		case "c":
			if len(m.status.ConflictFiles) == 0 {
				m.banner = "no conflicts to preview"
				return m, nil
			}
			return m, func() tea.Msg {
				return openConflictPreviewMsg{root: m.path, rels: m.status.ConflictFiles}
			}
		case "p":
			// Publish: local → private. Only meaningful on a local brain.
			if r != rungLocal {
				return m, nil
			}
			return m, func() tea.Msg {
				return launchPublishMsg{brainPath: m.path}
			}
		case "a":
			// Invite a collaborator: needs a hosted repo. Local brains
			// must publish first.
			if r == rungLocal {
				m.banner = "publish first (press p) — can't invite to a local-only brain"
				return m, nil
			}
			return m, func() tea.Msg {
				return launchInviteCollabMsg{brainPath: m.path}
			}
		case "r":
			// Remove a collaborator: only meaningful on a shared brain.
			if r != rungShared {
				return m, nil
			}
			if len(m.status.Recipients) == 0 {
				m.banner = "no collaborators to remove"
				return m, nil
			}
			return m, func() tea.Msg {
				return launchRecipientRemoveMsg{brainPath: m.path, recipients: m.status.Recipients}
			}
		case "l":
			// Leave this brain — only meaningful when it's shared (a
			// private/local brain is solely yours; there's nothing to
			// leave). Refuses inside LeaveBrain itself; the TUI just
			// routes to the confirm screen.
			if r != rungShared {
				return m, nil
			}
			return m, func() tea.Msg {
				return launchLeaveMsg{brainPath: m.path}
			}
		}
	}
	return m, nil
}

func (m detailModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("brain: %s", m.path)))
	b.WriteString("\n")

	if m.loading {
		b.WriteString(mutedStyle.Render("loading status..."))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("esc  back    q/ctrl+c  quit"))
		return b.String()
	}
	if m.err != nil {
		b.WriteString(warnStyle.Render("error: " + m.err.Error()))
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("esc  back    q/ctrl+c  quit"))
		return b.String()
	}

	s := m.status
	// Header reflects the topology rung (nous#33), not just recipient
	// count: a local brain and a hosted-solo brain are both "private" by
	// recipients but a rung apart. The label tells the operator where
	// the brain lives.
	rg := classifyRung(s.OriginURL != "", len(s.Manifest.Recipients))
	var headerLine string
	switch rg {
	case rungLocal:
		headerLine = "local — on this device only, no remote"
	case rungShared:
		headerLine = fmt.Sprintf("shared · %d collaborators · github", len(s.Manifest.Recipients))
	default:
		headerLine = "private · github"
	}
	// sync_substrate names any *additional* file-sync layer beyond
	// git+gcrypt (syncthing, git-daemon). The default "" / "none" means
	// "git+gcrypt only" — which IS the sync mechanism — so showing it
	// in the header was misleading ("am I not syncing?"). Surface only
	// when a substrate is actually configured.
	if sub := s.Manifest.SyncSubstrate; sub != "" && sub != "none" {
		headerLine += fmt.Sprintf(" · sync_substrate: %s", sub)
	}
	b.WriteString(mutedStyle.Render(headerLine))
	b.WriteString("\n")

	if s.Mismatch {
		b.WriteString(warnStyle.Render(
			"⚠ manifest collaborators and gcrypt-participants disagree — run `nous brain recipient list` to inspect"))
		b.WriteString("\n")
	}

	// Collaborators
	b.WriteString(sectionHeaderStyle.Render("Collaborators"))
	b.WriteString("\n")
	if len(s.Recipients) == 0 {
		b.WriteString(mutedStyle.Render("  (none)"))
		b.WriteString("\n")
	}
	for _, r := range s.Recipients {
		annot := r.Annotation
		annot = styledAnnotation(annot)
		flags := recipientFlags(r)
		b.WriteString(fmt.Sprintf("  %s  %s %s\n", shortFP(r.Fingerprint), annot, flags))
	}

	// Pending invitations the operator sent that aren't yet
	// accepted. Rendered only when there's at least one — keeps the
	// detail page quiet on the common (no-pending) path. Suppressed
	// entirely when the gh probe couldn't see the endpoint (no admin
	// access on the repo, gh outage, non-github origin).
	if len(s.PendingInvitations) > 0 {
		b.WriteString(sectionHeaderStyle.Render("Pending invitations"))
		b.WriteString("\n")
		for _, inv := range s.PendingInvitations {
			line := "  " + inv.Invitee.Login
			if inv.CreatedAt != "" {
				if t, err := time.Parse(time.RFC3339, inv.CreatedAt); err == nil {
					line += "  " + mutedStyle.Render("(invited "+libbrain.HumanizeDuration(time.Since(t))+")")
				}
			}
			if inv.Expired {
				line += "  " + warnStyle.Render("[expired]")
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	// Sync
	b.WriteString(sectionHeaderStyle.Render("Sync"))
	b.WriteString("\n")
	if s.LastCommit.Hash == "" {
		b.WriteString("  ")
		b.WriteString(mutedStyle.Render("(no commits yet)"))
		b.WriteString("\n")
	} else {
		b.WriteString(fmt.Sprintf("  last commit: %s  %s  %s\n",
			s.LastCommit.ShortHash, s.LastCommit.RelTime, s.LastCommit.Subject))
	}
	if !s.HasUpstream {
		b.WriteString("  ")
		if s.OriginURL == "" {
			// Intentional state for a local brain, not a misconfiguration.
			b.WriteString(mutedStyle.Render("local only — lives on this device, no remote"))
		} else {
			b.WriteString(mutedStyle.Render("no upstream configured"))
		}
		b.WriteString("\n")
	} else {
		ahead := fmt.Sprintf("ahead %d", s.Ahead)
		behind := fmt.Sprintf("behind %d", s.Behind)
		if s.Ahead > 0 {
			ahead = aheadStyle.Render(ahead)
		}
		if s.Behind > 0 {
			behind = behindStyle.Render(behind)
		}
		b.WriteString(fmt.Sprintf("  upstream: %s, %s\n", ahead, behind))
	}

	// Conflicts
	b.WriteString(sectionHeaderStyle.Render("Conflicts"))
	b.WriteString("\n")
	if len(s.ConflictFiles) == 0 {
		b.WriteString("  ")
		b.WriteString(mutedStyle.Render("(none)"))
		b.WriteString("\n")
	} else {
		b.WriteString(fmt.Sprintf("  %s\n", warnStyle.Render(fmt.Sprintf("%d file(s):", len(s.ConflictFiles)))))
		max := 5
		if len(s.ConflictFiles) < max {
			max = len(s.ConflictFiles)
		}
		for _, p := range s.ConflictFiles[:max] {
			b.WriteString("    " + p + "\n")
		}
		if len(s.ConflictFiles) > max {
			b.WriteString("    ")
			b.WriteString(mutedStyle.Render(fmt.Sprintf("... and %d more (press c to preview)", len(s.ConflictFiles)-max)))
			b.WriteString("\n")
		}
	}

	// Share with peers — tell the operator what to say to invitees
	// they've already admitted as collaborators. The canonical
	// joiner path is now the `nous brain` TUI itself: pending
	// GitHub invitations show up in its list with `enter` to
	// accept, and the brain then appears as accessible-but-not-
	// cloned with `enter` again to fetch. The legacy `nous brain
	// clone <gcrypt-url>` and `nous brain join <repo>` commands
	// still exist as plumbing but operators shouldn't need to
	// hand them out. Shown only for shared brains with a
	// configured origin URL.
	if s.Manifest.Shared() && s.OriginURL != "" {
		b.WriteString(sectionHeaderStyle.Render("Share with peers"))
		b.WriteString("\n")
		b.WriteString("  Tell each invitee to run ")
		b.WriteString(cursorRowStyle.Render("nous brain"))
		b.WriteString(" on their machine.\n")
		b.WriteString(mutedStyle.Render(
			"  Pending GitHub invitations appear in their brain list with\n" +
				"  `enter` to accept; the brain then shows as accessible with\n" +
				"  `enter` again to clone. Pubkey exchange happens automatically\n" +
				"  via the brain's keys branch. Out-of-band verification is\n" +
				"  optional: `nous brain recipient verify`."))
		b.WriteString("\n")
	}

	if m.banner != "" {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("• " + m.banner))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	// State-gated footer: show only the actions valid at this rung —
	// the "next gesture up the ladder" plus management for shared brains.
	var actions []string
	switch rg {
	case rungLocal:
		actions = []string{"p  publish to GitHub"}
	case rungShared:
		actions = []string{"a  invite", "r  remove collaborator", "l  leave brain"}
	default: // rungPrivate
		actions = []string{"a  invite a collaborator"}
	}
	if len(s.ConflictFiles) > 0 {
		actions = append(actions, "c  preview conflicts")
	}
	actions = append(actions, "esc  back", "q  quit")
	b.WriteString(helpStyle.Render(strings.Join(actions, "    ")))
	return b.String()
}

// shortFP returns the last 8 hex chars of a fingerprint, lowercased, to
// match the operator-facing convention used in cmd/nous (matches what
// `nous identity list` prints).
func shortFP(fp string) string {
	fp = strings.ToLower(fp)
	if len(fp) < 8 {
		return fp
	}
	return fp[len(fp)-8:]
}

func recipientFlags(r libbrain.RecipientInfo) string {
	switch {
	case r.InManifest && r.InGcrypt:
		return ""
	case r.InManifest && !r.InGcrypt:
		return warnStyle.Render("[manifest-only]")
	case !r.InManifest && r.InGcrypt:
		return warnStyle.Render("[gcrypt-only]")
	}
	return ""
}

func styledAnnotation(annot string) string {
	switch {
	case strings.HasPrefix(annot, "(self)"):
		return selfAnnotStyle.Render(annot)
	case strings.HasPrefix(annot, "(peer)"):
		return peerAnnotStyle.Render(annot)
	case strings.HasPrefix(annot, "(unknown"):
		return unknownAnnotStyle.Render(annot)
	}
	return annot
}

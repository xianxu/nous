package brain

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	libbrain "github.com/xianxu/nous/lib/brain"
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
	path    string
	loading bool
	status  libbrain.Status
	err     error

	// banner is a transient status line shown above the help row.
	// Used by M5b (recipient add/remove results) and the M5a "not
	// wired yet" hints for action keys.
	banner string
}

func newDetailModel(path string) detailModel {
	return detailModel{path: path, loading: true}
}

func (m detailModel) Init() tea.Cmd {
	return func() tea.Msg {
		s, err := libbrain.LoadStatus(m.path)
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
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return popToListMsg{} }
		case "q", "ctrl+c":
			return m, tea.Quit
		case "c":
			if m.loading || m.err != nil || len(m.status.ConflictFiles) == 0 {
				m.banner = "no conflicts to preview"
				return m, nil
			}
			return m, func() tea.Msg {
				return openConflictPreviewMsg{root: m.path, rels: m.status.ConflictFiles}
			}
		case "a":
			if m.loading || m.err != nil {
				return m, nil
			}
			return m, func() tea.Msg {
				return launchInviteCollabMsg{brainPath: m.path}
			}
		case "r":
			if m.loading || m.err != nil {
				return m, nil
			}
			if len(m.status.Recipients) == 0 {
				m.banner = "no recipients to remove"
				return m, nil
			}
			return m, func() tea.Msg {
				return launchRecipientRemoveMsg{brainPath: m.path, recipients: m.status.Recipients}
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
	kind := "private"
	if s.Manifest.Shared() {
		kind = "shared"
	}
	// sync_substrate names any *additional* file-sync layer beyond
	// git+gcrypt (syncthing, git-daemon). The default "" / "none" means
	// "git+gcrypt only" — which IS the sync mechanism — so showing it
	// in the header was misleading ("am I not syncing?"). Surface only
	// when a substrate is actually configured.
	headerLine := fmt.Sprintf("%s · %d recipient(s)", kind, len(s.Manifest.Recipients))
	if sub := s.Manifest.SyncSubstrate; sub != "" && sub != "none" {
		headerLine += fmt.Sprintf(" · sync_substrate: %s", sub)
	}
	b.WriteString(mutedStyle.Render(headerLine))
	b.WriteString("\n")

	if s.Mismatch {
		b.WriteString(warnStyle.Render(
			"⚠ manifest recipients and gcrypt-participants disagree — run `nous brain recipient list` to inspect"))
		b.WriteString("\n")
	}

	// Recipients
	b.WriteString(sectionHeaderStyle.Render("Recipients"))
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
		b.WriteString(mutedStyle.Render("no upstream configured"))
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

	// Share with peers — surface the one command an operator should
	// hand to a peer after admitting them as a recipient. Auto-imports
	// all peer pubkeys from the brain's keys branch (nous#23) before
	// running gcrypt clone, so the peer doesn't need any pubkey hand-
	// off beyond the initial fingerprint exchange to verify (opt-in
	// via `nous brain recipient verify`). Only shown for shared brains
	// with a configured origin URL.
	if s.Manifest.Shared() && s.OriginURL != "" {
		b.WriteString(sectionHeaderStyle.Render("Share with peers"))
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("  nous brain clone %s\n", s.OriginURL))
		b.WriteString(mutedStyle.Render(
			"  (run on the peer's machine; their fresh clone auto-imports\n" +
				"   every recipient's pubkey from the keys branch, then gcrypt-\n" +
				"   clones the brain. Verify any pubkey out-of-band later with\n" +
				"   `nous brain recipient verify` if desired.)"))
		b.WriteString("\n")
	}

	if m.banner != "" {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("• " + m.banner))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("a  add collaborator    r  remove recipient    c  preview conflicts    esc  back    q  quit"))
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

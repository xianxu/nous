package brain

import (
	"fmt"
	"strings"

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
				return launchRecipientAddMsg{brainPath: m.path}
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

	// Share with peers — surface the full onboarding sequence an
	// operator should hand to a peer they've just admitted as a
	// recipient. Three parts: peer needs YOUR pubkey to verify the
	// gcrypt manifest signature, you need peer's pubkey (already
	// done if they're listed above), and the clone command. Only
	// meaningful for shared brains (private brains have no peers)
	// AND when an origin URL is configured (a brand-new brain
	// before the first push has none).
	if s.Manifest.Shared() && s.OriginURL != "" {
		b.WriteString(sectionHeaderStyle.Render("Share with peers"))
		b.WriteString("\n")
		b.WriteString("  On YOUR machine, send peer your pubkey:\n")
		b.WriteString(mutedStyle.Render("    nous identity export > you.pub      # then sneakernet to peer\n"))
		b.WriteString("  On THEIR machine, import your pubkey + clone the brain:\n")
		b.WriteString(mutedStyle.Render("    nous identity import you.pub        # verify-fingerprint ceremony\n"))
		b.WriteString(mutedStyle.Render(fmt.Sprintf("    git clone %s\n", s.OriginURL)))
		b.WriteString(mutedStyle.Render(
			"  Both directions: gcrypt signs every manifest, so each peer\n" +
				"  needs every other peer's pubkey to verify before decrypting."))
		b.WriteString("\n")
	}

	if m.banner != "" {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("• " + m.banner))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("a  add recipient    r  remove recipient    c  preview conflicts    esc  back    q  quit"))
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

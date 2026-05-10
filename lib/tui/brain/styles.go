// Package brain renders the `nous brain` interactive TUI: list of
// brains under the workspace root → drill-in (recipients, sync state,
// conflicts) → actions (recipient add/remove, M5b).
//
// Domain-scoped sibling of lib/tui/ (which hosts the provider TUI).
// Keeping them separate prevents the two domains' models from sharing
// state by accident; small styling primitives are duplicated locally
// instead of imported, so each TUI evolves independently.
package brain

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7D56F4"))

	sectionHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#7D56F4")).
				MarginTop(1)

	cursorRowStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4"))

	mutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	helpStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Italic(true)

	// Recipient annotation styles — match the cmd/nous output palette
	// so the TUI and CLI feel like one product.
	selfAnnotStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("70"))  // green
	peerAnnotStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))  // blue
	unknownAnnotStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("167")) // red

	// Sync-state styles. behindStyle is more urgent than aheadStyle —
	// behind means a peer pushed work we haven't pulled.
	aheadStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("178")) // yellow
	behindStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("167")) // red

	// Conflict file marker styles for the preview pager.
	conflictHeadStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("167")).Bold(true) // <<<<<<<
	conflictMidStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("178")).Bold(true) // =======
	conflictTailStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)  // >>>>>>>

	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("167")).Bold(true)
)

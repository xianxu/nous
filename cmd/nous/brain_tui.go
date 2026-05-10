package main

import (
	tea "github.com/charmbracelet/bubbletea"
	braintui "github.com/xianxu/nous/lib/tui/brain"
)

// runBrainTUI starts the bubbletea program for `nous brain` (no args,
// TTY only). All UI logic lives in lib/tui/brain; this wrapper is the
// thin cmd-level seam between cobra and the program loop.
func runBrainTUI() error {
	p := tea.NewProgram(braintui.NewRoot(), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

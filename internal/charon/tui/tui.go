// Package tui implements the bubbletea-based scope management UI.
package tui

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xianxu/nous/internal/charon/providers"
	"github.com/xianxu/nous/internal/charon/vault"
	"golang.org/x/term"
)

// Run launches the TUI, blocking until the user exits.
//
// Required: vault. Optional: account (skips picker, implies google
// provider), addr (badges + cache-clear), auth (OAuth apply), and
// admin-key providers via WithAdminKeyProvider for OpenAI/Anthropic
// flows.
//
// gcpFactory is optional: when non-nil, the TUI offers Google Cloud
// project setup from a realized cloud-platform row in the scope
// view. When nil, that path falls back to a status hint.
func Run(v vault.Store, account, addr string, auth Authenticator, gcpFactory func(account string) (GCPSetupClient, error), adminProviders ...providers.Provider) error {
	var opts []Option
	if addr != "" {
		opts = append(opts, WithDenialFetcher(httpDenialFetcher(addr)))
		opts = append(opts, WithProxyAddr(addr))
	}
	if auth != nil {
		opts = append(opts, WithAuthenticator(auth))
	}
	if gcpFactory != nil {
		opts = append(opts, WithGCPClientFactory(gcpFactory))
	}
	for _, p := range adminProviders {
		opts = append(opts, WithAdminKeyProvider(p))
	}
	m, err := newModel(v, account, opts...)
	if err != nil {
		return err
	}
	// alt-screen by default; CHARON_TUI_NO_ALT=1 disables for diagnosing
	// terminals where alt-screen interacts badly with size reporting.
	teaOpts := []tea.ProgramOption{}
	if os.Getenv("CHARON_TUI_NO_ALT") == "" {
		teaOpts = append(teaOpts, tea.WithAltScreen())
	}
	prog := tea.NewProgram(m, teaOpts...)

	// Resize watchdog. Bubbletea relies on SIGWINCH to detect resize, but
	// some multiplexers (cmux #2588 in particular) don't propagate SIGWINCH
	// to child PTYs even though TIOCGWINSZ ioctl returns updated values.
	// nvim/Ink-based TUIs work in those environments because they re-query
	// terminal size on every render; bubbletea is purely event-driven and
	// would otherwise be stuck on whatever size it saw at startup. Poll
	// stdout's size and synthesize a WindowSizeMsg when it changes.
	// Goroutine exits when prog.Run returns and Send becomes a no-op.
	stop := make(chan struct{})
	go watchTerminalSize(prog, stop)
	defer close(stop)

	finalModel, err := prog.Run()
	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	final := finalModel.(model)
	if final.err != nil {
		return final.err
	}
	if final.exitNote != "" {
		fmt.Println(final.exitNote)
	}
	return nil
}

// watchTerminalSize polls stdout's terminal size and Sends a synthetic
// WindowSizeMsg whenever it changes. The first non-zero read also lands
// as a WindowSizeMsg, covering environments where bubbletea's startup
// checkResize either errored or never fired.
func watchTerminalSize(prog *tea.Program, stop <-chan struct{}) {
	var lastW, lastH int
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			w, h, err := term.GetSize(int(os.Stdout.Fd()))
			if err != nil || w <= 0 || h <= 0 {
				continue
			}
			if w == lastW && h == lastH {
				continue
			}
			lastW, lastH = w, h
			prog.Send(tea.WindowSizeMsg{Width: w, Height: h})
		}
	}
}

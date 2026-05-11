// nous serve — single-process foreground daemon that runs both
// nous-substrate runtimes (credential proxy + brain-sync watcher) as
// goroutines under one context. Replaces the legacy two-binary
// model (cmd/charon + cmd/brain-sync), each with its own launchd
// plist. M2 of nous#16.
//
// Audience: (a) for the daemon; scriptable. The interactive surfaces
// are elsewhere (`nous provider` TUI, `nous brain` TUI). Operators
// drive this via `make nous-dev` (foreground) or `nous service install`
// (launchd-managed).
//
// Lifecycle: one signal-handled context wraps both runtimes. If
// either daemon errors out, the context is cancelled and the other
// drains gracefully (proxy via http.Server.Shutdown; sync via Watch's
// ctx-aware loop). The first non-nil error is what nous serve returns.
//
// Flags pass through to the underlying lib functions. --proxy-only
// and --sync-only are mutually exclusive — pass neither (default) to
// run both.

package main

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/xianxu/nous/lib/brainsync"
	"github.com/xianxu/nous/lib/charoncli"
	"github.com/xianxu/nous/lib/provider/proxy"
	"github.com/xianxu/nous/lib/provider/providers/catalog"
	"github.com/xianxu/nous/lib/provider/vault/keychain"
)

func newServeCmd() *cobra.Command {
	var (
		proxyOnly   bool
		syncOnly    bool
		addr        string
		auditPath   string
		brainPaths  []string
		fetchEvery  time.Duration
		verbose     bool
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the credential proxy + brain-sync watcher in one process (a)",
		Long: `Unified foreground daemon: runs the HTTPS credential proxy
(formerly `+"`charon serve`"+`) and the brain-sync watcher (formerly
` + "`brain-sync`" + `) as goroutines under one context.

Default runs both. --proxy-only / --sync-only narrow the surface
for dev iteration on one daemon. Either daemon erroring cancels
the shared context; the other drains gracefully and nous serve
returns the first non-nil error.

Audience: (a). Scriptable; typically driven by ` + "`make nous-dev`" + `
(foreground iteration) or ` + "`nous service install`" + ` (launchd-managed
production). Interactive surfaces are ` + "`nous brain`" + ` / ` + "`nous provider`" + ` —
nous serve has no TUI.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if proxyOnly && syncOnly {
				return fmt.Errorf("--proxy-only and --sync-only are mutually exclusive")
			}
			ctx, cancel := signal.NotifyContext(context.Background(),
				syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			eg, egCtx := errgroup.WithContext(ctx)
			if !syncOnly {
				eg.Go(func() error {
					// Catalog bootstrap stays at this layer (lives at the
					// caller of proxy.Serve; see lib/charoncli for the
					// same pattern + dep-cycle rationale).
					cat, err := catalog.Load()
					if err != nil {
						return fmt.Errorf("load provider catalog: %w", err)
					}
					catalog.Register(cat)
					return proxy.Serve(egCtx, proxy.ServeOptions{
						Listen:    addr,
						Vault:     keychain.New(),
						AuditPath: auditPath,
						Verbose:   verbose,
					})
				})
			}
			if !proxyOnly {
				eg.Go(func() error {
					return brainsync.Run(egCtx, brainsync.RunOptions{
						Brains:     brainPaths,
						FetchEvery: fetchEvery,
						Verbose:    verbose,
					})
				})
			}
			return eg.Wait()
		},
	}
	cmd.Flags().BoolVar(&proxyOnly, "proxy-only", false, "run only the credential proxy")
	cmd.Flags().BoolVar(&syncOnly, "sync-only", false, "run only the brain-sync watcher")
	cmd.Flags().StringVar(&addr, "addr", charoncli.DefaultListenAddr, "credential proxy listen address")
	cmd.Flags().StringVar(&auditPath, "audit-log", "", "proxy audit log path (default: stderr)")
	cmd.Flags().StringSliceVar(&brainPaths, "brain", nil, "absolute path to a shared brain (repeatable; default: auto-discover)")
	cmd.Flags().DurationVar(&fetchEvery, "fetch-every", 30*time.Second, "brain-sync periodic fetch interval")
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "enable debug logging on both daemons")
	return cmd
}

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/xianxu/nous/lib/brainsync"
	"github.com/xianxu/nous/lib/service"
)

// serviceCmdImpl is the real `nous service` cluster (replaces the M3b
// placeholder). One install/start/stop manages both subsystems
// (brain-sync watcher + charon credential proxy) as a unit. No per-
// subsystem service subcommands — there's no value in starting one
// without the other.
//
// Implementation: each subcommand dispatches to both lib/service
// (charon's launchd manager, label com.charon.proxy) and lib/brainsync
// (brain-sync's launchd manager, label com.xianxu.brain-sync), aggregating
// output. Future M5+ work may collapse the two daemons into a single
// `nous serve` process; for now they stay separate launchd services
// for failure isolation.
//
// Binary paths resolved by sibling-binary discovery: cmd/nous looks for
// charon and brain-sync next to itself (same bin/ dir). Falls back to
// PATH lookup if not co-located.
func serviceCmdImpl() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Service control: install/start/stop brain-sync + proxy together",
		Long: `Manages all nous services together as a unit. brain-sync (the brain
sync watcher) and charon (the credential proxy) install/start/stop with
one command — there's no value in starting one without the other.

Subcommands:
  install     Install brain-sync + proxy as launchd services
  uninstall   Remove both
  start       Start (or restart) both
  stop        Stop both
  status      Show what's installed + running across both
  doctor      Prescriptive health check (gpg, identity, brains, services)
  audit       Tail/filter audit logs (charon proxy + brain-sync)`,
	}

	cmd.AddCommand(serviceInstallCmdImpl())
	cmd.AddCommand(serviceUninstallCmdImpl())
	cmd.AddCommand(serviceStartCmdImpl())
	cmd.AddCommand(serviceStopCmdImpl())
	cmd.AddCommand(serviceStatusCmdImpl())
	cmd.AddCommand(newServiceDoctorCmd())
	cmd.AddCommand(newServiceAuditCmd())

	return cmd
}

// resolveSiblingBinary looks for a binary next to the running nous
// executable, falling back to PATH lookup. Returns absolute path.
func resolveSiblingBinary(name string) (string, error) {
	nousBin, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(nousBin), name)
		if _, err := os.Stat(candidate); err == nil {
			abs, err := filepath.Abs(candidate)
			if err == nil {
				return abs, nil
			}
		}
	}
	// Fall back to PATH lookup (works if user has bin/ on PATH or
	// has installed binaries via go install).
	return exec.LookPath(name)
}

func serviceInstallCmdImpl() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install (or reinstall) the unified com.42shots.nous launchd service",
		Long: `Writes the launchd plist for com.42shots.nous and loads it
(launchd starts it immediately via RunAtLoad). Idempotent:
re-running after a binary rebuild updates the plist with the new
path and bounces the service.

Backed by ` + "`bin/nous serve`" + ` (one process running proxy +
brain-sync as goroutines under one signal-handled context). Output
goes to ~/Library/Logs/nous.log.

Migration: stops + uninstalls any pre-existing legacy plists found
(com.charon.proxy, com.xianxu.brain-sync, and the M4-era
com.xianxu.nous label that briefly existed before the 42shots
namespace move) so the install lands clean on machines that ran
earlier nous#16 milestones.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceInstall(cmd.OutOrStdout())
		},
	}
}

// runServiceInstall is the canonical install path: one unified plist
// (com.42shots.nous) backed by `nous serve`. Cleans up any legacy
// plists found before laying down the new one.
func runServiceInstall(out io.Writer) error {
	nousBin, err := resolveSelfBinary()
	if err != nil {
		return fmt.Errorf("resolve nous binary: %w", err)
	}

	// Migration: stop + uninstall any pre-existing legacy plists.
	// Errors are non-fatal — most operators won't have all of these.
	if charonMgr, mgrErr := service.New(); mgrErr == nil {
		_ = charonMgr.Stop()
		if err := charonMgr.Uninstall(); err == nil {
			fmt.Fprintln(out, "  [ok] legacy com.charon.proxy plist removed")
		}
	}
	if brainSyncMgr, mgrErr := brainsync.NewServiceManager(); mgrErr == nil {
		_ = brainSyncMgr.Stop()
		if err := brainSyncMgr.Uninstall(); err == nil {
			fmt.Fprintln(out, "  [ok] legacy com.xianxu.brain-sync plist removed")
		}
	}
	// The brief M4-era com.xianxu.nous label (renamed to
	// com.42shots.nous before any operator ran it for real, but
	// belt-and-braces in case anyone did).
	if oldUnified, mgrErr := service.NewLabeled("com.xianxu.nous", "nous.log", ""); mgrErr == nil {
		_ = oldUnified.Stop()
		if err := oldUnified.Uninstall(); err == nil {
			fmt.Fprintln(out, "  [ok] pre-rename com.xianxu.nous plist removed")
		}
	}

	unifiedMgr, err := service.NewUnified()
	if err != nil {
		return fmt.Errorf("unified service manager: %w", err)
	}
	// Re-install hygiene: if the plist already exists from a prior
	// install, stop+uninstall first so launchctl picks up the new
	// binary path.
	_ = unifiedMgr.Stop()
	_ = unifiedMgr.Uninstall()
	if err := unifiedMgr.Install(nousBin, []string{"serve"}); err != nil {
		return fmt.Errorf("install com.42shots.nous: %w", err)
	}
	fmt.Fprintf(out, "  [ok] com.42shots.nous installed (%s serve) — started by launchd\n", nousBin)
	fmt.Fprintln(out, "Use 'nous service status' to verify.")
	return nil
}

// resolveSelfBinary returns the absolute path of the running nous
// binary. Used by the unified install path so the plist points at
// whatever binary the operator ran — typically `~/.local/bin/nous`
// after a `make nous-install` run.
func resolveSelfBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func serviceUninstallCmdImpl() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the nous launchd service (and any legacy plists found)",
		Long: `Symmetric to ` + "`nous service install`" + `: stops and removes the
unified com.42shots.nous plist. Also clears any legacy plists from
prior milestones (com.charon.proxy, com.xianxu.brain-sync, the
pre-rename com.xianxu.nous) so a re-install lands clean.

Idempotent: services not currently installed silently skip; only
real failures surface.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			var firstErr error

			// Local interface so the helper can accept both
			// service.Manager (unified + charon legacy) and
			// brainsync.ServiceManager (brain-sync legacy) — Go's
			// nominal interfaces require this adapter at the call
			// site even though both have identical method sets.
			type uninstaller interface {
				Stop() error
				Uninstall() error
			}

			tryUninstall := func(label string, mgr uninstaller, mgrErr error) {
				if mgrErr != nil {
					if firstErr == nil {
						firstErr = mgrErr
					}
					return
				}
				_ = mgr.Stop()
				if err := mgr.Uninstall(); err != nil {
					// "service not installed" / file-not-found is the
					// common case for legacy labels on a machine that
					// only ever ran the unified install; suppress
					// those rather than surface noise.
					if !os.IsNotExist(err) {
						fmt.Fprintf(out, "  [warn] %s uninstall: %v\n", label, err)
						if firstErr == nil {
							firstErr = err
						}
					}
					return
				}
				fmt.Fprintf(out, "  [ok] %s uninstalled\n", label)
			}

			// Unified (the canonical install today).
			unifiedMgr, mgrErr := service.NewUnified()
			tryUninstall("com.42shots.nous", unifiedMgr, mgrErr)

			// Legacy / pre-rename plists. tryUninstall already
			// suppresses not-installed warnings so these are no-ops
			// on a clean machine.
			charonMgr, mgrErr := service.New()
			tryUninstall("com.charon.proxy", charonMgr, mgrErr)

			brainSyncMgr, mgrErr := brainsync.NewServiceManager()
			tryUninstall("com.xianxu.brain-sync", brainSyncMgr, mgrErr)

			oldUnified, mgrErr := service.NewLabeled("com.xianxu.nous", "nous.log", "")
			tryUninstall("com.xianxu.nous (pre-rename)", oldUnified, mgrErr)

			return firstErr
		},
	}
}

func serviceStartCmdImpl() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the com.42shots.nous launchd service",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := service.NewUnified()
			if err != nil {
				return err
			}
			if err := mgr.Start(); err != nil {
				return fmt.Errorf("start com.42shots.nous: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "  [ok] com.42shots.nous started")
			return nil
		},
	}
}

func serviceStopCmdImpl() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the com.42shots.nous launchd service",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := service.NewUnified()
			if err != nil {
				return err
			}
			if err := mgr.Stop(); err != nil {
				return fmt.Errorf("stop com.42shots.nous: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "  [ok] com.42shots.nous stopped")
			return nil
		},
	}
}

func serviceStatusCmdImpl() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show installed-and-running state for com.42shots.nous (+ any legacy plists found)",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			// Unified — the canonical service.
			unifiedMgr, err := service.NewUnified()
			if err != nil {
				return err
			}
			unifiedStatus, unifiedErr := unifiedMgr.Status()
			fmt.Fprintf(out, "com.42shots.nous:\n  %s\n", indent(unifiedStatus, "  "))
			if unifiedErr != nil {
				fmt.Fprintf(out, "  (status query error: %v)\n", unifiedErr)
			}

			// Legacy plists — only print if actually installed, so a
			// clean unified-only machine sees one line.
			legacy := []struct {
				label string
				make  func() (service.Manager, error)
			}{
				{"com.charon.proxy (legacy)", service.New},
				{"com.xianxu.nous (pre-rename)", func() (service.Manager, error) {
					return service.NewLabeled("com.xianxu.nous", "nous.log", "")
				}},
			}
			for _, l := range legacy {
				mgr, err := l.make()
				if err != nil {
					continue
				}
				status, _ := mgr.Status()
				if status == "not installed" || status == "" {
					continue
				}
				fmt.Fprintf(out, "%s:\n  %s\n", l.label, indent(status, "  "))
				fmt.Fprintln(out, "  (run `nous service install` to migrate, or `nous service uninstall` to remove)")
			}
			// brain-sync legacy uses a different manager interface;
			// handle separately.
			if brainSyncMgr, err := brainsync.NewServiceManager(); err == nil {
				status, _ := brainSyncMgr.Status()
				if status != "not installed" && status != "" {
					fmt.Fprintf(out, "com.xianxu.brain-sync (legacy):\n  %s\n", indent(status, "  "))
					fmt.Fprintln(out, "  (run `nous service install` to migrate, or `nous service uninstall` to remove)")
				}
			}

			return nil
		},
	}
}

// manager is the common subset of lib/service.Manager and lib/brainsync.ServiceManager
// (both expose Start/Stop). Retained for any future helper that needs to operate
// across both interface types.
type manager interface {
	Start() error
	Stop() error
}

// forEachManager retained for now in case future migrations want it.
// Unused after M5; can be deleted once we're confident nothing else needs it.
func forEachManager(out interface{ Write([]byte) (int, error) }, verb string, op func(manager) error) error {
	var firstErr error

	charonMgr, err := service.New()
	if err != nil {
		firstErr = err
	} else if err := op(charonMgr); err != nil {
		fmt.Fprintf(out, "  [warn] charon %s: %v\n", verb, err)
		if firstErr == nil {
			firstErr = err
		}
	} else {
		fmt.Fprintf(out, "  [ok] charon %s\n", verb)
	}

	brainSyncMgr, err := brainsync.NewServiceManager()
	if err != nil {
		if firstErr == nil {
			firstErr = err
		}
	} else if err := op(brainSyncMgr); err != nil {
		fmt.Fprintf(out, "  [warn] brain-sync %s: %v\n", verb, err)
		if firstErr == nil {
			firstErr = err
		}
	} else {
		fmt.Fprintf(out, "  [ok] brain-sync %s\n", verb)
	}

	return firstErr
}

// indent prefixes each line of s with prefix (skipping the first line so
// the caller can write a label before it). Cosmetic for status output.
func indent(s, prefix string) string {
	// Trivial implementation: replace newlines with newline+prefix.
	out := ""
	for i, c := range s {
		out += string(c)
		if c == '\n' && i != len(s)-1 {
			out += prefix
		}
	}
	return out
}

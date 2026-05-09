package main

import (
	"fmt"
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
  status      Show what's installed + running across both`,
	}

	cmd.AddCommand(serviceInstallCmdImpl())
	cmd.AddCommand(serviceUninstallCmdImpl())
	cmd.AddCommand(serviceStartCmdImpl())
	cmd.AddCommand(serviceStopCmdImpl())
	cmd.AddCommand(serviceStatusCmdImpl())

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
		Short: "Install brain-sync + charon proxy as launchd services",
		Long: `Writes both launchd plists and bootstraps the services. After install
both daemons run on login. Re-running is safe (idempotent).

Binaries resolved by sibling-binary lookup: nous expects bin/brain-sync
and bin/charon next to itself, falling back to PATH.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			charonBin, err := resolveSiblingBinary("charon")
			if err != nil {
				return fmt.Errorf("charon binary not found: %w (build with 'make build')", err)
			}
			brainSyncBin, err := resolveSiblingBinary("brain-sync")
			if err != nil {
				return fmt.Errorf("brain-sync binary not found: %w (build with 'make build')", err)
			}

			charonMgr, err := service.New()
			if err != nil {
				return fmt.Errorf("charon service manager: %w", err)
			}
			if err := charonMgr.Install(charonBin, []string{"serve"}); err != nil {
				return fmt.Errorf("install charon: %w", err)
			}
			fmt.Fprintf(out, "  [ok] charon service installed (%s serve)\n", charonBin)

			brainSyncMgr, err := brainsync.NewServiceManager()
			if err != nil {
				return fmt.Errorf("brain-sync service manager: %w", err)
			}
			if err := brainSyncMgr.Install(brainSyncBin, nil); err != nil {
				return fmt.Errorf("install brain-sync: %w", err)
			}
			fmt.Fprintf(out, "  [ok] brain-sync service installed (%s)\n", brainSyncBin)
			fmt.Fprintf(out, "Both services installed. Use 'nous service status' to verify.\n")
			return nil
		},
	}
}

func serviceUninstallCmdImpl() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove brain-sync + charon proxy launchd services",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			var firstErr error

			charonMgr, err := service.New()
			if err != nil {
				firstErr = err
			} else if err := charonMgr.Uninstall(); err != nil {
				fmt.Fprintf(out, "  [warn] charon uninstall: %v\n", err)
				if firstErr == nil {
					firstErr = err
				}
			} else {
				fmt.Fprintf(out, "  [ok] charon service uninstalled\n")
			}

			brainSyncMgr, err := brainsync.NewServiceManager()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
			} else if err := brainSyncMgr.Uninstall(); err != nil {
				fmt.Fprintf(out, "  [warn] brain-sync uninstall: %v\n", err)
				if firstErr == nil {
					firstErr = err
				}
			} else {
				fmt.Fprintf(out, "  [ok] brain-sync service uninstalled\n")
			}

			return firstErr
		},
	}
}

func serviceStartCmdImpl() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start (or restart) brain-sync + charon proxy services",
		RunE: func(cmd *cobra.Command, args []string) error {
			return forEachManager(cmd.OutOrStdout(), "start", func(m manager) error { return m.Start() })
		},
	}
}

func serviceStopCmdImpl() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop brain-sync + charon proxy services",
		RunE: func(cmd *cobra.Command, args []string) error {
			return forEachManager(cmd.OutOrStdout(), "stop", func(m manager) error { return m.Stop() })
		},
	}
}

func serviceStatusCmdImpl() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show installed-and-running state for brain-sync + charon proxy",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			charonMgr, err := service.New()
			if err != nil {
				return err
			}
			charonStatus, charonErr := charonMgr.Status()
			fmt.Fprintf(out, "charon proxy:\n  %s\n", indent(charonStatus, "  "))
			if charonErr != nil {
				fmt.Fprintf(out, "  (status query error: %v)\n", charonErr)
			}

			brainSyncMgr, err := brainsync.NewServiceManager()
			if err != nil {
				return err
			}
			brainSyncStatus, brainSyncErr := brainSyncMgr.Status()
			fmt.Fprintf(out, "brain-sync:\n  %s\n", indent(brainSyncStatus, "  "))
			if brainSyncErr != nil {
				fmt.Fprintf(out, "  (status query error: %v)\n", brainSyncErr)
			}

			return nil
		},
	}
}

// manager is the common subset of lib/service.Manager and lib/brainsync.ServiceManager
// (both expose Start/Stop). Used for shared start/stop dispatch helpers.
type manager interface {
	Start() error
	Stop() error
}

// forEachManager runs op against both managers, aggregating output. Returns
// the first error encountered (after running both, so partial success is
// reported).
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

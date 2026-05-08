package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/xianxu/nous/lib/brainsync"
)

func serviceCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "service",
		Short: "Manage brain-sync as a launchd service",
	}
	c.AddCommand(installCmd())
	c.AddCommand(uninstallCmd())
	c.AddCommand(startCmd())
	c.AddCommand(stopCmd())
	c.AddCommand(statusCmd())
	c.PersistentFlags().StringSliceVar(&brainPaths, "brain", nil,
		"shared brain path (repeatable; used by 'install')")
	c.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false,
		"persist --verbose into the plist (used by 'install')")
	return c
}

func installCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Write the launchd plist (does not start the service)",
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := brainsync.NewServiceManager()
			if err != nil {
				return err
			}
			bin, err := os.Executable()
			if err != nil {
				return err
			}
			// Bare brain-sync is the foreground watcher.
			var pargs []string
			for _, b := range brainPaths {
				pargs = append(pargs, "--brain", b)
			}
			if verbose {
				pargs = append(pargs, "--verbose")
			}
			if err := m.Install(bin, pargs); err != nil {
				return err
			}
			fmt.Println("installed; run 'brain-sync service start' to launch")
			return nil
		},
	}
}

func uninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the launchd plist",
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := brainsync.NewServiceManager()
			if err != nil {
				return err
			}
			return m.Uninstall()
		},
	}
}

func startCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "launchctl load",
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := brainsync.NewServiceManager()
			if err != nil {
				return err
			}
			return m.Start()
		},
	}
}

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "launchctl unload",
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := brainsync.NewServiceManager()
			if err != nil {
				return err
			}
			return m.Stop()
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show service state",
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := brainsync.NewServiceManager()
			if err != nil {
				return err
			}
			s, err := m.Status()
			fmt.Print(s)
			if !strings.HasSuffix(s, "\n") {
				fmt.Println()
			}
			return err
		},
	}
}

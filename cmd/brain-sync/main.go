// brain-sync — git-based sync for shared brains.
//
// Watches shared-brain repos (those declaring `mode: shared` in
// .brain/config.md), pushes local commits and pulls remote ones with
// file-level conflict resolution. See workshop/plans/000004-shared-brain-sync-daemon-plan.md.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/xianxu/nous/lib/brainsync"
)

var (
	brainPaths []string
	fetchEvery time.Duration
)

func main() {
	root := &cobra.Command{
		Use:   "brain-sync",
		Short: "Git-based sync for shared brains",
		Long:  "Watches shared-brain repos and propagates commits via gcrypt'd github with file-level conflict resolution.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			if len(brainPaths) == 0 {
				auto, err := brainsync.FindAllSharedBrainsInWorkspace()
				if err != nil {
					return err
				}
				if len(auto) == 0 {
					return fmt.Errorf("no shared brains found under $HOME/workspace; pass --brain explicitly")
				}
				brainPaths = auto
				log.Printf("brainsync: auto-discovered %d shared brain(s)", len(auto))
			}

			return brainsync.Watch(ctx, brainPaths, fetchEvery)
		},
	}
	root.Flags().StringSliceVar(&brainPaths, "brain", nil, "absolute path to a shared brain (repeatable)")
	root.Flags().DurationVar(&fetchEvery, "fetch-every", 30*time.Second, "periodic fetch interval")

	root.AddCommand(serviceCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// serviceCmd is implemented in chunk 6 (service.go).

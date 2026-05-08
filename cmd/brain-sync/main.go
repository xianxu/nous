// brain-sync — git-based sync for shared brains.
//
// Watches shared-brain repos (those declaring `mode: shared` in
// .brain/config.md), pushes local commits and pulls remote ones with
// file-level conflict resolution. See workshop/plans/000004-shared-brain-sync-daemon-plan.md.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "brain-sync",
		Short: "Git-based sync for shared brains",
		Long:  "Watches shared-brain repos and propagates commits via gcrypt'd github with file-level conflict resolution.",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("watcher not yet implemented — see chunk 5")
			return nil
		},
	}

	// service subcommand added in chunk 6.

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

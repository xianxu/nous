package main

import (
	"github.com/spf13/cobra"
)

// serviceCmd stub; real implementation in chunk 6.
func serviceCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "service",
		Short:  "Manage brain-sync as a launchd service",
		Hidden: true, // hidden until chunk 6 implements it
	}
}

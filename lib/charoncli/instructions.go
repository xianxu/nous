package charoncli

import (
	_ "embed"
	"fmt"

	"github.com/spf13/cobra"
)

//go:embed agent_instructions.md
var agentInstructions string

// InstructionsCmd prints the embedded agent-facing instructions. The
// content lives in agent_instructions.md alongside this file so it
// ships with every binary — agents calling `charon instructions`
// always get prose that matches the version of charon installed,
// no skill-doc-vs-binary drift.
func InstructionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "instructions",
		Short: "Print the agent-facing instructions for this charon version (Markdown)",
		Long: `Outputs the canonical agent-using-charon guide as Markdown.

Intended consumer: an AI agent that needs to learn how to talk to
charon (proxy URL, account selection, scope declaration, per-provider
URL conventions, error handling). The content is embedded in the
binary, so it always matches what this charon actually implements —
no parallel skill doc to keep in sync.

Usage:
  charon instructions               # print to stdout
  charon instructions | less        # browse interactively
  charon instructions > guide.md    # save for later`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(cmd.OutOrStdout(), agentInstructions)
			return nil
		},
	}
}

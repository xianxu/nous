package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/xianxu/nous/lib/brain"
)

// nous brain list — read-only enumeration of brains under the workspace
// root. Mirrors `nous identity list`'s scoping convention.

func newBrainListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List brains under the workspace root",
		Long: `Walk the workspace root (lib/workspace.Root) one level deep and
print every directory that's a brain (has .brain/config.md). Annotates
each with name, shared-vs-private, recipient count, and sync substrate.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBrainList(cmd.OutOrStdout())
		},
	}
}

func runBrainList(w io.Writer) error {
	brains, err := brain.DiscoverAll()
	if err != nil {
		return err
	}
	if len(brains) == 0 {
		fmt.Fprintln(w, "No brains under workspace root.")
		return nil
	}
	for _, b := range brains {
		kind := "private"
		if b.Shared() {
			kind = "shared"
		}
		fmt.Fprintf(w, "  %-20s  %s  %d recipient%s  sync=%s\n",
			b.Name, kind, len(b.Recipients), pluralS(len(b.Recipients)), defaultStr(b.SyncSubstrate, "?"))
		fmt.Fprintf(w, "      %s\n", b.Path)
	}
	return nil
}

func defaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// nous brain resolve — mechanical conflict-find for /nous-resolve.
//
// For now this is a thin shim that exits 0 with a "not yet wired"
// message. The real implementation routes through lib/brainsync's
// existing conflict-finding code, but extracting that as a clean public
// surface needs a small refactor we'll do alongside the /nous-resolve
// skill rewrite. Tracked in nous#5 follow-up.
func newBrainResolveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resolve BRAIN-PATH",
		Short: "Mechanical conflict-find (called by /nous-resolve skill)",
		Long: `Find unresolved files under a brain that need conflict resolution.
Used by the /nous-resolve Claude Code skill for the mechanical
list-and-preserve step; the agent-driven semantic merge happens
elsewhere.

Today: stubbed pending lib/brainsync surface refactor (nous#5
follow-up). The skill continues to call lib/brainsync directly.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("nous brain resolve is stubbed pending lib/brainsync refactor; /nous-resolve continues to call lib/brainsync directly. Tracked as a nous#5 follow-up")
		},
	}
}

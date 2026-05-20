package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/brainsync"
	"github.com/xianxu/nous/lib/gh"
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
	// Resolve auth'd login once for all the operator-marker probes.
	// Empty on outage — operator markers just don't render, which is
	// the safe degradation (worst case is "I don't know I'm operator").
	myLogin, _ := gh.AuthLogin()
	if myLogin != "" {
		fmt.Fprintf(w, "Brains (%s)\n\n", myLogin)
	} else {
		fmt.Fprintln(w, "Brains")
		fmt.Fprintln(w)
	}
	for _, b := range brains {
		marker := " "
		if brain.IsOperator(b.Path, myLogin) {
			marker = "*"
		}
		// Display directory basename — that's the unambiguous on-disk
		// identity. manifest.Name is operator-authored and can drift.
		// `*` prefix marks brains where the current user can act as
		// operator (invite/remove collaborators via gh). See nous#27.
		// Terminology: "collaborators" in UI; manifest still says
		// "recipients" (schema, not user-facing).
		n := len(b.Recipients)
		if n <= 1 {
			fmt.Fprintf(w, "%s %-22s  private                       sync=%s\n",
				marker, filepath.Base(b.Path), defaultStr(b.SyncSubstrate, "?"))
		} else {
			fmt.Fprintf(w, "%s %-22s  shared  %d collaborators  sync=%s\n",
				marker, filepath.Base(b.Path), n, defaultStr(b.SyncSubstrate, "?"))
		}
		fmt.Fprintf(w, "      %s\n", b.Path)
	}
	if myLogin != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  (* = owner)")
	}
	return nil
}

// (operator predicate now lives in lib/brain — see brain.IsOperator,
// shared between this CLI list and the bubbletea TUI's list view.)

func defaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// nous brain resolve — mechanical conflict-find for /nous-resolve.
//
// Audience: (a). Scriptable; agent-facing. The semantic merge happens
// in the /nous-resolve Claude Code skill — this command does only
// the list step. Default output is tab-separated tabular; --json
// emits a stable structured form for downstream parsing.
//
// Exit codes: 0 on success regardless of whether conflicts were
// found (empty list is a valid clean-brain state). Non-zero on
// invalid brain path / read errors.
func newBrainResolveCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "resolve BRAIN-PATH",
		Short: "List unresolved conflict files in a brain (called by /nous-resolve skill) (a)",
		Long: `Walk BRAIN-PATH and emit every file matching the brainsync
conflict convention: ` + "`<stem>.conflict-<peer>-<YYYYMMDDTHHMMSSZ>.<ext>`" + `.
Each row: canonical path, conflict-file path, peer, ISO-8601 UTC.

Default output is tab-separated; pass --json for structured output
that won't break if a future column is added.

The semantic merge (AI-prose reconciliation, snapshot, commit) is
the /nous-resolve Claude Code skill's job; this command exposes
only the mechanical list step so the skill can call a stable CLI
shape instead of grepping ` + "`find`" + `.

Audience: (a). Scriptable; safe to call from automation.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBrainResolve(cmd.OutOrStdout(), args[0], jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of tab-separated text")
	return cmd
}

func runBrainResolve(w io.Writer, brainPath string, jsonOut bool) error {
	if _, err := os.Stat(filepath.Join(brainPath, ".brain", "config.md")); err != nil {
		return fmt.Errorf("%s is not a brain (missing .brain/config.md)", brainPath)
	}
	conflicts, err := brainsync.ConflictFiles(brainPath)
	if err != nil {
		return err
	}
	if jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		// Always emit an array (possibly empty) — agents can rely on
		// `len(parse(stdout))` without special-casing "no output".
		if conflicts == nil {
			conflicts = []brainsync.Conflict{}
		}
		return enc.Encode(conflicts)
	}
	// Text mode emits the shape the existing find-conflicts.sh / SKILL.md
	// pipeline expects: absolute paths + compact UTC timestamp
	// (YYYYMMDDTHHMMSSZ — matches what's embedded in the filename).
	// JSON mode is the structured surface for agents wanting relative
	// paths + RFC3339 timestamps.
	absRoot, err := filepath.Abs(brainPath)
	if err != nil {
		return err
	}
	for _, c := range conflicts {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			filepath.Join(absRoot, c.Canonical),
			filepath.Join(absRoot, c.ConflictFile),
			c.Peer,
			c.At.UTC().Format("20060102T150405Z"))
	}
	return nil
}

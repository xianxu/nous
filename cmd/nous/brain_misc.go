package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
	for _, b := range brains {
		kind := "private"
		if b.Shared() {
			kind = "shared"
		}
		marker := " "
		if isOperator(b.Path, myLogin) {
			marker = "*"
		}
		// Display directory basename — that's the unambiguous on-disk
		// identity. manifest.Name is operator-authored and can drift.
		// `*` prefix marks brains where the current user can act as
		// operator (invite/remove recipients via gh). See nous#27.
		fmt.Fprintf(w, "%s %-22s  %s  %d recipient%s  sync=%s\n",
			marker, filepath.Base(b.Path), kind, len(b.Recipients), pluralS(len(b.Recipients)), defaultStr(b.SyncSubstrate, "?"))
		fmt.Fprintf(w, "      %s\n", b.Path)
	}
	if myLogin != "" {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  (* = you can act as operator on this brain — github admin/maintain or owner; current login: %s)\n", myLogin)
	}
	return nil
}

// isOperator probes whether the current user can act as operator
// (invite/remove collaborators) on the brain rooted at brainPath.
// Returns false for any brain without a parsable github.com remote,
// any gh outage, or any non-admin/maintain permission level.
//
// Best-effort: a slow gh response would block `nous brain list`,
// but the typical case is local-cache-fast. If we ever need to
// guarantee bounded latency, this can be moved behind a flag or
// run in parallel with a cap; for now the simplicity wins.
func isOperator(brainPath, myLogin string) bool {
	if myLogin == "" {
		return false
	}
	origin := readBrainOriginURL(brainPath)
	if origin == "" {
		return false
	}
	owner, repo, err := brain.GitHubOwnerRepo(origin)
	if err != nil {
		return false
	}
	if strings.EqualFold(owner, myLogin) {
		// Personal repo owner = operator by definition.
		return true
	}
	perm, err := gh.CollaboratorPermission(owner, repo, myLogin)
	if err != nil {
		return false
	}
	return perm == "admin" || perm == "maintain"
}

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

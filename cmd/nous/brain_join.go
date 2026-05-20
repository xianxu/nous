package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/gh"
	"github.com/xianxu/nous/lib/identity"
)

// newBrainJoinCmd builds `nous brain join`: shows the joiner's
// pending GitHub repo invitations filtered to brain projects,
// lets them pick which to accept, and publishes their pubkey to
// each picked brain's keys branch. The auto-admit on the operator
// side (M3) handles the rest.
//
// Trust model: joining IS the affirmative "I want in." Operator's
// earlier `nous brain invite` already established trust. No
// fingerprint negotiation, no out-of-band verify required to start
// participating — verification is a separate opt-in step (M6).
//
// Audience: (h) human joiner. Multi-select is mandatory when
// multiple brain invitations are pending; non-TTY non-empty case
// errors out.
func newBrainJoinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "join",
		Short: "Join one or more shared brains you've been invited to",
		Long: `List your pending GitHub repository invitations for brain projects,
let you pick which to join, then publish your pubkey to each
brain's keys branch. The operator's brain-sync auto-admits you
to the gcrypt recipient set on the next pull cycle.

Brain projects are detected via the repo's description (starts
with 'nous-brain:' or contains 'gcrypt-encrypted brain') or its
topics (contains 'nous-brain'). Non-brain invitations are ignored
— you can still accept those via the web UI or 'gh' directly.

After this command succeeds, wait for the operator's next sync
cycle (default ~10s) and then run 'nous brain clone <gcrypt-url>'
to materialize the brain locally.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBrainJoin(cmd.Context(), cmd.OutOrStdout(), cmd.InOrStdin())
		},
	}
}

func runBrainJoin(ctx context.Context, w io.Writer, in io.Reader) error {
	// 1. Find pending invitations.
	invites, err := gh.PendingInvitations()
	if err != nil {
		return fmt.Errorf("list invitations: %w", err)
	}
	brainInvites := filterBrainInvitations(invites)
	if len(brainInvites) == 0 {
		fmt.Fprintln(w, "No pending brain invitations.")
		fmt.Fprintln(w, "(If you expect one, ask the operator to run `nous brain invite <your-github-login>`.)")
		return nil
	}
	sort.Slice(brainInvites, func(i, j int) bool {
		return brainInvites[i].Repository.FullName < brainInvites[j].Repository.FullName
	})

	// 2. Display + multi-select.
	fmt.Fprintln(w, "Pending brain invitations:")
	for i, inv := range brainInvites {
		desc := strings.TrimSpace(inv.Repository.Description)
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(w, "  [%d] %-30s  %s\n", i+1, inv.Repository.FullName, desc)
		fmt.Fprintf(w, "      invited by %s\n", inv.Inviter.Login)
	}
	fmt.Fprintln(w)
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("brain invitations pending but stdin is not a TTY; run 'nous brain join' interactively")
	}
	picked, err := promptIndices(w, in, "Pick brains to join (comma-separated, e.g. 1,3 — or 'all', or Enter to cancel): ", len(brainInvites))
	if err != nil {
		return err
	}
	if len(picked) == 0 {
		fmt.Fprintln(w, "Aborted.")
		return nil
	}

	// 3. Pick GPG identity.
	fp, armor, err := selectIdentityForJoin(w, in)
	if err != nil {
		return err
	}

	// 4. Resolve own github login (filename stem).
	myLogin, err := gh.AuthLogin()
	if err != nil {
		return fmt.Errorf("resolve own login: %w", err)
	}

	// 5. For each pick: accept invite + publish pubkey.
	for _, idx := range picked {
		inv := brainInvites[idx]
		fmt.Fprintf(w, "\n→ %s\n", inv.Repository.FullName)
		if err := gh.AcceptInvitation(inv.ID); err != nil {
			fmt.Fprintf(w, "  accept: %v\n", err)
			continue
		}
		fmt.Fprintf(w, "  accepted invitation.\n")
		if err := brain.PublishOwnPubkeyToRemote(ctx, inv.Repository.SSHURL, myLogin, armor); err != nil {
			fmt.Fprintf(w, "  publish pubkey: %v\n", err)
			fmt.Fprintf(w, "  (you're a collaborator now but the operator can't auto-admit until you re-publish)\n")
			continue
		}
		fmt.Fprintf(w, "  published %s.asc to keys branch.\n", myLogin)
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Done. The operator's brain-sync will auto-admit you on its next pull cycle.")
	fmt.Fprintln(w, "Once admitted, materialize the brain locally with:")
	fmt.Fprintln(w, "  nous brain clone <gcrypt-url>")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  (your fingerprint: %s)\n", fp)
	return nil
}

// filterBrainInvitations keeps only those whose repo looks like a
// brain project. Conservative — better to miss one than to surface
// a non-brain invitation in this flow.
//
// Markers:
//   - description starts with "nous-brain:" (new convention, M5+)
//   - description contains "gcrypt-encrypted brain" (legacy
//     scripts/new-brain.sh wording)
//   - topics include "nous-brain"
func filterBrainInvitations(invites []gh.Invitation) []gh.Invitation {
	out := make([]gh.Invitation, 0, len(invites))
	for _, inv := range invites {
		if isBrainRepo(inv) {
			out = append(out, inv)
		}
	}
	return out
}

func isBrainRepo(inv gh.Invitation) bool {
	desc := strings.ToLower(inv.Repository.Description)
	if strings.HasPrefix(desc, "nous-brain:") {
		return true
	}
	if strings.Contains(desc, "gcrypt-encrypted brain") {
		return true
	}
	for _, t := range inv.Repository.Topics {
		if strings.EqualFold(t, "nous-brain") {
			return true
		}
	}
	return false
}

// selectIdentityForJoin returns (fingerprint, armoredPubkey) for the
// GPG identity to publish. 0 keys → error; 1 → use it; >1 → prompt.
// Mirrors the same UX as scripts/new-brain.sh's identity-pick block.
func selectIdentityForJoin(w io.Writer, in io.Reader) (fp, armor string, err error) {
	keys, err := identity.List()
	if err != nil {
		return "", "", fmt.Errorf("list GPG identities: %w", err)
	}
	if len(keys) == 0 {
		return "", "", errors.New("no GPG secret keys found. Run 'nous identity init' first")
	}
	var picked identity.Key
	if len(keys) == 1 {
		picked = keys[0]
		fmt.Fprintf(w, "\nUsing GPG identity %s [%s]\n", picked.UID, picked.Last8())
	} else {
		fmt.Fprintln(w, "\nMultiple GPG identities — pick one:")
		for i, k := range keys {
			fmt.Fprintf(w, "  [%d] %s\n      %s\n", i+1, k.UID, k.Fingerprint)
		}
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return "", "", errors.New("multiple GPG keys and stdin is not a TTY")
		}
		idx, perr := promptIndex(w, in, "Select [1-"+strconv.Itoa(len(keys))+"]: ", len(keys))
		if perr != nil {
			return "", "", perr
		}
		picked = keys[idx]
	}
	armor, err = identity.Export(picked.Fingerprint)
	if err != nil {
		return "", "", fmt.Errorf("export pubkey: %w", err)
	}
	return picked.Fingerprint, armor, nil
}

// promptIndices reads a comma-separated set of 1-based indices in
// [1, n] and returns sorted unique 0-based indices. The literal
// "all" expands to the full range. Empty input returns nil (cancel).
func promptIndices(w io.Writer, in io.Reader, prompt string, n int) ([]int, error) {
	fmt.Fprint(w, prompt)
	r := bufio.NewReader(in)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return nil, err
	}
	s := strings.TrimSpace(line)
	if s == "" {
		return nil, nil
	}
	if strings.EqualFold(s, "all") {
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out, nil
	}
	seen := make(map[int]bool)
	var out []int
	for _, part := range strings.Split(s, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		i, perr := strconv.Atoi(p)
		if perr != nil {
			return nil, fmt.Errorf("not a number: %q", p)
		}
		if i < 1 || i > n {
			return nil, fmt.Errorf("out of range: %d not in [1, %d]", i, n)
		}
		zero := i - 1
		if !seen[zero] {
			seen[zero] = true
			out = append(out, zero)
		}
	}
	sort.Ints(out)
	return out, nil
}

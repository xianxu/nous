package brainsync

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/gh"
	"github.com/xianxu/nous/lib/identity"
)

// RemoveRecipientResult carries the outcome of RemoveRecipient. The
// manifest re-key push is load-bearing; the verified.yaml clear,
// keys-branch revoke, and GitHub collaborator removal are best-effort
// cleanups whose errors are recorded here (not returned) once the push
// has landed, so the caller can surface a manual-retry hint.
type RemoveRecipientResult struct {
	Match   string // canonical 40-hex fingerprint matched/removed
	ShortFp string // lowercased last-8 (for log lines / banners)

	AlreadyAbsent bool // fp wasn't in the manifest
	RetriedPush   bool // an unpushed prior removal was flushed instead
	Pushed        bool // manifest + verified.yaml re-key push landed

	RemovedLogins []string // verified.yaml entries cleared (leak #2)
	VerifiedErr   error    // non-fatal verified.yaml clear failure
	KeysBranchErr error    // non-fatal keys-branch revoke failure (leak #1)

	HadRemote           bool // brain has a GitHub origin
	Owner, Repo, Login  string
	CollaboratorRevoked bool  // leak #3
	CollaboratorErr     error // non-fatal collaborator-removal failure
	// LoginUnresolved is true when the brain has a remote but we could not
	// map the fingerprint to a GitHub login from any source — so the
	// collaborator could NOT be revoked and the caller must surface a
	// manual-removal hint (never a silent skip).
	LoginUnresolved bool
}

// RemoveRecipient fully revokes fpArg from a SINGLE brain — the one
// complete remove path shared by `nous brain recipient remove` (CLI)
// and the brain TUI so the two can't drift (the CLI/TUI divergence is
// how the leak class in nous#38 arose). It:
//
//  1. clears the verified.yaml entry (so a later keys-branch re-publish
//     isn't silently auto-admitted),
//  2. removes the fp from the manifest and pushes (gcrypt re-encrypts to
//     the remaining recipients),
//  3. strips EVERY keys-branch pubkey for the fp (all naming
//     conventions, via brain.RevokePubkey's content match), and
//  4. removes the matching GitHub collaborator (brains with a remote).
//
// Safeguards: the last-recipient guard (CanRemoveRecipient) and the
// would-lock-out self-removal floor (gated by force). Steps 1/3/4 are
// best-effort: their errors land in the result, not the return, once the
// push (step 2) has succeeded.
//
// This is per-brain only — cross-brain fan-out + ban list live in
// nous#37. It does NOT touch the local GPG keyring.
func RemoveRecipient(ctx context.Context, brainPath, fpArg string, force bool) (*RemoveRecipientResult, error) {
	res := &RemoveRecipientResult{}

	m, err := brain.Read(brainPath)
	if err != nil {
		return res, fmt.Errorf("read manifest: %w", err)
	}
	match, err := brain.MatchRecipient(m.Recipients, fpArg)
	if err != nil {
		return res, err
	}
	if match == "" {
		// Not in the manifest: either a typo, or a prior remove that
		// committed locally but failed to push — retry the push so the
		// remote catches up instead of confusingly erroring.
		res.AlreadyAbsent = true
		if unpushed, _ := HasUnpushedCommits(brainPath); unpushed {
			if err := Push(brainPath); err != nil {
				return res, fmt.Errorf("push: %w", err)
			}
			res.RetriedPush = true
			res.Pushed = true
			return res, nil
		}
		return res, fmt.Errorf("not a recipient of %s: %s", filepath.Base(brainPath), fpArg)
	}
	res.Match = match
	res.ShortFp = shortFpLast8(match)

	if err := brain.CanRemoveRecipient(m); err != nil {
		return res, err
	}
	wouldLock, err := brain.WouldLockOut(m.Recipients, match)
	if err != nil {
		return res, fmt.Errorf("check decrypt path: %w", err)
	}
	if wouldLock && !force {
		return res, fmt.Errorf("removing %s leaves no decrypt path on %s — pass force to override", res.ShortFp, filepath.Base(brainPath))
	}

	// (#2) Clear verified.yaml BEFORE the push so manifest + verified land
	// in one commit. Capture the login(s) for the collaborator removal.
	logins, verr := brain.RemoveVerifiedFor(brainPath, match)
	res.RemovedLogins = logins
	if verr != nil {
		res.VerifiedErr = verr
	}

	// Resolve the GitHub login NOW — before RevokePubkey deletes the
	// keys-branch <login>.asc that LoginForFingerprint reads. Auto-admitted
	// recipients have no verified.yaml entry, so resolving only after the
	// delete left login=="" and silently skipped the collaborator revoke
	// (nous#40 bug A). Sources in order: verified.yaml → keys branch → peer
	// sidecar.
	login := ""
	if len(logins) > 0 {
		login = logins[0]
	}
	if login == "" {
		login, _ = brain.LoginForFingerprint(ctx, brainPath, match)
	}
	if login == "" {
		if pm, perr := identity.LoadPeerMeta(match); perr == nil {
			login = pm.GithubUser
		}
	}
	res.Login = login

	// Manifest update + re-key push (load-bearing).
	m.Recipients = brain.WithoutRecipient(m.Recipients, match)
	if err := brain.RewriteFrontmatter(brainPath, m); err != nil {
		return res, fmt.Errorf("rewrite frontmatter: %w", err)
	}
	if err := AddCommitPush(brainPath, fmt.Sprintf("recipient: revoke %s", res.ShortFp)); err != nil {
		return res, fmt.Errorf("push: %w (manifest committed locally; re-run to retry)", err)
	}
	res.Pushed = true

	// (#1) Strip every keys-branch pubkey for the fp — best-effort.
	if kerr := brain.RevokePubkey(ctx, brainPath, match); kerr != nil {
		res.KeysBranchErr = kerr
	}

	// (#3) Remove the GitHub collaborator — only for brains with a remote.
	// login was resolved above (before RevokePubkey ran).
	if originURL := brain.ReadOriginURL(brainPath); originURL != "" {
		if owner, repo, oerr := brain.GitHubOwnerRepo(originURL); oerr == nil {
			res.HadRemote = true
			res.Owner, res.Repo = owner, repo
			if login != "" {
				if cerr := gh.RemoveCollaborator(owner, repo, login); cerr != nil {
					res.CollaboratorErr = cerr
				} else {
					res.CollaboratorRevoked = true
				}
			} else {
				// Couldn't map fp → login from any source: don't silently
				// leave them with access — flag it so the caller prints a
				// manual-removal hint.
				res.LoginUnresolved = true
			}
		}
	}
	return res, nil
}

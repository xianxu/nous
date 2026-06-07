package brainsync

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/gh"
	"github.com/xianxu/nous/lib/identity"
)

// membershipPushMaxAttempts bounds the pull-rebase-retry loop in
// pushMembershipChange. Five is comfortably more than any realistic number
// of operators racing the same brain's main branch.
const membershipPushMaxAttempts = 5

// pushMembershipChange applies a membership mutation and pushes it, retrying
// on a rejected push by resetting to the remote's membership state and
// re-applying (nous#41 #6). Membership changes are idempotent set-operations
// on the recipient list / recipient_logins map, so re-applying on top of
// whatever a concurrent operator just pushed converges — unlike content
// edits, which need conflict resolution.
//
// apply performs the local mutation from freshly-read on-disk state: it reads
// the current manifest (+ any sibling store it commits in the same commit,
// e.g. verified.yaml), performs its set-operation, writes it back via
// RewriteFrontmatter, and returns the commit message. It must NOT push, and
// must be safe to run repeatedly. Returning ("", nil) means the mutation is
// already reflected (a concurrent push did the work) — treated as a no-op.
func pushMembershipChange(brainPath string, apply func() (string, error)) error {
	// Refuse if the brain has pre-existing uncommitted *tracked* changes. The
	// retry's `reset --hard origin/main` (ResetToRemoteMain) discards local
	// commits + tracked-file changes, and AddCommitPush's `git add -A` would
	// have bundled those unrelated edits into the rejected commit — so a retry
	// would silently roll them back. Starting from a clean tracked tree means
	// only the membership change is ever staged, so a reset loses nothing the
	// caller didn't author here (nous#41 #6). Untracked files are tolerated
	// (reset --hard leaves them); brains legitimately carry untracked drafts.
	if safe, err := SafeToFastForward(brainPath); err != nil {
		return fmt.Errorf("check working tree before membership push: %w", err)
	} else if !safe {
		return fmt.Errorf("brain has uncommitted tracked changes — commit or discard them before a membership change (its re-key push can't safely bundle unrelated edits)")
	}

	var lastErr error
	for attempt := 0; attempt < membershipPushMaxAttempts; attempt++ {
		msg, err := apply()
		if err != nil {
			return err
		}
		if msg == "" {
			// Nothing new to record; AddCommitPush no-ops on a clean tree.
			msg = "membership update"
		}
		err = AddCommitPush(brainPath, msg)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrPushRejected) {
			return err
		}
		lastErr = err
		if rerr := ResetToRemoteMain(brainPath); rerr != nil {
			return fmt.Errorf("reconcile after rejected membership push: %w (push was: %v)", rerr, err)
		}
	}
	return fmt.Errorf("membership push still rejected after %d attempts: %w", membershipPushMaxAttempts, lastErr)
}

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

// RemovePersonResult reports what RemovePerson did across the membership
// layers. WasRecipient + Recipient are set when the person was a manifest
// recipient (the heavy path ran); the GitHub-layer fields are set for the
// invitation/collaborator cleanup in every case.
type RemovePersonResult struct {
	Selector            string
	Login               string
	Fingerprint         string
	Owner, Repo         string
	WasRecipient        bool
	Recipient           *RemoveRecipientResult
	InvitationCancelled bool
	CollaboratorRevoked bool
	CollaboratorErr     error
	KeysBranchStripped  bool  // a keys-branch pubkey for the login was removed
	KeysBranchErr       error // non-fatal keys-branch revoke failure
	LoginUnresolved     bool
	NothingToDo         bool // selector matched no layer (no recipient, invite, collaborator, or keys-branch pubkey)
}

// RemovePerson removes a person from a SINGLE brain at whatever lifecycle
// stage they're in — manifest recipient, accepted collaborator, or merely a
// pending invitation. The selector is a GitHub login OR a fingerprint/last-8.
// This is the unified "remove this person" entry the CLI + TUI call (the
// cross-brain fan-out + ban list stay nous#37):
//
//   - recipient (selector is an fp/last-8, or a login that resolves to a
//     manifest recipient) → full RemoveRecipient (manifest + verified + keys
//     branch + collaborator), then cancel any lingering pending invitation.
//   - non-recipient login (pending invitee, or accepted-but-not-admitted) →
//     cancel the pending invitation + revoke the collaborator. No manifest
//     touch (they were never a recipient).
func RemovePerson(ctx context.Context, c gh.Client, brainPath, selector string, force bool) (*RemovePersonResult, error) {
	res := &RemovePersonResult{Selector: selector}
	m, err := brain.Read(brainPath)
	if err != nil {
		return res, fmt.Errorf("read manifest: %w", err)
	}

	// Resolve to a recipient fingerprint — directly (fp/last-8) or via login.
	fp, _ := brain.MatchRecipient(m.Recipients, selector)
	if fp == "" {
		if cand, _ := brain.FingerprintForLogin(ctx, brainPath, selector); cand != "" {
			if mm, _ := brain.MatchRecipient(m.Recipients, cand); mm != "" {
				fp = mm
			}
		}
	}

	if fp != "" {
		// Recipient path — RemoveRecipient owns manifest + verified + keys +
		// collaborator (with the nous#40 early login resolution).
		rr, rerr := RemoveRecipient(ctx, c, brainPath, fp, force)
		res.Recipient, res.WasRecipient = rr, true
		if rr != nil {
			res.Fingerprint, res.Login = rr.Match, rr.Login
			res.Owner, res.Repo = rr.Owner, rr.Repo
			res.CollaboratorRevoked, res.CollaboratorErr = rr.CollaboratorRevoked, rr.CollaboratorErr
			res.LoginUnresolved = rr.LoginUnresolved
		}
		if rerr != nil {
			return res, rerr
		}
		// Clear any lingering pending invitation for the login too.
		if rr != nil && rr.HadRemote && res.Login != "" {
			if cancelled, _ := cancelPendingInvitation(c, rr.Owner, rr.Repo, res.Login); cancelled {
				res.InvitationCancelled = true
			}
		}
		return res, nil
	}

	// Non-recipient path: selector is a GitHub login in the pending /
	// collaborator-only state. Needs a remote.
	originURL := brain.ReadOriginURL(brainPath)
	if originURL == "" {
		return res, fmt.Errorf("%q is not a recipient of %s and the brain has no GitHub remote — nothing to remove", selector, filepath.Base(brainPath))
	}
	owner, repo, oerr := brain.GitHubOwnerRepo(originURL)
	if oerr != nil {
		return res, fmt.Errorf("parse origin URL: %w", oerr)
	}
	res.Owner, res.Repo, res.Login = owner, repo, selector

	cancelled, _ := cancelPendingInvitation(c, owner, repo, selector)
	res.InvitationCancelled = cancelled

	// Revoke the collaborator only if they actually are one (keeps NothingToDo
	// accurate — gh's DELETE is a silent no-op for non-collaborators).
	if perm, perr := c.CollaboratorPermission(owner, repo, selector); perr == nil && perm != "" && perm != "none" {
		if cerr := c.RemoveCollaborator(owner, repo, selector); cerr != nil {
			res.CollaboratorErr = cerr
		} else {
			res.CollaboratorRevoked = true
		}
	}

	// Strip any keys-branch pubkey for this login — the "Pubkey published but
	// not yet admitted" state. Without this, auto-admit re-admits them after
	// removal (the no-resurrection invariant must hold on the non-recipient
	// path too, not just the recipient path; nous#40 codex review #1).
	if fp, _ := brain.FingerprintForLogin(ctx, brainPath, selector); fp != "" {
		res.Fingerprint = fp
		if kerr := brain.RevokePubkey(ctx, brainPath, fp); kerr != nil {
			res.KeysBranchErr = kerr
		} else {
			res.KeysBranchStripped = true
		}
	}

	if !res.InvitationCancelled && !res.CollaboratorRevoked && !res.KeysBranchStripped &&
		res.CollaboratorErr == nil && res.KeysBranchErr == nil {
		res.NothingToDo = true
	}
	return res, nil
}

// cancelPendingInvitation deletes a pending repo invitation for login if one
// exists. Returns whether it deleted one.
func cancelPendingInvitation(c gh.Client, owner, repo, login string) (bool, error) {
	invs, err := c.RepoPendingInvitations(owner, repo)
	if err != nil {
		return false, err
	}
	for _, inv := range invs {
		if strings.EqualFold(inv.Invitee.Login, login) {
			if derr := c.DeleteRepoInvitation(owner, repo, inv.ID); derr != nil {
				return false, derr
			}
			return true, nil
		}
	}
	return false, nil
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
func RemoveRecipient(ctx context.Context, c gh.Client, brainPath, fpArg string, force bool) (*RemoveRecipientResult, error) {
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

	sr, serr := stripMember(ctx, c, brainPath, match, fmt.Sprintf("recipient: revoke %s", res.ShortFp), "")
	res.RemovedLogins = sr.RemovedLogins
	res.VerifiedErr = sr.VerifiedErr
	res.Login = sr.Login
	res.Pushed = sr.Pushed
	res.KeysBranchErr = sr.KeysBranchErr
	res.HadRemote = sr.HadRemote
	res.Owner, res.Repo = sr.Owner, sr.Repo
	res.CollaboratorRevoked = sr.CollaboratorRevoked
	res.CollaboratorErr = sr.CollaboratorErr
	res.LoginUnresolved = sr.LoginUnresolved
	if serr != nil {
		return res, serr
	}
	return res, nil
}

// stripMemberResult records which stores stripMember cleared for one
// fingerprint. The manifest re-key push (Pushed) is load-bearing — a
// non-nil return error means it failed; the verified.yaml / keys-branch /
// collaborator steps are best-effort cleanups whose errors are recorded
// here, not returned, once the push has landed.
type stripMemberResult struct {
	Login         string
	RemovedLogins []string
	Pushed        bool
	VerifiedErr   error
	KeysBranchErr error

	HadRemote           bool
	Owner, Repo         string
	CollaboratorRevoked bool
	CollaboratorErr     error
	LoginUnresolved     bool
}

// stripMember performs the full "clear every store" removal of fp from a
// brain — the sequence the collaborator-state-machine target's invariant #2
// demands. Shared by RemoveRecipient (operator removes another) and
// LeaveBrain (operator removes self) so the two can never drift (invariant
// #4); before this was extracted, LeaveBrain cleared only the manifest +
// collaborator and left the leaver's keys-branch <login>.asc + verified.yaml
// behind, so a peer's auto-admit could resurrect them (nous#41 #12).
//
// The caller runs the guards (last-recipient, owner-refuse, would-lock-out)
// and supplies the already-read manifest m + the commit message. stripMember
// owns the ordering that encodes nous#40 bug A: resolve the login BEFORE
// RevokePubkey deletes the keys-branch <login>.asc that LoginForFingerprint
// reads. knownLogin lets the self-leave caller pass its authoritative login
// (gh.AuthLogin) directly; pass "" to resolve it from: the login of the
// just-cleared verified.yaml entry (a removal hint from RemoveVerifiedFor's
// return — NOT verified.yaml as a canonical mapping, which #3 dropped), then
// the keys-branch <login>.asc (LoginForFingerprint), then the peer sidecar.
func stripMember(ctx context.Context, c gh.Client, brainPath, fp, commitMsg, knownLogin string) (*stripMemberResult, error) {
	res := &stripMemberResult{}

	// Resolve the GitHub login NOW — before RevokePubkey (below) deletes the
	// keys-branch <login>.asc that LoginForFingerprint reads (nous#40 bug A),
	// and before the retry loop (the login is stable across re-applies). The
	// keys-branch <login>.asc is the canonical source (#3); verified.yaml is
	// NOT consulted for the mapping (resolves nous#41 M1 review Important #2 —
	// the revoke-target login no longer prefers a stale verified.yaml hint).
	login := knownLogin
	if login == "" {
		login, _ = brain.LoginForFingerprint(ctx, brainPath, fp)
	}
	if login == "" {
		if pm, perr := identity.LoadPeerMeta(fp); perr == nil {
			login = pm.GithubUser
		}
	}
	res.Login = login

	// Manifest re-key + verified.yaml clear, in one commit, pushed with
	// concurrent-operator retry (nous#41 #6): on a rejected push, reset to the
	// remote's membership state and re-apply this idempotent set-op on top.
	if perr := pushMembershipChange(brainPath, func() (string, error) {
		cur, err := brain.Read(brainPath)
		if err != nil {
			return "", fmt.Errorf("read manifest: %w", err)
		}
		// Clear verified.yaml in the same commit as the manifest re-key.
		if logins, verr := brain.RemoveVerifiedFor(brainPath, fp); verr != nil {
			res.VerifiedErr = verr
		} else if len(logins) > 0 && len(res.RemovedLogins) == 0 {
			res.RemovedLogins = logins
		}
		cur.Recipients = brain.WithoutRecipient(cur.Recipients, fp)
		// Keep recipient_logins in sync (nous#41 #7/#8): drop any entry pointing
		// at the removed fp, else a later re-admit looks like a rotation.
		for l, lfp := range cur.RecipientLogins {
			if strings.EqualFold(lfp, fp) {
				delete(cur.RecipientLogins, l)
			}
		}
		if err := brain.RewriteFrontmatter(brainPath, cur); err != nil {
			return "", fmt.Errorf("rewrite frontmatter: %w", err)
		}
		return commitMsg, nil
	}); perr != nil {
		return res, fmt.Errorf("re-key push: %w (re-run to retry)", perr)
	}
	res.Pushed = true

	// (#1/#12) Strip every keys-branch pubkey for the fp — best-effort.
	if kerr := brain.RevokePubkey(ctx, brainPath, fp); kerr != nil {
		res.KeysBranchErr = kerr
	}

	// (#3) Remove the GitHub collaborator — only for brains with a remote.
	// login was resolved above (before RevokePubkey ran). Self-removal
	// (knownLogin == own login) is allowed by GitHub's DELETE collaborator.
	if originURL := brain.ReadOriginURL(brainPath); originURL != "" {
		if owner, repo, oerr := brain.GitHubOwnerRepo(originURL); oerr == nil {
			res.HadRemote = true
			res.Owner, res.Repo = owner, repo
			if login != "" {
				if cerr := c.RemoveCollaborator(owner, repo, login); cerr != nil {
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

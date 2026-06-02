package brainsync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/xianxu/nous/lib/brain"
	"github.com/xianxu/nous/lib/gh"
)

// LeaveResult carries the outcome of a successful or partially-
// successful Leave. The manifest update is the load-bearing step;
// if it landed, the leave is semantically done even when the
// GitHub-side revoke or the local-cleanup step fails — those are
// best-effort cleanups the operator can retry.
type LeaveResult struct {
	// MyLogin is the GitHub login we removed.
	MyLogin string
	// ShortFp is the lowercased last-8 of the removed fingerprint
	// (for log lines / banners).
	ShortFp string
	// Owner / Repo are the GitHub repo coordinates the brain points at.
	Owner string
	Repo  string

	// ManifestPushed is true iff step (1) — manifest rewrite + commit
	// + push — completed. Always true on a nil-error return; the
	// callers care more about the partial-failure cases below.
	ManifestPushed bool

	// CollaboratorRevoked is true iff step (2) — gh DELETE
	// collaborator — completed. Soft-fails: false here with a
	// non-nil CollaboratorRevokeErr means manifest update DID land
	// but GitHub cleanup didn't.
	CollaboratorRevoked   bool
	CollaboratorRevokeErr error

	// VerifiedErr / KeysBranchErr record best-effort failures of the
	// store-strip that runs alongside the manifest re-key: clearing the
	// leaver's own verified.yaml entry and keys-branch <login>.asc. A
	// non-nil KeysBranchErr means the leaver's pubkey may still linger on
	// the keys branch — a peer's auto-admit could resurrect them, so the
	// caller should surface a manual-cleanup hint (nous#41 #12).
	VerifiedErr   error
	KeysBranchErr error

	// LocalDeleted is true iff the optional --delete-local step ran
	// and succeeded.
	LocalDeleted bool
}

// LeaveBrain implements the collaborator-leave gesture: removes the
// local operator's fingerprint from `brainPath`'s manifest, pushes
// the change (gcrypt re-encrypts to the remaining collaborators),
// and revokes the operator's GitHub collaborator status on the
// underlying repo.
//
// Refuses to act when:
//   - the brain has no parseable github origin URL,
//   - the local operator is the github owner of the repo (orphans it),
//   - no fingerprint in the manifest matches a secret key on this host
//     (operator isn't a collaborator on this brain),
//   - removing the operator's fingerprint would leave zero collaborators
//     (would orphan the brain — admit someone else first).
//
// `deleteLocal=true` recursively removes brainPath after a successful
// manifest push. Off by default — the local clone is the only record
// of unique decrypted-by-me work; let the operator delete by hand.
//
// Mid-flow failure semantics:
//   - manifest commit/push fails → returns error; LeaveResult zero-
//     value. Nothing on the GitHub side has changed.
//   - collaborator revoke fails → returns nil error, LeaveResult with
//     ManifestPushed=true and CollaboratorRevoked=false +
//     CollaboratorRevokeErr populated. The leave is semantically
//     done; the GitHub-side cleanup is the operator's retry.
//   - delete-local fails → returns the error (manifest already
//     pushed, collaborator already revoked).
func LeaveBrain(ctx context.Context, brainPath string, deleteLocal bool) (LeaveResult, error) {
	var res LeaveResult

	m, err := brain.Read(brainPath)
	if err != nil {
		return res, fmt.Errorf("read manifest: %w", err)
	}
	originURL := brain.ReadOriginURL(brainPath)
	if originURL == "" {
		return res, errors.New("brain has no origin remote configured — nothing to leave (no GitHub side to revoke from)")
	}
	owner, repo, err := brain.GitHubOwnerRepo(originURL)
	if err != nil {
		return res, fmt.Errorf("parse origin URL: %w", err)
	}
	res.Owner = owner
	res.Repo = repo

	myLogin, err := gh.AuthLogin()
	if err != nil || myLogin == "" {
		return res, fmt.Errorf("resolve current github login (gh auth?): %w", err)
	}
	res.MyLogin = myLogin

	if strings.EqualFold(owner, myLogin) {
		return res, fmt.Errorf("refusing to leave: you are the GitHub owner of %s/%s. Transfer ownership or delete the brain repo instead", owner, repo)
	}

	mySecretFps, err := brain.LocalSecretFingerprints(m.Recipients)
	if err != nil {
		return res, fmt.Errorf("enumerate local secret keys: %w", err)
	}
	if len(mySecretFps) == 0 {
		return res, fmt.Errorf("refusing to leave: no fingerprint in this brain's manifest matches a secret key on this machine — you're not a collaborator on this brain")
	}
	myFp := mySecretFps[0]
	res.ShortFp = shortFpLast8(myFp)

	if err := brain.CanRemoveRecipient(m); err != nil {
		return res, fmt.Errorf("refusing to leave: %w", err)
	}

	// Full "clear every store" strip: manifest re-key (load-bearing) + the
	// leaver's own verified.yaml entry + keys-branch <login>.asc + GitHub
	// collaborator. Shared with RemoveRecipient via stripMember so leave
	// can't drift back to manifest-only — a lingering keys-branch pubkey
	// lets a peer's auto-admit resurrect the leaver (nous#41 #12). knownLogin
	// = myLogin (authoritative; GitHub allows self-removal). The collaborator
	// revoke runs LAST (after the push), preserving push access through the
	// re-key + keys-branch strip.
	commitMsg := fmt.Sprintf("leave: %s (%s) left the brain", myLogin, res.ShortFp)
	sr, serr := stripMember(ctx, brainPath, m, myFp, commitMsg, myLogin)
	res.ManifestPushed = sr.Pushed
	res.VerifiedErr = sr.VerifiedErr
	res.KeysBranchErr = sr.KeysBranchErr
	res.CollaboratorRevoked = sr.CollaboratorRevoked
	res.CollaboratorRevokeErr = sr.CollaboratorErr
	if serr != nil {
		return res, fmt.Errorf("commit + push manifest update: %w", serr)
	}

	if deleteLocal {
		if err := os.RemoveAll(brainPath); err != nil {
			return res, fmt.Errorf("delete-local: %w", err)
		}
		res.LocalDeleted = true
	}
	return res, nil
}

// shortFpLast8 returns the lowercased last-8 hex chars of a
// fingerprint, falling back to the full string for inputs shorter
// than 8 (which shouldn't happen for real fingerprints).
func shortFpLast8(fp string) string {
	if len(fp) >= 8 {
		return strings.ToLower(fp[len(fp)-8:])
	}
	return strings.ToLower(fp)
}

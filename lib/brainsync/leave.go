package brainsync

import (
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
func LeaveBrain(brainPath string, deleteLocal bool) (LeaveResult, error) {
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

	// Manifest update.
	m.Recipients = brain.WithoutRecipient(m.Recipients, myFp)
	if err := brain.RewriteFrontmatter(brainPath, m); err != nil {
		return res, fmt.Errorf("rewrite frontmatter: %w", err)
	}
	commitMsg := fmt.Sprintf("leave: %s (%s) left the brain", myLogin, res.ShortFp)
	if err := AddCommitPush(brainPath, commitMsg); err != nil {
		return res, fmt.Errorf("commit + push manifest update: %w", err)
	}
	res.ManifestPushed = true

	// Soft-fail revoke: continue + record the error.
	if err := gh.RemoveCollaborator(owner, repo, myLogin); err != nil {
		res.CollaboratorRevokeErr = err
	} else {
		res.CollaboratorRevoked = true
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

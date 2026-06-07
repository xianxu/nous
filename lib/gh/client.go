// Package gh is the provider-neutral port for the GitHub control plane
// nous integrates against (collaborator invitations, the
// repository_invitations listing, repository representations, multi-user
// access tokens). All of nous's GitHub access goes through the Client
// interface; the real adapter (real.go) is the only thing that execs the
// `gh` CLI, and the in-memory fake (fake.go) is a stateful stand-in for
// hermetic tests.
//
// This is instance 1 of the ariadne#71 shim(X)/shim'(X) pattern (see
// workshop/issues/000042-*.md). The uniform constructor convention is
// New(Conf) (real) / NewFake(Conf) (fake); Conf is opaque and
// service-specific.
package gh

import (
	"fmt"
	"strings"
)

// Conf is the opaque, service-specific construction config. The one
// cross-service convention is the shape New(Conf)/NewFake(Conf), not
// these fields.
type Conf struct {
	// CloneURLBase is prepended to "<full_name>.git" when a repo view
	// omits ssh_url (MinimalRepository). Default "git@github.com:".
	CloneURLBase string
	// Token, when non-empty, is passed to the gh exec as GH_TOKEN (via
	// cmd.Env) so two realClients in one process can act as two users —
	// needed only by the build-tagged conformance backend (M3). Empty =
	// inherit ambient gh auth (production). The fake ignores it.
	Token string
}

func (c Conf) cloneBase() string {
	if c.CloneURLBase == "" {
		return "git@github.com:"
	}
	return c.CloneURLBase
}

// Client is the provider-neutral port for the repo-hosting collaboration
// control plane. Real (execs gh) and fake (in-memory) adapters implement
// it; consumers depend only on this interface. The surface is exactly
// what nous's consumers use — not a verbatim copy of the GitHub API.
type Client interface {
	// identity
	AuthLogin() (string, error)
	UserExists(login string) error // ErrUserNotVisible (wrapped) on 404

	// collaborators
	CollaboratorPermission(owner, repo, login string) (string, error)
	ListCollaborators(owner, repo string) ([]string, error)
	AddCollaborator(owner, repo, login, permission string) error
	InviteCollaborator(owner, repo, login, permission string) (InviteResult, error)
	RemoveCollaborator(owner, repo, login string) error

	// invitations — repo side (operator/admin)
	RepoPendingInvitations(owner, repo string) ([]RepoInvitation, error)
	DeleteRepoInvitation(owner, repo string, id int) error

	// invitations — user side (invitee)
	PendingInvitations() ([]Invitation, error)
	AcceptInvitation(id int) error
	DeclineInvitation(id int) error

	// repos
	UserRepos() ([]UserRepo, error)

	// CloneURL resolves a clone URL for a repository view, applying the
	// adapter's CloneURLBase to the MinimalRepository fallback (empty
	// ssh_url). Real → git@github.com:; fake → file://<tmpdir>/.
	CloneURL(fullName, sshURL string) string
}

// inviteCollaborator is the FRESH-invite composite, shared by every
// adapter (ARCH-DRY / ARCH-PURE: pure orchestration over Client methods,
// owned by neither adapter). It works around GitHub's behavior where
// `PUT /collaborators/{login}` is a no-op (204, no email) when an
// invitation already exists for that login — INCLUDING an expired one —
// by deleting any existing repo invitation for `login` first, then PUTting.
//
// The stale-invitation clearing is load-bearing, not best-effort: if we
// can't confirm there's no existing invitation, the PUT may silently
// no-op (sending no email), so list/delete failures are hard errors
// rather than swallowed. nous#41 #11. Keeping this in one place is what
// guarantees the real and fake adapters can't drift on this contract.
func inviteCollaborator(c Client, owner, repo, login, permission string) (InviteResult, error) {
	var res InviteResult
	invs, err := c.RepoPendingInvitations(owner, repo)
	if err != nil {
		return res, fmt.Errorf("list pending invitations for %s/%s: %w (can't guarantee a stale invitation won't no-op the re-invite)", owner, repo, err)
	}
	for _, inv := range invs {
		if strings.EqualFold(inv.Invitee.Login, login) {
			if derr := c.DeleteRepoInvitation(owner, repo, inv.ID); derr != nil {
				return res, fmt.Errorf("delete stale invitation %d for %s on %s/%s: %w (PUT would no-op against it)", inv.ID, login, owner, repo, derr)
			}
			res.ReplacedStale = true
		}
	}
	if err := c.AddCollaborator(owner, repo, login, permission); err != nil {
		return res, err
	}
	return res, nil
}

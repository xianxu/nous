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

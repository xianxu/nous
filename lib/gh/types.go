package gh

import "errors"

// ErrUserNotVisible is returned by Client.UserExists when GitHub's public
// /users/<login> endpoint returns 404. For brand-new accounts that's
// often propagation lag rather than a real "no such user" — the caller
// decides whether to gate on it (nous brain invite) or proceed
// (nous#25's SKIP_REPO_CREATE-style escape hatch).
var ErrUserNotVisible = errors.New("github user not visible via public API")

// Invitation captures the minimal subset of GitHub's
// /user/repository_invitations response that nous brain join needs to
// filter, display, and accept.
//
// The embedded repository representation in this endpoint is a
// "MinimalRepository" — it omits clone_url, ssh_url, git_url, and
// topics. We populate them as best-effort (json tags still set so
// they pick up the values when present, e.g., on endpoints that do
// return the full repository object), and fall back to constructing
// from full_name via Client.CloneURL. Empirically confirmed 2026-05-19
// against a real invitation: ssh_url was empty, topics was null.
type Invitation struct {
	ID         int
	Repository struct {
		FullName    string                 `json:"full_name"`
		Name        string                 `json:"name"`
		Owner       struct{ Login string } `json:"owner"`
		Private     bool                   `json:"private"`
		Description string                 `json:"description"`
		Topics      []string               `json:"topics"`
		CloneURL    string                 `json:"clone_url"`
		SSHURL      string                 `json:"ssh_url"`
		HTMLURL     string                 `json:"html_url"`
	} `json:"repository"`
	Inviter struct{ Login string } `json:"inviter"`
}

// AsUserRepo converts an Invitation's embedded repository view into
// a UserRepo. Used by the TUI to splice a just-accepted repo into
// the visible list right after the operator presses `enter` on a
// pending row — GitHub's /user/repos endpoint lags
// invitation-acceptance by tens of seconds, so without this splice
// the brain disappears from the list during that gap.
func (i Invitation) AsUserRepo() UserRepo {
	r := UserRepo{
		FullName:    i.Repository.FullName,
		Name:        i.Repository.Name,
		Private:     i.Repository.Private,
		Description: i.Repository.Description,
		Topics:      i.Repository.Topics,
		SSHURL:      i.Repository.SSHURL,
		CloneURL:    i.Repository.CloneURL,
	}
	r.Owner.Login = i.Repository.Owner.Login
	return r
}

// UserRepo is the minimal subset of GitHub's repository
// representation that nous needs for the "accessible but not yet
// cloned" detection in the brain list view. Mirrors the
// MinimalRepository fields the /user/repos endpoint returns by
// default.
type UserRepo struct {
	FullName    string                 `json:"full_name"`
	Name        string                 `json:"name"`
	Owner       struct{ Login string } `json:"owner"`
	Private     bool                   `json:"private"`
	Description string                 `json:"description"`
	Topics      []string               `json:"topics"`
	SSHURL      string                 `json:"ssh_url"`
	CloneURL    string                 `json:"clone_url"`
}

// RepoInvitation is one pending invitation the operator (or another
// admin on the repo) sent that the invitee hasn't accepted yet.
// Mirrors GitHub's response to GET /repos/{owner}/{repo}/invitations.
type RepoInvitation struct {
	ID        int                    `json:"id"`
	Invitee   struct{ Login string } `json:"invitee"`
	Inviter   struct{ Login string } `json:"inviter"`
	CreatedAt string                 `json:"created_at"`
	Expired   bool                   `json:"expired"`
}

// InviteResult reports what Client.InviteCollaborator did.
type InviteResult struct {
	// ReplacedStale is true when an existing (pending or expired)
	// invitation for the login was deleted before re-inviting.
	ReplacedStale bool
}

// cloneURL resolves a clone URL from a repository view. Prefers an
// explicit ssh_url (full Repository); else fabricates from full_name
// using base (the MinimalRepository fallback that
// /user/repository_invitations forces). base is "git@github.com:" in
// production; the fake uses a file://<tmpdir>/ base so clones resolve
// to local bare repos. This is the single resolver replacing the two
// former CloneSSHURL method bodies (ARCH-DRY) and is the seam where
// the control-plane fake meets the tmpdir data plane (ARCH-PURE: pure).
func cloneURL(base, fullName, sshURL string) string {
	if sshURL != "" {
		return sshURL
	}
	if fullName == "" {
		return ""
	}
	return base + fullName + ".git"
}

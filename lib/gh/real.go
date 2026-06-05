package gh

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// realClient is the production Client: every method shells out to the
// `gh` CLI. It is the only thing in nous that execs `gh`.
//
// We deliberately don't import the upstream go-gh library — the surface
// area we need is small, the subprocess pattern matches the rest of the
// codebase (gpg/git wrappers in lib/brain, lib/identity), and a hard dep
// on go-gh would pull a transitive tree we don't otherwise need.
//
// The /users/<login> endpoint can lag for brand-new accounts (~minutes
// to hours; see nous#25 for the original repro). UserExists treats that
// 404 as "not visible right now" and lets the caller decide what to do.
type realClient struct{ conf Conf }

// New returns the production Client that execs `gh`.
func New(conf Conf) Client { return &realClient{conf: conf} }

// runImpl is the swappable exec seam (tests replace it). It takes conf
// so per-client GH_TOKEN works (two conformance clients, two tokens, one
// process). Production path: conf.Token == "" → inherit ambient gh auth.
// On failure the error includes stderr (gh's stderr is the
// operator-readable message; stdout is JSON for `gh api`).
var runImpl = func(conf Conf, args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	if conf.Token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+conf.Token)
	}
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("gh %s: %s", strings.Join(args, " "), strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

func (c *realClient) run(args ...string) ([]byte, error) { return runImpl(c.conf, args...) }

// CloneURL applies the adapter's CloneURLBase to the MinimalRepository
// fallback. For real GitHub the base is "git@github.com:".
func (c *realClient) CloneURL(fullName, sshURL string) string {
	return cloneURL(c.conf.cloneBase(), fullName, sshURL)
}

// AuthLogin returns the github login of the currently-authenticated
// gh token. The `/user` (singular) endpoint reads through the bearer
// token directly — works even when `/users/<login>` is lagging for
// brand-new accounts.
func (c *realClient) AuthLogin() (string, error) {
	out, err := c.run("api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// UserExists probes `/users/<login>`. Returns nil when 200, returns
// ErrUserNotVisible (wrapped) on 404, returns the raw gh error on
// anything else.
func (c *realClient) UserExists(login string) error {
	_, err := c.run("api", "users/"+login, "--silent")
	if err != nil {
		if strings.Contains(err.Error(), "HTTP 404") || strings.Contains(err.Error(), "Not Found") {
			return fmt.Errorf("%w: %s", ErrUserNotVisible, login)
		}
		return err
	}
	return nil
}

// CollaboratorPermission returns the authenticated user's permission
// level on owner/repo: one of "admin", "maintain", "push", "triage",
// "pull", or "" (not a collaborator / not visible).
//
// For personal repos, the owner is implicitly "admin" — the GitHub
// API actually returns "admin" for the owner's own query, so callers
// don't need a separate "is owner" check.
//
// Errors propagate on infrastructure failures; a 404 (no access) is
// surfaced as ("", nil) rather than an error — the most common
// reason a brain shows up in someone's workspace without permission
// info is "I'm a recipient via gcrypt but not a github collaborator,"
// which is a valid state.
func (c *realClient) CollaboratorPermission(owner, repo, login string) (string, error) {
	out, err := c.run("api",
		fmt.Sprintf("repos/%s/%s/collaborators/%s/permission", owner, repo, login),
		"--jq", ".permission")
	if err != nil {
		// 404 = not a collaborator. Most callers want to treat this as
		// "no permission" rather than "error" — surface as empty string.
		if strings.Contains(err.Error(), "HTTP 404") || strings.Contains(err.Error(), "Not Found") {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ListCollaborators returns the GitHub logins of every current collaborator
// on owner/repo (any permission level, accepted only — pending invitations
// are not collaborators yet). Used to detect membership-record drift: logins
// in the brain's records (recipient_logins / keys branch) that are no longer
// collaborators, a possible GitHub login rename (nous#41 #10).
func (c *realClient) ListCollaborators(owner, repo string) ([]string, error) {
	out, err := c.run("api", "--paginate",
		fmt.Sprintf("repos/%s/%s/collaborators", owner, repo),
		"--jq", ".[].login")
	if err != nil {
		return nil, err
	}
	var logins []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			logins = append(logins, l)
		}
	}
	return logins, nil
}

// AddCollaborator invites `login` to `owner/repo` with the given
// permission ("push", "pull", "admin", "maintain", "triage"). The
// invitee must accept (via web UI or `nous brain join`) for the
// invitation to take effect.
//
// Idempotent: GitHub returns 204 (no content) if `login` is already a
// collaborator; 201 (created) if a new invitation was created. We
// don't surface the difference — both are "the invitation is in
// place" from the operator's perspective.
func (c *realClient) AddCollaborator(owner, repo, login, permission string) error {
	_, err := c.run("api", "-X", "PUT",
		fmt.Sprintf("repos/%s/%s/collaborators/%s", owner, repo, login),
		"-f", "permission="+permission,
		"--silent")
	return err
}

// DeleteRepoInvitation deletes a pending repository invitation by id —
// the owner-side `DELETE /repos/{owner}/{repo}/invitations/{id}`
// (distinct from DeclineInvitation, which is the invitee-side decline).
// Needed because a lingering invitation (pending OR expired) makes
// AddCollaborator's PUT a silent no-op, so it must be cleared before a
// re-invite can actually re-send.
func (c *realClient) DeleteRepoInvitation(owner, repo string, id int) error {
	_, err := c.run("api", "-X", "DELETE",
		fmt.Sprintf("repos/%s/%s/invitations/%d", owner, repo, id),
		"--silent")
	return err
}

// InviteCollaborator sends a FRESH collaborator invitation, working
// around GitHub's behavior where `PUT /collaborators/{login}` is a
// no-op (204, no email) when an invitation already exists for that
// login — INCLUDING an expired one. A naive re-invite therefore sends
// nothing. This deletes any existing repo invitation for `login` first,
// then PUTs, so a re-invite always re-sends.
//
// The stale-invitation clearing is load-bearing, not best-effort: if we
// can't confirm there's no existing invitation, the PUT may silently
// no-op (sending no email), so list/delete failures are hard errors
// rather than swallowed. nous#41 #11.
func (c *realClient) InviteCollaborator(owner, repo, login, permission string) (InviteResult, error) {
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

// PendingInvitations lists all repository invitations the
// authenticated user has not yet accepted/declined. Returns an empty
// slice (not nil) when there are none.
func (c *realClient) PendingInvitations() ([]Invitation, error) {
	out, err := c.run("api", "user/repository_invitations")
	if err != nil {
		return nil, err
	}
	var invs []Invitation
	if err := json.Unmarshal(out, &invs); err != nil {
		return nil, fmt.Errorf("parse repository_invitations: %w", err)
	}
	return invs, nil
}

// AcceptInvitation accepts the invitation identified by id (returned
// by PendingInvitations). GitHub's PATCH endpoint returns 204 on
// success; the body is empty, so we discard stdout.
func (c *realClient) AcceptInvitation(id int) error {
	_, err := c.run("api", "-X", "PATCH",
		"user/repository_invitations/"+strconv.Itoa(id),
		"--silent")
	return err
}

// UserRepos lists every repository the authenticated user has any
// access to (owned, collaborator, org-member). Single page; for
// operators with > 100 repos the result is truncated — the
// "accessible-but-not-cloned" view it powers is informational, not
// security-critical, so partial results are acceptable.
func (c *realClient) UserRepos() ([]UserRepo, error) {
	out, err := c.run("api", "user/repos", "--paginate", "-X", "GET", "-f", "per_page=100")
	if err != nil {
		return nil, err
	}
	var repos []UserRepo
	// --paginate emits a single JSON array concatenated across pages.
	if err := json.Unmarshal(out, &repos); err != nil {
		return nil, fmt.Errorf("parse user/repos: %w", err)
	}
	return repos, nil
}

// RemoveCollaborator removes `login` as a collaborator on
// owner/repo. Used by `nous brain leave` so a collaborator can
// revoke their own GitHub access as the final step of leaving a
// shared brain.
//
// GitHub allows a collaborator to remove themselves (200 OK).
// Repo owners can remove any collaborator (other than themselves).
// 204 NoContent on success; bubbled gh error otherwise.
func (c *realClient) RemoveCollaborator(owner, repo, login string) error {
	_, err := c.run("api", "-X", "DELETE",
		fmt.Sprintf("repos/%s/%s/collaborators/%s", owner, repo, login),
		"--silent")
	return err
}

// RepoPendingInvitations lists invitations the operator (or other
// repo admins) has sent for this repo that haven't been
// accepted/declined yet. Surfaces the operator-side limbo state
// between "I invited X" and "X accepted + auto-admit ran" so the
// brain TUI can show invited-but-not-yet-collaborating peers.
//
// Requires push or admin access on the repo (GitHub returns 404
// otherwise). Errors are returned to the caller; nil slice means
// the call succeeded with no pending invitations.
func (c *realClient) RepoPendingInvitations(owner, repo string) ([]RepoInvitation, error) {
	out, err := c.run("api", "--paginate", fmt.Sprintf("repos/%s/%s/invitations", owner, repo))
	if err != nil {
		return nil, err
	}
	var invs []RepoInvitation
	if err := json.Unmarshal(out, &invs); err != nil {
		return nil, fmt.Errorf("parse repo invitations: %w", err)
	}
	return invs, nil
}

// DeclineInvitation declines an invitation. Symmetric with
// AcceptInvitation. Not used in the happy-path flow but useful for
// the operator-tooling cases where an invite shouldn't actually be
// accepted (e.g., test artifacts).
func (c *realClient) DeclineInvitation(id int) error {
	_, err := c.run("api", "-X", "DELETE",
		"user/repository_invitations/"+strconv.Itoa(id),
		"--silent")
	return err
}

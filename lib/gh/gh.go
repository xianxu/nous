// Package gh wraps the `gh` CLI for the few GitHub operations nous
// performs as part of the recipient-onboarding flow (#26):
//
//   - AuthLogin           — who is gh authenticated as
//   - UserExists          — does this github user exist (public lookup)
//   - AddCollaborator     — invite <login> to <owner>/<repo>
//   - PendingInvitations  — list repo invites for the auth'd user
//   - AcceptInvitation    — accept one by id
//
// All operations shell out to `gh api` and parse JSON. We deliberately
// don't import the upstream go-gh library — the surface area we need is
// small, the subprocess pattern matches the rest of the codebase
// (gpg/git wrappers in lib/brain, lib/identity), and a hard dep on
// go-gh would pull a transitive tree we don't otherwise need.
//
// The /users/<login> endpoint can lag for brand-new accounts (~minutes
// to hours; see nous#25 for the original repro). UserExists treats that
// 404 as "not visible right now" and lets the caller decide what to do.
package gh

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Invitation captures the minimal subset of GitHub's
// /user/repository_invitations response that nous brain join needs to
// filter, display, and accept.
//
// The embedded repository representation in this endpoint is a
// "MinimalRepository" — it omits clone_url, ssh_url, git_url, and
// topics. We populate them as best-effort (json tags still set so
// they pick up the values when present, e.g., on endpoints that do
// return the full repository object), and fall back to constructing
// from full_name via CloneSSHURL. Empirically confirmed 2026-05-19
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

// CloneSSHURL returns the SSH clone URL for the invitation's repo.
// Uses the embedded ssh_url if present (full Repository object);
// otherwise constructs it from full_name (MinimalRepository case,
// which is what /user/repository_invitations actually returns).
func (i Invitation) CloneSSHURL() string {
	if i.Repository.SSHURL != "" {
		return i.Repository.SSHURL
	}
	if i.Repository.FullName == "" {
		return ""
	}
	return "git@github.com:" + i.Repository.FullName + ".git"
}

// run invokes gh with the given args and returns stdout + an error
// that includes stderr on failure (gh's stderr is the operator-readable
// message; stdout is JSON for `gh api`).
func run(args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
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

// AuthLogin returns the github login of the currently-authenticated
// gh token. The `/user` (singular) endpoint reads through the bearer
// token directly — works even when `/users/<login>` is lagging for
// brand-new accounts.
func AuthLogin() (string, error) {
	out, err := run("api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ErrUserNotVisible is returned by UserExists when GitHub's public
// /users/<login> endpoint returns 404. For brand-new accounts that's
// often propagation lag rather than a real "no such user" — the caller
// decides whether to gate on it (nous brain invite) or proceed
// (nous#25's SKIP_REPO_CREATE-style escape hatch).
var ErrUserNotVisible = errors.New("github user not visible via public API")

// UserExists probes `/users/<login>`. Returns nil when 200, returns
// ErrUserNotVisible (wrapped) on 404, returns the raw gh error on
// anything else.
func UserExists(login string) error {
	_, err := run("api", "users/"+login, "--silent")
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
func CollaboratorPermission(owner, repo, login string) (string, error) {
	out, err := run("api",
		fmt.Sprintf("repos/%s/%s/collaborators/%s/permission", owner, repo, login),
		"--jq", ".permission")
	if err != nil {
		// 404 = not a collaborator. Most callers want to treat
		// this as "no permission" rather than "error" — surface
		// as empty string.
		if strings.Contains(err.Error(), "HTTP 404") || strings.Contains(err.Error(), "Not Found") {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
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
func AddCollaborator(owner, repo, login, permission string) error {
	_, err := run("api", "-X", "PUT",
		fmt.Sprintf("repos/%s/%s/collaborators/%s", owner, repo, login),
		"-f", "permission="+permission,
		"--silent")
	return err
}

// PendingInvitations lists all repository invitations the
// authenticated user has not yet accepted/declined. Returns an empty
// slice (not nil) when there are none.
func PendingInvitations() ([]Invitation, error) {
	out, err := run("api", "user/repository_invitations")
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
func AcceptInvitation(id int) error {
	_, err := run("api", "-X", "PATCH",
		"user/repository_invitations/"+strconv.Itoa(id),
		"--silent")
	return err
}

// UserRepo is the minimal subset of GitHub's repository
// representation that nous needs for the "accessible but not yet
// cloned" detection in the brain list view. Mirrors the
// MinimalRepository fields the /user/repos endpoint returns by
// default.
type UserRepo struct {
	FullName    string   `json:"full_name"`
	Name        string   `json:"name"`
	Owner       struct{ Login string } `json:"owner"`
	Private     bool     `json:"private"`
	Description string   `json:"description"`
	Topics      []string `json:"topics"`
	SSHURL      string   `json:"ssh_url"`
	CloneURL    string   `json:"clone_url"`
}

// CloneSSHURL returns a usable ssh clone URL for this repo. Falls
// back to constructing from FullName when GitHub doesn't populate
// ssh_url (same MinimalRepository fallback Invitation uses).
func (r UserRepo) CloneSSHURL() string {
	if r.SSHURL != "" {
		return r.SSHURL
	}
	if r.FullName == "" {
		return ""
	}
	return "git@github.com:" + r.FullName + ".git"
}

// UserRepos lists every repository the authenticated user has any
// access to (owned, collaborator, org-member). Single page; for
// operators with > 100 repos the result is truncated — the
// "accessible-but-not-cloned" view it powers is informational, not
// security-critical, so partial results are acceptable.
func UserRepos() ([]UserRepo, error) {
	out, err := run("api", "user/repos", "--paginate", "-X", "GET", "-f", "per_page=100")
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

// DeclineInvitation declines an invitation. Symmetric with
// AcceptInvitation. Not used in the happy-path flow but useful for
// the operator-tooling cases where an invite shouldn't actually be
// accepted (e.g., test artifacts).
func DeclineInvitation(id int) error {
	_, err := run("api", "-X", "DELETE",
		"user/repository_invitations/"+strconv.Itoa(id),
		"--silent")
	return err
}

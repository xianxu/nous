package gh

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// fakeClient is the stateful in-memory Client adapter (shim'(gh)) — a
// model of GitHub's control plane to the fidelity nous actually
// exercises. It is the hermetic test substrate: real-flow code paths run
// through it unchanged. Per AGENTS.md §5 (spirit), it is a *stateful*
// fake, not a per-call stub.
//
// Control plane (invitations, collaborators, user/repo lookups) lives in
// fakeState; the data plane (repo content) is real git on tmpdir bare
// repos, joined to the control plane by CloneURL — see CreateRepo.
//
// Encoded peculiarities (the GitHub-isms nous#26 exposed):
//   - /user/repository_invitations returns a MinimalRepository: ssh_url
//     is EMPTY (PendingInvitations below).
//   - PUT collaborators/<login> no-ops against an existing invitation
//     (AddCollaborator below) — the nous#41 #11 surface.
//   - /users/<login> 404s for shadow-flagged users while the bearer
//     token still works (UserExists / AuthLogin below).
type fakeClient struct {
	st           *fakeState
	currentToken string
	base         string
}

type fakeUser struct {
	login    string
	token    string
	shadowed bool // /users/<login> 404s while the token still works
}

type fakeRepo struct {
	owner         string
	name          string
	private       bool
	topics        []string
	collaborators map[string]string // login -> permission (owner seeded as "admin")
	barePath      string            // tmpdir bare repo (data plane), "" if no file:// base
}

type fakeInvitation struct {
	id         int
	owner      string
	repo       string
	invitee    string
	inviter    string
	permission string
	accepted   bool
}

type fakeState struct {
	mu                  sync.Mutex
	dir                 string // filesystem root for bare repos (base minus file://)
	hasFileBase         bool
	users               map[string]*fakeUser // login -> user
	tokens              map[string]string    // token -> login
	repos               map[string]*fakeRepo // "owner/name" -> repo
	invitations         []*fakeInvitation
	nextInvID           int
	failListInvitations bool
}

func newFakeState(base string) *fakeState {
	dir := strings.TrimPrefix(base, "file://")
	return &fakeState{
		dir:         dir,
		hasFileBase: strings.HasPrefix(base, "file://"),
		users:       map[string]*fakeUser{},
		tokens:      map[string]string{},
		repos:       map[string]*fakeRepo{},
		nextInvID:   1,
	}
}

func (s *fakeState) tokenFor(login string) string {
	if u := s.users[login]; u != nil {
		return u.token
	}
	return ""
}

// NewFake returns an in-memory Client. Conf.CloneURLBase should be a
// caller-owned file://<dir>/ base (use t.TempDir() so Go cleans it up —
// NewFake never creates its own tmpdir, since NewFake(Conf) has no
// *testing.T and cannot register cleanup; per-test teardown is the
// caller's t.TempDir()). When the base is not file://-rooted, the fake
// is still usable for pure control-plane tests; only git clone/push
// against CloneURL won't resolve.
func NewFake(conf Conf) Client {
	base := conf.cloneBase()
	return &fakeClient{st: newFakeState(base), currentToken: "", base: base}
}

// CloneURL applies the fake's (file://-rooted) base to the
// MinimalRepository fallback, so clones resolve to local bare repos.
func (c *fakeClient) CloneURL(fullName, sshURL string) string {
	return cloneURL(c.base, fullName, sshURL)
}

// ---- test seeding API (concrete type, off-interface) ----
// Tests reach these via NewFake(...).(*fakeClient).

// AddUser registers a user and returns its bearer token.
func (c *fakeClient) AddUser(login string) string {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	tok := "tok-" + login
	c.st.users[login] = &fakeUser{login: login, token: tok}
	c.st.tokens[tok] = login
	return tok
}

// SwitchUser points this client at login's bearer token.
func (c *fakeClient) SwitchUser(login string) { c.currentToken = c.st.tokenFor(login) }

// ShadowUser toggles the shadow flag: /users/<login> 404s while the
// token still authenticates (the brand-new-account asymmetry).
func (c *fakeClient) ShadowUser(login string, shadowed bool) {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	if u := c.st.users[login]; u != nil {
		u.shadowed = shadowed
	}
}

// CreateRepo registers a repo (owner seeded as "admin" collaborator) and,
// when the base is file://-rooted, git-inits a bare repo so the data
// plane round-trips.
func (c *fakeClient) CreateRepo(owner, name string, private bool) {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	r := &fakeRepo{
		owner:         owner,
		name:          name,
		private:       private,
		collaborators: map[string]string{owner: "admin"},
	}
	if c.st.hasFileBase {
		r.barePath = filepath.Join(c.st.dir, owner, name+".git")
		_ = os.MkdirAll(filepath.Dir(r.barePath), 0o755)
		// best-effort; a clone against a missing bare repo will fail
		// visibly in the test that needs the data plane.
		_ = exec.Command("git", "init", "--bare", r.barePath).Run()
	}
	c.st.repos[owner+"/"+name] = r
}

// FailListInvitations injects a fault: RepoPendingInvitations errors.
// Used to exercise the nous#41 #11 hard-error path (a list failure must
// NOT be swallowed, or a re-invite silently no-ops).
func (c *fakeClient) FailListInvitations(fail bool) {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	c.st.failListInvitations = fail
}

// AsUser returns a SECOND *fakeClient sharing the SAME fakeState but
// bound to login's token. This is how a test (or the contract suite)
// gets an operator client and an invitee client over one shared world —
// the real backend's two-GH_TOKEN shape, in memory.
func (c *fakeClient) AsUser(login string) *fakeClient {
	return &fakeClient{st: c.st, currentToken: c.st.tokenFor(login), base: c.base}
}

// ---- Client implementation ----

func (c *fakeClient) currentLogin() (string, error) {
	if c.currentToken == "" {
		return "", fmt.Errorf("gh fake: no current token (call SwitchUser)")
	}
	login, ok := c.st.tokens[c.currentToken]
	if !ok {
		return "", fmt.Errorf("gh fake: unknown token %q", c.currentToken)
	}
	return login, nil
}

func (c *fakeClient) AuthLogin() (string, error) {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	// Reads through the bearer token directly — works even for a
	// shadow-flagged user (mirrors the real /user endpoint).
	return c.currentLogin()
}

func (c *fakeClient) UserExists(login string) error {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	u := c.st.users[login]
	if u == nil || u.shadowed {
		return fmt.Errorf("%w: %s", ErrUserNotVisible, login)
	}
	return nil
}

func (c *fakeClient) CollaboratorPermission(owner, repo, login string) (string, error) {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	r := c.st.repos[owner+"/"+repo]
	if r == nil {
		return "", nil // 404 → not a collaborator → "" (matches real)
	}
	return r.collaborators[login], nil // "" if not a collaborator
}

func (c *fakeClient) ListCollaborators(owner, repo string) ([]string, error) {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	r := c.st.repos[owner+"/"+repo]
	if r == nil {
		return nil, nil
	}
	var logins []string
	for l := range r.collaborators {
		logins = append(logins, l)
	}
	return logins, nil
}

// AddCollaborator models GitHub's invite-or-noop. Already a collaborator
// → 204 no-op. An invitation already pending for login → no-op (no new
// invite, no email) — the peculiarity nous#41 #11 works around. Else a
// fresh pending invitation is created (the invitee must AcceptInvitation).
func (c *fakeClient) AddCollaborator(owner, repo, login, permission string) error {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	r := c.st.repos[owner+"/"+repo]
	if r == nil {
		return fmt.Errorf("gh fake: no such repo %s/%s", owner, repo)
	}
	if _, ok := r.collaborators[login]; ok {
		return nil // already a collaborator (204)
	}
	for _, inv := range c.st.invitations {
		if inv.owner == owner && inv.repo == repo && strings.EqualFold(inv.invitee, login) && !inv.accepted {
			return nil // PECULIARITY: PUT no-ops against an existing invitation
		}
	}
	inviter, _ := c.currentLogin()
	c.st.invitations = append(c.st.invitations, &fakeInvitation{
		id:         c.st.nextInvID,
		owner:      owner,
		repo:       repo,
		invitee:    login,
		inviter:    inviter,
		permission: permission,
	})
	c.st.nextInvID++
	return nil
}

// InviteCollaborator mirrors the real adapter's composite: clear any
// stale (pending) invitation for login first, then AddCollaborator, so a
// re-invite actually re-sends instead of no-opping. A list/delete failure
// is a hard error (nous#41 #11) — not swallowed.
func (c *fakeClient) InviteCollaborator(owner, repo, login, permission string) (InviteResult, error) {
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

func (c *fakeClient) RemoveCollaborator(owner, repo, login string) error {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	if r := c.st.repos[owner+"/"+repo]; r != nil {
		delete(r.collaborators, login)
	}
	return nil
}

func (c *fakeClient) RepoPendingInvitations(owner, repo string) ([]RepoInvitation, error) {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	if c.st.failListInvitations {
		return nil, fmt.Errorf("gh fake: injected list-invitations failure")
	}
	var out []RepoInvitation
	for _, inv := range c.st.invitations {
		if inv.owner == owner && inv.repo == repo && !inv.accepted {
			ri := RepoInvitation{ID: inv.id}
			ri.Invitee.Login = inv.invitee
			ri.Inviter.Login = inv.inviter
			out = append(out, ri)
		}
	}
	return out, nil
}

func (c *fakeClient) DeleteRepoInvitation(owner, repo string, id int) error {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	for i, inv := range c.st.invitations {
		if inv.id == id && inv.owner == owner && inv.repo == repo {
			c.st.invitations = append(c.st.invitations[:i], c.st.invitations[i+1:]...)
			return nil
		}
	}
	return nil
}

// PendingInvitations returns the user-side invitations for the current
// token, in the MinimalRepository shape: ssh_url is EMPTY (the nous#26
// bug-2 surface — consumers must fall back to CloneURL).
func (c *fakeClient) PendingInvitations() ([]Invitation, error) {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	login, err := c.currentLogin()
	if err != nil {
		return nil, err
	}
	invs := []Invitation{}
	for _, inv := range c.st.invitations {
		if !strings.EqualFold(inv.invitee, login) || inv.accepted {
			continue
		}
		r := c.st.repos[inv.owner+"/"+inv.repo]
		var iv Invitation
		iv.ID = inv.id
		iv.Repository.FullName = inv.owner + "/" + inv.repo
		iv.Repository.Name = inv.repo
		iv.Repository.Owner.Login = inv.owner
		if r != nil {
			iv.Repository.Private = r.private
			iv.Repository.Topics = r.topics
		}
		// MinimalRepository: ssh_url / clone_url intentionally left empty.
		iv.Inviter.Login = inv.inviter
		invs = append(invs, iv)
	}
	return invs, nil
}

// AcceptInvitation transitions the invitation to accepted and makes the
// invitee a collaborator at the invited permission.
func (c *fakeClient) AcceptInvitation(id int) error {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	login, err := c.currentLogin()
	if err != nil {
		return err
	}
	for _, inv := range c.st.invitations {
		if inv.id == id && strings.EqualFold(inv.invitee, login) {
			inv.accepted = true
			if r := c.st.repos[inv.owner+"/"+inv.repo]; r != nil {
				perm := inv.permission
				if perm == "" {
					perm = "push"
				}
				r.collaborators[login] = perm
			}
			return nil
		}
	}
	return fmt.Errorf("gh fake: no pending invitation %d for %s", id, login)
}

func (c *fakeClient) DeclineInvitation(id int) error {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	login, _ := c.currentLogin()
	for i, inv := range c.st.invitations {
		if inv.id == id && strings.EqualFold(inv.invitee, login) {
			c.st.invitations = append(c.st.invitations[:i], c.st.invitations[i+1:]...)
			return nil
		}
	}
	return nil
}

// UserRepos lists repos the current user owns or collaborates on, in the
// MinimalRepository shape (empty ssh_url).
func (c *fakeClient) UserRepos() ([]UserRepo, error) {
	c.st.mu.Lock()
	defer c.st.mu.Unlock()
	login, err := c.currentLogin()
	if err != nil {
		return nil, err
	}
	repos := []UserRepo{}
	for key, r := range c.st.repos {
		if _, ok := r.collaborators[login]; !ok {
			continue
		}
		var ur UserRepo
		ur.FullName = key
		ur.Name = r.name
		ur.Owner.Login = r.owner
		ur.Private = r.private
		ur.Topics = r.topics
		// MinimalRepository: ssh_url left empty.
		repos = append(repos, ur)
	}
	return repos, nil
}

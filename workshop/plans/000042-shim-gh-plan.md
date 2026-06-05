# shim(gh)+shim'(gh) Implementation Plan

> **For agentic workers:** Consult AGENTS.md Section 3 (Subagent Strategy) to determine the appropriate execution approach: use superpowers-subagent-driven-development (if subagents are suitable per AGENTS.md) or superpowers-executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Put nous's GitHub access behind a provider-neutral port (`gh.Client`) with two adapters — a `real` adapter that execs `gh` and a stateful in-memory `fake` — so every GitHub-touching flow gets a hermetic e2e substrate, grounded against reality by a dual-backend contract test.

**Architecture:** Ports & Adapters. One Go interface (`gh.Client`) owned by nous, constructed uniformly via `New(Conf)` (real) / `NewFake(Conf)` (fake) and injected into all 13 consumers (constructor/struct DI). Wire-fidelity (below-the-seam endpoint bugs) is owned by a periodic dual-backend contract test, not the fast tests. The fake models GitHub's *control plane* in memory; repo content stays real git on tmpdir bare repos, joined to the fake via an injectable clone-URL base.

**Tech Stack:** Go, `gh` CLI (real adapter only), `os/exec`, `net/http`-free (no bridge), `testing`, real `git` on tmpdir bare repos, bubbletea (TUI consumers).

**Spec:** `workshop/issues/000042-shim-gh-shim-gh-hermetic-github-control-plane-fake-behind-a-provider-neutral-port.md`

---

## Core concepts

### Pure entities (the conceptual core)

| Name | Lives in | Status |
|------|----------|--------|
| `Invitation` | `lib/gh/types.go` | modified (moved) |
| `UserRepo` | `lib/gh/types.go` | modified (moved) |
| `RepoInvitation` | `lib/gh/types.go` | modified (moved) |
| `InviteResult` | `lib/gh/types.go` | modified (moved) |
| `Conf` | `lib/gh/client.go` | new |
| `cloneURL` (pure helper) | `lib/gh/types.go` | new |
| `fakeState` | `lib/gh/fake.go` | new |

- **`Invitation` / `UserRepo` / `RepoInvitation` / `InviteResult`** — unchanged data shapes, relocated out of `gh.go` into `types.go` so both adapters share them (no adapter owns the wire types).
  - **DRY rationale:** one definition of each shape consumed by real adapter, fake adapter, and consumers. First occurrence of "shared wire types under a port."
- **`Conf`** — opaque, service-specific construction config. For gh: `CloneURLBase string` (default `git@github.com:`; the fake sets a `file://<tmpdir>/` base). The one cross-service convention is the *shape* `New(Conf)`/`NewFake(Conf)`, not the contents.
  - **Future extensions:** add fields as new peculiarities need configuring (e.g. fixed clock for invitation timestamps) without changing call sites.
- **`cloneURL(base, fullName, sshURL string) string`** — pure clone-URL resolver. Returns `sshURL` when present, else `base + fullName + ".git"`, else `""`. Replaces the two duplicated `CloneSSHURL()` method bodies (gh.go:85–93, 306–314).
  - **DRY rationale:** collapses the duplicated MinimalRepository fallback into one function; **this is the seam that resolves the data-plane-coupling hazard** (spec) — the real adapter passes `git@github.com:`, the fake passes the tmpdir base, so clones in tests resolve to local bare repos while preserving the "MinimalRepository omits ssh_url" peculiarity.
- **`fakeState`** — pure in-memory model of GitHub's control plane: users (login→token, shadowed flag), repos (owner/name→{private, topics, collaborators login→perm, bareRepoPath}), user-side invitations (id→{invitee, repo-minimal, inviter, accepted}). All mutations are plain method calls on this struct; no IO except the one-time `git init --bare` when a repo is seeded.
  - **Relationships:** 1 `fakeState` : N users : N repos : N invitations. `fakeClient` holds a `*fakeState` + a `currentToken`.
  - **Future extensions:** add fields per peculiarity (rate-limit counters, eventual-consistency lag toggles) without touching the `Client` surface.

### Integration points (where pure meets the world)

| Name | Lives in | Status | Wraps |
|------|----------|--------|-------|
| `Client` (interface) | `lib/gh/client.go` | new | the port |
| `realClient` | `lib/gh/real.go` | new (from `gh.go`) | `gh` CLI via `os/exec` |
| `fakeClient` | `lib/gh/fake.go` | new | in-memory `fakeState` + tmpdir git |
| consumer DI wiring | 13 files (see M1) | modified | — |

- **`Client`** — the provider-neutral port. Exactly the surface the 13 consumers use (see method list in M1). Both adapters implement it; consumers depend only on it.
  - **Injected into:** every consumer that today calls `gh.*` free functions — threaded via constructors (`NewRoot(c)`, `newXModel(c, …)`) and function params (brain/brainsync layers). Keeps consumer logic unit-testable with the fake.
- **`realClient`** — today's exec-`gh` logic, verbatim, behind the interface; the only thing that execs `gh`. Holds `Conf` (for `CloneURL`).
  - **Future extensions:** a `gitlabClient` adapter satisfying the same `Client`.
- **`fakeClient`** — stateful in-memory adapter. Multi-user via `SwitchUser(login)`; seeding via `AddUser`/`CreateRepo`; encodes peculiarities (MinimalRepository empty `ssh_url`, `AddCollaborator` no-op against an existing invitation, `UserExists` shadow-flag 404). Clone URLs resolve to tmpdir bare repos.
  - **Test surface:** this fake IS the deliverable's test substrate (per AGENTS.md §5 spirit — a stateful fake, not per-call stubs). Grounded by the M3 contract test against real `gh`.

---

## Milestones (each row is a review boundary → its own `sdlc milestone-close`)

- [ ] **M1 — Port + real adapter + DI migration** (the seam; bug-1 endpoint test)
- [ ] **M2 — Fake adapter** (state model, peculiarities, tmpdir data-plane coupling)
- [ ] **M3 — Dual-backend contract test** (grounding; certify against real `gh`)
- [ ] **M4 — Hermetic regression tests** (nous#26 bugs 2–5 + nous#41 #11; atlas)

---

## Chunk 1: M1 — Port + real adapter + DI migration

**Outcome:** `lib/gh` exposes `Client`/`Conf`/`New`; `gh.go` is split into `types.go` + `real.go` (logic unchanged); all 13 consumers receive a `Client` instead of calling free functions; a real-adapter unit test pins the bug-1 endpoint. Build, vet, and all existing tests pass.

### Task 1.1: Extract shared types

**Files:**
- Create: `lib/gh/types.go`
- Modify: `lib/gh/gh.go` (remove the moved types)

- [ ] **Step 1: Move `Invitation`, `UserRepo`, `RepoInvitation`, `InviteResult`, `ErrUserNotVisible` into `types.go`.** Keep them package-level (consumers reference `gh.Invitation` etc. unchanged).
- [ ] **Step 2: Add the pure `cloneURL` helper to `types.go`:**

```go
// cloneURL resolves a clone URL from a repository view. Prefers an
// explicit ssh_url (full Repository); else fabricates from full_name
// using base (the MinimalRepository fallback that
// /user/repository_invitations forces). base is "git@github.com:" in
// production; the fake uses a file://<tmpdir>/ base so clones resolve
// to local bare repos.
func cloneURL(base, fullName, sshURL string) string {
	if sshURL != "" {
		return sshURL
	}
	if fullName == "" {
		return ""
	}
	return base + fullName + ".git"
}
```

- [ ] **Step 3: Delete the two `CloneSSHURL()` methods** (gh.go:85–93, 306–314). Their callers will move to `Client.CloneURL` in Task 1.3 / migration. (ARCH-DRY: one resolver, parameterized by base.)
- [ ] **Step 4:** `go build ./... ` — expect failures only at the deleted `CloneSSHURL` call sites (fixed during migration, Task 1.4). Proceed.
- [ ] **Step 5: Commit** `#42 M1: gh: extract shared wire types + pure cloneURL helper`.

### Task 1.2: Define the port and Conf

**Files:**
- Create: `lib/gh/client.go`

- [ ] **Step 1: Write `Conf` and `Client`:**

```go
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

// Client is the provider-neutral port for repo-hosting collaboration
// control plane. Real (execs gh) and fake (in-memory) adapters
// implement it; consumers depend only on this interface.
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

	// clone-url resolution (carries the adapter's CloneURLBase so the
	// MinimalRepository fallback points at the right place)
	CloneURL(fullName, sshURL string) string
}
```

- [ ] **Step 2: Commit** `#42 M1: gh: define Client port + Conf`.

### Task 1.3: Real adapter (logic unchanged)

**Files:**
- Create: `lib/gh/real.go`
- Modify/Delete: `lib/gh/gh.go` (becomes empty → delete)

- [ ] **Step 1: Create `realClient` and `New`:**

```go
type realClient struct{ conf Conf }

// New returns the production Client that execs `gh`.
func New(conf Conf) Client { return &realClient{conf: conf} }

func (c *realClient) CloneURL(fullName, sshURL string) string {
	return cloneURL(c.conf.cloneBase(), fullName, sshURL)
}
```

- [ ] **Step 2: Convert each free function in `gh.go` into a `*realClient` method**, the only change being `run(...)` → `c.run(...)` (see Task 1.4 Step 1). E.g. `func AuthLogin()` → `func (c *realClient) AuthLogin()`. This is a mechanical move; **do not change any `gh api` argument** — that fidelity is what the M3 contract test certifies.
- [ ] **Step 3:** `go build ./lib/gh/` — expect PASS (the package compiles; consumers still broken).
- [ ] **Step 4: Commit** `#42 M1: gh: real adapter behind the port (logic unchanged)`.

### Task 1.4: Real-adapter endpoint test (bug-1 regression home)

**Files:**
- Create: `lib/gh/real_test.go`

> Bug 1 (the 404 on `/users/<login>` validation lookup, distinct from `/user`) lives *below* the seam — a library-level fake cannot see it (spec, Division of labor). Its permanent regression home is here: assert the exact endpoint strings the real adapter passes to `gh`, with `run` stubbed (no network).

- [ ] **Step 1:** Make exec injectable *and* token-aware. Today's free `run(args...)` becomes:

```go
// runImpl is the swappable exec seam (tests replace it). It takes conf
// so per-client GH_TOKEN works (two conformance clients, two tokens, one
// process). Production path: conf.Token == "" → inherit ambient gh auth.
var runImpl = func(conf Conf, args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	if conf.Token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+conf.Token)
	}
	out, err := cmd.Output()
	// ...existing ExitError → stderr-wrapped error handling, unchanged...
	return out, err
}

func (c *realClient) run(args ...string) ([]byte, error) { return runImpl(c.conf, args...) }
```

In Task 1.3 the moved method bodies call `c.run(...)` (the only delta from the original free-function bodies, which called `run(...)`). Tests swap `runImpl` with `t.Cleanup` to restore.
- [ ] **Step 2: Write the failing test** capturing args:

```go
func TestRealClient_UserExists_HitsUsersEndpoint(t *testing.T) {
	var gotArgs []string
	old := runImpl
	runImpl = func(_ Conf, args ...string) ([]byte, error) { gotArgs = args; return nil, nil }
	t.Cleanup(func() { runImpl = old })

	_ = New(Conf{}).UserExists("octocat")
	// bug 1: must probe /users/<login> (public lookup), NOT /user.
	want := []string{"api", "users/octocat", "--silent"}
	if !slices.Equal(gotArgs, want) {
		t.Fatalf("UserExists args = %v, want %v", gotArgs, want)
	}
}

func TestRealClient_AuthLogin_HitsUserEndpoint(t *testing.T) {
	var gotArgs []string
	old := runImpl
	runImpl = func(_ Conf, args ...string) ([]byte, error) { gotArgs = args; return []byte("x\n"), nil }
	t.Cleanup(func() { runImpl = old })
	_, _ = New(Conf{}).AuthLogin()
	want := []string{"api", "user", "--jq", ".login"} // /user, the bearer-token lookup
	if !slices.Equal(gotArgs, want) {
		t.Fatalf("AuthLogin args = %v, want %v", gotArgs, want)
	}
}
```

- [ ] **Step 3:** `go test ./lib/gh/ -run Endpoint -v` → expect PASS (asserts current correct behavior; fails if someone reverts the endpoint, which is the bug-1 guard).
- [ ] **Step 4: Commit** `#42 M1: gh: real-adapter endpoint tests (bug-1 below-seam guard)`.

### Task 1.5: Migrate the 13 consumers onto `Client` (constructor/struct DI)

**Files (all Modify):** `cmd/nous/brain_misc.go`, `brain_invite.go`, `brain_join.go`, `brain_publish.go`, `brain_recipient.go`, `brain_tui.go`; `lib/tui/brain/{root,list,accept_invite,invite_collab}.go` (+ any submodel that calls `gh.*`); `lib/brain/{operator,status}.go`; `lib/brainsync/{recipient,leave}.go`

**Pattern:** every site that called a free function `gh.X(...)` now calls `c.X(...)` on an injected `gh.Client`; every `inv.CloneSSHURL()` / `repo.CloneSSHURL()` becomes `c.CloneURL(inv.Repository.FullName, inv.Repository.SSHURL)`.

- [ ] **Step 1 — brain / brainsync layers:** add a `gh.Client` parameter to the exported functions in `lib/brain/operator.go`, `lib/brain/status.go`, `lib/brainsync/recipient.go`, `lib/brainsync/leave.go` (or a struct field if the function hangs off a struct). Update internal callers to pass it through.
- [ ] **Step 2 — TUI:** add `gh gh.Client` to `rootModel` and to each submodel struct that calls `gh.*` (`listModel`, `acceptInviteModel`, `inviteCollabModel`, and any others surfaced by `grep -n 'gh\.' lib/tui/brain/*.go`). Thread it: `NewRoot(c gh.Client)` → store on `rootModel` → pass to every `newXModel(c, …)` constructor and into the `tea.Cmd` closures that today call `gh.*`. **Non-mechanical part:** those `tea.Cmd` closures currently capture nothing; each must now close over the injected `c` — take care that every closure gets the client from its model, not a package symbol.
- [ ] **Step 2b — fix broken TUI tests:** the `NewRoot()` signature change breaks **both** `lib/tui/brain/root_test.go` (5 call sites) and `lib/tui/brain/detail_test.go:131`. Update them to `NewRoot(gh.NewFake(gh.Conf{CloneURLBase: "file://" + t.TempDir() + "/"}))`. (These become the first consumers of the fake — a good smoke check that DI + fake compose.)
- [ ] **Step 3 — cmd entrypoints:** construct the real client once per command — `c := gh.New(gh.Conf{})` — and pass it into the brain/brainsync calls and into `braintui.NewRoot(c)` (`brain_tui.go:12`).
- [ ] **Step 4:** `grep -rn 'gh\.\(AuthLogin\|UserExists\|CollaboratorPermission\|ListCollaborators\|AddCollaborator\|InviteCollaborator\|RemoveCollaborator\|RepoPendingInvitations\|DeleteRepoInvitation\|PendingInvitations\|AcceptInvitation\|DeclineInvitation\|UserRepos\|CloneSSHURL\)' --include='*.go' .` → expect **zero** non-test hits (all now go through a `Client`).
- [ ] **Step 5:** `go build ./... && go vet ./... && go test ./...` → expect PASS.
- [ ] **Step 6: Commit** `#42 M1: gh: migrate 13 consumers onto the Client port (constructor DI)`.

### Task 1.6: Close M1

- [ ] `sdlc milestone-close --issue 42 --milestone M1` (auto-dispatches the fresh-eyes boundary review over the branch point→HEAD window; fix Critical/Important before crossing). Log the `Review-Verdict:` outcome in `## Log`.

---

## Chunk 2: M2 — Fake adapter

**Outcome:** `gh.NewFake(Conf) Client` returns a stateful in-memory adapter with seeding + multi-user + the three peculiarities + tmpdir bare-repo coupling, covered by colocated unit tests. No consumer changes.

### Task 2.1: `fakeState` + `fakeClient` skeleton

**Files:**
- Create: `lib/gh/fake.go`
- Test: `lib/gh/fake_test.go`

- [ ] **Step 1: Write `fakeState`, `fakeClient`, `NewFake`, and seeding helpers:**

```go
type fakeUser struct {
	login    string
	token    string
	shadowed bool // /users/<login> 404s while the token still works
}

type fakeRepo struct {
	owner, name   string
	private       bool
	topics        []string
	collaborators map[string]string // login -> permission
	barePath      string            // tmpdir bare repo (data plane)
}

type fakeInvitation struct {
	id       int
	owner    string
	repo     string
	invitee  string
	inviter  string
	accepted bool
}

type fakeState struct {
	tmpdir      string
	users       map[string]*fakeUser
	repos       map[string]*fakeRepo // key "owner/name"
	invitations []*fakeInvitation
	nextInvID   int
}

type fakeClient struct {
	st           *fakeState
	currentToken string
	base         string
}

// NewFake returns an in-memory Client. Conf.CloneURLBase MUST be a
// caller-owned file://<dir>/ base (use t.TempDir() so Go cleans it up —
// the fake never creates its own tmpdir, since NewFake(Conf) has no
// *testing.T and cannot register cleanup; per-test teardown is the
// caller's t.TempDir()). The dir under that base holds the bare repos.
func NewFake(conf Conf) Client {
	base := conf.cloneBase() // "file://<dir>/"; reuse the same base for git init --bare paths
	return &fakeClient{st: newFakeState(base), currentToken: "", base: base}
}

func (c *fakeClient) CloneURL(fullName, sshURL string) string {
	return cloneURL(c.base, fullName, sshURL) // peculiarity preserved: sshURL is "" for invitations
}

// --- test seeding API (concrete type, off-interface) ---
// Tests construct via NewFake(...).(*fakeClient) to reach these.
func (c *fakeClient) AddUser(login string) (token string)   // registers user; returns its bearer token
func (c *fakeClient) SwitchUser(login string)               // sets currentToken to login's token
func (c *fakeClient) ShadowUser(login string, shadowed bool)
func (c *fakeClient) CreateRepo(owner, name string, private bool) // git init --bare at <base dir>/owner/name.git
func (c *fakeClient) FailListInvitations(fail bool)              // fault injection: RepoPendingInvitations errors (nous#41 #11 hard-error path)

// AsUser returns a SECOND *fakeClient sharing the SAME *fakeState but
// bound to login's token. This is how the contract test gets an
// operator client and an invitee client over one shared world (the
// real backend gets two clients with two GH_TOKENs — same shape).
func (c *fakeClient) AsUser(login string) *fakeClient {
	return &fakeClient{st: c.st, currentToken: c.st.tokenFor(login), base: c.base}
}
```

- [ ] **Step 2:** `go build ./lib/gh/` → PASS. **Commit** `#42 M2: gh: fake state model + seeding skeleton`.

### Task 2.2: Implement `Client` methods on the fake (TDD, peculiarities first)

For each behavior: write the failing test, run it (FAIL), implement, run (PASS), commit. Key tests:

- [ ] **Invite → pending → accept → collaborator (happy path, multi-user):**

```go
func TestFake_InviteAcceptFlow(t *testing.T) {
	c := NewFake(Conf{}).(*fakeClient)
	op := c.AddUser("op"); c.AddUser("ying")
	c.SwitchUser("op")
	c.CreateRepo("op", "brain", true)
	if err := c.AddCollaborator("op", "brain", "ying", "push"); err != nil { t.Fatal(err) }

	c.SwitchUser("ying")
	invs, _ := c.PendingInvitations()
	if len(invs) != 1 { t.Fatalf("want 1 pending invite, got %d", len(invs)) }
	// PECULIARITY: MinimalRepository omits ssh_url (bug 2 surface)
	if invs[0].Repository.SSHURL != "" { t.Fatalf("fake must return empty ssh_url") }
	if err := c.AcceptInvitation(invs[0].ID); err != nil { t.Fatal(err) }

	c.SwitchUser("op")
	cols, _ := c.ListCollaborators("op", "brain")
	if !slices.Contains(cols, "ying") { t.Fatalf("ying should be a collaborator") }
	_ = op
}
```

- [ ] **Peculiarity — `AddCollaborator` no-ops against an existing invitation** (nous#41 #11 surface): a second `AddCollaborator` for a login with a pending invite must NOT create a second invite and must NOT error; `InviteCollaborator` must delete-then-readd so it *does* re-send (assert `ReplacedStale == true`).
- [ ] **Peculiarity — `UserExists` shadow flag** (bug-from-repro): `ShadowUser("ying", true)` → `UserExists("ying")` returns `ErrUserNotVisible`; but `SwitchUser("ying"); AuthLogin()` still returns `"ying"`.
- [ ] **`CloneURL` resolves to tmpdir:** `c.CloneURL("op/brain", "")` returns `file://<tmpdir>/op/brain.git`, and `git clone` of it into another tmpdir succeeds against the seeded bare repo.
- [ ] **Remaining methods** (`CollaboratorPermission`, `RepoPendingInvitations`, `DeleteRepoInvitation`, `DeclineInvitation`, `RemoveCollaborator`, `UserRepos`): one test each against seeded state.
- [ ] **Commit after each** (`#42 M2: gh: fake <behavior>`).

### Task 2.3: Close M2

- [ ] `go test ./lib/gh/ -v` → PASS. `sdlc milestone-close --issue 42 --milestone M2`; log verdict.

---

## Chunk 3: M3 — Dual-backend contract test (grounding)

**Outcome:** one contract suite of port-behavior assertions runs against the fake (always, CI) and, build-tagged `conformance`, against real `gh`; the real backend is run once to certify the fake and the cadence is documented.

### Task 3.1: Shared contract suite

**Files:**
- Create: `lib/gh/contract_test.go` (no build tag — runs the fake)
- Create: `lib/gh/contract_real_test.go` (`//go:build conformance`)

- [ ] **Step 1: Define the world the suite operates on.** The suite is parameterized by a factory that hands back two clients over one shared world plus the identifiers, so fake and real are exercised identically:

```go
// contractWorld is what each backend must provision: an operator client
// and an invitee client over the SAME underlying state, plus the repo
// coordinates and the invitee's login. The fake builds this in-memory;
// the real backend builds it from two GH_TOKENs + a disposable repo.
type contractWorld struct {
	operator, invitee   Client
	owner, repo, invitee_login string
}

// runContract asserts the port-behavior invariants that must hold for
// ANY Client. newWorld is called fresh per subtest for isolation.
func runContract(t *testing.T, newWorld func(t *testing.T) contractWorld) {
	t.Run("invite_then_pending_then_accept_then_collaborator", func(t *testing.T) {
		w := newWorld(t)
		if err := w.operator.AddCollaborator(w.owner, w.repo, w.invitee_login, "push"); err != nil {
			t.Fatalf("AddCollaborator: %v", err)
		}
		invs, err := w.invitee.PendingInvitations()
		if err != nil { t.Fatalf("PendingInvitations: %v", err) }
		if len(invs) != 1 { t.Fatalf("want 1 pending invite, got %d", len(invs)) }
		if err := w.invitee.AcceptInvitation(invs[0].ID); err != nil { t.Fatalf("Accept: %v", err) }
		cols, err := w.operator.ListCollaborators(w.owner, w.repo)
		if err != nil { t.Fatalf("ListCollaborators: %v", err) }
		if !slices.Contains(cols, w.invitee_login) {
			t.Fatalf("invitee %q not a collaborator after accept; got %v", w.invitee_login, cols)
		}
	})

	t.Run("invitation_repo_omits_ssh_url", func(t *testing.T) {
		w := newWorld(t)
		_ = w.operator.AddCollaborator(w.owner, w.repo, w.invitee_login, "push")
		invs, _ := w.invitee.PendingInvitations()
		// MinimalRepository invariant: /user/repository_invitations omits ssh_url.
		if len(invs) == 1 && invs[0].Repository.SSHURL != "" {
			t.Fatalf("invitation must carry empty ssh_url (MinimalRepository); got %q", invs[0].Repository.SSHURL)
		}
	})

	t.Run("add_collaborator_noops_against_existing_invitation", func(t *testing.T) {
		w := newWorld(t)
		_ = w.operator.AddCollaborator(w.owner, w.repo, w.invitee_login, "push")
		_ = w.operator.AddCollaborator(w.owner, w.repo, w.invitee_login, "push") // 2nd: must no-op
		pend, _ := w.operator.RepoPendingInvitations(w.owner, w.repo)
		n := 0
		for _, p := range pend { if strings.EqualFold(p.Invitee.Login, w.invitee_login) { n++ } }
		if n != 1 { t.Fatalf("want exactly 1 pending invite after double-add (no-op), got %d", n) }
	})
	// Add one subtest per invariant the fake claims to model; each is
	// written ONCE here and runs against both backends.
}
```

- [ ] **Step 2: Fake backend** (`contract_test.go`, always on) — uses `AsUser` to get two clients over one `fakeState`:

```go
func TestContract_Fake(t *testing.T) {
	runContract(t, func(t *testing.T) contractWorld {
		op := NewFake(Conf{CloneURLBase: "file://" + t.TempDir() + "/"}).(*fakeClient)
		op.AddUser("op"); op.AddUser("ying")
		op.SwitchUser("op")
		op.CreateRepo("op", "brain", true)
		return contractWorld{operator: op, invitee: op.AsUser("ying"), owner: "op", repo: "brain", invitee_login: "ying"}
	})
}
```

- [ ] **Step 3: Real backend** (`contract_real_test.go`, `//go:build conformance`): the factory reads two pre-provisioned test-account tokens from env (`GH_TOKEN_OP`, `GH_TOKEN_YING`; `t.Skip` if absent), builds `operator := New(Conf{})` / `invitee := New(Conf{})` each invoked with the matching `GH_TOKEN` (set per-call via the adapter's env or a `Conf.Token` field — add one if needed), against a disposable private repo created in `newWorld` and deleted in `t.Cleanup`. Cleanup also removes any leftover collaborators/invitations so re-runs start clean.
- [ ] **Step 4:** `go test ./lib/gh/ -run Contract_Fake -v` → PASS.
- [ ] **Step 5: Commit** `#42 M3: gh: dual-backend contract suite (fake + build-tagged real)`.

### Task 3.2: Certify + document cadence

- [ ] **Step 1:** Run the real backend once to certify: `GH_TOKEN_OP=… GH_TOKEN_YING=… go test -tags conformance ./lib/gh/ -run Contract_Real -v`. Record the result + date in `## Log`.
- [ ] **Step 2:** Document the grounding cadence (run `-tags conformance` ~monthly / on suspected drift) in `atlas/` (see M4 atlas task) and a short comment header in `contract_real_test.go`.
- [ ] **Step 3:** `sdlc milestone-close --issue 42 --milestone M3`; log verdict.

> If the real run reveals the fake diverges from GitHub, fix the fake (not the test) and re-certify — that is the grounding loop doing its job.

---

## Chunk 4: M4 — Hermetic regression tests + atlas

**Outcome:** the nous#26 / nous#41 control-plane flows run hermetically through the fake + tmpdir data plane, each pinning a historical bug; atlas updated.

### Task 4.1: Regression tests — pin each bug at the layer that can see it

**Bug coverage map (verified against the tree — do not re-pin what's already pinned):**

| Bug | Layer | Where it's pinned | Action in M4 |
|-----|-------|-------------------|--------------|
| 1 (wrong endpoint) | below the seam | `lib/gh/real_test.go` (M1) + contract real-run (M3) | already done in M1/M3 |
| 2 (MinimalRepository empty ssh_url) | control plane (consumer) | **new**, through the fake | write |
| 3 (accepted-but-unpublished recovery) | control plane (consumer) | **new**, through the fake | write |
| nous#41 #11 (re-invite re-sends) | control plane (consumer) | **new**, through the fake | write |
| 4 (discovery filter) | data plane / brain-logic | **already** `lib/brainsync/discovery_test.go`, `lib/brain/manifest_test.go` | verify green; do NOT re-pin |
| 5 (operator pubkey on keys branch) | data plane / brain-logic | **already** `lib/brain/integration_test.go` (`TestPublishOwnPubkeyToRemote_OrphanCreate` + the republish-path tests) | verify green; do NOT re-pin |

Bugs 4 & 5 are brain-logic/data-plane bugs that already have regression homes using `file://` bare repos and never route through `gh` (`PublishOwnPubkeyToRemote` takes a `cloneURL` and execs git directly; `DiscoverAll` is filesystem-only). The gh fake does **not** newly pin them — claiming it does would be the over-reach the spec review caught. M4's *new* contribution is the three control-plane bugs (2, 3, #41 #11), plus optionally lifting `TestEndToEnd_GitHubMediatedOnboarding` to run its control-plane half through the fake (stretch).

**Files (new control-plane tests):**
- Create: `lib/gh/regression_test.go` for bug 2's clone-URL behavior and bug #41 #11's re-invite behavior (pure control-plane, fake-only — fastest home).
- Modify: `lib/brain/integration_test.go` — extend/clone `TestEndToEnd_GitHubMediatedOnboarding` (~line 516) to drive the invite→accept control plane through `gh.NewFake(...)` (it today exercises only the `file://` data plane), giving bugs 2 & 3 an end-to-end home. The fake's `CloneURL` (tmpdir base) is the seam that lets the existing `file://` setup and the fake's invitations meet.

- [ ] **Bug 2 — MinimalRepository empty ssh_url (fake-level + e2e):** in `regression_test.go`, seed an invitation, assert `c.CloneURL(inv.Repository.FullName, inv.Repository.SSHURL)` (with `SSHURL==""`) yields the tmpdir `file://…/owner/repo.git` and a real `git clone` of it succeeds — the pre-fix path fabricated/passed an empty string and failed. In `integration_test.go`, assert the join flow clones via `c.CloneURL(...)` not a hardcoded URL.
- [ ] **Bug 3 — accepted-but-unpublished recovery (e2e):** in the extended `integration_test.go` flow, accept the invite through the fake, force the gcrypt push to fail (data plane), assert the documented recovery path runs (not a stuck collaborator-but-unpublished state).
- [ ] **nous#41 #11 — re-invite re-sends (fake-level):** in `regression_test.go`, with a pending invite present, `InviteCollaborator` deletes-then-readds (`ReplacedStale==true`); and assert that when the underlying list/delete fails, `InviteCollaborator` returns a hard error (not swallowed) — inject the failure via a fake knob (e.g. `fakeClient.FailListInvitations(true)`).
- [ ] **Verify bugs 4 & 5 still green** (`-run` is global, so invoke per-package with exact names):
  - bug 4: `go test ./lib/brainsync/ -run 'TestFindSharedBrains' -v` (pinpoint: `TestFindSharedBrains_SingleRecipientWithGcryptRemote`) **and** `go test ./lib/brain/ -run 'TestDiscoverAll' -v`
  - bug 5: `go test ./lib/brain/ -run 'TestEndToEnd_OperatorPubkeyMissingThenRepublish|TestPublishOwnPubkeyToRemote_OrphanCreate' -v`
  - Both → PASS. Record in `## Log` that bugs 4/5 remain pinned by these existing data-plane tests (no new test needed).
- [ ] **Commit after each** (`#42 M4: regression: <bug>`).

> Note: bug 1 is NOT here — it is pinned by `real_test.go` (M1) + the contract real-run (M3). Do not attempt to reproduce a below-the-seam endpoint bug through the fake.

### Task 4.2: Atlas + close

- [ ] **Step 1:** Update `atlas/` for the new `lib/gh` port/adapter/fake surface and the grounding cadence; ensure `atlas/index.md` links any new file. (ARCH note: cite ARCH-PURE — pure `fakeState`/`cloneURL` core + thin `realClient` IO seam — and ARCH-DRY — single `cloneURL`, single contract suite over two backends — in the `## Log`.)
- [ ] **Step 2:** `go test ./...` → PASS. `sdlc close --issue 42 --actual <h> --verified '<evidence: fake+contract+regression green; real contract certified YYYY-MM-DD>'`.
- [ ] **Step 3:** This unblocks ariadne#71's `deps`; note in nous `## Log` that the gh instance (instance 1 of the pattern) is complete.

---

## Notes for the executor

- **ARCH-PURE:** keep logic in `fakeState`/`cloneURL` (pure, unit-tested without mocks); confine IO to `realClient` (exec gh) and the fake's one-time `git init --bare`. If a "pure" test needs `runner` stubbing, that code belongs in `real.go`, not the pure core.
- **ARCH-DRY:** `cloneURL` is the single clone-resolver (don't reintroduce per-type `CloneSSHURL`); the contract suite is one body run against two backends (don't fork fake/real assertions).
- **Don't touch the `gh api` argument strings** in the real adapter during M1 — that fidelity is the contract test's to certify, and changing it silently breaks the bug-1 guard.
- **Frequent commits**, one per TDD cycle. Each `Mx` row is its own `sdlc milestone-close` boundary.

## Revisions

### 2026-06-05 — M1 boundary review (FIX-THEN-SHIP, resolved)

- **Plan bug fixed:** Task 1.5 Step 2b said to retrofit the broken TUI tests to `NewRoot(gh.NewFake(...))`, but `NewFake` is an M2 deliverable — chicken-and-egg. M1 correctly used `gh.New(gh.Conf{})` (real client, never invoked by the nav tests). **Action moved to M2:** once `fake.go` lands, retrofit `lib/tui/brain/root_test.go` + `detail_test.go` to inject the fake for true isolation, at the same `gh.New(gh.Conf{})` sites (the scattered composition root the review flagged).
- **Important finding addressed in-window:** the bug-1 below-seam guard now covers **all 13** endpoints (`TestRealClient_Endpoints` table + `TestRealClient_InviteCollaborator_ClearsStaleThenAdds`), not just the original 2 — a future endpoint-string regression fails fast instead of waiting for the monthly M3 conformance run.

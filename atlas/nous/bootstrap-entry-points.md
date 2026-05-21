# Bootstrap entry points

Four `make` targets cooperate to take a fresh Mac to a working nous + brain stack. Each owns one slice; running them in the right order matters.

## The four targets

```
nous/Makefile.nous → make bootstrap         (alias: make nous-bootstrap, deprecated)
nous/Makefile.nous → make identity
nous/Makefile.nous → make new-brain [target-path]
nous/.openshell/Makefile → make sandbox-bootstrap   (called transitively from bootstrap)
```

(`make nous-bootstrap` stays as a backward-compat alias for
operators who still type the old name. `make sandbox-bootstrap`
was previously `make bootstrap` until nous wanted that name —
ariadne renamed in early 2026-05-20.)

## Order on a fresh Mac

```
git clone https://github.com/xianxu/nous && cd nous
make bootstrap              # 1. installs everything; calls identity + .openshell sandbox-bootstrap
nous brain new ../brain     # 2. provisions an encrypted brain repo
```

Two commands cover the cold start. The other targets are usually invoked by `bootstrap` rather than by hand.

## Per-target responsibility

### `make bootstrap` — the umbrella

Runs ten idempotent steps:

1. **Xcode CLT** — `xcode-select --install` if missing; polls for completion (up to 20 min).
2. **Homebrew** — official installer if missing.
3. **Brewfile** — `brew bundle install --file=Brewfile`. The package set is the source of truth for what nous expects on a Mac.
4. **GPG identity** — delegates to `scripts/identity.sh`. Skippable via `NOUS_BOOTSTRAP_SKIP_IDENTITY=1`.
5. **Workflow tools** — delegates to `.openshell/Makefile`'s `sandbox-bootstrap` (`gh auth login`, openshell CLI, mutagen tap). Skippable via `NOUS_BOOTSTRAP_SKIP_OPENSHELL=1`.
6. **GitHub SSH key** — generates `~/.ssh/id_ed25519` (prompting for passphrase) if missing; registers via `gh ssh-key add` if not already on the account; runs `ssh-add --apple-use-keychain` to cache the passphrase in macOS Keychain; appends `UseKeychain yes`/`AddKeysToAgent yes` to `~/.ssh/config`. Required for `gcrypt::ssh://...` brain remotes; the Keychain hookup is what lets the brain-sync daemon push without prompting.
7. **fzf shell hook** — `fzf --update-rc`.
8. **Verify go on PATH.**
9. **Build the nous binary** — `make nous-build` produces `nous/bin/nous`. Skippable via `NOUS_BOOTSTRAP_SKIP_BUILD=1`. Future (nous#28): fetch a signed prebuilt binary into `nous/bin/nous` so end users don't need a Go toolchain at all.
10. **Add nous/bin to PATH + start the nous service** — appends `export PATH="$NOUS_DIR/bin:$PATH"` to `~/.zshrc` or `~/.bash_profile` (idempotent — skipped if already present); runs `nous service uninstall || true; nous service install; nous service status`. Skippable via `NOUS_BOOTSTRAP_SKIP_SERVICE=1`. The binary stays at the canonical `nous/bin/nous` location — single source of truth. No copy to `~/.local/bin`; PATH wiring handles the "type `nous`" UX.

After step 10, the operator has a running `com.42shots.nous` launchd service, `nous` on PATH, and is ready to `nous brain` to create their first brain.

Re-running on a complete machine is a no-op. Spec: `workshop/issues/000011-nous-bootstrap.md`.

### `make identity` — the GPG layer

Standalone; idempotent. Generates an RSA-4096 keypair if none exists; configures `~/.gnupg/gpg-agent.conf` (auto-detects SSH context → `pinentry-curses`, otherwise `pinentry-mac`); appends `export GPG_TTY=$(tty)` to `~/.zshrc` for terminal-pinentry contexts.

Inputs: `IDENTITY_NAME`, `IDENTITY_EMAIL`, `IDENTITY_EXPIRY` env vars override interactive prompts. `IDENTITY_PINENTRY` overrides pinentry binary selection.

Recovery flow: if the user is bringing an existing GPG key from another machine, identity.sh prints an escape-hatch message before generating fresh — `Ctrl+C, gpg --import path/to/key.asc, re-run`. Imported keys are detected on subsequent runs and generation is skipped.

Spec: `nous#3` M1 step 0; threat model `brain/atlas/threat-model-shared-brain.md`.

### `make new-brain [path]` — provision a fresh brain

Interactive end-to-end. Prompts for GitHub owner+repo (creates the repo via `gh repo create --private`; if the repo already exists with content, prompts to delete + recreate — force-push can't recover from gcrypt state encrypted to a different GPG key), GPG identity, target path. Performs `git init`, runs `nous/setup.sh --all --yes` to symlink `construct/`, authors `.brain/config.md`, configures the `gcrypt::ssh://...` remote, pushes the initial encrypted commit.

Output: a working private brain at `<path>` with full encryption-at-rest. The first plaintext commit lives only on the local clone; only ciphertext touches GitHub.

Prereqs: `make identity` (GPG keypair); `gh auth login` (HTTPS); a GitHub SSH key registered (`make bootstrap` step 6).

### `.openshell/Makefile` `sandbox-bootstrap` — workflow tools

Vendored from NVIDIA/OpenShell (via ariadne). Installs `gh`, `mutagen`, the `openshell` CLI, runs `gh auth login`. Called transitively by `make bootstrap` step 5; can also run standalone via `make sandbox-bootstrap` from the nous root (which is wired via `-include .openshell/Makefile`).

Was named `bootstrap` until 2026-05-20; renamed to `sandbox-bootstrap` so nous could claim `make bootstrap` as its end-user umbrella. The other openshell verbs (sandbox, sandbox-build, sandbox-clean, etc.) already use the `sandbox-` prefix; this brings naming in line.

## Test infrastructure

Three additional make targets validate the bootstrap path automatically:

- `make nous-test-bootstrap` — full cold-start in a fresh tart `tahoe-base` clone via SSH. ~2:30. Regression test for the bootstrap-script-itself; run when Brewfile or `nous-bootstrap.sh` changes.
- `make nous-test-snapshot` — one-time build of `tahoe-bootstrapped` (a stopped VM with bootstrap pre-applied). Refresh when the Brewfile changes.
- `make nous-test-roundtrip` — fast iteration test against the snapshot. ~25s. Validates the GPG round-trip without paying for the Brewfile install.

Spec: `workshop/issues/000011-nous-bootstrap.md` M2/M3.

## Dependencies between targets

```
nous-bootstrap
  ├── (xcode-select, brew, brew bundle from Brewfile)
  ├── identity                       (skippable; recovery path)
  ├── .openshell bootstrap           (skippable; gh auth login)
  ├── github SSH key generation + gh ssh-key add
  └── fzf hook

new-brain
  ├── needs identity (GPG keypair on file)
  ├── needs .openshell bootstrap's gh auth (for gh repo create)
  └── needs github SSH key (for gcrypt::ssh push)
```

Running `make nous-bootstrap` once on a fresh machine satisfies every prereq for `make new-brain`. The skippable env-var gates exist for the test harness, not for end users.

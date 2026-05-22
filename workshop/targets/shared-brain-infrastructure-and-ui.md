---
type: target
slug: shared-brain-infrastructure-and-ui
status: active
created: 2026-05-22
updated: 2026-05-22
sources:
  - /Users/xianxu/workspace/ariadne/docs/vision/2026-05-22-01-pensive-durable-target-pattern.md
  - /Users/xianxu/workspace/brain/docs/vision/2026-05-22-01-pensive-workbench-bet.md
---

# Target: shared brain infrastructure and user interface

A `brain` is a git repo containing `.brain/config.md`; `nous` is the binary + ecosystem of glue (skills, conventions, scripts) that operates on brains. Until now brains have been single-recipient (private). Now it's a good time to introduce shared brains, where multiple collaborators read and write a common workbench. The default substrate is git for synchronization + gcrypt for encryption + GitHub as the remote, though the manifest schema also allows other substrates (`syncthing`, `git-daemon`, `none`) for future use. The key issues we want to solve are:

1. How do we synchronize the shared brain among several collaborators securely? 
    * Our answer is to use gcrypt to encrypt data and use GitHub as the substrate for synchronization. Specifically, each shared brain will have two branches, a `main` branch of encrypted data; and a `keys` branch of public keys of participants.
2. How do we resolve conflict when multiple collaborators' edit conflict with each other. 
    * Our answer is semantic merge, essentially relying on agents to rewrite files in conflict. 

The canonical example we use is I, as Xian, want to work with my wife (Ying), on a travel plan this summer to Europe. We need a place where both of us, and our agents can change shared state, thus shared brain.

From end user's POV, the current nous deployment works as:

1. User clones `nous` repo and runs `make bootstrap`. This gets the `nous` ecosystem into an operational state.
    * Today only one mode exists: build from source via `make nous-build`. A future release mode that downloads a pre-compiled signed-and-notarized binary is planned (tracked in nous#28); for now everyone compiles locally. The locally-built binary is signed (via `make nous-install`, not by `make nous-build` itself) but not notarized — Gatekeeper accepts a Developer-ID-signed binary from the same Apple ID on the operator's own Mac. Notarization is reserved for cross-Mac distribution (`make nous-install-notarized`).
    * Bootstrap also starts up the local `nous` service, which provides two key functions: a credential proxy (the `charon` lineage) as a gateway to external services; and brain-sync to facilitate synchronization using GitHub as substrate.
2. Then, user use `nous brain` TUI to operate their brains, for example, they can:
    1. create new private brain.
    2. invite collaborators to a brain (private or already shared), using their GitHub user name. 
    3. accept invitation to a shared brain and clone it locally. 
    4. leave a shared repo.

That's it. Once a brain is shared — or is single-recipient but has a gcrypt-backed GitHub remote configured (the "provisioned for sharing, not yet admitted any collaborator" interim state) — the `nous` service watches the git repo directory and:

1. auto-commits with a debounced timer (currently 5 seconds of idle since the last file change).
2. auto-pushes with a debounced timer (currently 60 seconds of idle since the last commit or file change).
3. auto-pulls on a periodic ticker (currently every 5 seconds; pull happens only when the working tree has no modified-tracked files — untracked files are tolerated).

One thing to note is the two crypto passphrases, one guarding the GPG private key and one guarding the SSH key used between host and GitHub. The operator can store both in Keychain so they're supplied automatically. Currently we do not support removal of those passphrases from Keychain — a follow-up item.

- **SSH passphrase**: `make bootstrap` invokes `ssh-add --apple-use-keychain` for the standard SSH key path and writes `UseKeychain yes` + `AddKeysToAgent yes` into `~/.ssh/config` (Host *). After bootstrap the passphrase is in Keychain. A subsequent SSH key rotation (new key, new passphrase) requires re-running `ssh-add --apple-use-keychain <new-key>` once; bootstrap doesn't re-run automatically.
- **GPG passphrase**: Stored in Keychain via pinentry-mac's "Save in Keychain" checkbox on the first prompt. Today this requires pinentry-mac to be configured manually in `~/.gnupg/gpg-agent.conf` (`pinentry-program /opt/homebrew/bin/pinentry-mac`); a follow-up makes bootstrap do this automatically.


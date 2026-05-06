# gcrypt brain encryption — operational procedure

How to configure a brain repo for end-to-end encryption against GitHub (or any git host) using `git-remote-gcrypt`. Procedure-shaped; assumes the security rationale is already familiar — for that, read `brain/atlas/threat-model-shared-brain.md`.

## What gcrypt does

`git-remote-gcrypt` is a transparent encryption layer between your git operations and a remote. Configured against a remote, it:

- Encrypts every push (objects, refs, history graph) under a GPG recipient list before sending to the host. The host stores ciphertext.
- Decrypts every pull/clone using your GPG private key. Local working tree is plaintext as usual.
- Operates as a remote helper named `gcrypt::<actual-remote-url>` (e.g., `gcrypt::ssh://git@github.com:user/brain-private.git`). The `gcrypt::` prefix is what tells git to route through the helper.

What GitHub sees: a repo with a single binary blob per push, no readable filenames, no commit graph, no commit messages, no branch names. The repo size grows; that's about it.

## Single-GPG-scheme posture

Per the shared-brain threat model: every brain — private and shared — is encrypted to a GPG recipient list. Private brains have a one-element list (the user's own GPG public key). Shared brains have multiple. There's no separate symmetric passphrase for any brain; the unlock chain is uniform: GPG private key in `~/.gnupg/`, passphrase in macOS login Keychain, mediated by gpg-agent + pinentry-mac.

This means a "private" brain is operationally identical to a single-recipient shared brain. Setup procedure, push/pull, recovery — all the same.

## Provisioning a new gcrypt'd brain

Prerequisite: GPG identity configured per `make identity` (in `nous/`). Confirm with `gpg --list-secret-keys --keyid-format LONG`; you should see your `[SC]` primary plus `ssb` `[E]` encrypt subkey.

```sh
# 1. Create the GitHub repo (private, empty)
gh repo create <name> --private --description "encrypted brain repo"

# 2. Initialize the local checkout
mkdir ~/workspace/<name>
cd ~/workspace/<name>
git init -b main

# 3. Configure the gcrypt remote (note the gcrypt:: prefix)
git remote add origin gcrypt::ssh://git@github.com/<your-user>/<name>.git

# 4. Declare the GPG recipient list (single recipient = your fingerprint)
git config remote.origin.gcrypt-participants <your-gpg-fingerprint>

# 5. Author the .brain/config.md manifest per ariadne AGENTS.md §1
mkdir .brain
cat > .brain/config.md <<EOF
---
mode: private
name: <slug>
recipients: [<your-gpg-fingerprint>]
sync_substrate: none
---
EOF

# 6. Initial commit + push (gcrypt encrypts during push;
#    gpg-agent unlocks the key from Keychain transparently)
git add .
git commit -m "init: brain manifest"
git push -u origin main
```

After step 6, browse the repo on GitHub — contents should be opaque (a `gcrypt-{tags}` ref or similar, no readable file tree).

## Adding a recipient (shared brains, later)

When a shared brain admits a new collaborator:

```sh
# Add their GPG public key to your local keyring (one-time, however you got it)
gpg --import collaborator-public-key.asc

# Append their fingerprint to the participants list
git config --add remote.origin.gcrypt-participants <collaborator-fingerprint>

# Update the manifest's recipients: list to match
# (edit .brain/config.md, add the fingerprint to the recipients: array)

# Push — gcrypt re-encrypts the session key for both recipients
git commit -am "admit <collaborator-name>"
git push
```

Net effect: future pushes are decryptable by any participant. **History before this push is decryptable to the previous recipient list only** — the new recipient cannot read pre-admission content unless you re-encrypt history (rarely needed).

## Removing a recipient (revocation)

Per the threat model: structurally heavy. Remove the fingerprint from `gcrypt-participants` and the manifest's `recipients:`, then **rotate the encryption key** (most reliably done by re-keying the gcrypt remote — this is a destructive operation requiring re-push of all history). Treat the previously-admitted recipient as having a permanent copy of pre-revocation content.

For a personal-private brain (one recipient), revocation is moot — there's no one to revoke.

## Verifying opacity

After a push, browse the repo on GitHub. You should see:

- A few refs like `gcrypt-{tags}` and a single ref under `refs/gcrypt/` if your gcrypt config declares one — or nothing recognizable.
- No readable files; trying to view via the GitHub UI shows binary or fails to render.
- Commit graph: not visible; GitHub's commit list shows opaque commit hashes that don't match anything locally and have no readable messages.

If any of those don't hold, the repo isn't actually gcrypt'd — verify `git config --get remote.origin.url` shows the `gcrypt::` prefix and that you pushed via that remote.

## Recovery semantics

- **Lose the GPG private key**: lose the brain. The host's ciphertext is unrecoverable. This is why brain#10 (Apple-account hardening + iCloud Keychain promotion) matters — backup channel for the key.
- **Lose the GPG passphrase**: same as losing the key (the passphrase-encrypted private key file is unusable without it).
- **Lose Keychain entry only** (passphrase no longer cached): pinentry-mac prompts on next decrypt; type the passphrase from your password manager; tick "Save in Keychain" again.

## See also

- `brain/atlas/threat-model-shared-brain.md` — security posture and trust boundaries
- `brain/atlas/threat-model-shared-brain.md` *§ GPG key bootstrap* — cross-machine GPG key transfer
- `nous/scripts/identity.sh` (`make identity`) — initial GPG bootstrap
- ariadne `AGENTS.md` §1 — `.brain/config.md` manifest convention

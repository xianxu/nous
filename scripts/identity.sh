#!/usr/bin/env bash
# scripts/identity.sh — set up the user's GPG identity for brain encryption.
#
# Idempotent: re-running detects an existing key and skips generation.
# Configures gpg-agent + pinentry-mac so the GPG passphrase is fetched from
# macOS login Keychain on subsequent uses.
#
# Used by `make identity` (Makefile.nous).
#
# Inputs:
#   When env vars are set they're used directly. When unset, the script prompts
#   interactively, suggesting `git config user.name|email` as defaults the user
#   can accept or override. Non-interactive (no TTY) runs require all three env
#   vars set or the script bails with a clear message.
#
#   IDENTITY_NAME    Real name to embed in the key (prompts; suggests git config user.name)
#   IDENTITY_EMAIL   Email to embed in the key (prompts; suggests git config user.email)
#   IDENTITY_EXPIRY  Key expiry passed to `gpg --quick-generate-key` (prompts; default 5y)
#
# Spec: see nous#3 M1 Step 0; threat model `brain/atlas/threat-model-shared-brain.md`
# `## Privilege concentration` and `## GPG key bootstrap and passphrase storage`.

set -euo pipefail

# ── Colors ───────────────────────────────────────────────────────────────────
RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; CYAN=$'\033[0;36m'; RESET=$'\033[0m'
info() { printf "%s==>%s %s\n" "$CYAN" "$RESET" "$*" >&2; }
ok()   { printf "%s  [ok]%s %s\n" "$GREEN" "$RESET" "$*" >&2; }
warn() { printf "%s  [!]%s %s\n" "$YELLOW" "$RESET" "$*" >&2; }
die()  { printf "%serror:%s %s\n" "$RED" "$RESET" "$*" >&2; exit 1; }

# ── Inputs ───────────────────────────────────────────────────────────────────
# Env vars take priority. If unset, prompt interactively, with git config as
# the suggested default (but always confirmed by the user).
NAME="${IDENTITY_NAME:-}"
EMAIL="${IDENTITY_EMAIL:-}"
EXPIRY="${IDENTITY_EXPIRY:-}"

prompt_with_default() {
    local var_name="$1" prompt_label="$2" default="$3" answer
    if [ -n "${!var_name}" ]; then
        return 0  # already set via env
    fi
    if [ ! -t 0 ]; then
        die "IDENTITY_$var_name is not set and stdin is not a TTY (cannot prompt). Re-run with IDENTITY_$var_name=... or in an interactive shell."
    fi
    if [ -n "$default" ]; then
        read -rp "$prompt_label [$default]: " answer
        printf -v "$var_name" '%s' "${answer:-$default}"
    else
        read -rp "$prompt_label: " answer
        [ -n "$answer" ] || die "$var_name cannot be empty."
        printf -v "$var_name" '%s' "$answer"
    fi
}

# ── 1. Dependencies ──────────────────────────────────────────────────────────
info "Checking dependencies..."
command -v brew >/dev/null || die "Homebrew is required. Install from https://brew.sh"

needs=()
command -v gpg              >/dev/null || needs+=(gnupg)
command -v pinentry-mac     >/dev/null || needs+=(pinentry-mac)
command -v git-remote-gcrypt >/dev/null || needs+=(git-remote-gcrypt)
if [ ${#needs[@]} -gt 0 ]; then
    info "Installing via Homebrew: ${needs[*]}"
    brew install "${needs[@]}"
fi
ok "Dependencies present: gnupg, pinentry-mac, git-remote-gcrypt."

# ── 2. Configure gpg-agent.conf ──────────────────────────────────────────────
mkdir -p ~/.gnupg
chmod 700 ~/.gnupg
PINENTRY_PATH=$(command -v pinentry-mac)
if ! grep -q "^pinentry-program.*pinentry-mac" ~/.gnupg/gpg-agent.conf 2>/dev/null; then
    cat >> ~/.gnupg/gpg-agent.conf <<EOF
pinentry-program $PINENTRY_PATH
default-cache-ttl 600
max-cache-ttl 7200
EOF
    gpgconf --kill gpg-agent 2>/dev/null || true
    ok "Wrote ~/.gnupg/gpg-agent.conf with pinentry-mac."
else
    ok "~/.gnupg/gpg-agent.conf already configured for pinentry-mac."
fi

# ── 3. Existing key check ────────────────────────────────────────────────────
existing=$(gpg --list-secret-keys --keyid-format LONG 2>/dev/null | grep '^sec' || true)
if [ -n "$existing" ]; then
    warn "Existing GPG secret key(s) found. Skipping generation:"
    gpg --list-secret-keys --keyid-format LONG >&2
else
    # ── 4. Generate a new key ───────────────────────────────────────────────
    info "No GPG secret key found. Setting up a new key."
    info ""

    git_name=$(git config --global user.name  2>/dev/null || true)
    git_email=$(git config --global user.email 2>/dev/null || true)

    prompt_with_default NAME   "Real name to embed in the GPG key"  "$git_name"
    prompt_with_default EMAIL  "Email to embed in the GPG key"      "$git_email"
    prompt_with_default EXPIRY "Key expiry (e.g. 5y, 0 for none)"    "5y"

    echo
    info "Will generate: $NAME <$EMAIL> (brain encryption key)"
    info "Type: rsa4096    Expiry: $EXPIRY"
    if [ -t 0 ]; then
        read -rp "Proceed? [Y/n] " confirm
        if [[ "$confirm" =~ ^[Nn] ]]; then
            die "Aborted by user."
        fi
    fi

    info ""
    info "You will be prompted by pinentry-mac for a passphrase."
    info "Tick the 'Save in Keychain' checkbox so subsequent uses are non-interactive."
    info ""

    gpg --quick-generate-key "$NAME <$EMAIL> (brain encryption key)" rsa4096 sign "$EXPIRY"

    # --quick-generate-key with rsa4096 produces a primary key with [SC]
    # (Sign + Certify) capability only — no encrypt subkey. We need an
    # encrypt subkey for gcrypt to use as a recipient key. Add it explicitly.
    NEW_FP=$(gpg --list-secret-keys --with-colons | awk -F: '/^fpr:/ {print $10; exit}')
    info "Adding encrypt subkey (rsa4096) to $NEW_FP..."
    gpg --quick-add-key "$NEW_FP" rsa4096 encrypt "$EXPIRY"

    ok "Key generated with encrypt subkey."
fi

# ── 5. Print fingerprint + next steps ────────────────────────────────────────
FP=$(gpg --list-secret-keys --with-colons | awk -F: '/^fpr:/ {print $10; exit}')
echo
ok "GPG identity ready."
echo "  Fingerprint: $FP"
echo
echo "Next steps:"
echo "  1. Use this fingerprint in your brain's .brain/config.md:"
echo "       recipients: [$FP]"
echo "  2. Trigger pinentry-mac's 'Save in Keychain' prompt by running a test"
echo "     decrypt — the first decrypt of any gcrypt'd repo will do this."
echo "  3. (End-of-project, brain#10) Promote the key to iCloud Keychain so it"
echo "     syncs across your Apple devices."

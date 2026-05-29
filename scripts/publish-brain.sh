#!/usr/bin/env bash
# scripts/publish-brain.sh — publish an EXISTING local-only brain to GitHub.
#
# The "local → private" rung of the topology ladder (nous#33). Takes a
# brain created by `nous brain new` (git repo, go.mod, manifest, NO
# remote) and gives it a hosted encrypted backup:
#   1. create a private GitHub repo to host the encrypted form
#   2. wire the gcrypt remote, encrypted to the manifest's recipients
#   3. push --force --set-upstream — only gcrypt ciphertext touches GitHub
#
# Differs from new-brain.sh: that one bootstraps a brand-new repo (git
# init, go.mod, setup.sh, manifest, commit) AND publishes in one shot.
# This one operates on an already-scaffolded local brain — it does ONLY
# the GitHub half.
#
# DRY note (nous#33 M2): the gh-repo-create ceremony below is duplicated
# from new-brain.sh's step 3. The intended end state is a sourced helper
# both scripts share; deferred until both paths are verified against a
# real GitHub remote (this env has no gh auth / gpg secret key).
#
# Args:  $1 = path to an existing local brain
# Env:   NOUS_GCRYPT_PARTICIPANTS  (required) space-separated GPG fingerprints
#        NOUS_GH_OWNER             (optional) default: gh authenticated user
#        NOUS_GH_NAME              (optional) default: basename of brain dir
#        SKIP_REPO_CREATE          (optional) skip create + existence check
#        NOUS_QUIET                (optional) suppress the splash

set -euo pipefail

RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; CYAN=$'\033[0;36m'; RESET=$'\033[0m'
info() { printf "%s==>%s %s\n" "$CYAN" "$RESET" "$*" >&2; }
ok()   { printf "%s  [ok]%s %s\n" "$GREEN" "$RESET" "$*" >&2; }
warn() { printf "%s  [!]%s %s\n" "$YELLOW" "$RESET" "$*" >&2; }
die()  { printf "%serror:%s %s\n" "$RED" "$RESET" "$*" >&2; exit 1; }

# ── 0. Resolve + validate the brain ──────────────────────────────────────────
BRAIN="${1:-}"
[ -n "$BRAIN" ] || die "usage: publish-brain.sh <brain-path>"
[ -d "$BRAIN/.git" ] || die "$BRAIN is not a git repo."
[ -f "$BRAIN/.brain/config.md" ] || die "$BRAIN has no .brain/config.md — not a brain."
cd "$BRAIN"
BRAIN_ABS="$(pwd)"

# Guard: publish is local → private. A brain that already has a remote
# is past this rung; refuse rather than clobber its origin.
if git config --get remote.origin.url >/dev/null 2>&1; then
    die "$BRAIN_ABS already has remote.origin.url — it's already published. (Use \`nous brain invite\` to share it.)"
fi

PARTICIPANTS="${NOUS_GCRYPT_PARTICIPANTS:-}"
[ -n "$PARTICIPANTS" ] || die "NOUS_GCRYPT_PARTICIPANTS not set (the recipient fingerprint list to encrypt to)."

# ── 1. Validate environment ──────────────────────────────────────────────────
command -v git >/dev/null 2>&1 || die "git not installed."
command -v gh  >/dev/null 2>&1 || die "GitHub CLI 'gh' not installed. brew install gh"
command -v gpg >/dev/null 2>&1 || die "gpg not installed."
command -v git-remote-gcrypt >/dev/null 2>&1 || die "git-remote-gcrypt not installed."
gh auth status >/dev/null 2>&1 || die "gh is not authenticated. Run: gh auth login"

TARGET_NAME=$(basename "$BRAIN_ABS")

[ "${NOUS_QUIET:-0}" = 1 ] || {
    printf "\n%spublish-brain%s — backing up a local brain to GitHub (encrypted).\n" "$CYAN" "$RESET" >&2
    printf "  Only gcrypt ciphertext touches GitHub. You may be prompted for your\n" >&2
    printf "  GPG passphrase (to sign/encrypt the push) and SSH passphrase (to\n" >&2
    printf "  authenticate to GitHub).\n\n" >&2
}

# ── 2. GitHub owner + repo name ──────────────────────────────────────────────
DEFAULT_OWNER=$(gh api user --jq .login 2>/dev/null) || die "Could not read gh authenticated user."
GH_OWNER="${NOUS_GH_OWNER:-$DEFAULT_OWNER}"
GH_NAME="${NOUS_GH_NAME:-$TARGET_NAME}"
GH_FULL="$GH_OWNER/$GH_NAME"

# ── 3. Create or recreate the GH repo ────────────────────────────────────────
# (Adapted from new-brain.sh step 3 — see that script for the rationale
# behind delete+recreate over force-push, and the fresh-account /users
# propagation lag handling.)
create_repo() {
    gh repo create "$GH_FULL" --private \
        --description "nous-brain: $TARGET_NAME (gcrypt-encrypted)" \
        --disable-issues --disable-wiki >/dev/null
    gh api -X PUT "repos/$GH_FULL/topics" -f 'names[]=nous-brain' --silent 2>/dev/null || \
        warn "Topic set failed (nous brain will still discover this brain via the description marker)."
}

SKIP_REPO_CREATE="${SKIP_REPO_CREATE:-0}"
if [ "$SKIP_REPO_CREATE" = "1" ]; then
    warn "SKIP_REPO_CREATE=1 — skipping GitHub repo creation/verification."
    warn "  Assuming $GH_FULL exists and is empty. Push will fail if not."
elif gh api "repos/$GH_FULL" --silent >/dev/null 2>&1; then
    BRANCH_COUNT=$(gh api "repos/$GH_FULL/branches" --jq 'length' 2>/dev/null || echo 0)
    if [ "${BRANCH_COUNT:-0}" -eq 0 ]; then
        ok "$GH_FULL exists but is empty — using it."
    else
        warn "GitHub repo $GH_FULL already exists with content ($BRANCH_COUNT branch(es))."
        warn "If that content is gcrypt state from a different GPG key, push will"
        warn "fail at manifest decrypt — force-push can't recover. The only safe"
        warn "path is to delete the repo and recreate it fresh."
        TOKEN_SCOPES=$(gh auth status 2>&1 | sed -n "s/.*Token scopes: //p" | tr -d "'" || true)
        if ! echo "$TOKEN_SCOPES" | grep -qw delete_repo; then
            warn ""
            warn "  Note: deleting a repo via gh requires the 'delete_repo' scope."
            warn "  Your current scopes: ${TOKEN_SCOPES:-(unknown)}"
            warn "  To add it:           gh auth refresh -h github.com -s delete_repo"
            warn "  Or delete manually:  https://github.com/$GH_FULL/settings → 'Delete this repository'"
            warn ""
        fi
        [ -t 0 ] || die "$GH_FULL has content and stdin is not a TTY (cannot prompt)."
        read -rp "Delete $GH_FULL on GitHub and recreate it empty? [y/N] " ans
        [[ "$ans" =~ ^[Yy] ]] || die "Aborted. Pick a different repo name (NOUS_GH_NAME=...), or delete it manually first."
        info "Deleting $GH_FULL ..."
        if ! gh repo delete "$GH_FULL" --yes >/dev/null 2>&1; then
            die "gh repo delete failed — most likely missing 'delete_repo' scope. Run: gh auth refresh -h github.com -s delete_repo  (or delete manually at https://github.com/$GH_FULL/settings)"
        fi
        ok "Deleted https://github.com/$GH_FULL"
        info "Recreating $GH_FULL (private)..."
        create_repo
        ok "Recreated https://github.com/$GH_FULL"
    fi
else
    info "Creating GitHub repo $GH_FULL (private, no issues, no wiki)..."
    create_err=$(mktemp -t publish-brain-create.XXXXXX)
    trap 'rm -f "$create_err"' EXIT
    if ! create_repo 2>"$create_err"; then
        if grep -q "users/$GH_OWNER" "$create_err" 2>/dev/null \
           && [ "$(gh api user --jq .login 2>/dev/null)" = "$GH_OWNER" ]; then
            warn ""
            warn "  GitHub's /users/$GH_OWNER endpoint hasn't propagated yet."
            warn "  Your account is recent enough that 'gh repo create' can't"
            warn "  validate the owner. Two options:"
            warn "    1. Wait 30-60 min and retry."
            warn "    2. Create the repo manually at https://github.com/new"
            warn "       ($GH_FULL, private, empty), then rerun with:"
            warn "         SKIP_REPO_CREATE=1 nous brain publish"
            warn ""
        fi
        cat "$create_err" >&2
        rm -f "$create_err"
        die "gh repo create failed."
    fi
    rm -f "$create_err"
    ok "Created https://github.com/$GH_FULL"
fi

# ── 4. Wire the gcrypt remote and push ───────────────────────────────────────
REMOTE_URL="gcrypt::ssh://git@github.com/$GH_FULL.git"
git remote add origin "$REMOTE_URL"
git config remote.origin.gcrypt-participants "$PARTICIPANTS"
ok "gcrypt remote configured: $REMOTE_URL"
ok "Recipients: $PARTICIPANTS"

info "Pushing to $REMOTE_URL ..."
git push --force --set-upstream origin main
ok "Pushed."

echo >&2
ok "Published."
echo "  Local:  $BRAIN_ABS" >&2
echo "  Remote: https://github.com/$GH_FULL" >&2

#!/usr/bin/env bash
# scripts/new-brain.sh — bootstrap a fresh gcrypt-encrypted brain at a new path.
#
# Differs from cloneto.sh: that one mirrors the *current* repo (full git
# history) into a new gcrypt'd remote. This one provisions a brand-new empty
# brain — no source repo, no history. Use when you're spinning up a brand-new
# private or shared brain (e.g. brain-shared-family).
#
# Flow:
#   1. Validate deps (git, gh, gh auth, jq, gpg, git-remote-gcrypt).
#   2. Resolve target local path (from $1 or interactive prompt).
#   3. Resolve GitHub owner (default: gh authenticated user) and repo name
#      (default: basename of target). Create the GH repo (private). If it
#      already exists with content, offer to delete + recreate (force-push
#      can't recover from gcrypt state encrypted to a key we don't hold).
#   4. Select a GPG identity (single → auto; multiple → prompt; zero → bail).
#   5. mkdir target; git init; set branch main; set git user identity.
#   6. Pre-create go.mod with right module path (so setup.sh doesn't infer
#      from the opaque gcrypt remote URL).
#   7. Run nous/setup.sh --yes from the target directory (default
#      symlink mode after the 2026-05-19 simplification).
#   8. Author .brain/config.md per ariadne AGENTS.md §1.
#   9. Set gcrypt remote and gcrypt-participants.
#  10. git add . && commit && push --force --set-upstream origin main.
#
# Used by `make new-brain` (Makefile.nous).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NOUS_DIR="$(dirname "$SCRIPT_DIR")"

# ── Colors ───────────────────────────────────────────────────────────────────
RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; CYAN=$'\033[0;36m'; RESET=$'\033[0m'
info() { printf "%s==>%s %s\n" "$CYAN" "$RESET" "$*" >&2; }
ok()   { printf "%s  [ok]%s %s\n" "$GREEN" "$RESET" "$*" >&2; }
warn() { printf "%s  [!]%s %s\n" "$YELLOW" "$RESET" "$*" >&2; }
die()  { printf "%serror:%s %s\n" "$RED" "$RESET" "$*" >&2; exit 1; }

# ── Splash ───────────────────────────────────────────────────────────────────
# Tells a first-time operator what 'make new-brain' is about to do, which
# passphrases will be asked for, and that only ciphertext touches GitHub.
# Suppress via NOUS_QUIET=1 for CI / scripted re-runs.
splash() {
    # Soft anatomical / Apple-emoji brain-pink via truecolor (#F4ACB7).
    # Falls back to a 256-color magenta on terminals without truecolor.
    local pink=$'\033[1;38;2;244;172;183m' bold=$'\033[1m'
    printf "\n" >&2
    while IFS= read -r line; do
        printf "          %s%s%s\n" "$pink" "$line" "$RESET" >&2
    done <<'ART'
                          :%#***. ...
              +:::*:*:*#=*::=%%=*+:..:.%%*:.--
         -+%##%######%*##*++:+#%%*:=#%=*::::::..
       +%####%%###%%%=.%%%*: =#%#+==+%%**+:.:..:-
    :=#%####%####%%%=*%#%=+:=###=:%##==%==***%....:-
  .=##%%######%*###=:###++:%###::####*-#=%%%=:::::.+-
  #######%:##%*%%%##%####-:#####:####+.%=%+.=#%*-+-:: .
 ####*#####+:%#####%:=###%*%####=###%=###%%=.##%=:-.--.:
*########%*=%####=###:#######%##%#%=*====##*+:%=+++*:.:-..-
 =#=%###########%*=#####%++=*::=**:-.-%%##%=***+.*+::*::..
 %########***%%%*+-==*=*+- %#++=#%%%%=+%#==%==*-#==+.#=:   -
  :==%==###%==*::*=%%%%%%==#####=%##%%*%%#%=*%=#=+.=+==: .
    -.+==***:+%##############%=:===+:*+=*:*=#%%*+:%*:.=+-:.-
            +###%%####%*:=++-:#%###%=*###%=#=*+.=*+-: =- --
            .=%##=+=%==#########%%=*****=*++**.:+::=+:. -.
              .%=*%%%#%==:%%%%%%%=#=*=*%##=:::.:+::.--
                   %##%=+*===++:----####++*#==+*+:.:
                                -:%#####=%=+++.::--
                                  -.#%#%%%=+..- -
                                     %#*
ART
    cat >&2 <<EOF

${bold}make new-brain${RESET} — provisioning a fresh encrypted brain.

${CYAN}What this does${RESET}
  1. create a local git repo at <path> (plaintext working tree)
  2. create a GitHub repo to host the encrypted form
  3. wire up the gcrypt remote, encrypted to your GPG public key
  4. seed .brain/config.md (the brain manifest)
  5. initial commit + push — only gcrypt ciphertext touches GitHub

${CYAN}You'll be prompted for${RESET}
  ${bold}GPG passphrase${RESET}  to sign and encrypt the initial push (set during
                  'make nous-bootstrap'). gpg-agent caches it after first
                  use; flush via idle timeout or by ${bold}disarming${RESET}.
  ${bold}SSH passphrase${RESET}  to authenticate the push to GitHub (also set during
                  'make nous-bootstrap'). Cached in ssh-agent for the
                  current login session.

If you haven't run ${bold}make nous-bootstrap${RESET} on this machine, ^C now and do
that first — it generates the GPG keypair and SSH key this script expects.

EOF
}
[ "${NOUS_QUIET:-0}" = 1 ] || splash

# ── 0. Validate environment ──────────────────────────────────────────────────
command -v git >/dev/null 2>&1 || die "git not installed."
command -v gh  >/dev/null 2>&1 || die "GitHub CLI 'gh' not installed. brew install gh"
command -v jq  >/dev/null 2>&1 || die "jq not installed. brew install jq"
command -v gpg >/dev/null 2>&1 || die "gpg not installed. Run 'make identity' first."
command -v git-remote-gcrypt >/dev/null 2>&1 || die "git-remote-gcrypt not installed. Run 'make identity' first."
gh auth status >/dev/null 2>&1 || die "gh is not authenticated. Run: gh auth login"

# ── 1. Target local path ─────────────────────────────────────────────────────
TARGET="${1:-}"
if [ -z "$TARGET" ]; then
    [ -t 0 ] || die "TARGET not provided and stdin is not a TTY."
    read -rp "Target local path (e.g. ../brain-shared-family): " TARGET
    [ -n "$TARGET" ] || die "Target path cannot be empty."
fi

if [ -e "$TARGET" ]; then
    die "Local path $TARGET already exists. Move it aside or pick a different path."
fi

TARGET_NAME=$(basename "$TARGET")
TARGET_PARENT=$(cd "$(dirname "$TARGET")" 2>/dev/null && pwd) || die "Parent of $TARGET doesn't exist."
TARGET_ABS="$TARGET_PARENT/$TARGET_NAME"

info "Bootstrapping fresh brain at: $TARGET_ABS"

# ── 2. GitHub owner + repo name ──────────────────────────────────────────────
DEFAULT_OWNER=$(gh api user --jq .login 2>/dev/null) || die "Could not read gh authenticated user."

if [ -t 0 ]; then
    read -rp "GitHub owner [$DEFAULT_OWNER]: " GH_OWNER
    GH_OWNER="${GH_OWNER:-$DEFAULT_OWNER}"
    read -rp "GitHub repo name [$TARGET_NAME]: " GH_NAME
    GH_NAME="${GH_NAME:-$TARGET_NAME}"
else
    GH_OWNER="$DEFAULT_OWNER"
    GH_NAME="$TARGET_NAME"
fi
GH_FULL="$GH_OWNER/$GH_NAME"

# ── 3. Create or recreate the GH repo ────────────────────────────────────────
# A pre-existing repo with content is the failure mode that bit us: gcrypt
# always reads the existing manifest before push to chain protocol state, so a
# manifest encrypted to a previous identity fails with "Wrong secret key used"
# *before* `git push --force` semantics ever apply. The honest recovery is
# delete + recreate, not force-push.
create_repo() {
    gh repo create "$GH_FULL" --private \
        --description "gcrypt-encrypted brain (bootstrapped by make new-brain)" \
        --disable-issues --disable-wiki >/dev/null
}

if gh repo view "$GH_FULL" >/dev/null 2>&1; then
    BRANCH_COUNT=$(gh api "repos/$GH_FULL/branches" --jq 'length' 2>/dev/null || echo 0)
    if [ "${BRANCH_COUNT:-0}" -eq 0 ]; then
        ok "$GH_FULL exists but is empty — using it."
    else
        warn "GitHub repo $GH_FULL already exists with content ($BRANCH_COUNT branch(es))."
        warn "If that content is gcrypt state from a different GPG key, push will"
        warn "fail at manifest decrypt — force-push can't recover. The only safe"
        warn "path is to delete the repo and recreate it fresh."
        # delete_repo scope is sensitive and NOT granted by default — neither
        # `gh auth login --web` nor most PATs include it. Surface that upfront
        # so the user can add the scope (or delete manually via web) before
        # answering the prompt, rather than discovering it post-confirmation.
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
        [[ "$ans" =~ ^[Yy] ]] || die "Aborted. Pick a different repo name, or delete it manually first."
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
    create_repo
    ok "Created https://github.com/$GH_FULL"
fi

# ── 4. Select GPG identity ───────────────────────────────────────────────────
# Parse `gpg --list-secret-keys --with-colons` into per-identity (FP, UID) pairs.
# (Same logic as cloneto.sh — see comments there for the parsing rationale.)
declare -a FPS UIDS
idx=-1
fpr_taken=0
while IFS= read -r line; do
    case "$line" in
        sec:*)
            idx=$((idx+1))
            fpr_taken=0
            FPS[$idx]=""
            UIDS[$idx]=""
            ;;
        fpr:*)
            if [ $idx -ge 0 ] && [ $fpr_taken -eq 0 ]; then
                FPS[$idx]=$(echo "$line" | awk -F: '{print $10}')
                fpr_taken=1
            fi
            ;;
        uid:*)
            if [ $idx -ge 0 ] && [ -z "${UIDS[$idx]}" ]; then
                UIDS[$idx]=$(echo "$line" | awk -F: '{print $10}')
            fi
            ;;
    esac
done < <(gpg --list-secret-keys --with-colons 2>/dev/null)

N_KEYS=$((idx+1))
if [ "$N_KEYS" -eq 0 ]; then
    die "No GPG secret keys found. Run 'make identity' first."
elif [ "$N_KEYS" -eq 1 ]; then
    FP="${FPS[0]}"
    info "Using only available GPG identity:"
    printf "  %s\n  %s\n" "${UIDS[0]}" "$FP" >&2
else
    info "Multiple GPG identities found:"
    for j in "${!FPS[@]}"; do
        printf "  [%d] %s\n      %s\n" "$((j+1))" "${UIDS[$j]}" "${FPS[$j]}" >&2
    done
    [ -t 0 ] || die "Multiple keys but stdin is not a TTY."
    read -rp "Select identity [1-$N_KEYS]: " sel
    [[ "$sel" =~ ^[0-9]+$ ]] && [ "$sel" -ge 1 ] && [ "$sel" -le "$N_KEYS" ] || die "Invalid selection."
    FP="${FPS[$((sel-1))]}"
    info "Selected: ${UIDS[$((sel-1))]} [$FP]"
fi

# ── 5. Initialize the new repo ───────────────────────────────────────────────
info "Initializing $TARGET_ABS ..."
mkdir -p "$TARGET_ABS"
cd "$TARGET_ABS"
git init -q -b main
GIT_NAME=$(git config --get user.name  || echo "unknown")
GIT_EMAIL=$(git config --get user.email || echo "unknown@example.com")
ok "git init (branch main; author $GIT_NAME <$GIT_EMAIL>)"

# ── 6. Pre-create go.mod ─────────────────────────────────────────────────────
# setup.sh infers the module path from `git remote get-url origin`, which would
# parse the gcrypt URL into nonsense. Pre-creating sidesteps that.
cat > go.mod <<EOF
module github.com/$GH_FULL

go 1.22
EOF
ok "Wrote go.mod (module github.com/$GH_FULL)"

# ── 7. Run nous setup.sh --yes ───────────────────────────────────────────────
# Default mode is symlink (matches the old --all behavior).
info "Running nous/setup.sh --yes ..."
"$NOUS_DIR/nous/setup.sh" --yes
ok "nous setup complete."

# ── 8. Author .brain/config.md ───────────────────────────────────────────────
mkdir -p .brain
cat > .brain/config.md <<EOF
---
mode: private
name: $TARGET_NAME
recipients: [$FP]
sync_substrate: none
---

# $TARGET_NAME brain manifest

Encrypted via gcrypt with single-recipient GPG list (the user's key). Bootstrapped fresh by \`make new-brain\` on $(date +%Y-%m-%d).

Schema reference: ariadne \`AGENTS.md\` §1 (Peer Repo). Security posture: \`atlas/threat-model-shared-brain.md\` (in the personal brain).
EOF
ok "Authored .brain/config.md"

# ── 9. Configure gcrypt remote ───────────────────────────────────────────────
REMOTE_URL="gcrypt::ssh://git@github.com/$GH_FULL.git"
git remote add origin "$REMOTE_URL"
git config remote.origin.gcrypt-participants "$FP"
ok "gcrypt remote configured: $REMOTE_URL"
ok "Recipient: $FP"

# ── 10. Commit and push ──────────────────────────────────────────────────────
git add -A
git -c user.email="$GIT_EMAIL" -c user.name="$GIT_NAME" \
    commit -q -m "init: bootstrap brain ($TARGET_NAME)"
ok "Initial commit created."

info "Pushing to $REMOTE_URL ..."
git push --force --set-upstream origin main
ok "Pushed."

# ── 11. Done ─────────────────────────────────────────────────────────────────
echo
ok "Bootstrap complete."
echo "  Local:  $TARGET_ABS"
echo "  Remote: https://github.com/$GH_FULL"
echo
echo "Verify opacity: visit the remote URL — contents should be opaque (hash-named blobs)."
echo "Round-trip clone test:"
echo "  cd /tmp && git clone $REMOTE_URL bootstrap-test && rm -rf /tmp/bootstrap-test"

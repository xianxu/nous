#!/usr/bin/env bash
# scripts/cloneto.sh — clone the current git repo to a new location AS a gcrypt-encrypted brain.
#
# Brain-specific wrapper around moveto.sh. Handles:
#   - Prompting for target path if not provided
#   - Discovering the source's GitHub owner; constructing the target's GitHub repo path
#   - Creating the target GitHub repo if missing, or confirming force-push if it exists
#   - Selecting a GPG identity from the local keyring (prompts if multiple)
#   - Local clone (delegated to moveto.sh)
#   - Configuring the gcrypt remote with the selected identity as recipient
#   - Authoring .brain/config.md per ariadne AGENTS.md §1
#   - Force-pushing (gcrypt force-pushes new repos implicitly anyway)
#
# Used by `make cloneto` (Makefile.nous).
#
# Spec: nous#3 M1 step 3 (mirror brain's full git history into a new gcrypt'd repo).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ── Colors ───────────────────────────────────────────────────────────────────
RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; CYAN=$'\033[0;36m'; RESET=$'\033[0m'
info() { printf "%s==>%s %s\n" "$CYAN" "$RESET" "$*" >&2; }
ok()   { printf "%s  [ok]%s %s\n" "$GREEN" "$RESET" "$*" >&2; }
warn() { printf "%s  [!]%s %s\n" "$YELLOW" "$RESET" "$*" >&2; }
die()  { printf "%serror:%s %s\n" "$RED" "$RESET" "$*" >&2; exit 1; }

# ── 0. Validate environment ──────────────────────────────────────────────────
git rev-parse --git-dir >/dev/null 2>&1 || die "Not in a git repo (run from the source repo's working tree)."
command -v gh  >/dev/null 2>&1 || die "GitHub CLI 'gh' not installed. brew install gh"
command -v jq  >/dev/null 2>&1 || die "jq not installed. brew install jq"
gh auth status >/dev/null 2>&1 || die "gh is not authenticated. Run: gh auth login"

# ── 1. Source GitHub info ────────────────────────────────────────────────────
SOURCE_INFO=$(gh repo view --json owner,name 2>/dev/null) || \
    die "Source repo isn't on GitHub (or 'gh' can't see it). Configure an origin first."
GITHUB_OWNER=$(echo "$SOURCE_INFO" | jq -r '.owner.login')
SOURCE_REPO=$(echo "$SOURCE_INFO" | jq -r '.name')
info "Source: $GITHUB_OWNER/$SOURCE_REPO ($(pwd))"

# ── 2. Target local path ─────────────────────────────────────────────────────
TARGET="${1:-}"
if [ -z "$TARGET" ]; then
    if [ ! -t 0 ]; then
        die "TARGET not provided and stdin is not a TTY."
    fi
    SOURCE_NAME=$(basename "$(pwd)")
    DEFAULT_TARGET="../${SOURCE_NAME}-private"
    read -rp "Target local path [$DEFAULT_TARGET]: " TARGET
    TARGET="${TARGET:-$DEFAULT_TARGET}"
fi
TARGET_NAME=$(basename "$TARGET")
TARGET_GITHUB="$GITHUB_OWNER/$TARGET_NAME"

# Refuse if local target exists; offer to remove (interactive) or bail
if [ -e "$TARGET" ]; then
    warn "Local path $TARGET already exists."
    if [ -t 0 ]; then
        read -rp "Remove it? [y/N] " ans
        [[ "$ans" =~ ^[Yy] ]] || die "Aborted (won't clobber existing local path)."
        rm -rf "$TARGET"
        ok "Removed existing $TARGET"
    else
        die "Local target exists and stdin is not a TTY (cannot prompt). Move it aside and re-run."
    fi
fi

# ── 3. Target GitHub repo: create or confirm overwrite ───────────────────────
if gh repo view "$TARGET_GITHUB" >/dev/null 2>&1; then
    warn "GitHub repo $TARGET_GITHUB already exists."
    if [ -t 0 ]; then
        read -rp "Force-push to replace its contents? [y/N] " ans
        [[ "$ans" =~ ^[Yy] ]] || die "Aborted."
        ok "Will force-push to existing $TARGET_GITHUB"
    else
        die "$TARGET_GITHUB exists and stdin is not a TTY (cannot prompt for force confirmation)."
    fi
else
    info "GitHub repo $TARGET_GITHUB doesn't exist; creating it (private, no issues, no wiki)..."
    gh repo create "$TARGET_GITHUB" --private \
        --description "gcrypt-encrypted brain (created by make cloneto)" \
        --disable-issues --disable-wiki >/dev/null
    ok "Created https://github.com/$TARGET_GITHUB"
fi

# ── 4. Select GPG identity ───────────────────────────────────────────────────
# Build parallel arrays of fingerprints and uids from gpg --list-secret-keys.
declare -a FPS UIDS
i=0
while IFS= read -r line; do
    case "$line" in
        fpr:*) FPS[$i]=$(echo "$line" | awk -F: '{print $10}') ;;
        uid:*)
            UIDS[$i]=$(echo "$line" | awk -F: '{print $10}')
            i=$((i+1))
            ;;
    esac
done < <(gpg --list-secret-keys --with-colons 2>/dev/null)

N_KEYS=${#FPS[@]}
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
    if [ ! -t 0 ]; then
        die "Multiple keys but stdin is not a TTY. Set IDENTITY_FP=<fingerprint> to disambiguate."
    fi
    read -rp "Select identity [1-$N_KEYS]: " sel
    [[ "$sel" =~ ^[0-9]+$ ]] && [ "$sel" -ge 1 ] && [ "$sel" -le "$N_KEYS" ] || die "Invalid selection."
    FP="${FPS[$((sel-1))]}"
    info "Selected: ${UIDS[$((sel-1))]} [$FP]"
fi

# ── 5. Local clone (delegated to moveto.sh) ──────────────────────────────────
"$SCRIPT_DIR/moveto.sh" "$TARGET"
TARGET_ABS=$(cd "$TARGET" && pwd)

# ── 6. Configure gcrypt remote ───────────────────────────────────────────────
cd "$TARGET_ABS"
TARGET_REMOTE_URL="gcrypt::ssh://git@github.com/$TARGET_GITHUB.git"
git remote add origin "$TARGET_REMOTE_URL"
git config remote.origin.gcrypt-participants "$FP"
ok "gcrypt remote configured: $TARGET_REMOTE_URL"
ok "Recipient: $FP"

# ── 7. Author .brain/config.md manifest ──────────────────────────────────────
mkdir -p .brain
cat > .brain/config.md <<EOF
---
mode: private
name: $TARGET_NAME
recipients: [$FP]
sync_substrate: none
---

# $TARGET_NAME brain manifest

Encrypted via gcrypt with single-recipient GPG list (the user's key). Provisioned by \`make cloneto\` from $GITHUB_OWNER/$SOURCE_REPO on $(date +%Y-%m-%d).

Schema reference: ariadne \`AGENTS.md\` §1 (Peer Repo). Security posture: \`brain/atlas/threat-model-shared-brain.md\`.
EOF

git add .brain/config.md
git -c user.email="$(git config --get user.email || echo unknown@example.com)" \
    -c user.name="$(git config --get user.name || echo unknown)" \
    commit -m "init: brain manifest"
ok "Authored .brain/config.md and committed."

# ── 8. Push (force; gcrypt force-pushes new repos implicitly) ────────────────
info "Pushing to $TARGET_REMOTE_URL ..."
git push --force --set-upstream origin "$(git branch --show-current)"
ok "Pushed."

# ── 9. Verification hints ────────────────────────────────────────────────────
echo
ok "Done."
echo "  Local:  $TARGET_ABS"
echo "  Remote: https://github.com/$TARGET_GITHUB"
echo
echo "Verify opacity: visit the remote URL — contents should be opaque (hash-named blobs)."
echo "Round-trip clone test:"
echo "  cd /tmp && git clone $TARGET_REMOTE_URL cloneto-test && rm -rf /tmp/cloneto-test"

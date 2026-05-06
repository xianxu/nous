#!/usr/bin/env bash
# scripts/moveto.sh — clone the current git repo to a new location with full history.
#
# Idempotent: refuses to overwrite an existing target. Strips the auto-created
# `origin` remote in the new clone (which would otherwise point back at the
# source) so the caller can configure a fresh remote without confusion.
#
# Used by `make moveto <target>` (Makefile.nous).
#
# Spec: nous#3 M1 step 3 (mirror brain's full git history into brain-private).

set -euo pipefail

# ── Colors ───────────────────────────────────────────────────────────────────
RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; CYAN=$'\033[0;36m'; RESET=$'\033[0m'
info() { printf "%s==>%s %s\n" "$CYAN" "$RESET" "$*" >&2; }
ok()   { printf "%s  [ok]%s %s\n" "$GREEN" "$RESET" "$*" >&2; }
die()  { printf "%serror:%s %s\n" "$RED" "$RESET" "$*" >&2; exit 1; }

TARGET="${1:-}"
[ -n "$TARGET" ] || die "Usage: make moveto <target-path>   (e.g., make moveto ../brain-private)"

# Verify we're in a git repo
git rev-parse --git-dir >/dev/null 2>&1 || die "Not in a git repo (run from the source repo's working tree)."

# Verify target doesn't exist (don't clobber)
if [ -e "$TARGET" ]; then
    die "Target already exists: $TARGET. Refusing to overwrite. Move it aside or pick a different target."
fi

SOURCE_PATH=$(pwd)
SOURCE_NAME=$(basename "$SOURCE_PATH")
TARGET_ABS=$(cd "$(dirname "$TARGET")" && pwd)/$(basename "$TARGET")

info "Cloning $SOURCE_NAME → $TARGET_ABS (full git history)"
git clone "$SOURCE_PATH" "$TARGET_ABS"

# In the new clone, the auto-created origin points back at the source path
# (effectively the "upstream" for this clone). Strip it so the caller doesn't
# accidentally push the moved repo back at its source.
cd "$TARGET_ABS"
if git remote get-url origin >/dev/null 2>&1; then
    git remote remove origin
    ok "Removed auto-created origin remote (was: $SOURCE_PATH)"
fi

NUM_COMMITS=$(git rev-list --count HEAD 2>/dev/null || echo 0)
ok "Cloned: $NUM_COMMITS commits, no remotes configured."

echo
echo "Next steps:"
echo "  cd $TARGET_ABS"
echo "  git remote add origin <new-remote-url>"
echo "  # for a gcrypt'd remote:"
echo "  git remote add origin gcrypt::ssh://git@github.com/<user>/<repo>.git"
echo "  git config remote.origin.gcrypt-participants <gpg-fingerprint>"
echo "  # configure / commit / push from there"

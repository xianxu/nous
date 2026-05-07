#!/usr/bin/env bash
# scripts/nous-bootstrap.sh — install the nous developer toolchain on a fresh Mac.
#
# Idempotent: every step checks before installing.
#
# Layers:
#   1. Substrate — Xcode CLT, Homebrew, Brewfile packages
#   2. Identity  — delegates to scripts/identity.sh (GPG)
#   3. Workflow  — delegates to .openshell `bootstrap` (gh auth, openshell, mutagen);
#                  fzf shell hook; go env verification
#
# Used by `make nous-bootstrap` (Makefile.nous).
#
# Spec: workshop/issues/000011-nous-bootstrap.md.

set -euo pipefail

# ── Colors ───────────────────────────────────────────────────────────────────
RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; CYAN=$'\033[0;36m'; RESET=$'\033[0m'
info() { printf "%s==>%s %s\n" "$CYAN" "$RESET" "$*" >&2; }
ok()   { printf "%s  [ok]%s %s\n" "$GREEN" "$RESET" "$*" >&2; }
warn() { printf "%s  [!]%s %s\n" "$YELLOW" "$RESET" "$*" >&2; }
die()  { printf "%serror:%s %s\n" "$RED" "$RESET" "$*" >&2; exit 1; }

NOUS_DIR=$(cd "$(dirname "$0")/.." && pwd)
BREWFILE="$NOUS_DIR/Brewfile"

# ── 1. Xcode Command Line Tools ──────────────────────────────────────────────
info "Checking Xcode Command Line Tools..."
if xcode-select -p >/dev/null 2>&1; then
    ok "Xcode CLT installed at $(xcode-select -p)."
else
    info "Triggering Xcode CLT install (a system dialog will appear)..."
    xcode-select --install || true
    die "Re-run this script after the Xcode CLT install completes."
fi

# ── 2. Homebrew ──────────────────────────────────────────────────────────────
info "Checking Homebrew..."
if command -v brew >/dev/null 2>&1; then
    ok "Homebrew installed at $(brew --prefix)."
else
    info "Installing Homebrew (official one-liner)..."
    /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
    # Apple Silicon installs to /opt/homebrew; ensure PATH for this script.
    if [ -x /opt/homebrew/bin/brew ]; then
        eval "$(/opt/homebrew/bin/brew shellenv)"
    elif [ -x /usr/local/bin/brew ]; then
        eval "$(/usr/local/bin/brew shellenv)"
    fi
    ok "Homebrew installed."
fi

# ── 3. Brewfile ──────────────────────────────────────────────────────────────
[ -f "$BREWFILE" ] || die "Brewfile not found at $BREWFILE."
info "Applying Brewfile ($BREWFILE)..."
brew bundle install --file="$BREWFILE"
ok "Brewfile applied."

# ── 4. Identity (GPG) ────────────────────────────────────────────────────────
info "Bootstrapping GPG identity..."
"$NOUS_DIR/scripts/identity.sh"

# ── 5. Workflow tools (gh auth, openshell, mutagen) ──────────────────────────
# `.openshell/Makefile`'s `bootstrap` runs `gh auth login` if not authenticated,
# installs the openshell CLI, and ensures mutagen is present. Overlap with the
# Brewfile is intentional — its idempotent gh-auth gate is the value-add.
if [ -f "$NOUS_DIR/.openshell/Makefile" ]; then
    info "Running .openshell bootstrap (gh auth, openshell CLI)..."
    (cd "$NOUS_DIR" && make bootstrap)
else
    warn ".openshell/Makefile not found; skipping openshell bootstrap."
fi

# ── 6. fzf shell hook ────────────────────────────────────────────────────────
FZF_INSTALL="$(brew --prefix)/opt/fzf/install"
if [ -x "$FZF_INSTALL" ]; then
    info "Installing fzf shell key bindings + completion..."
    "$FZF_INSTALL" --no-bash --no-fish --key-bindings --completion --update-rc
    ok "fzf shell hooks installed."
else
    warn "fzf install script not at $FZF_INSTALL; skipping shell hook."
fi

# ── 7. Verify go on PATH ─────────────────────────────────────────────────────
if command -v go >/dev/null 2>&1; then
    ok "Go on PATH: $(go version)"
else
    warn "Go not on PATH after bootstrap. Open a new shell and re-check."
fi

echo
ok "Nous bootstrap complete."
echo
echo "Next steps:"
echo "  - Open a new shell so PATH and shell hooks pick up."
echo "  - Clone peer repos if not present:"
echo "      git clone https://github.com/xianxu/ariadne.git ../ariadne"
echo "  - Bootstrap an encrypted brain: make new-brain ../brain"
echo "  - Build nous binaries:          make build"

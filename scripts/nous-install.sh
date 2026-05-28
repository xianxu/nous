#!/usr/bin/env bash
# scripts/nous-install.sh — put the nous binary on the developer's PATH.
#
# Idempotent: appends $NOUS_DIR/bin to the user's shell rc (zsh/bash)
# if not already there, then prints the export line so the user can
# paste it manually as backup (in case rc-detection picked the wrong
# file).
#
# Two entry points:
#   • `make nous-install`               — standalone (re-run PATH step
#                                          after rc churn / new shell).
#   • scripts/nous-bootstrap.sh         — calls this script as its final
#                                          install step on fresh Mac.
#
# Mirrors ariadne's sdlc-install.sh convention so multiple repo
# `*-install` gestures compose idempotently on the same rc file.
#
# Naming note: the `nous-install` target name was previously retired
# (see Makefile.nous comment block near install-nous-and-launchd) when
# the "copy to install prefix + register launchd" gesture was
# unbundled. This is a different gesture (PATH-only), and the name is
# reclaimed.

set -euo pipefail

RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; CYAN=$'\033[0;36m'; RESET=$'\033[0m'
info() { printf "%s==>%s %s\n" "$CYAN" "$RESET" "$*" >&2; }
ok()   { printf "%s  [ok]%s %s\n" "$GREEN" "$RESET" "$*" >&2; }
warn() { printf "%s  [!]%s %s\n" "$YELLOW" "$RESET" "$*" >&2; }
die()  { printf "%serror:%s %s\n" "$RED" "$RESET" "$*" >&2; exit 1; }

NOUS_DIR=$(cd "$(dirname "$0")/.." && pwd)

if [ ! -f "$NOUS_DIR/bin/nous" ]; then
    die "no nous binary at $NOUS_DIR/bin/nous — run \`make nous-build\` first"
fi

# ── PATH wiring ─────────────────────────────────────────────────────────────
SHELL_RC=""
case "${SHELL:-}" in
    */zsh)  SHELL_RC="$HOME/.zshrc" ;;
    */bash) [ -f "$HOME/.bash_profile" ] && SHELL_RC="$HOME/.bash_profile" || SHELL_RC="$HOME/.bashrc" ;;
esac

EXPORT_LINE="export PATH=\"$NOUS_DIR/bin:\$PATH\""

if [ -n "$SHELL_RC" ] && ! grep -q "$NOUS_DIR/bin" "$SHELL_RC" 2>/dev/null; then
    info "Adding $NOUS_DIR/bin to PATH in $SHELL_RC..."
    printf '\n# Added by nous-install: nous binary location\n%s\n' "$EXPORT_LINE" >> "$SHELL_RC"
    ok "PATH updated. Open a new shell (or run: source $SHELL_RC) to pick it up."
elif [ -z "$SHELL_RC" ]; then
    warn "couldn't auto-detect shell rc from SHELL=$SHELL — see manual step below"
fi

# ── Manual-paste reminder (belt-and-suspenders) ─────────────────────────────
# Print even when auto-write succeeded so the user can verify, or
# paste into a different rc file if their setup differs from what
# shell detection assumes.
echo
ok "If nous isn't on PATH in a new shell, add this line to your ~/.zshrc or ~/.bashrc:"
echo "    $EXPORT_LINE"
echo

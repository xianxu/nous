#!/usr/bin/env bash
# scripts/brain-vm-setup.sh — make a headless tart VM GPG-unattended for
# brain testing (nous#36). Idempotent; runs on every boot via the
# .tart/vm-hooks.d/00-gpg-setup.sh hook (ariadne#59's vm-hooks convention).
#
# Why: a headless `make tart` VM has no window server, so pinentry-mac can't
# draw its dialog and every GPG/gcrypt passphrase prompt fails over SSH. This
# installs a fake pinentry that always returns the throwaway test passphrase,
# so gpg + gcrypt (clone/decrypt, sign, re-key) run non-interactively. The VM
# holds only throwaway identities (Ying/Emma test keys created in-VM), so a
# hardcoded test passphrase is safe here.
#
# UNSAFE outside a disposable VM — it points gpg-agent at a fake pinentry that
# hands out a known passphrase. It refuses to run unless it sees tart-VM
# markers, so it can't clobber a real machine's gpg-agent. Override with
# NOUS_BRAIN_VM_FORCE=1 only if you know what you're doing.
#
# Spec: workshop/issues/000036-headless-brain-test.md M1.
set -euo pipefail

info() { printf '  [brain-vm] %s\n' "$*"; }
die()  { printf '  [brain-vm] error: %s\n' "$*" >&2; exit 1; }

# ── Safety: refuse outside a disposable tart VM ──────────────────────
# tart VMs run as the 'admin' user and carry the ~/.tart-current-repo
# marker written by tart-vm-setup.sh. Require both (or an explicit force).
if [ "${NOUS_BRAIN_VM_FORCE:-}" != "1" ]; then
    [ "$(id -un)" = "admin" ] \
        || die "refusing: not the tart 'admin' user — set NOUS_BRAIN_VM_FORCE=1 to override"
    [ -f "$HOME/.tart-current-repo" ] \
        || die "refusing: no ~/.tart-current-repo marker (not a tart VM) — set NOUS_BRAIN_VM_FORCE=1 to override"
fi

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PASS_FILE="$here/../testdata/test-bootstrap/test-key.passphrase"
[ -f "$PASS_FILE" ] || die "test passphrase fixture not found: $PASS_FILE"
PASS="$(cat "$PASS_FILE")"

# The vm-hook runs from tart-vm-setup.sh's non-login ssh context, whose PATH is
# bare (/usr/bin:/bin:…) and excludes /opt/homebrew/bin — so brew-installed gpg
# + gpgconf wouldn't be found and this script would bail before installing the
# shim. Load Homebrew's env first (mirrors nous-test-bootstrap.sh).
[ -x /opt/homebrew/bin/brew ] && eval "$(/opt/homebrew/bin/brew shellenv)"

command -v gpg >/dev/null || die "gpg not installed in VM (run 'make nous-bootstrap' first)"

# ── Persistent fake pinentry ─────────────────────────────────────────
# Lives under ~/.local/bin so it survives the cold-reboot that `make tart`
# performs (a /tmp shim would be wiped, leaving gpg-agent pointing at a
# dangling program). On GETPIN it returns the test passphrase; every other
# Assuan command gets a bare OK.
SHIM="$HOME/.local/bin/pinentry-brain-test"
mkdir -p "$HOME/.local/bin"
cat > "$SHIM" <<PINENTRY
#!/bin/bash
# Fake pinentry — always returns the throwaway brain test passphrase.
# Installed by nous scripts/brain-vm-setup.sh (nous#36). Test VM only.
echo "OK"
while IFS= read -r cmd; do
  case "\$cmd" in
    GETPIN) echo "D $PASS"; echo "OK" ;;
    *)      echo "OK" ;;
  esac
done
PINENTRY
chmod +x "$SHIM"

# ── Point gpg-agent at the shim (idempotent) ─────────────────────────
mkdir -p "$HOME/.gnupg"; chmod 700 "$HOME/.gnupg"
CONF="$HOME/.gnupg/gpg-agent.conf"
WANT="pinentry-program $SHIM"
if [ ! -f "$CONF" ] || ! grep -qxF "$WANT" "$CONF"; then
    cat > "$CONF" <<AGENT
$WANT
default-cache-ttl 3600
max-cache-ttl 7200
AGENT
    gpgconf --kill gpg-agent >/dev/null 2>&1 || true
    info "gpg-agent → fake pinentry ($SHIM); agent restarted"
else
    info "gpg-agent already using the fake pinentry — no change"
fi

info "GPG unattended: gpg/gcrypt won't prompt for a passphrase in this VM."

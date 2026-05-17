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

# ── Splash ───────────────────────────────────────────────────────────────────
# Tells a first-time operator (e.g. a non-default user like a family member)
# what's about to happen, what passphrases they'll be asked for, and which
# remote actors (nous / brain / GitHub) hold which secrets. Suppress via
# NOUS_QUIET=1 for CI / re-runs.
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

${bold}make nous-bootstrap${RESET} — one-time setup of this machine for nous.

${CYAN}What this does${RESET}
  • install the toolchain (Homebrew, Go, GPG, gh, pinentry, claude-code, …)
  • create your GPG keypair → asks you to set a ${bold}GPG passphrase${RESET}
  • connect to your GitHub as storage for your brain extension
      • register an SSH key with GitHub → asks you to set an ${bold}SSH passphrase${RESET}
      • authenticate the GitHub CLI

${CYAN}You'll set two passphrases${RESET}
  Pick ones you can remember — losing them means losing access. Store in a
  password manager (Bitwarden / 1Password / Apple Passwords / Keychain).

  ${bold}GPG passphrase${RESET}  unlocks your GPG private key for encrypting and
                  decrypting brain content. gpg-agent caches it after
                  first use; the cache flushes on idle timeout or when
                  you ${bold}disarm${RESET} — your off-switch against any agent on this
                  machine misusing your key. See atlas/threat-model.

  ${bold}SSH passphrase${RESET}  unlocks your SSH private key for ${bold}git push/pull${RESET} to
                  GitHub. Cached in ssh-agent for the login session.

  ${bold}Tip:${RESET} both passphrases are local-only locks on this machine — using
       the ${bold}same passphrase${RESET} for both is fine and recommended. One thing
       to remember, no security loss vs. picking two.

${CYAN}Three actors you'll work with${RESET}
  ${bold}Nous${RESET}    this toolchain — one install per machine.
  ${bold}Brain${RESET}   your encrypted git repo(s) — created later via 'make new-brain'.
          Locally a plaintext working tree; on GitHub, gcrypt ciphertext only.
  ${bold}GitHub${RESET}  remote storage. Sees only gcrypt blobs — your GPG key is what
          unlocks them, and GitHub never holds your key.

${CYAN}Keys involved${RESET}
  • ${bold}GPG keypair${RESET}  encryption — your private key unlocks brain content;
                 your public key is what others on a shared brain encrypt to.
  • ${bold}SSH keypair${RESET}  transit — authenticates you to GitHub for git operations.
                 Independent of the GPG key; GitHub only sees this one.

EOF
}
[ "${NOUS_QUIET:-0}" = 1 ] || splash

# Pause for explicit operator consent before touching the machine. Skipped
# under NOUS_QUIET=1 (CI / scripted re-runs) and when stdin isn't a TTY
# (piped invocations — proceed silently).
if [ "${NOUS_QUIET:-0}" != 1 ] && [ -t 0 ]; then
    printf "%sPress any key to continue (Ctrl-C to abort)...%s " "$CYAN" "$RESET" >&2
    read -n 1 -s
    printf "\n\n" >&2
fi

# ── 1. Xcode Command Line Tools ──────────────────────────────────────────────
info "Checking Xcode Command Line Tools..."
if xcode-select -p >/dev/null 2>&1; then
    ok "Xcode CLT installed at $(xcode-select -p)."
else
    info "Triggering Xcode CLT install (a system dialog will appear)..."
    xcode-select --install || true
    info "Click 'Install' in the system dialog. Polling every 30s for up to 20 min."
    for attempt in $(seq 1 40); do
        sleep 30
        if xcode-select -p >/dev/null 2>&1; then
            ok "Xcode CLT installed at $(xcode-select -p)."
            break
        fi
        # Status update every 2 minutes so the user sees progress.
        if [ $((attempt % 4)) -eq 0 ]; then
            info "  ($((attempt / 2))m elapsed; still waiting for CLT install...)"
        fi
    done
    xcode-select -p >/dev/null 2>&1 \
        || die "Xcode CLT install did not complete within 20 min. Re-run when ready."
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
# Set NOUS_BOOTSTRAP_SKIP_IDENTITY=1 to skip — used by the test harness, which
# manages its own GPG environment (custom pinentry + pre-staged test key).
if [ "${NOUS_BOOTSTRAP_SKIP_IDENTITY:-}" = "1" ]; then
    info "Skipping GPG identity bootstrap (NOUS_BOOTSTRAP_SKIP_IDENTITY=1)."
else
    info "Bootstrapping GPG identity..."
    "$NOUS_DIR/scripts/identity.sh"
fi

# ── 5. Workflow tools (gh auth, openshell, mutagen) ──────────────────────────
# `.openshell/Makefile`'s `bootstrap` runs `gh auth login` if not authenticated,
# installs the openshell CLI, and ensures mutagen is present. Overlap with the
# Brewfile is intentional — its idempotent gh-auth gate is the value-add.
#
# Set NOUS_BOOTSTRAP_SKIP_OPENSHELL=1 to skip — used by the test harness, which
# can't drive interactive `gh auth login` in a non-TTY SSH session. The same
# gate also disables the GitHub SSH-key flow below (both touch GitHub state).
if [ "${NOUS_BOOTSTRAP_SKIP_OPENSHELL:-}" = "1" ]; then
    info "Skipping .openshell bootstrap (NOUS_BOOTSTRAP_SKIP_OPENSHELL=1)."
elif [ -f "$NOUS_DIR/.openshell/Makefile" ]; then
    info "Running .openshell bootstrap (gh auth, openshell CLI)..."
    (cd "$NOUS_DIR" && make bootstrap)
else
    warn ".openshell/Makefile not found; skipping openshell bootstrap."
fi

# ── 6. GitHub SSH key ────────────────────────────────────────────────────────
# Required for `gcrypt::ssh://...` brain remotes and any `git@github.com:...`
# clone/push. Generates an ed25519 key if missing, registers with GitHub via
# `gh ssh-key add` if not already there. Idempotent.
if [ "${NOUS_BOOTSTRAP_SKIP_OPENSHELL:-}" = "1" ]; then
    info "Skipping GitHub SSH-key check (NOUS_BOOTSTRAP_SKIP_OPENSHELL=1)."
elif command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    info "Checking GitHub SSH key..."
    SSH_PUB=""
    if   [ -f "$HOME/.ssh/id_ed25519.pub" ]; then SSH_PUB="$HOME/.ssh/id_ed25519.pub"
    elif [ -f "$HOME/.ssh/id_rsa.pub" ];     then SSH_PUB="$HOME/.ssh/id_rsa.pub"
    fi

    if [ -z "$SSH_PUB" ]; then
        if [ -t 0 ]; then
            email=$(git config --global user.email 2>/dev/null || echo "$(whoami)@$(hostname -s)")
            info "No SSH key found. Generating ed25519 key with no passphrase..."
            mkdir -p "$HOME/.ssh"; chmod 700 "$HOME/.ssh"
            ssh-keygen -t ed25519 -C "$email" -f "$HOME/.ssh/id_ed25519" -N "" >/dev/null
            SSH_PUB="$HOME/.ssh/id_ed25519.pub"
            ok "SSH key generated at $SSH_PUB."
        else
            warn "No SSH key and stdin is not a TTY. Generate manually: ssh-keygen -t ed25519"
        fi
    else
        ok "SSH key found: $SSH_PUB."
    fi

    # Check whether the local pubkey is already registered with GitHub.
    if [ -n "$SSH_PUB" ]; then
        local_keymat=$(awk '{print $2}' "$SSH_PUB")
        if gh api user/keys --jq '.[].key' 2>/dev/null | awk '{print $2}' | grep -qF "$local_keymat"; then
            ok "SSH key already registered with GitHub."
        elif [ -t 0 ]; then
            read -rp "Register $SSH_PUB with GitHub? [Y/n] " confirm
            if [[ ! "$confirm" =~ ^[Nn] ]]; then
                title="$(hostname -s)-$(date +%Y%m%d)"
                gh ssh-key add "$SSH_PUB" --title "$title"
                ok "SSH key registered with GitHub as '$title'."
            else
                warn "Skipping SSH-key registration. Add later: gh ssh-key add $SSH_PUB --title <name>"
            fi
        else
            warn "stdin is not a TTY; skipping SSH-key registration. Add later: gh ssh-key add $SSH_PUB --title <name>"
        fi
    fi
else
    warn "gh not authenticated; skipping GitHub SSH-key check."
fi

# ── 7. fzf shell hook ────────────────────────────────────────────────────────
FZF_INSTALL="$(brew --prefix)/opt/fzf/install"
if [ -x "$FZF_INSTALL" ]; then
    info "Installing fzf shell key bindings + completion..."
    "$FZF_INSTALL" --no-bash --no-fish --key-bindings --completion --update-rc
    ok "fzf shell hooks installed."
else
    warn "fzf install script not at $FZF_INSTALL; skipping shell hook."
fi

# ── 8. Verify go on PATH ─────────────────────────────────────────────────────
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
echo "  - Bootstrap an encrypted brain: make new-brain ../brain"
echo "  - Build nous binaries:          make build"

#!/usr/bin/env bash
# bootstrap-mac.sh — one-shot learner environment for the "plug into the matrix"
# curriculum (brain#12). Takes a near-fresh Apple-Silicon Mac — assuming the
# learner has nothing but a GitHub account — to a working agentic-writing setup:
# a GitHub identity, a terminal, an editor, a coding agent, and the ariadne
# construct, ready to write essays with an in-context agent.
#
# ── Design thesis ────────────────────────────────────────────────────────────
# The villain of the curriculum is *incidental complexity* — the flaky-setup
# friction that discouraged the learner at robotics club. So lesson 1 is NOT a
# live install gauntlet. This script delivers a prepackaged, known-good
# environment; the "lift the hood" lessons come later, by choice, not by force.
#
# ── Flow ─────────────────────────────────────────────────────────────────────
#   1. Git identity     — name + GitHub email (for commits + the SSH key)
#   2. SSH key          — ed25519 with passphrase, loaded into ssh-agent +
#                         macOS Keychain; you paste the public half into GitHub
#   3. Clone nous       — https://github.com/xianxu/nous (public, no auth)
#   4. nous bootstrap   — REUSED: nous/bootstrap.sh clones ariadne + installs the
#                         whole toolchain (Homebrew, Brewfile incl. claude-code,
#                         go, node, ripgrep, fzf) and builds the nous binary
#   5. Learner delta    — cmux, pair (→ neovim/zellij), pandoc, oh-my-zsh, the
#                         parley.nvim editor config, and the shell init file
#
# ── DRY ──────────────────────────────────────────────────────────────────────
# The substrate (steps 3–4) is already done and VM-tested by nous. We only add
# the learner delta and the GitHub identity a beginner needs up front.
#
# ── What it SKIPS for a beginner (liftable later) ────────────────────────────
# By default this stays local-only: NO GPG keypair and NO brain-sync daemon —
# the encrypt-and-sync-to-GitHub machinery a first-essay writer doesn't need.
# `nous brain new` makes brains locally without GPG. Flip the full path on with
# LEARNER_ENABLE_SYNC=1 when she wants encrypted, multi-device, GitHub-backed
# brains. (SSH *is* set up regardless — it's how she'll push her blog/repos.)
#
# ── Where this lives / fresh-Mac entry point ─────────────────────────────────
# This script lives in `nous` (public), so a brand-new Mac can fetch it without
# any of the rest of the stack first. Canonical first run:
#     git clone https://github.com/xianxu/nous ~/workspace/nous
#     ~/workspace/nous/bootstrap-mac.sh
# Run from ~/workspace/nous, the nous clone step below is a no-op (it's already
# there); run as a standalone copy from anywhere and it clones nous into
# ~/workspace/nous itself. Either way the repos land under ~/workspace — the
# convention the generated shell init (~/.config/learner-env/init.zsh) assumes.
#
# ── Usage ────────────────────────────────────────────────────────────────────
#   ./bootstrap-mac.sh                        # local-only learner setup
#   CREATE_BRAIN=essays ./bootstrap-mac.sh    # also provision a local brain
#   LEARNER_ENABLE_SYNC=1 ./bootstrap-mac.sh  # full GPG/GitHub/daemon path
#
# Idempotent: every step checks before acting. Safe to re-run.

set -euo pipefail

# ── Colors / logging (match nous-bootstrap.sh idiom) ─────────────────────────
RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; CYAN=$'\033[0;36m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
info() { printf "%s==>%s %s\n" "$CYAN" "$RESET" "$*" >&2; }
ok()   { printf "%s  [ok]%s %s\n" "$GREEN" "$RESET" "$*" >&2; }
warn() { printf "%s  [!]%s %s\n" "$YELLOW" "$RESET" "$*" >&2; }
die()  { printf "%serror:%s %s\n" "$RED" "$RESET" "$*" >&2; exit 1; }
step() { printf "\n%s── %s ──%s\n" "$BOLD" "$*" "$RESET" >&2; }

# ── Config (env-overridable) ─────────────────────────────────────────────────
WORKSPACE="${WORKSPACE:-$HOME/workspace}"        # matches the machine + tart convention
NOUS_REPO_URL="${NOUS_REPO_URL:-https://github.com/xianxu/nous}"  # public; HTTPS = no auth
CREATE_BRAIN="${CREATE_BRAIN:-}"                 # set to a name to also provision a local brain
LEARNER_ENABLE_SYNC="${LEARNER_ENABLE_SYNC:-}"   # set =1 for nous's full GPG/GitHub/daemon path
NOUS_DIR="$WORKSPACE/nous"
ENV_FILE="$HOME/.config/learner-env/init.zsh"    # the separate, sourced shell init
SSH_KEY="$HOME/.ssh/id_ed25519"

# ── Preflight ────────────────────────────────────────────────────────────────
step "Preflight"
[ "$(uname -s)" = "Darwin" ] || die "This script is for macOS. Detected $(uname -s)."
[ "$(uname -m)" = "arm64" ] || warn "Not Apple Silicon (arm64). Untested; Homebrew paths may differ."
[ "$(id -u)" != "0" ] || die "Don't run as root. Run as the learner's normal user account."
ok "macOS $(sw_vers -productVersion 2>/dev/null || echo '?') on $(uname -m); user $(whoami)."
mkdir -p "$WORKSPACE"

# ── Xcode Command Line Tools (gives us git) ──────────────────────────────────
step "Xcode Command Line Tools"
if xcode-select -p >/dev/null 2>&1; then
    ok "Already installed at $(xcode-select -p)."
else
    info "Triggering install (a system dialog will appear — click Install)..."
    xcode-select --install || true
    info "Polling every 30s for up to 20 min..."
    for attempt in $(seq 1 40); do
        sleep 30
        xcode-select -p >/dev/null 2>&1 && break
        [ $((attempt % 4)) -eq 0 ] && info "  ($((attempt / 2))m elapsed; still waiting...)"
    done
    xcode-select -p >/dev/null 2>&1 || die "CLT install didn't finish in 20 min. Re-run when ready."
    ok "Installed at $(xcode-select -p)."
fi

# ── 1. Git identity (name + GitHub email) ────────────────────────────────────
step "Git identity"
GIT_NAME="$(git config --global user.name 2>/dev/null || true)"
GIT_EMAIL="$(git config --global user.email 2>/dev/null || true)"
if [ -t 0 ]; then
    if [ -z "$GIT_NAME" ];  then read -rp "Your name (for git commits): " GIT_NAME;  git config --global user.name  "$GIT_NAME"; fi
    if [ -z "$GIT_EMAIL" ]; then read -rp "Your GitHub email: "          GIT_EMAIL; git config --global user.email "$GIT_EMAIL"; fi
else
    [ -n "$GIT_EMAIL" ] || { GIT_EMAIL="$(whoami)@$(hostname -s)"; warn "No TTY; using placeholder email $GIT_EMAIL. Fix with: git config --global user.email <email>"; }
fi
ok "Git identity: ${GIT_NAME:-?} <${GIT_EMAIL:-?}>"

# ── 2. SSH key → ssh-agent + Keychain, then register with GitHub ─────────────
step "SSH identity (GitHub)"
mkdir -p "$HOME/.ssh"; chmod 700 "$HOME/.ssh"
if [ -f "$SSH_KEY.pub" ]; then
    ok "SSH key already exists: $SSH_KEY.pub"
elif [ -t 0 ]; then
    info "Creating an SSH key. You'll set a ${BOLD}passphrase${RESET} — pick one you can remember"
    info "  (a password manager is the right place to keep it)."
    ssh-keygen -t ed25519 -C "$GIT_EMAIL" -f "$SSH_KEY"
    ok "Created $SSH_KEY."
else
    warn "No TTY; skipping SSH keygen. Run later: ssh-keygen -t ed25519 -C <email>"
fi
# Load into the agent + persist the passphrase in macOS Keychain.
if [ -f "$SSH_KEY" ]; then
    info "Loading the key into ssh-agent + Keychain..."
    ssh-add --apple-use-keychain "$SSH_KEY" 2>&1 | sed 's/^/  /' || warn "ssh-add: key may already be loaded."
fi
# ~/.ssh/config so the key auto-loads from Keychain on every login.
SSH_CONFIG="$HOME/.ssh/config"
if ! grep -q "UseKeychain yes" "$SSH_CONFIG" 2>/dev/null; then
    info "Adding UseKeychain + AddKeysToAgent to ~/.ssh/config..."
    touch "$SSH_CONFIG"; chmod 600 "$SSH_CONFIG"
    cat >> "$SSH_CONFIG" <<EOF

# Added by bootstrap-mac: load the SSH key from Keychain on session start.
Host *
  UseKeychain yes
  AddKeysToAgent yes
  IdentityFile $SSH_KEY
EOF
    ok "~/.ssh/config updated."
fi
# Register the public half with GitHub. gh isn't installed yet (and we assume
# only a GitHub *account*), so this is the honest beginner path: copy + paste.
if [ -f "$SSH_KEY.pub" ]; then
    command -v pbcopy >/dev/null 2>&1 && pbcopy < "$SSH_KEY.pub" && info "Public key copied to your clipboard."
    printf "\n%sAdd this key to GitHub →%s %shttps://github.com/settings/ssh/new%s\n" "$CYAN" "$RESET" "$BOLD" "$RESET" >&2
    printf "%s\n\n" "$(cat "$SSH_KEY.pub")" >&2
    if [ -t 0 ]; then read -rp "Press Enter once you've added it (or to skip for now)... " _ || true; fi
fi

# ── 3 + 4. Clone nous + hand off to its (reused) mac bootstrap ───────────────
# nous/bootstrap.sh transitively clones ariadne, then `make bootstrap` →
# nous-bootstrap.sh installs Homebrew + the Brewfile (claude-code, go, node,
# ripgrep, fzf, …) and builds the nous binary.
step "The construct (nous + ariadne + toolchain)"
if [ -d "$NOUS_DIR/.git" ]; then
    # Update an existing clone so re-runs pick up new files/fixes (a clone made
    # before, e.g., this asset was added would otherwise stay stale). Best-effort:
    # a dirty tree or a non-main branch (the VM mirror) just keeps what's there.
    info "Updating existing nous clone at $NOUS_DIR ..."
    git -C "$NOUS_DIR" pull --ff-only 2>/dev/null \
        && ok "nous up to date." \
        || warn "  couldn't fast-forward nous (local changes / a branch?); using the existing clone."
else
    info "Cloning nous → $NOUS_DIR ..."
    git clone "$NOUS_REPO_URL" "$NOUS_DIR"
    ok "Cloned nous."
fi
# Skip the encrypt/sync ceremony unless the operator opts into the full path.
# (SSH is already done above, so we skip nous's gh-auth/SSH step too.)
nous_env=()
if [ -z "$LEARNER_ENABLE_SYNC" ]; then
    info "Local-only mode: skipping GPG identity and the sync daemon."
    info "  (Re-run with LEARNER_ENABLE_SYNC=1 for the full encrypted-brain path.)"
    nous_env=(
        NOUS_BOOTSTRAP_SKIP_IDENTITY=1   # no GPG keypair / passphrase
        NOUS_BOOTSTRAP_SKIP_OPENSHELL=1  # no gh auth (we did SSH ourselves)
        NOUS_BOOTSTRAP_SKIP_SERVICE=1    # no brain-sync launchd daemon
        NOUS_QUIET=1                     # no passphrase splash
    )
fi

# A pre-existing Homebrew (e.g. shipped in a VM base image) can be owned by
# another user, so brew errors "permission denied" and asks for sudo — both in
# nous's Brewfile (run by the handoff below) and in our casks. If brew is
# already here and its prefix isn't writable, fix ownership once, up front. A
# brew freshly installed by nous is already user-owned, so this no-ops.
ensure_brew_writable() {
    if ! command -v brew >/dev/null 2>&1; then
        if   [ -x /opt/homebrew/bin/brew ]; then eval "$(/opt/homebrew/bin/brew shellenv)"
        elif [ -x /usr/local/bin/brew ];   then eval "$(/usr/local/bin/brew shellenv)"; fi
    fi
    command -v brew >/dev/null 2>&1 || return 0
    local p; p="$(brew --prefix 2>/dev/null)" || return 0
    [ -w "$p" ] && return 0
    warn "Homebrew at $p isn't writable by $(whoami) — fixing ownership (one-time sudo)..."
    sudo chown -R "$(whoami):admin" "$p" || warn "chown failed; brew may still prompt for sudo."
}
ensure_brew_writable

info "Handing off to nous bootstrap..."
# bash 3.2 (macOS default) errors on an empty array under `set -u`, so branch.
if [ ${#nous_env[@]} -gt 0 ]; then
    ( cd "$NOUS_DIR" && env "${nous_env[@]}" ./bootstrap.sh )
else
    ( cd "$NOUS_DIR" && ./bootstrap.sh )
fi
ok "Construct ready: toolchain installed, nous binary built."

# Make Homebrew available in *this* shell for the steps below.
if   [ -x /opt/homebrew/bin/brew ]; then eval "$(/opt/homebrew/bin/brew shellenv)"
elif [ -x /usr/local/bin/brew ];   then eval "$(/usr/local/bin/brew shellenv)"
fi
command -v brew >/dev/null 2>&1 || die "brew not on PATH after nous bootstrap — can't continue."
ensure_brew_writable   # re-check now that nous may have just installed brew

# ── helpers for the learner delta ────────────────────────────────────────────
# `set -o pipefail` makes a non-zero `brew list` (a deprecation warning, etc.)
# abort the pipeline even when grep matched — which would then run `brew install`
# on an already-installed package and error out. So query each package directly:
# `brew list <name>` exits 0 iff it's installed, making re-runs a clean no-op.
brew_tap() { local t; t="$(brew tap 2>/dev/null)" || true; printf '%s\n' "$t" | grep -qx "$1" || { info "tap $1"; brew tap "$1"; }; }
brew_get() { brew list --formula "$1" >/dev/null 2>&1 || { info "brew install $1"; brew install "$1"; }; }
cask_get() {
    if brew list --cask "$1" >/dev/null 2>&1; then return 0; fi   # already brew-managed
    info "brew install --cask $1"
    if brew install --cask "$1"; then return 0; fi
    # A pre-existing app (manual install or a leftover) makes brew error
    # "It seems there is already an App at /Applications/<name>.app". Adopt the
    # existing one if it's identical, else overwrite; never abort the whole run.
    warn "  $1: an existing app is in the way — adopting/overwriting it..."
    brew install --cask --adopt "$1" 2>/dev/null \
        || brew install --cask --force "$1" \
        || warn "  $1: still couldn't install (resolve manually); continuing."
}

# ── 5a. Terminal + editor delta (not in nous's Brewfile) ─────────────────────
step "Terminal + editor (cmux, pair, pandoc)"
brew_tap "manaflow-ai/cmux"; cask_get "cmux"   # GUI terminal she lives in
brew_tap "xianxu/pair";      brew_get "pair"   # two-pane agent+editor; brings neovim/zellij/fzf/jq
brew_get "pandoc"                              # parley.nvim HTML export (<C-g>eh)
ok "Terminal + editor tools installed."

# ── 5b. Shell: oh-my-zsh + a few quality-of-life plugins ─────────────────────
step "Shell (oh-my-zsh)"
ZSH_DIR="${ZSH:-$HOME/.oh-my-zsh}"
if [ -d "$ZSH_DIR" ]; then
    ok "oh-my-zsh already installed."
else
    info "Installing oh-my-zsh (unattended; keeps any existing ~/.zshrc)..."
    RUNZSH=no KEEP_ZSHRC=yes CHSH=no \
        sh -c "$(curl -fsSL https://raw.githubusercontent.com/ohmyzsh/ohmyzsh/master/tools/install.sh)" || \
        warn "oh-my-zsh install hiccupped; continuing (plugins are best-effort)."
fi
ZSH_CUSTOM="${ZSH_CUSTOM:-$ZSH_DIR/custom}"
clone_plugin() {  # <name> <repo>
    local dest="$ZSH_CUSTOM/plugins/$1"
    [ -d "$dest" ] && return 0
    info "  plugin: $1"
    git clone --depth 1 "$2" "$dest" >/dev/null 2>&1 || warn "  couldn't clone $1 (skip)."
}
if [ -d "$ZSH_DIR" ]; then
    clone_plugin zsh-autosuggestions      https://github.com/zsh-users/zsh-autosuggestions
    clone_plugin fast-syntax-highlighting https://github.com/zdharma-continuum/fast-syntax-highlighting
    clone_plugin fzf-tab                  https://github.com/Aloxaf/fzf-tab
fi

# ── 5c. Shell init file (aliases + PATH) — separate, sourced from ~/.zshrc ────
# Per the operator's request: keep the managed config in its own file so ~/.zshrc
# stays a one-liner she can read, and the learner block is easy to find + edit.
# Starter aliases only (v, s, …) — paste the rest from your real ~/.zshrc.
step "Shell init ($ENV_FILE)"
mkdir -p "$(dirname "$ENV_FILE")"
[ "$WORKSPACE" = "$HOME/workspace" ] || warn "WORKSPACE=$WORKSPACE differs from ~/workspace; adjust the paths in $ENV_FILE."
# Quoted heredoc (<<'EOF') — everything here is literal zsh config, so the
# functions/bindkeys land verbatim and $HOME/$1/$(tty) resolve at shell-runtime.
# Paths use $HOME/workspace (the convention), matching the operator's own rc.
cat > "$ENV_FILE" <<'EOF'
# init.zsh — learner environment, managed by bootstrap-mac.sh.
# Sourced from ~/.zshrc. Edit freely — this is your machine now.

# ── oh-my-zsh ────────────────────────────────────────────────────────────────
export ZSH="$HOME/.oh-my-zsh"
ZSH_THEME="robbyrussell"
plugins=(gitfast fzf-tab zsh-autosuggestions fast-syntax-highlighting)
[ -r "$ZSH/oh-my-zsh.sh" ] && source "$ZSH/oh-my-zsh.sh"

# ── PATH / tools ─────────────────────────────────────────────────────────────
[ -x /opt/homebrew/bin/brew ] && eval "$(/opt/homebrew/bin/brew shellenv)"
export PATH="$HOME/workspace/nous/bin:$PATH"        # the nous CLI
command -v zoxide >/dev/null 2>&1 && eval "$(zoxide init zsh)"   # 'z <partial>' smart cd

export EDITOR="nvim"
export VISUAL="nvim"

# ── editor ───────────────────────────────────────────────────────────────────
# zsh refuses to define a function whose name is already an alias (e.g. the tart
# VM rc — or an old dotfile — sets `alias v='${EDITOR}'`), failing with "defining
# function based on alias". Drop any such aliases first so our functions win.
unalias v p dir 2>/dev/null || true
# v — open nvim; supports file:line  (e.g. `v notes.md:42`)
v() {
    if [[ "$1" == *:* ]]; then
        nvim +"${1##*:}" "${1%:*}" "${@:2}"
    else
        nvim "$@"
    fi
}

# ── git ──────────────────────────────────────────────────────────────────────
alias s="git status"
alias a="git add"
alias d="git diff"
# p — commit everything (message optional), then push if there's a remote
p() {
    local has_remote
    has_remote=$(git remote | head -1)
    if [[ $# -gt 0 ]]; then
        git commit -a -m "$*"
    else
        git commit -a
    fi
    [[ -n "$has_remote" ]] && git push
}

# ── zellij (the multiplexer pair runs in) ────────────────────────────────────
alias zl="zellij list-sessions"
alias ze="zellij"
alias za="zellij a"
# pc — the daily driver: coding agent + editor in two panes
alias pc="pair claude"

# dir — list sub-directories (recursive), oldest-modified first
dir() {
    print -l ${1:-.}/**/*(/Om)
}

# ── shell editing ────────────────────────────────────────────────────────────
bindkey -v                            # vi-style line editing (transfers to nvim)
stty -ixon 2>/dev/null                # free ^S/^Q from terminal flow-control so...
bindkey '^R' history-incremental-search-backward
bindkey '^S' history-incremental-search-forward   # ...this ^S bind works (else it freezes the screen)

# GPG needs a tty for passphrase prompts (used once you enable encrypted brains)
export GPG_TTY=$(tty)

# Default to the prebuilt binaries (~/workspace/nous/bin) — fast, no Go rebuild
# on every call. Flip to NOUS_DEV=1 when you start *hacking on* the construct
# itself, for build-on-every-call dev functions (the contributor turn).
export NOUS_DEV=0
# ariadne-styled cmd → shell functions (sdlc, nous, …) when NOUS_DEV=1; the
# dev-aliases gate makes this a no-op when NOUS_DEV=0.
[ -r "$HOME/workspace/ariadne/construct/dev-aliases.sh" ] && \
    source <("$HOME/workspace/ariadne/construct/dev-aliases.sh")
EOF
ok "Wrote $ENV_FILE."

# Make ~/.zshrc source it (idempotent, marker-guarded).
ZSHRC="$HOME/.zshrc"; touch "$ZSHRC"
MARK="# >>> bootstrap-mac (learner env) >>>"
if ! grep -qF "$MARK" "$ZSHRC"; then
    info "Wiring ~/.zshrc to source the learner init file..."
    cat >> "$ZSHRC" <<EOF

$MARK
[ -r "$ENV_FILE" ] && source "$ENV_FILE"
# <<< bootstrap-mac (learner env) <<<
EOF
    ok "~/.zshrc updated."
else
    ok "~/.zshrc already sources the learner init file."
fi

# ── 5d. Editor config: neovim wired to parley.nvim ───────────────────────────
step "Editor config (parley.nvim)"
NVIM_INIT="$HOME/.config/nvim/init.lua"
if [ -f "$NVIM_INIT" ]; then
    ok "~/.config/nvim/init.lua exists — leaving it alone (won't clobber)."
    warn "  If parley isn't loading, add the xianxu/parley.nvim lazy spec yourself."
else
    info "Writing the minimal writing-focused init.lua (treesitter + parley + finder; first nvim launch installs the plugins)..."
    mkdir -p "$(dirname "$NVIM_INIT")"
    cat > "$NVIM_INIT" <<'LUA'
-- ~/.config/nvim/init.lua — minimal Neovim for the learner environment.
-- Tuned for writing (markdown / essays) with parley.nvim, plus light syntax
-- coloring + indentation for code (go / python / lua / …) via treesitter.
-- NO language server — just the editing essentials. One file on purpose: read
-- it top to bottom, change anything, it's yours to grow.

-- ── leader key (must be set before lazy loads) ───────────────────────────────
vim.g.mapleader = ' '
vim.g.maplocalleader = ' '

-- ── options ──────────────────────────────────────────────────────────────────
local o = vim.opt
o.number = true
o.relativenumber = true
o.wrap = true               -- prose: wrap long lines
o.linebreak = true          -- wrap at word boundaries, not mid-word
o.breakindent = true        -- wrapped lines keep their indent
o.showbreak = '↳ '          -- little marker where a line wrapped
o.expandtab = true          -- spaces, not tabs
o.shiftwidth = 4
o.tabstop = 4
o.softtabstop = 4
o.termguicolors = true      -- 24-bit color
o.ignorecase = true         -- search ignores case...
o.smartcase = true          -- ...unless you type a capital letter
o.clipboard = 'unnamedplus' -- share the system clipboard with other apps
o.spelllang = 'en_us'

-- ── autocommands ─────────────────────────────────────────────────────────────
-- Spell-check on for prose. Put the cursor on a word and press z= to fix it.
vim.api.nvim_create_autocmd('FileType', {
  pattern = { 'markdown', 'text', 'gitcommit' },
  callback = function() vim.opt_local.spell = true end,
})
-- Auto-save markdown as you write, so an essay is never lost mid-thought.
vim.api.nvim_create_autocmd({ 'InsertLeave', 'TextChanged' }, {
  pattern = '*.md',
  callback = function()
    if vim.bo.modified and vim.fn.expand('%') ~= '' and vim.bo.buftype == '' then
      vim.cmd('silent! write')
    end
  end,
})

-- ── keymaps ──────────────────────────────────────────────────────────────────
local map = vim.keymap.set
-- Arrow keys move by VISUAL line, so wrapped prose feels natural.
map({ 'n', 'v' }, '<Up>',   'gk', { silent = true })
map({ 'n', 'v' }, '<Down>', 'gj', { silent = true })
map('i', '<Up>',   '<C-o>gk', { silent = true })
map('i', '<Down>', '<C-o>gj', { silent = true })
-- Keep the selection after indenting with < / >.
map('v', '<', '<gv')
map('v', '>', '>gv')
-- Esc clears search highlight and closes any popup window.
map('n', '<Esc>', function()
  for _, win in ipairs(vim.api.nvim_list_wins()) do
    if vim.api.nvim_win_get_config(win).relative ~= '' then
      vim.api.nvim_win_close(win, false)
    end
  end
  vim.cmd('nohlsearch')
end, { silent = true })

-- ── lazy.nvim (plugin manager) self-bootstrap ───────────────────────────────
local lazypath = vim.fn.stdpath('data') .. '/lazy/lazy.nvim'
if not vim.loop.fs_stat(lazypath) then
  vim.fn.system({ 'git', 'clone', '--filter=blob:none',
    'https://github.com/folke/lazy.nvim.git', '--branch=stable', lazypath })
end
vim.opt.rtp:prepend(lazypath)

require('lazy').setup({
  -- Colorscheme.
  { 'bluz71/vim-moonfly-colors', name = 'moonfly', priority = 1000,
    config = function() vim.cmd.colorscheme('moonfly') end },

  -- Syntax coloring + indentation for many languages. NO language server —
  -- treesitter just understands a file's structure enough to color + indent it.
  { 'nvim-treesitter/nvim-treesitter', branch = 'master', build = ':TSUpdate',
    config = function()
      require('nvim-treesitter.configs').setup({
        ensure_installed = { 'markdown', 'markdown_inline', 'go', 'python',
                             'lua', 'json', 'bash', 'vim', 'vimdoc' },
        auto_install = true,
        highlight = { enable = true },
        indent = { enable = true, disable = { 'python' } }, -- TS python indent is flaky
      })
    end },

  -- parley.nvim — chat with LLMs inside a markdown buffer + the 🤖[] review
  -- markers. The heart of the writing-with-an-agent workflow.
  { 'xianxu/parley.nvim',
    config = function()
      require('parley').setup({
        -- Supply at least one provider key. Easiest: add a line to
        -- ~/.config/learner-env/init.zsh, e.g.  export ANTHROPIC_API_KEY=…
        -- (Sturdier: store it in the macOS Keychain and fetch with `security`
        -- — see parley.nvim's README, "macOS Keychain example".)
        api_keys = {
          anthropic = os.getenv('ANTHROPIC_API_KEY'),
          openai    = os.getenv('OPENAI_API_KEY'),
          googleai  = os.getenv('GOOGLEAI_API_KEY'),
        },
      })
    end },

  -- Press <space> and pause — a menu shows every keybinding. Your map of nvim.
  { 'folke/which-key.nvim', event = 'VeryLazy', opts = {} },

  -- Fuzzy finder. <leader>ff = find files, <leader>fg = search inside files,
  -- <leader>fb = switch between open buffers.
  { 'nvim-telescope/telescope.nvim', branch = '0.1.x',
    dependencies = { 'nvim-lua/plenary.nvim' },
    config = function()
      local t = require('telescope.builtin')
      map('n', '<leader>ff', t.find_files, { desc = 'Find files' })
      map('n', '<leader>fg', t.live_grep,  { desc = 'Search in files' })
      map('n', '<leader>fb', t.buffers,    { desc = 'Open buffers' })
    end },

  -- Edit your filesystem like a buffer (rename/move/delete by editing text).
  -- `-` opens the parent folder; <leader>fo opens it too.
  { 'stevearc/oil.nvim',
    dependencies = { 'nvim-tree/nvim-web-devicons' },
    config = function()
      require('oil').setup({ view_options = { show_hidden = false } })
      map('n', '<leader>fo', '<cmd>Oil<cr>', { desc = 'Open file manager' })
    end },

  -- Surround text: cs"' (change), ds( (delete), ysiw) (add). Handy for editing.
  { 'kylechui/nvim-surround', event = 'VeryLazy', opts = {} },

  -- Toggle comments: gcc on a line, gc over a selection.
  { 'numToStr/Comment.nvim', opts = {} },

  -- Auto-detect a file's indent width (tabs vs spaces, and how many).
  'tpope/vim-sleuth',

  -- Statusline at the bottom (mode, file, git branch, position).
  { 'nvim-lualine/lualine.nvim',
    dependencies = { 'nvim-tree/nvim-web-devicons' },
    opts = { options = { globalstatus = true } } },

  -- Git changes in the gutter; <leader>h previews the change under the cursor.
  { 'lewis6991/gitsigns.nvim',
    config = function()
      require('gitsigns').setup()
      map('n', '<leader>h', function() require('gitsigns').preview_hunk() end,
        { desc = 'Preview git hunk' })
    end },

  -- Auto-close brackets and quotes as you type.
  { 'windwp/nvim-autopairs', event = 'InsertEnter', opts = {} },
}, {
  ui = { border = 'rounded' },
  change_detection = { notify = false },
})
LUA
    ok "Wrote $NVIM_INIT."
fi

# ── 5e. Keyboard layout: Option key → Alt (no dead-key accents) ──────────────
# macOS's stock U.S. layout turns Option+key into dead-key accents (Option+e → ´,
# Option+n → ˜), which swallows the Alt bindings the learner relies on — pair's
# Alt+Return, zellij, nvim Alt+hjkl. This bundled layout makes Option behave as a
# plain Alt. Installed as a bare .keylayout (macOS reads it directly). NOTE: macOS
# only scans for new layouts at LOGIN, and you still enable it in Input Sources.
step "Keyboard layout (Option → Alt, no dead-key accents)"
KBD_SRC="$NOUS_DIR/assets/keyboard/US-No-Dead-Letter.keylayout"
KBD_DST_DIR="$HOME/Library/Keyboard Layouts"
KBD_DST="$KBD_DST_DIR/US-No-Dead-Letter.keylayout"
KBD_URL="https://raw.githubusercontent.com/xianxu/nous/main/assets/keyboard/US-No-Dead-Letter.keylayout"
mkdir -p "$KBD_DST_DIR"
# Prefer the cloned copy; fall back to a direct download — an older nous clone
# (or the VM mirror) may predate this asset.
if [ -f "$KBD_SRC" ]; then
    cp -f "$KBD_SRC" "$KBD_DST"
else
    curl -fsSL "$KBD_URL" -o "$KBD_DST" 2>/dev/null || true
fi
if [ -f "$KBD_DST" ]; then
    ok "Installed the 'U.S. No Dead Letter' keyboard layout."
    info "  Turn it on: log out and back in, then System Settings → Keyboard →"
    info "  Input Sources → ＋ → English → 'U.S. No Dead Letter' → Add, then pick it"
    info "  in the top-right input menu. Option+key then sends Alt, not ´˜¨ accents."
else
    warn "Keyboard layout couldn't be installed (no clone copy + download failed); skipping."
fi

# ── 6. (optional) provision a local brain ────────────────────────────────────
if [ -n "$CREATE_BRAIN" ]; then
    step "Brain ($CREATE_BRAIN)"
    BRAIN_PATH="$WORKSPACE/$CREATE_BRAIN"
    if [ -f "$BRAIN_PATH/.brain/config.md" ]; then
        ok "Brain already exists at $BRAIN_PATH."
    else
        info "Provisioning local brain at $BRAIN_PATH ..."
        "$NOUS_DIR/bin/nous" brain new "$BRAIN_PATH" || \
            warn "Brain provisioning needs attention — finish with: nous brain"
    fi
fi

# ── 7. Verify + next steps ───────────────────────────────────────────────────
step "Verify"
check() {  # <cmd> [version-args...]
    local cmd="$1"; shift
    if command -v "$cmd" >/dev/null 2>&1; then
        ok "$cmd → $("$cmd" "${@:---version}" 2>/dev/null | head -1)"
    else
        warn "$cmd not on PATH (open a fresh shell?)"
    fi
}
check brew
check go version
check nvim
check claude
check pair
[ -x "$NOUS_DIR/bin/nous" ] && ok "nous → $NOUS_DIR/bin/nous" || warn "nous binary missing at $NOUS_DIR/bin/nous"
brew list --cask 2>/dev/null | grep -qx cmux && ok "cmux installed (open from /Applications or Spotlight)" || warn "cmux cask not found"
ssh-add -l >/dev/null 2>&1 && ok "SSH key loaded in agent." || warn "SSH key not in agent (run: ssh-add --apple-use-keychain $SSH_KEY)"

cat >&2 <<EOF

${GREEN}${BOLD}Done.${RESET} Learner environment is up.

${CYAN}Next steps${RESET}
  1. Open a ${BOLD}fresh terminal${RESET} (or: exec zsh) so PATH + aliases load.
  2. Set your API key in ${BOLD}$ENV_FILE${RESET}, e.g.:
       ${BOLD}export ANTHROPIC_API_KEY=...${RESET}    (for parley.nvim in the editor)
     Then run ${BOLD}claude${RESET} once to log in to Claude Code (browser OAuth).
  3. Create your first brain (your writing workbench):
       ${BOLD}nous brain${RESET}      (TUI — press 'n' for a new local brain)
  4. Launch the two-pane writing setup inside it:
       ${BOLD}cd \$(your brain dir) && pc${RESET}     (alias for: pair claude)
  5. Try the editor: ${BOLD}v a-note.md${RESET}  (first launch installs parley.nvim)

You're plugged in. Anything that stops you is a question away.
EOF

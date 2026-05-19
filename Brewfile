# Brewfile — nous developer toolchain.
# Source of truth for `make nous-bootstrap`.
# Apply manually with: brew bundle install --file=Brewfile
# Scope rationale: workshop/issues/000011-nous-bootstrap.md.

tap "mutagen-io/mutagen"
tap "xianxu/pair"

# ── Core (nous-required) ─────────────────────────────────────────────────────
brew "go"
brew "gh"
brew "gnupg"
brew "pinentry-mac"   # GUI dialog with macOS Keychain integration (default for Aqua sessions)
brew "pinentry"       # curses/tty variants for SSH/headless contexts
brew "git-remote-gcrypt"
brew "terminal-notifier"  # preferred dev fallback for lib/notify; supports actions vs osascript
brew "mutagen-io/mutagen/mutagen"
brew "xianxu/pair/pair"

# ── Dev runtimes ─────────────────────────────────────────────────────────────
brew "deno"
brew "lua@5.4"
brew "luarocks"
brew "node"

# ── Daily CLI ────────────────────────────────────────────────────────────────
brew "ripgrep"
brew "fzf"
brew "bat"
brew "zoxide"
brew "tree"
brew "watch"
brew "glow"

# ── Casks ────────────────────────────────────────────────────────────────────
cask "claude-code"
cask "font-hack-nerd-font"

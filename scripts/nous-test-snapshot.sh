#!/usr/bin/env bash
# scripts/nous-test-snapshot.sh — build the `tahoe-bootstrapped` tart VM.
#
# Boots a clone of `tahoe-base`, runs `nous-bootstrap.sh` end-to-end, cleans
# up nous source + GPG state, gracefully shuts down. The result is a stopped
# VM named `tahoe-bootstrapped` that `make nous-test-roundtrip` clones for
# fast tests (~25s vs ~2:30 for the full cold-start path).
#
# Run when Brewfile or scripts/nous-bootstrap.sh changes.
#
# Used by `make nous-test-snapshot` (Makefile.nous).
#
# Spec: workshop/issues/000011-nous-bootstrap.md M3.

set -euo pipefail

# ── Colors ───────────────────────────────────────────────────────────────────
RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; CYAN=$'\033[0;36m'; RESET=$'\033[0m'
info() { printf "%s==>%s %s\n" "$CYAN" "$RESET" "$*" >&2; }
ok()   { printf "%s  [ok]%s %s\n" "$GREEN" "$RESET" "$*" >&2; }
warn() { printf "%s  [!]%s %s\n" "$YELLOW" "$RESET" "$*" >&2; }
die()  { printf "%serror:%s %s\n" "$RED" "$RESET" "$*" >&2; exit 1; }

NOUS_DIR=$(cd "$(dirname "$0")/.." && pwd)
SNAPSHOT_VM="tahoe-bootstrapped"
BASE_VM="tahoe-base"
SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o PreferredAuthentications=password -o PubkeyAuthentication=no"
VM_USER="admin"
VM_PASS="admin"
VM_RUN_PID=""
VM_IP=""
BUILD_FAILED=1   # flips to 0 only on success — cleanup uses to decide whether to delete partial snapshot

# ── Cleanup trap ─────────────────────────────────────────────────────────────
# On success: stop the VM, leave it. The stopped VM IS the artifact.
# On failure: delete the partial snapshot so the next attempt starts clean.
cleanup() {
    info "Stopping VM..."
    tart stop "$SNAPSHOT_VM" >/dev/null 2>&1 || true
    [ -n "$VM_RUN_PID" ] && kill "$VM_RUN_PID" 2>/dev/null || true
    pkill -f "tart run $SNAPSHOT_VM" 2>/dev/null || true
    sleep 1
    if [ "$BUILD_FAILED" -eq 1 ]; then
        warn "Build failed — removing partial snapshot."
        tart delete "$SNAPSHOT_VM" >/dev/null 2>&1 || true
    else
        ok "VM stopped (snapshot retained)."
    fi
}
trap cleanup EXIT

# ── Pre-flight ───────────────────────────────────────────────────────────────
info "Pre-flight..."
command -v tart >/dev/null || die "tart not installed. brew install cirruslabs/cli/tart"
if ! command -v sshpass >/dev/null; then
    info "Installing sshpass..."
    brew install hudochenkov/sshpass/sshpass
fi
tart list 2>/dev/null | grep -q "^local.*$BASE_VM" \
    || die "Base image '$BASE_VM' not found. Run: tart clone ghcr.io/cirruslabs/macos-tahoe-base:latest $BASE_VM"
ok "Pre-flight passed."

# ── Remove any existing snapshot ─────────────────────────────────────────────
if tart list 2>/dev/null | grep -q "^local.*$SNAPSHOT_VM" \
   || pgrep -f "tart run $SNAPSHOT_VM" >/dev/null; then
    info "Removing existing $SNAPSHOT_VM..."
    tart stop "$SNAPSHOT_VM" >/dev/null 2>&1 || true
    pkill -f "tart run $SNAPSHOT_VM" 2>/dev/null || true
    sleep 1
    tart delete "$SNAPSHOT_VM" >/dev/null 2>&1 || true
fi

# ── Clone + boot ─────────────────────────────────────────────────────────────
info "Cloning $BASE_VM → $SNAPSHOT_VM..."
tart clone "$BASE_VM" "$SNAPSHOT_VM"

info "Booting (background)..."
tart run "$SNAPSHOT_VM" >/tmp/nous-snapshot-vm.log 2>&1 &
VM_RUN_PID=$!

info "Waiting for VM IP..."
for _ in {1..60}; do
    VM_IP=$(tart ip "$SNAPSHOT_VM" 2>/dev/null || true)
    [ -n "$VM_IP" ] && break
    sleep 2
done
[ -n "$VM_IP" ] || die "VM did not get IP within 120s."
ok "VM IP: $VM_IP"

info "Waiting for SSH..."
# Two consecutive successful logins — see nous-test-bootstrap.sh.
SUCCESSES=0
for _ in {1..120}; do
    if sshpass -p "$VM_PASS" ssh $SSH_OPTS -o ConnectTimeout=3 "$VM_USER@$VM_IP" "echo ready" >/dev/null 2>&1; then
        SUCCESSES=$((SUCCESSES + 1))
        [ $SUCCESSES -ge 2 ] && break
    else
        SUCCESSES=0
    fi
    sleep 1
done
[ "$SUCCESSES" -ge 2 ] || die "sshd never accepted two consecutive logins."
ok "SSH responding."

# ── Rsync nous source into VM ────────────────────────────────────────────────
info "Copying nous source..."
for attempt in 1 2 3; do
    sshpass -p "$VM_PASS" rsync -e "ssh $SSH_OPTS" -az --delete \
        --exclude='.git/' --exclude='cmd/*/bin/' --exclude='.openshell/sandbox-state/' \
        "$NOUS_DIR/" "$VM_USER@$VM_IP:/Users/$VM_USER/nous/" >/dev/null && break
    [ $attempt -eq 3 ] && die "rsync failed after 3 attempts."
    warn "rsync attempt $attempt failed, retrying in 5s..."
    sleep 5
done
ok "Source copied."

# ── Run bootstrap + clean up state ───────────────────────────────────────────
# Wrapped in a function so we can retry if sshd flakes on auth.
run_vmscript() {
sshpass -p "$VM_PASS" ssh $SSH_OPTS "$VM_USER@$VM_IP" 'bash -s' <<'VMSCRIPT'
set -euo pipefail
cd ~/nous
NONINTERACTIVE=1 NOUS_BOOTSTRAP_SKIP_IDENTITY=1 NOUS_BOOTSTRAP_SKIP_OPENSHELL=1 \
    ./scripts/nous-bootstrap.sh < /dev/null

# Strip artifacts that should not bake into the snapshot:
#   - nous source: round-trip test rsyncs fresh
#   - .gnupg: round-trip test imports its own test key
#   - shell history, brew downloads cache: hygiene
rm -rf ~/nous ~/.gnupg ~/.bash_history ~/.zsh_history
brew cleanup -s 2>/dev/null || true

# Flush filesystem caches so the snapshot disk image is consistent.
sync
VMSCRIPT
}

info "Running nous-bootstrap.sh inside VM (this is the slow part — Brewfile install)..."
for attempt in 1 2 3; do
    if run_vmscript; then break; fi
    [ $attempt -eq 3 ] && die "ssh bootstrap failed after 3 attempts."
    warn "ssh bootstrap attempt $attempt failed, retrying in 5s..."
    sleep 5
done
ok "Bootstrap applied. Snapshot footprint cleaned."

# ── Graceful shutdown ────────────────────────────────────────────────────────
# Cirruslabs admin has passwordless sudo. Disconnect happens mid-shutdown — fine.
info "Shutting down VM cleanly..."
sshpass -p "$VM_PASS" ssh $SSH_OPTS "$VM_USER@$VM_IP" "sudo -n shutdown -h now" >/dev/null 2>&1 || true

# Wait for the tart-run process to exit on its own (VM powered off).
for _ in {1..30}; do
    kill -0 "$VM_RUN_PID" 2>/dev/null || break
    sleep 2
done
ok "VM powered off."

# ── Done — mark success so cleanup trap retains the snapshot ─────────────────
BUILD_FAILED=0
echo
ok "Snapshot '$SNAPSHOT_VM' ready."
ok "Use 'make nous-test-roundtrip' for fast tests (~25s)."

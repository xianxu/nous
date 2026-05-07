#!/usr/bin/env bash
# scripts/nous-test-roundtrip.sh — fast gcrypt round-trip test on the
# pre-bootstrapped snapshot.
#
# Clones `tahoe-bootstrapped` (built once via `make nous-test-snapshot`),
# imports the throwaway test GPG key, and runs a gcrypt push+clone round-trip.
# Skips the ~125s Brewfile install — runs in ~25s end-to-end.
#
# When to use this vs `make nous-test-bootstrap`:
#   - Iterating on round-trip / pinentry / gcrypt config:  nous-test-roundtrip
#   - Verified Brewfile or nous-bootstrap.sh changes:      nous-test-bootstrap
#
# Used by `make nous-test-roundtrip` (Makefile.nous).
#
# Spec: workshop/issues/000011-nous-bootstrap.md M3.

set -euo pipefail

# ── Colors ───────────────────────────────────────────────────────────────────
RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; CYAN=$'\033[0;36m'; MAGENTA=$'\033[0;35m'; RESET=$'\033[0m'
info() { printf "%s==>%s %s\n" "$CYAN" "$RESET" "$*" >&2; }
ok()   { printf "%s  [ok]%s %s\n" "$GREEN" "$RESET" "$*" >&2; }
warn() { printf "%s  [!]%s %s\n" "$YELLOW" "$RESET" "$*" >&2; }
die()  { printf "%serror:%s %s\n" "$RED" "$RESET" "$*" >&2; exit 1; }

# Phase timing
T_START=$(date +%s)
T_PREV=$T_START
phase() {
    local now=$(date +%s)
    printf "%s  [%3ds]%s %s\n" "$MAGENTA" "$((now - T_PREV))" "$RESET" "$1" >&2
    T_PREV=$now
}

NOUS_DIR=$(cd "$(dirname "$0")/.." && pwd)
TESTDATA="$NOUS_DIR/testdata/test-bootstrap"
VM_NAME="nous-roundtrip-test"
SNAPSHOT_VM="tahoe-bootstrapped"
SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o PreferredAuthentications=password -o PubkeyAuthentication=no"
VM_USER="admin"
VM_PASS="admin"
VM_RUN_PID=""

# ── Cleanup trap ─────────────────────────────────────────────────────────────
cleanup() {
    info "Tearing down VM..."
    tart stop "$VM_NAME" >/dev/null 2>&1 || true
    [ -n "$VM_RUN_PID" ] && kill "$VM_RUN_PID" 2>/dev/null || true
    pkill -f "tart run $VM_NAME" 2>/dev/null || true
    sleep 1
    tart delete "$VM_NAME" >/dev/null 2>&1 || true
    ok "VM torn down."
}
trap cleanup EXIT

# ── Pre-flight ───────────────────────────────────────────────────────────────
info "Pre-flight..."
command -v tart >/dev/null || die "tart not installed."
command -v sshpass >/dev/null || die "sshpass not installed. Run 'make nous-test-bootstrap' once or 'brew install hudochenkov/sshpass/sshpass'."
tart list 2>/dev/null | grep -q "^local.*$SNAPSHOT_VM" \
    || die "Snapshot '$SNAPSHOT_VM' not found. Run 'make nous-test-snapshot' first."
[ -d "$TESTDATA" ] || die "Test data not found: $TESTDATA"

# Staleness check: warn if snapshot predates Brewfile / nous-bootstrap.sh.
SNAPSHOT_DIR="$HOME/.tart/vms/$SNAPSHOT_VM"
if [ -d "$SNAPSHOT_DIR" ]; then
    snap_mtime=$(stat -f %m "$SNAPSHOT_DIR")
    brewfile_mtime=$(stat -f %m "$NOUS_DIR/Brewfile" 2>/dev/null || echo 0)
    bootstrap_mtime=$(stat -f %m "$NOUS_DIR/scripts/nous-bootstrap.sh" 2>/dev/null || echo 0)
    newer=$brewfile_mtime
    [ "$bootstrap_mtime" -gt "$newer" ] && newer=$bootstrap_mtime
    if [ "$snap_mtime" -lt "$newer" ]; then
        warn "Snapshot is older than Brewfile/nous-bootstrap.sh."
        warn "Rebuild via 'make nous-test-snapshot' for an accurate test."
    fi
fi
ok "Pre-flight passed."
phase "pre-flight"

# ── Clean prior VM ───────────────────────────────────────────────────────────
if tart list 2>/dev/null | grep -q "^local.*$VM_NAME" \
   || pgrep -f "tart run $VM_NAME" >/dev/null; then
    info "Removing prior VM '$VM_NAME'..."
    tart stop "$VM_NAME" >/dev/null 2>&1 || true
    pkill -f "tart run $VM_NAME" 2>/dev/null || true
    sleep 1
    tart delete "$VM_NAME" >/dev/null 2>&1 || true
fi
phase "clean prior VM"

# ── Clone + boot from snapshot ───────────────────────────────────────────────
info "Cloning $SNAPSHOT_VM → $VM_NAME..."
tart clone "$SNAPSHOT_VM" "$VM_NAME"
phase "tart clone"

info "Booting..."
tart run "$VM_NAME" >/tmp/nous-roundtrip-vm.log 2>&1 &
VM_RUN_PID=$!

info "Waiting for VM IP..."
VM_IP=""
for _ in {1..60}; do
    VM_IP=$(tart ip "$VM_NAME" 2>/dev/null || true)
    [ -n "$VM_IP" ] && break
    sleep 2
done
[ -n "$VM_IP" ] || die "VM did not get IP."
ok "VM IP: $VM_IP"
phase "VM boot (until IP)"

info "Waiting for SSH..."
# Two consecutive successful logins — see nous-test-bootstrap.sh for the
# rationale behind this rather than a blanket settle sleep.
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
phase "SSH ready + settle"

# ── Copy fixtures ────────────────────────────────────────────────────────────
# (nous source not needed — snapshot already has Brewfile/identity.sh applied
# and round-trip doesn't re-run nous-bootstrap.sh.)
info "Copying test fixtures..."
for attempt in 1 2 3; do
    sshpass -p "$VM_PASS" scp $SSH_OPTS -r "$TESTDATA" "$VM_USER@$VM_IP:/Users/$VM_USER/test-bootstrap" >/dev/null && break
    [ $attempt -eq 3 ] && die "scp failed after 3 attempts."
    warn "scp attempt $attempt failed, retrying in 5s..."
    sleep 5
done
ok "Fixtures copied."
phase "scp"

# ── GPG setup + round-trip ───────────────────────────────────────────────────
# Wrapped in a function so we can retry if sshd flakes on auth.
run_vmscript() {
sshpass -p "$VM_PASS" ssh $SSH_OPTS "$VM_USER@$VM_IP" 'bash -s' <<'VMSCRIPT'
set -euo pipefail
trap 'echo "VM-INSIDE: aborted at line $LINENO" >&2' ERR

eval "$(/opt/homebrew/bin/brew shellenv)"

# Set up a custom pinentry that always returns the test passphrase, so gcrypt
# decryption is non-interactive. Only safe in a disposable test VM.
mkdir -p ~/.gnupg && chmod 700 ~/.gnupg
PASS=$(cat ~/test-bootstrap/test-key.passphrase)
cat > /tmp/pinentry-test <<PINENTRY
#!/bin/bash
echo "OK"
while IFS= read -r cmd; do
  case "\$cmd" in
    GETPIN) echo "D $PASS"; echo "OK" ;;
    *)      echo "OK" ;;
  esac
done
PINENTRY
chmod +x /tmp/pinentry-test
cat > ~/.gnupg/gpg-agent.conf <<AGENT
pinentry-program /tmp/pinentry-test
default-cache-ttl 3600
max-cache-ttl 7200
AGENT
gpgconf --kill gpg-agent 2>/dev/null || true

gpg --batch --import ~/test-bootstrap/test-key.asc
FP=$(cat ~/test-bootstrap/test-key.fingerprint)
echo -e "trust\n5\ny\nquit\n" | gpg --command-fd 0 --batch --yes --edit-key "$FP" trust quit \
    >/dev/null 2>&1 || true

# Local bare gcrypt remote (encrypt target).
rm -rf /tmp/test-brain.git /tmp/test-work /tmp/test-clone
mkdir -p /tmp/test-brain.git
(cd /tmp/test-brain.git && git init --bare -q)

# Working repo: commit, configure gcrypt participants, push (encrypts).
mkdir -p /tmp/test-work
cd /tmp/test-work
git init -q -b main
git config user.email test@nous.local
git config user.name "Nous Bootstrap Test"
MARKER="round-trip-$(date -u +%s)-$RANDOM"
echo "$MARKER" > marker.txt
git add marker.txt
git commit -q -m "test marker"
git config gcrypt.participants "$FP"
git remote add origin gcrypt::file:///tmp/test-brain.git
git push origin main

# Fresh clone (decrypts).
cd /tmp
git clone gcrypt::file:///tmp/test-brain.git test-clone

# Verify decrypted content matches.
GOT=$(cat /tmp/test-clone/marker.txt)
if [ "$GOT" = "$MARKER" ]; then
    echo "ROUND-TRIP-OK marker=$MARKER"
else
    echo "ROUND-TRIP-FAIL expected=$MARKER got=$GOT"
    exit 1
fi
VMSCRIPT
}

info "Running round-trip..."
for attempt in 1 2 3; do
    if run_vmscript; then break; fi
    [ $attempt -eq 3 ] && die "ssh round-trip failed after 3 attempts."
    warn "ssh round-trip attempt $attempt failed, retrying in 5s..."
    sleep 5
done
phase "round-trip in VM"

ok "Round-trip succeeded."
echo
total=$(($(date +%s) - T_START))
printf "%s  [%3ds]%s %sTOTAL%s\n" "$MAGENTA" "$total" "$RESET" "$MAGENTA" "$RESET" >&2
ok "TEST PASSED"

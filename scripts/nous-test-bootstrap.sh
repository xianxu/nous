#!/usr/bin/env bash
# scripts/nous-test-bootstrap.sh — automated VM-based test of nous-bootstrap.sh.
#
# Drives a fresh tart VM through bootstrap + a gcrypt round-trip end-to-end,
# using a throwaway GPG identity from testdata/test-bootstrap/.
#
# Used by `make nous-test-bootstrap` (Makefile.nous).
#
# Always recreates the VM from `tahoe-base`. Always tears down on exit.
#
# Prereqs (host):
#   - tart installed (brew install cirruslabs/cli/tart)
#   - tahoe-base VM present locally (tart clone ghcr.io/cirruslabs/macos-tahoe-base:latest tahoe-base)
#   - sshpass installed (auto-installed if missing via Homebrew)
#
# Spec: workshop/issues/000011-nous-bootstrap.md M2.

set -euo pipefail

# ── Colors + timing ──────────────────────────────────────────────────────────
RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; CYAN=$'\033[0;36m'; MAGENTA=$'\033[0;35m'; RESET=$'\033[0m'
info() { printf "%s==>%s %s\n" "$CYAN" "$RESET" "$*" >&2; }
ok()   { printf "%s  [ok]%s %s\n" "$GREEN" "$RESET" "$*" >&2; }
warn() { printf "%s  [!]%s %s\n" "$YELLOW" "$RESET" "$*" >&2; }
die()  { printf "%serror:%s %s\n" "$RED" "$RESET" "$*" >&2; exit 1; }

# Phase timing — `phase <name>` prints elapsed since previous `phase` call.
T_START=$(date +%s)
T_PREV=$T_START
declare -a PHASE_NAMES PHASE_DURATIONS
phase() {
    local now=$(date +%s)
    local elapsed=$((now - T_PREV))
    PHASE_NAMES+=("$1")
    PHASE_DURATIONS+=("$elapsed")
    printf "%s  [%3ds]%s %s\n" "$MAGENTA" "$elapsed" "$RESET" "$1" >&2
    T_PREV=$now
}
print_timing_summary() {
    local total=$(($(date +%s) - T_START))
    echo >&2
    printf "%s── timing summary ──%s\n" "$MAGENTA" "$RESET" >&2
    local i
    for i in "${!PHASE_NAMES[@]}"; do
        printf "  %4ds  %s\n" "${PHASE_DURATIONS[$i]}" "${PHASE_NAMES[$i]}" >&2
    done
    printf "  %4ds  %sTOTAL%s\n" "$total" "$MAGENTA" "$RESET" >&2
}

NOUS_DIR=$(cd "$(dirname "$0")/.." && pwd)
TESTDATA="$NOUS_DIR/testdata/test-bootstrap"
VM_NAME="nous-bootstrap-test"
BASE_VM="tahoe-base"
# Force password-only auth: host's ssh-agent often holds many keys; sshd's
# default MaxAuthTries=6 rejects the connection after ssh tries them all
# before falling back to the password sshpass provides.
SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o PreferredAuthentications=password -o PubkeyAuthentication=no"
VM_USER="admin"
VM_PASS="admin"
VM_RUN_PID=""

# ── Cleanup trap ─────────────────────────────────────────────────────────────
# `tart stop` doesn't always reap the host-side `tart run` process fast enough,
# and macOS Virtualization.framework caps the number of live VMs. Belt-and-
# suspenders: stop, kill the run process, sleep briefly, delete.
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
info "Pre-flight checks..."
command -v tart >/dev/null || die "tart not installed. brew install cirruslabs/cli/tart"
if ! command -v sshpass >/dev/null; then
    info "Installing sshpass..."
    brew install hudochenkov/sshpass/sshpass
fi
tart list 2>/dev/null | grep -q "^local.*$BASE_VM" \
    || die "Base image '$BASE_VM' not found locally. Run: tart clone ghcr.io/cirruslabs/macos-tahoe-base:latest $BASE_VM"
[ -d "$TESTDATA" ] || die "Test data not found: $TESTDATA"
[ -f "$TESTDATA/test-key.asc" ] || die "Test key not found: $TESTDATA/test-key.asc"
[ -f "$TESTDATA/test-key.fingerprint" ] || die "Test fingerprint not found: $TESTDATA/test-key.fingerprint"
[ -f "$TESTDATA/test-key.passphrase" ] || die "Test passphrase not found: $TESTDATA/test-key.passphrase"
ok "Pre-flight passed."
phase "pre-flight"

# ── Clean prior VM ───────────────────────────────────────────────────────────
# Also kill any lingering `tart run $VM_NAME` process, which can hold a slot
# in macOS's per-host VM count even after `tart stop` returns.
if tart list 2>/dev/null | grep -q "^local.*$VM_NAME" \
   || pgrep -f "tart run $VM_NAME" >/dev/null; then
    info "Removing prior VM '$VM_NAME'..."
    tart stop "$VM_NAME" >/dev/null 2>&1 || true
    pkill -f "tart run $VM_NAME" 2>/dev/null || true
    sleep 1
    tart delete "$VM_NAME" >/dev/null 2>&1 || true
fi
phase "clean prior VM"

# ── Clone + boot VM ──────────────────────────────────────────────────────────
info "Cloning $BASE_VM → $VM_NAME..."
tart clone "$BASE_VM" "$VM_NAME"
phase "tart clone"

info "Booting VM (background)..."
tart run "$VM_NAME" >/tmp/nous-bootstrap-test-vm.log 2>&1 &
VM_RUN_PID=$!

info "Waiting for VM IP (up to 120s)..."
VM_IP=""
for _ in {1..60}; do
    VM_IP=$(tart ip "$VM_NAME" 2>/dev/null || true)
    [ -n "$VM_IP" ] && break
    sleep 2
done
[ -n "$VM_IP" ] || die "VM did not get an IP within 120s."
ok "VM IP: $VM_IP"
phase "VM boot (until IP)"

info "Waiting for SSH..."
# Wait for TWO consecutive successful ssh logins. One success isn't enough on
# freshly-booted tahoe — the *next* connection (rsync) can get spuriously
# rejected. Requiring two-in-a-row directly tests the condition we need and
# avoids a blanket sleep.
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

# ── Copy nous source + fixtures ──────────────────────────────────────────────
# rsync the local tree (not git clone) so the test exercises in-progress local
# changes, not whatever happens to be on origin/main.
info "Copying local nous source + test fixtures into VM..."
# Retry both transfers — freshly-booted sshd occasionally rejects the password
# on the second connection even when the first succeeds.
rsync_attempt() {
    sshpass -p "$VM_PASS" rsync -e "ssh $SSH_OPTS" -az --delete \
        --exclude='.git/' --exclude='cmd/*/bin/' --exclude='.openshell/sandbox-state/' \
        "$NOUS_DIR/" "$VM_USER@$VM_IP:/Users/$VM_USER/nous/" >/dev/null
}
scp_attempt() {
    sshpass -p "$VM_PASS" scp $SSH_OPTS -r "$TESTDATA" "$VM_USER@$VM_IP:/Users/$VM_USER/test-bootstrap" >/dev/null
}
for attempt in 1 2 3; do
    if rsync_attempt; then break; fi
    [ $attempt -eq 3 ] && die "rsync failed after 3 attempts."
    warn "rsync attempt $attempt failed, retrying in 5s..."
    sleep 5
done
for attempt in 1 2 3; do
    if scp_attempt; then break; fi
    [ $attempt -eq 3 ] && die "scp failed after 3 attempts."
    warn "scp attempt $attempt failed, retrying in 5s..."
    sleep 5
done
ok "Source + fixtures copied."
phase "rsync + scp"

# ── Drive bootstrap + round-trip in VM ───────────────────────────────────────
info "Running bootstrap + round-trip test in VM (this can take a few minutes)..."
sshpass -p "$VM_PASS" ssh $SSH_OPTS "$VM_USER@$VM_IP" 'bash -s' <<'VMSCRIPT'
set -euo pipefail
trap 'echo "VM-INSIDE: aborted at line $LINENO" >&2' ERR

VM_T0=$(date +%s)
vm_phase() { local now=$(date +%s); echo "VM-PHASE $((now - VM_T0))s $1" >&2; VM_T0=$now; }

cd ~/nous

# Run the bootstrap with both interactive layers skipped:
#   - NOUS_BOOTSTRAP_SKIP_IDENTITY=1: identity.sh would prompt for name/email
#     when generating a fresh key; the test imports a pre-made key instead.
#   - NOUS_BOOTSTRAP_SKIP_OPENSHELL=1: .openshell bootstrap calls `gh auth
#     login` interactively; not relevant to what the test validates.
#
# `< /dev/null`: the Homebrew install script (and possibly other tools in the
# chain) reads from stdin; without this redirect, it would consume the rest
# of this heredoc, leaving the round-trip section unrun (and bash exits 0
# silently when stdin EOFs).
NONINTERACTIVE=1 NOUS_BOOTSTRAP_SKIP_IDENTITY=1 NOUS_BOOTSTRAP_SKIP_OPENSHELL=1 \
    ./scripts/nous-bootstrap.sh < /dev/null
vm_phase "nous-bootstrap.sh (substrate + brewfile + fzf)"

eval "$(/opt/homebrew/bin/brew shellenv)"

# Set up GPG environment (test-only — replaces what identity.sh would have done).
mkdir -p ~/.gnupg && chmod 700 ~/.gnupg

# Custom pinentry that always returns the test passphrase, so gcrypt
# decryption is non-interactive. Only safe in a disposable test VM.
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

# Mark ultimately trusted (no untrusted-recipient warnings).
echo -e "trust\n5\ny\nquit\n" | gpg --command-fd 0 --batch --yes --edit-key "$FP" trust quit \
    >/dev/null 2>&1 || true
vm_phase "GPG setup (pinentry + import + trust)"

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
vm_phase "gcrypt push (encrypt)"

# Fresh clone (decrypts).
cd /tmp
git clone gcrypt::file:///tmp/test-brain.git test-clone
vm_phase "gcrypt clone (decrypt)"

# Verify decrypted content matches.
GOT=$(cat /tmp/test-clone/marker.txt)
if [ "$GOT" = "$MARKER" ]; then
    echo "ROUND-TRIP-OK marker=$MARKER"
else
    echo "ROUND-TRIP-FAIL expected=$MARKER got=$GOT"
    exit 1
fi
VMSCRIPT

phase "VM-INSIDE total"
ok "Bootstrap + round-trip succeeded in VM."

print_timing_summary
echo
ok "TEST PASSED"

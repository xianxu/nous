#!/usr/bin/env bash
# scripts/test-brain-sync.sh — local integration test for brain-sync.
#
# Sets up a local bare repo + two clones (peerA, peerB), starts two
# brain-sync daemons (one per clone), exercises:
#   1. Basic propagation: peerA commits → peerB sees the file
#   2. Conflict: both peers commit different content to same file →
#      both converge to canonical + conflict file
#
# Used by `make nous-test-brain-sync` (Makefile.nous).
#
# Spec: workshop/plans/000004-shared-brain-sync-daemon-plan.md chunk 7.

set -euo pipefail

RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; CYAN=$'\033[0;36m'; RESET=$'\033[0m'
info() { printf "%s==>%s %s\n" "$CYAN" "$RESET" "$*" >&2; }
ok()   { printf "%s  [ok]%s %s\n" "$GREEN" "$RESET" "$*" >&2; }
warn() { printf "%s  [!]%s %s\n" "$YELLOW" "$RESET" "$*" >&2; }
die()  { printf "%serror:%s %s\n" "$RED" "$RESET" "$*" >&2; exit 1; }

NOUS_DIR=$(cd "$(dirname "$0")/.." && pwd)
WORK="$(mktemp -d "${TMPDIR:-/tmp}/brain-sync-test-XXXXXX")"
PIDS=()

cleanup() {
    info "Cleaning up..."
    for pid in "${PIDS[@]}"; do
        kill "$pid" 2>/dev/null || true
    done
    rm -rf "$WORK"
    ok "Done."
}
trap cleanup EXIT

# ── Build ───────────────────────────────────────────────────────────────────
info "Building brain-sync..."
(cd "$NOUS_DIR" && go build -o cmd/brain-sync/bin/brain-sync ./cmd/brain-sync) \
    || die "build failed"
BIN="$NOUS_DIR/cmd/brain-sync/bin/brain-sync"

# ── Set up bare + two peer clones ───────────────────────────────────────────
info "Setting up bare + peerA + peerB in $WORK"
BARE="$WORK/bare"
PEERA="$WORK/peerA"
PEERB="$WORK/peerB"

git init --bare -q -b main "$BARE"

# Seed via temporary clone.
SEED="$WORK/seed"
git clone -q "$BARE" "$SEED"
git -C "$SEED" config user.email seed@nous.local
git -C "$SEED" config user.name seed
mkdir -p "$SEED/.brain"
cat > "$SEED/.brain/config.md" <<EOF
---
mode: shared
name: test-brain
recipients: [TEST]
---
EOF
echo "Day 1: arrive" > "$SEED/paris.md"
git -C "$SEED" add -A
git -C "$SEED" commit -q -m "seed"
git -C "$SEED" push -q -u origin main

git clone -q "$BARE" "$PEERA"
git -C "$PEERA" config user.email a@nous.local
git -C "$PEERA" config user.name peerA

git clone -q "$BARE" "$PEERB"
git -C "$PEERB" config user.email b@nous.local
git -C "$PEERB" config user.name peerB

ok "Setup complete."

# ── Start two brain-sync daemons ────────────────────────────────────────────
info "Starting brain-sync watchers (5s fetch interval for fast test)..."
"$BIN" --brain "$PEERA" --fetch-every 5s > "$WORK/peerA.log" 2>&1 &
PIDS+=($!)
"$BIN" --brain "$PEERB" --fetch-every 5s > "$WORK/peerB.log" 2>&1 &
PIDS+=($!)
sleep 2  # let them initialize

# ── Test 1: basic propagation ───────────────────────────────────────────────
info "Test 1: peerA commits → peerB should see"
echo "Day 2: museum" >> "$PEERA/paris.md"
git -C "$PEERA" add paris.md
git -C "$PEERA" commit -q -m "A: day 2"

# brain-sync on peerA pushes (commit-driven), brain-sync on peerB ff-pulls
# next ticker. Wait up to 20s.
for _ in $(seq 1 20); do
    if grep -q "Day 2: museum" "$PEERB/paris.md" 2>/dev/null; then
        break
    fi
    sleep 1
done

if ! grep -q "Day 2: museum" "$PEERB/paris.md" 2>/dev/null; then
    cat "$WORK/peerA.log" "$WORK/peerB.log" >&2
    die "Test 1 FAILED: peerB never received peerA's commit"
fi
ok "Test 1 PASSED — propagation works"

# ── Test 2: conflict ────────────────────────────────────────────────────────
info "Test 2: both commit different content to paris.md → conflict file"

# Both edit BEFORE either has time to commit + push. To race them:
# write both files, then commit on both in quick succession.
echo "A's plan" > "$PEERA/paris.md"
echo "B's plan" > "$PEERB/paris.md"
git -C "$PEERA" add paris.md
git -C "$PEERB" add paris.md
git -C "$PEERA" commit -q -m "A: paris" &
P1=$!
git -C "$PEERB" commit -q -m "B: paris" &
P2=$!
wait "$P1" "$P2"

# Wait up to 30s for resolution.
for _ in $(seq 1 30); do
    # Conflict file naming: paris.conflict-<peer>-<utc>.md
    if ls "$PEERA"/paris.conflict-* 2>/dev/null | grep -q . && \
       ls "$PEERB"/paris.conflict-* 2>/dev/null | grep -q .; then
        break
    fi
    sleep 1
done

# Verify both peers have a conflict file.
A_CONFLICT=$(ls "$PEERA"/paris.conflict-* 2>/dev/null | head -1 || true)
B_CONFLICT=$(ls "$PEERB"/paris.conflict-* 2>/dev/null | head -1 || true)

if [ -z "$A_CONFLICT" ] || [ -z "$B_CONFLICT" ]; then
    cat "$WORK/peerA.log" "$WORK/peerB.log" >&2
    info "peerA dir:"; ls -la "$PEERA" >&2
    info "peerB dir:"; ls -la "$PEERB" >&2
    die "Test 2 FAILED: no conflict files appeared on both peers"
fi

# Verify both peers' canonical paris.md matches each other (convergence).
A_CANON=$(cat "$PEERA/paris.md")
B_CANON=$(cat "$PEERB/paris.md")
if [ "$A_CANON" != "$B_CANON" ]; then
    die "Test 2 FAILED: peers diverged. A=$A_CANON  B=$B_CANON"
fi

ok "Test 2 PASSED — both peers converged with conflict files"
ok "  canonical: $(echo $A_CANON)"
ok "  A's conflict: $(basename $A_CONFLICT)"
ok "  B's conflict: $(basename $B_CONFLICT)"

# ── Done ────────────────────────────────────────────────────────────────────
echo
ok "ALL TESTS PASSED"

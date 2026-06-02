#!/usr/bin/env bash
# scripts/brain-vm-e2e.sh — self-contained, GitHub-free end-to-end test of the
# scriptable brain ceremony (nous#36 M4). Drives the real `nous` CLI through the
# full two-peer flow against a `file://` bare gcrypt remote — no GitHub, no tart
# VM, no operator keys — so it runs anywhere gpg + git + git-remote-gcrypt do.
#
# What it proves:
#   - `nous identity import --verified-last8`     (M2, import path, non-TTY)
#   - `nous brain recipient add --verified-last8` (M2, fingerprint path, non-TTY)
#   - wrong --verified-last8 is refused           (M2 guard still holds)
#   - gcrypt multi-recipient re-key + clone + edit/push/pull round-trip:
#     the peer, admitted via the scripted ceremony, can decrypt and the
#     operator sees the peer's edit (the substrate actually works end-to-end).
#
# Out of scope here (verified elsewhere):
#   - the fake-pinentry shim / `nous identity init` (M1/M3) — those need a real
#     passphrase path; covered by scripts/brain-vm-setup.sh's local sign test +
#     the live tart-VM smoke. This script uses %no-protection throwaway keys so
#     gpg/gcrypt never prompt, keeping it dependency-light.
#
# Usage:  scripts/brain-vm-e2e.sh
# Needs a working gpg-agent (real IPC socket) — won't run under a sandbox that
# blocks agent sockets. Self-cleans on exit.
set -euo pipefail

RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; CYAN=$'\033[0;36m'; RESET=$'\033[0m'
step() { printf '%s==>%s %s\n' "$CYAN" "$RESET" "$*"; }
ok()   { printf '%s  [ok]%s %s\n' "$GREEN" "$RESET" "$*"; }
die()  { printf '%serror:%s %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }

for bin in gpg git git-remote-gcrypt; do
    command -v "$bin" >/dev/null || die "$bin not on PATH"
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Short-path work dir: a GNUPGHOME under /var/folders or a long $TMPDIR blows
# the macOS unix-socket path limit (104 chars) and gpg-agent can't bind. $HOME
# is short enough. (Mirrors the lib integration test's shortTempBase.)
WORK="$(mktemp -d "$HOME/.cache/n36e2e.XXXXXX")"
cleanup() {
    for h in "$WORK"/gnupg-*; do
        [ -d "$h" ] && gpgconf --homedir "$h" --kill all >/dev/null 2>&1 || true
    done
    rm -rf "$WORK"
}
trap cleanup EXIT

step "building nous"
NOUS="$WORK/nous"
( cd "$REPO_ROOT" && go build -o "$NOUS" ./cmd/nous )

# ── helpers ──────────────────────────────────────────────────────────
# genkey HOME LABEL EMAIL → echoes the new key's fingerprint. %no-protection
# means no passphrase, so gpg/gcrypt never prompt.
genkey() {
    local home="$1" label="$2" email="$3"
    mkdir -p "$home"; chmod 700 "$home"
    gpg --homedir "$home" --batch --generate-key >/dev/null 2>&1 <<EOF
%no-protection
Key-Type: eddsa
Key-Curve: ed25519
Subkey-Type: ecdh
Subkey-Curve: cv25519
Name-Real: $label
Name-Email: $email
Expire-Date: 0
%commit
EOF
    gpg --homedir "$home" --list-secret-keys --with-colons \
        | awk -F: '/^fpr:/{print $10; exit}'
}
last8() { printf '%s' "${1: -8}" | tr 'A-Z' 'a-z'; }

OP_H="$WORK/gnupg-op"; PEER_H="$WORK/gnupg-peer"

step "generate throwaway keys (operator + peer)"
OP_FP="$(genkey "$OP_H" "E2E Operator" "op@e2e.local")"
PEER_FP="$(genkey "$PEER_H" "E2E Peer" "peer@e2e.local")"
[ -n "$OP_FP" ] && [ -n "$PEER_FP" ] || die "keygen produced no fingerprint"
PEER_L8="$(last8 "$PEER_FP")"
ok "operator $OP_FP / peer $PEER_FP (last8 $PEER_L8)"
gpg --homedir "$PEER_H" --armor --export "$PEER_FP" > "$WORK/peer.pub"

step "bare gcrypt remote (stands in for GitHub)"
git init --bare -q -b main "$WORK/remote.git"
REMOTE="gcrypt::file://$WORK/remote.git"

step "operator provisions the brain (single recipient) + first encrypted push"
BRAIN_OP="$WORK/brain-op"
git init -q -b main "$BRAIN_OP"
git -C "$BRAIN_OP" config user.email op@e2e.local
git -C "$BRAIN_OP" config user.name "E2E Operator"
git -C "$BRAIN_OP" remote add origin "$REMOTE"
git -C "$BRAIN_OP" config remote.origin.gcrypt-participants "$OP_FP"  # initial set; nous re-syncs from manifest on later pushes
mkdir -p "$BRAIN_OP/.brain"
cat > "$BRAIN_OP/.brain/config.md" <<EOF
---
name: e2e-test
recipients: [$OP_FP]
sync_substrate: gcrypt
---

# e2e-test brain
EOF
printf '# seed\n' > "$BRAIN_OP/README.md"
git -C "$BRAIN_OP" add -A
git -C "$BRAIN_OP" commit -q -m "init brain"
( cd "$BRAIN_OP" && GNUPGHOME="$OP_H" "$NOUS" push "init" >/dev/null )
ok "provisioned + pushed"

step "operator imports peer pubkey — nous identity import --verified-last8 (M2)"
GNUPGHOME="$OP_H" "$NOUS" identity import "$WORK/peer.pub" \
    --verified-last8 "$PEER_L8" --github-user e2epeer </dev/null >/dev/null
GNUPGHOME="$OP_H" gpg --list-keys --with-colons | grep -qi "$PEER_FP" \
    || die "peer pubkey not in operator keyring after import"
ok "peer imported non-interactively"

step "negative: wrong --verified-last8 must be refused"
if GNUPGHOME="$OP_H" "$NOUS" brain recipient add "$BRAIN_OP" \
        --fingerprint "$PEER_FP" --verified-last8 00000000 </dev/null >/dev/null 2>&1; then
    die "recipient add accepted a WRONG --verified-last8 (guard broken!)"
fi
ok "wrong last-8 rejected"

step "operator admits peer — nous brain recipient add --verified-last8 (M2) → re-key + push"
GNUPGHOME="$OP_H" "$NOUS" brain recipient add "$BRAIN_OP" \
    --fingerprint "$PEER_FP" --verified-last8 "$PEER_L8" </dev/null >/dev/null
grep -q "$PEER_FP" "$BRAIN_OP/.brain/config.md" || die "peer not in manifest after admit"
ok "peer admitted + brain re-keyed to both recipients"

# Peer needs the operator's pubkey to verify the gcrypt-signed manifest. The
# nous#23 `keys`-branch auto-bootstrap (`nous brain clone`) only applies to
# brains provisioned via `nous brain new`/`join`, which publish the operator's
# own login pubkey there — out of reach for this GitHub-free file:// provision.
# So we use the documented pre-#23 sneakernet fallback, which also exercises
# `nous identity import --verified-last8` from the PEER side, then a raw gcrypt
# `git clone` (the keys-branch bootstrap is orthogonal to nous#36's surface).
step "peer imports operator pubkey — nous identity import --verified-last8 (M2, peer side)"
OP_L8="$(last8 "$OP_FP")"
gpg --homedir "$OP_H" --armor --export "$OP_FP" > "$WORK/op.pub"
GNUPGHOME="$PEER_H" "$NOUS" identity import "$WORK/op.pub" \
    --verified-last8 "$OP_L8" --github-user e2eop </dev/null >/dev/null
ok "operator pubkey imported on peer side"

step "peer clones the brain (gcrypt decrypt as new recipient)"
BRAIN_PEER="$WORK/brain-peer"
GNUPGHOME="$PEER_H" git clone -q "$REMOTE" "$BRAIN_PEER" \
    || die "gcrypt clone failed (decrypt as recipient)"
[ -f "$BRAIN_PEER/README.md" ] || die "peer clone missing README (decrypt failed)"
grep -q "$PEER_FP" "$BRAIN_PEER/.brain/config.md" || die "peer clone manifest missing peer recipient"
ok "peer decrypted the brain (admission via scripted ceremony works)"

step "peer edits + pushes — nous push (re-encrypt)"
MARK="peer-edit-$RANDOM"
printf 'peer note: %s\n' "$MARK" >> "$BRAIN_PEER/notes.md"
git -C "$BRAIN_PEER" add -A
( cd "$BRAIN_PEER" && GNUPGHOME="$PEER_H" "$NOUS" push "peer edit" >/dev/null )
ok "peer pushed an edit"

step "operator pulls — sees the peer's edit (round-trip + decrypt)"
( cd "$BRAIN_OP" && GNUPGHOME="$OP_H" git pull -q --no-edit origin main )
grep -q "$MARK" "$BRAIN_OP/notes.md" 2>/dev/null \
    || die "operator did not receive the peer's edit (round-trip failed)"
ok "operator received the peer's edit"

# ── remove sticks: no resurrection vectors left (nous#38) ────────────
# Seed the two vectors a GitHub-mediated peer would have — a verified.yaml
# entry and a <login>.asc on the keys branch (alongside the <FP>.asc that
# `recipient add` published) — then prove `recipient remove` clears ALL of
# them (manifest + verified.yaml + every keys-branch entry for the fp).
step "remove sticks — recipient remove clears manifest + verified.yaml + keys branch"
PEER_LOGIN="e2epeer"
cat > "$BRAIN_OP/.brain/verified.yaml" <<EOF
$PEER_LOGIN:
  fingerprint: $PEER_FP
  verified_by: e2eop
  verified_at: 2026-06-01T00:00:00Z
EOF
git -C "$BRAIN_OP" add -A
git -C "$BRAIN_OP" commit -q -m "seed verified.yaml"
( cd "$BRAIN_OP" && GNUPGHOME="$OP_H" "$NOUS" push >/dev/null )

# Add <login>.asc next to <FP>.asc on the (plaintext) keys branch.
git clone -q --branch keys --single-branch "file://$WORK/remote.git" "$WORK/keysco" \
    || die "setup: keys branch not plain-cloneable"
cp "$WORK/keysco/${PEER_FP}.asc" "$WORK/keysco/${PEER_LOGIN}.asc"
git -C "$WORK/keysco" -c user.email=o@e2e -c user.name=o add -A
git -C "$WORK/keysco" -c user.email=o@e2e -c user.name=o commit -q -m "add ${PEER_LOGIN}.asc"
git -C "$WORK/keysco" push -q origin keys
ok "seeded verified.yaml + <login>.asc (resurrection vectors present)"

# --force lifts the TTY gate for scripted use.
GNUPGHOME="$OP_H" "$NOUS" brain recipient remove "$BRAIN_OP" "$PEER_FP" --force </dev/null >/dev/null

grep -q "$PEER_FP" "$BRAIN_OP/.brain/config.md" && die "peer still in manifest after remove" || true
grep -qi "$PEER_LOGIN" "$BRAIN_OP/.brain/verified.yaml" 2>/dev/null && die "verified.yaml entry survived remove (leak #2)" || true
rm -rf "$WORK/keysco2"
git clone -q --branch keys --single-branch "file://$WORK/remote.git" "$WORK/keysco2" || die "re-clone keys branch"
for f in "$WORK/keysco2"/*.asc; do
    [ -e "$f" ] || continue
    fp=$(gpg --show-keys --with-colons "$f" 2>/dev/null | awk -F: '/^fpr/{print $10; exit}')
    [ "$fp" = "$PEER_FP" ] && die "keys-branch entry $(basename "$f") still resolves to peer (leak #1)"
done
ok "manifest + verified.yaml + all keys-branch entries cleared — no resurrection vectors"

printf '\n%s✅ brain e2e PASSED%s — scripted ceremony + gcrypt round-trip + complete remove verified.\n' "$GREEN" "$RESET"

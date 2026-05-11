#!/usr/bin/env bash
# sign.sh — codesign a nous-substrate binary so it satisfies the runtime
# self-check in lib/provider/vault/keychain/codesign_darwin.go.
#
# Usage: scripts/sign.sh <path-to-binary> [identifier]
#
# The runtime check evaluates `identifier "com.charon.cli"` against the
# binary's code-signing record. Match → ResolveServiceName returns
# "charon" (production keychain namespace); mismatch → returns
# "charon-dev". This script stamps that identifier regardless of which
# signing identity is used, so the namespace routing works the same
# way for ad-hoc and Developer ID signed binaries.
#
# Signing identity precedence:
#   1. $NOUS_CODESIGN_IDENTITY explicitly set → use it
#   2. Otherwise → ad-hoc ("-")
#
# Ad-hoc note: ad-hoc signing satisfies the self-check (identifier
# matches), but keychain ACL entries that were originally bound to a
# real Developer ID won't be readable from an ad-hoc binary — the
# kernel-side ACL check pins to the cert leaf hash, which ad-hoc lacks.
# Recovery from an ad-hoc-only install: re-grant the ACL when the
# Developer ID arrives, or wipe and re-create entries.
#
# Hardened-runtime: enabled only when signing with a real identity
# (--options runtime). Ad-hoc + hardened-runtime fails outright.
#
# nous-security needs its own identifier (separate menubar lifecycle);
# pass it as the second arg, or sign nous-security via a different
# script entirely.

set -euo pipefail

if [[ $# -lt 1 ]]; then
    echo "usage: $0 <binary> [identifier]" >&2
    exit 2
fi

bin="$1"
identifier="${2:-com.charon.cli}"

if [[ ! -f "$bin" ]]; then
    echo "$bin: not a file" >&2
    exit 1
fi

identity="${NOUS_CODESIGN_IDENTITY:--}"

if [[ "$identity" == "-" ]]; then
    echo "  signing $bin ad-hoc (identifier=$identifier)"
    codesign --force --sign - --identifier "$identifier" "$bin"
else
    echo "  signing $bin with $identity (identifier=$identifier)"
    codesign --force --sign "$identity" \
        --identifier "$identifier" \
        --options runtime \
        --timestamp \
        "$bin"
fi

# Verify the stamp landed.
if ! codesign --verify --strict "$bin" 2>/dev/null; then
    echo "  [warn] codesign --verify --strict failed (ok for ad-hoc; flag for Developer ID)"
fi

# Surface the identifier for the operator (catches typos / wrong target).
got=$(codesign -d --verbose=1 "$bin" 2>&1 | awk -F= '/^Identifier=/ { print $2 }')
if [[ "$got" != "$identifier" ]]; then
    echo "  [error] stamped identifier=$got; wanted $identifier" >&2
    exit 1
fi

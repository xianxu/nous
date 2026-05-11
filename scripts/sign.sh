#!/usr/bin/env bash
# sign.sh — codesign a nous-substrate binary so it satisfies the runtime
# self-check in lib/provider/vault/keychain/codesign_darwin.go.
#
# Usage: scripts/sign.sh <path-to-binary> [identifier]
#
# The runtime check evaluates `identifier "com.charon.cli"` against the
# binary's code-signing record. Match → ResolveServiceName returns
# "charon" (production keychain namespace); mismatch → returns
# "charon-dev". This script stamps that identifier so the namespace
# routing works correctly for the signed production binary.
#
# Signing identity resolution:
#   1. $NOUS_CODESIGN_IDENTITY explicitly set → use it verbatim.
#   2. Otherwise → auto-detect a single "Developer ID Application"
#      identity from the user's codesigning keychain.
#   3. Zero or >1 candidates → fail loudly with the candidate list and
#      the next action (set the env var, or use `make nous-dev` for
#      unsigned development iteration).
#
# Ad-hoc signing intentionally has no path here. It produced a
# cdhash-only Designated Requirement (no cert chain to anchor with),
# which made every rebuild invalidate prior keychain ACL grants —
# operator-hostile. Also blocks hardened runtime (codesign rejects
# --options runtime + ad-hoc). The unsigned-dev path is `make nous-dev`;
# the signed-prod path is here. No degraded middle ground.
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

identity="${NOUS_CODESIGN_IDENTITY:-}"

if [[ -z "$identity" ]]; then
    # Auto-detect. `security find-identity -v -p codesigning` emits one
    # line per valid identity, formatted as:
    #   1) <40-char SHA1> "Developer ID Application: <Name> (<TEAMID>)"
    # We extract the quoted CN of each Developer ID Application identity.
    # Portable to bash 3.2 (macOS default) — no `mapfile`.
    candidates=()
    while IFS= read -r line; do
        [[ -n "$line" ]] && candidates+=("$line")
    done < <(
        security find-identity -v -p codesigning 2>/dev/null \
            | awk -F'"' '/"Developer ID Application/ { print $2 }'
    )
    case "${#candidates[@]}" in
        0)
            echo "  [error] no 'Developer ID Application' identity found in codesigning keychain" >&2
            echo "          options:" >&2
            echo "            - acquire a Developer ID cert and import it into login.keychain" >&2
            echo "            - or run \`make nous-dev\` for unsigned dev iteration (charon-dev namespace)" >&2
            echo "            - or set NOUS_CODESIGN_IDENTITY explicitly to override auto-detect" >&2
            exit 1
            ;;
        1)
            identity="${candidates[0]}"
            echo "  auto-detected identity: $identity"
            ;;
        *)
            echo "  [error] multiple 'Developer ID Application' identities found; auto-detect refuses to guess" >&2
            for c in "${candidates[@]}"; do
                echo "            - $c" >&2
            done
            echo "          set NOUS_CODESIGN_IDENTITY to the one you want and re-run." >&2
            exit 1
            ;;
    esac
fi

echo "  signing $bin with $identity (identifier=$identifier)"
codesign --force --sign "$identity" \
    --identifier "$identifier" \
    --options runtime \
    --timestamp \
    "$bin"

# Verify the stamp landed.
if ! codesign --verify --strict "$bin" 2>/dev/null; then
    echo "  [error] codesign --verify --strict failed on $bin" >&2
    exit 1
fi

# Surface the identifier for the operator (catches typos / wrong target).
got=$(codesign -d --verbose=1 "$bin" 2>&1 | awk -F= '/^Identifier=/ { print $2 }')
if [[ "$got" != "$identifier" ]]; then
    echo "  [error] stamped identifier=$got; wanted $identifier" >&2
    exit 1
fi

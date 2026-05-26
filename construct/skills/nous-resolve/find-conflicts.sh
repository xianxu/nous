#!/usr/bin/env bash
# find-conflicts.sh — discover brain-sync conflict files in a brain root.
#
# Usage: find-conflicts.sh <brain-root>
#
# Output (tab-separated, one per line):
#   <canonical-abs-path>\t<conflict-abs-path>\t<peer>\t<utc-iso-compact>
#
# Conflict-file convention (per nous#4 M3, atlas/sync-substrate-decision.md):
#   <base>.conflict-<peer>-<YYYYMMDDTHHMMSSZ>.<ext>
#
# Implementation: prefers `nous brain resolve` (the Go surface added in
# nous#18) when the binary is on PATH — same parse, also exposes
# --json for callers that want structured output. Falls back to the
# legacy `find` + python parser when the binary is missing (older
# installs, CI sandboxes). Output shape is identical either way.

set -euo pipefail

if [[ $# -ne 1 ]]; then
    echo "usage: $0 <brain-root>" >&2
    exit 2
fi

brain_root="$1"

if [[ ! -f "$brain_root/.brain/config.md" ]]; then
    echo "$brain_root: no .brain/config.md found — not a brain" >&2
    exit 1
fi

# Prefer the Go surface when available — same output contract.
if command -v nous >/dev/null 2>&1; then
    exec nous brain resolve "$brain_root"
fi

# Legacy fallback. Pattern: <stem>.conflict-<peer>-<compact-utc>.<ext>
# Compact UTC = YYYYMMDDTHHMMSSZ (per nous/lib/brainsync/resolve.go).
# Example: paris.conflict-peerB-20260508T150000Z.md
find "$brain_root" -type f -name '*.conflict-*-*Z.*' 2>/dev/null | while IFS= read -r conflict; do
    name="${conflict##*/}"
    dir="${conflict%/*}"

    parsed=$(python3 -c '
import re, sys
m = re.match(r"^(.+)\.conflict-([^.]+)-(\d{8}T\d{6}Z)(\..+)$", sys.argv[1])
if not m:
    sys.exit(1)
stem, peer, ts, ext = m.groups()
print(f"{stem}{ext}\t{peer}\t{ts}")
' "$name") || continue

    canonical_name="${parsed%%$'\t'*}"
    rest="${parsed#*$'\t'}"
    peer="${rest%%$'\t'*}"
    ts="${rest#*$'\t'}"

    canonical="$dir/$canonical_name"
    printf '%s\t%s\t%s\t%s\n' "$canonical" "$conflict" "$peer" "$ts"
done

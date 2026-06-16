#!/usr/bin/env bash
# test-synthetic.sh — exercise the mechanical layer of /nous-resolve.
#
# Builds a throwaway brain in $TMPDIR, injects a conflict pair on a
# travel-plan file, runs find-conflicts.sh + preserve.py end-to-end,
# asserts:
#   1. find-conflicts.sh emits the expected tuple
#   2. preserve.py creates .brain/merges/<ts>-<slug>/{canonical.md, peer.md, meta.json}
#   3. meta.json has the right shape
#
# Does NOT exercise the agent-driven merge step (that's M3 dogfood).
# Does NOT run git ops; the commit step is exercised in chunk 6.

set -euo pipefail

skill_dir="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/nous-resolve-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

brain="$work/brain"
mkdir -p "$brain/.brain" "$brain/data/life/travel"
cat >"$brain/.brain/config.md" <<'EOF'
mode: shared
name: test-brain
EOF

# Canonical travel-plan
cat >"$brain/data/life/travel/2026-08-01-paris.md" <<'EOF'
---
type: travel-plan
destination: Paris
start: 2026-08-01
end: 2026-08-08
travelers: [self, alice]
status: planning
updated: 2026-05-08
---

# Paris — 2026-08-01 to 2026-08-08

Family week in Paris.

## Itinerary

### 2026-08-01
- Arrive CDG
- Hotel check-in

## Open questions
- which arrondissement to stay in
EOF

# Peer's conflicting version
cat >"$brain/data/life/travel/2026-08-01-paris.conflict-peerB-20260508T150000Z.md" <<'EOF'
---
type: travel-plan
destination: Paris
start: 2026-08-01
end: 2026-08-08
travelers: [self, alice, bob]
status: planning
updated: 2026-05-08
---

# Paris — 2026-08-01 to 2026-08-08

Family week in Paris.

## Itinerary

### 2026-08-01
- Arrive CDG

### 2026-08-02
- Louvre

## Open questions
- which arrondissement to stay in
EOF

echo "==> fixture brain built at $brain"

# Initialize git first, with the fixture (canonical + conflict file) as the
# initial commit. This mirrors real use: the brain has history; preserve.py
# creates the snapshot dir later, as part of the merge transaction; the merge
# commit then includes the snapshot files as new additions.
git -C "$brain" init -q -b main
git -C "$brain" config user.name "test-runner"
git -C "$brain" config user.email "test@example.com"
git -C "$brain" add .
git -C "$brain" commit -q -m "initial fixture"

# (1) find-conflicts.sh
echo "==> find-conflicts.sh"
output=$("$skill_dir/find-conflicts.sh" "$brain")
echo "$output"
line_count=$(echo "$output" | grep -c . || true)
[[ "$line_count" == "1" ]] || { echo "FAIL: expected 1 conflict tuple, got $line_count"; exit 1; }

IFS=$'\t' read -r canonical conflict_file peer ts <<<"$output"
[[ "$canonical" == "$brain/data/life/travel/2026-08-01-paris.md" ]] || { echo "FAIL: canonical $canonical"; exit 1; }
[[ "$peer" == "peerB" ]] || { echo "FAIL: peer $peer"; exit 1; }
[[ "$ts" == "20260508T150000Z" ]] || { echo "FAIL: ts $ts"; exit 1; }
echo "  [ok] canonical, conflict, peer, ts all parsed correctly"

# (2) preserve.py
echo "==> preserve.py"
snapshot_rel=$(python3 "$skill_dir/preserve.py" "$canonical" "$conflict_file")
snapshot="$brain/$snapshot_rel"
echo "  snapshot: $snapshot_rel"

[[ -f "$snapshot/canonical.md" ]] || { echo "FAIL: canonical.md missing"; exit 1; }
[[ -f "$snapshot/peer.md" ]] || { echo "FAIL: peer.md missing"; exit 1; }
[[ -f "$snapshot/meta.json" ]] || { echo "FAIL: meta.json missing"; exit 1; }
echo "  [ok] canonical.md, peer.md, meta.json all present"

# Content sanity: canonical.md should equal canonical, peer.md should equal conflict-file
diff -q "$canonical" "$snapshot/canonical.md" >/dev/null || { echo "FAIL: canonical.md content mismatch"; exit 1; }
diff -q "$conflict_file" "$snapshot/peer.md" >/dev/null || { echo "FAIL: peer.md content mismatch"; exit 1; }
echo "  [ok] preserved content matches originals"

# (3) meta.json shape
meta_canonical=$(python3 -c "import json,sys; print(json.load(open(sys.argv[1]))['canonical'])" "$snapshot/meta.json")
meta_peer=$(python3 -c "import json,sys; print(json.load(open(sys.argv[1]))['peer'])" "$snapshot/meta.json")
meta_conflict_ts=$(python3 -c "import json,sys; print(json.load(open(sys.argv[1]))['conflict_ts'])" "$snapshot/meta.json")
[[ "$meta_canonical" == "data/life/travel/2026-08-01-paris.md" ]] || { echo "FAIL: meta.canonical $meta_canonical"; exit 1; }
[[ "$meta_peer" == "peerB" ]] || { echo "FAIL: meta.peer $meta_peer"; exit 1; }
[[ "$meta_conflict_ts" == "20260508T150000Z" ]] || { echo "FAIL: meta.conflict_ts $meta_conflict_ts"; exit 1; }
echo "  [ok] meta.json fields correct"


# (4) git ops path: simulate the on-confirm write + cleanup + commit
echo "==> git ops path"
# (git already initialized at top of file with the fixture as initial commit;
# preserve.py just ran, so the snapshot dir exists in the working tree but
# isn't yet committed — the merge commit will add it.)

# Hand-crafted "merged" content (in real use, this is what the agent produces)
cat >"$canonical" <<'EOF'
---
type: travel-plan
destination: Paris
start: 2026-08-01
end: 2026-08-08
travelers: [self, alice, bob]
status: planning
updated: 2026-05-08
---

# Paris — 2026-08-01 to 2026-08-08

Family week in Paris.

## Itinerary

### 2026-08-01
- Arrive CDG
- Hotel check-in

### 2026-08-02
- Louvre

## Open questions
- which arrondissement to stay in
EOF
rm "$conflict_file"

git -C "$brain" add "$canonical" .brain/merges
git -C "$brain" rm -q "$conflict_file" 2>/dev/null || true  # already rm'd; just stage deletion
git -C "$brain" commit -q -m "merge: data/life/travel/2026-08-01-paris.md via /nous-resolve

peer: peerB
conflict-ts: 20260508T150000Z
preserved at: $snapshot_rel

structural choices:
- travelers: union (added bob)
- itinerary: kept canonical's 2026-08-01; added peer's 2026-08-02
- prose sections: identical, no merge needed"

# Assert commit shape
last_subject=$(git -C "$brain" log -1 --format=%s)
[[ "$last_subject" == "merge: data/life/travel/2026-08-01-paris.md via /nous-resolve" ]] \
    || { echo "FAIL: commit subject $last_subject"; exit 1; }
echo "  [ok] commit landed: $last_subject"

# Assert canonical updated, conflict file gone, snapshot intact
[[ -f "$canonical" ]] || { echo "FAIL: canonical missing"; exit 1; }
[[ ! -f "$conflict_file" ]] || { echo "FAIL: conflict file still present"; exit 1; }
[[ -f "$snapshot/canonical.md" ]] || { echo "FAIL: snapshot lost across commit"; exit 1; }
echo "  [ok] canonical updated, conflict file gone, snapshot intact"


# (5) undo path: revert the most recent merge commit, assert restoration
echo "==> undo path"

canonical_pre_undo=$(cat "$canonical")  # post-merge content (for comparison)

# Find the most recent /nous-resolve merge commit
merge_sha=$(git -C "$brain" log --grep '^merge: .* via /nous-resolve$' -1 --format='%H')
[[ -n "$merge_sha" ]] || { echo "FAIL: no /nous-resolve merge commit found"; exit 1; }
echo "  merge sha: ${merge_sha:0:8}"

# Revert
git -C "$brain" revert "$merge_sha" --no-edit >/dev/null

# Assert: canonical reverted to pre-merge content
canonical_now=$(cat "$canonical")
[[ "$canonical_now" != "$canonical_pre_undo" ]] || { echo "FAIL: canonical didn't change"; exit 1; }
grep -q '^travelers: \[self, alice\]$' "$canonical" || { echo "FAIL: canonical's travelers field not restored"; exit 1; }
echo "  [ok] canonical restored to pre-merge content"

# Assert: conflict file restored
[[ -f "$conflict_file" ]] || { echo "FAIL: conflict file not restored"; exit 1; }
grep -q 'travelers: \[self, alice, bob\]' "$conflict_file" || { echo "FAIL: conflict file content wrong"; exit 1; }
echo "  [ok] conflict file restored at original path"

# Assert: snapshot files gone (git revert removes tracked files; empty dir may remain)
# Note: `find ... | wc -l` would interact poorly with pipefail if the dir doesn't
# exist; just check the three known files.
for f in canonical.md peer.md meta.json; do
    [[ ! -f "$snapshot/$f" ]] || { echo "FAIL: $snapshot/$f still present after revert"; exit 1; }
done
echo "  [ok] .brain/merges snapshot files removed by revert"

# Assert: revert commit landed
revert_subject=$(git -C "$brain" log -1 --format=%s)
[[ "$revert_subject" == 'Revert "merge: data/life/travel/2026-08-01-paris.md via /nous-resolve"' ]] \
    || { echo "FAIL: revert commit subject $revert_subject"; exit 1; }
echo "  [ok] revert commit landed: $revert_subject"

echo ""
echo "PASS — synthetic mechanical test green (find-conflicts + preserve + git ops + undo)"

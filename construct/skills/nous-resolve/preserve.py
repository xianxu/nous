#!/usr/bin/env python3
"""Pre-merge preservation for /nous-resolve.

Snapshot the canonical and the conflict-file to .brain/merges/<utc-iso>-<slug>/
before nous-resolve overwrites canonical. Safety floor: any merge can be
reverted by restoring from this snapshot.

Usage:
    preserve.py <canonical> <conflict-file>

Both arguments must reside under the same brain (a directory containing
.brain/config.md, walking parents from each).

Writes:
    <brain-root>/.brain/merges/<utc-iso>-<canonical-slug>/canonical<ext>
    <brain-root>/.brain/merges/<utc-iso>-<canonical-slug>/peer<ext>
    <brain-root>/.brain/merges/<utc-iso>-<canonical-slug>/meta.json
"""

import json
import re
import shutil
import sys
from datetime import datetime, timezone
from pathlib import Path


CONFLICT_RE = re.compile(r"^(.+)\.conflict-([^.]+)-(\d{8}T\d{6}Z)(\..+)$")


def find_brain_root(path: Path) -> Path:
    p = path.resolve()
    for ancestor in [p] + list(p.parents):
        if (ancestor / ".brain" / "config.md").is_file():
            return ancestor
    raise SystemExit(f"no .brain/config.md found walking up from {path}")


def parse_conflict(conflict_path: Path) -> tuple[str, str, str, str]:
    m = CONFLICT_RE.match(conflict_path.name)
    if not m:
        raise SystemExit(f"conflict filename does not match pattern: {conflict_path.name}\n"
                         f"expected <base>.conflict-<peer>-<utc-iso>Z.<ext>")
    base, peer, ts, ext = m.groups()
    return base, peer, ts, ext


def slugify(rel_path: str) -> str:
    no_ext = re.sub(r"\.[^.]+$", "", rel_path)
    return re.sub(r"[^a-zA-Z0-9._-]", "-", no_ext).strip("-")


def main() -> None:
    if len(sys.argv) != 3:
        sys.exit("usage: preserve.py <canonical> <conflict-file>")
    canonical = Path(sys.argv[1]).resolve()
    conflict = Path(sys.argv[2]).resolve()
    if not canonical.is_file():
        sys.exit(f"canonical not a file: {canonical}")
    if not conflict.is_file():
        sys.exit(f"conflict-file not a file: {conflict}")

    brain_root = find_brain_root(canonical)
    if find_brain_root(conflict) != brain_root:
        sys.exit("canonical and conflict-file are in different brains")

    stem, peer, conflict_ts, ext = parse_conflict(conflict)
    canonical_rel = canonical.relative_to(brain_root)
    expected_canonical_name = f"{stem}{ext}"
    if expected_canonical_name != canonical.name:
        sys.exit(f"conflict file's stem+ext {expected_canonical_name!r} doesn't match canonical {canonical.name!r}")

    now_iso = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    snapshot_dir = brain_root / ".brain" / "merges" / f"{now_iso}-{slugify(str(canonical_rel))}"
    snapshot_dir.mkdir(parents=True, exist_ok=False)

    shutil.copy2(canonical, snapshot_dir / f"canonical{ext}")
    shutil.copy2(conflict, snapshot_dir / f"peer{ext}")

    meta = {
        "canonical": str(canonical_rel),
        "conflict_file": str(conflict.relative_to(brain_root)),
        "peer": peer,
        "conflict_ts": conflict_ts,
        "preserved_at": now_iso,
    }
    (snapshot_dir / "meta.json").write_text(json.dumps(meta, indent=2) + "\n")

    print(snapshot_dir.relative_to(brain_root))


if __name__ == "__main__":
    main()

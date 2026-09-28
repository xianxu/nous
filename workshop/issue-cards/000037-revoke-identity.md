---
id: 000037
status: open
created: 2026-06-01
updated: 2026-06-01
estimate_hours:
github_issue:
---

# system-wide identity revocation

## Problem

There is no clean way to revoke a person from the whole nous/brain system.
Today's tools are partial and per-brain:
- `nous brain recipient remove <brain> <fp>` — one brain, manifest + re-key only.
- `nous brain leave` — self only (removes own GitHub collaborator + revokes).
- `nous brain invite` — add side of collaborator management; there is no
  "remove this collaborator everywhere" verb.

Worse, a local `gpg --delete-keys` is *silently undone*: a fingerprint a brain
was granted to lives in **three remote places** — the manifest (`recipients:`),
the `keys` branch (`<login>.asc` / `<fp>.asc`), and `verified.yaml` — and
brain-sync actively pulls from them (`ImportAllPubkeys` re-imports the pubkey,
`AutoAdmitFromKeysBranch` re-admits it). So deleting locally just leaves the
recipient "(unknown)" until the next sync/invite resurrects it. Observed live
during nous#36 dogfood setup (2026-06-01): removing a test key locally, then
inviting the same GitHub user, re-associated the old fingerprint.

Without system-wide revocation, durable multi-day dogfooding (nous#12) is
painful — you can't cleanly retire a test identity and the only reliable reset
is `gh repo delete && recreate`.

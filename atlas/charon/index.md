# Charon — credential proxy + provider auth

The charon-origin code lives here as part of nous's substrate. Original charon GitHub repo (`xianxu/charon`) is being archived (per `nous#14`); the substantive code moved to `nous/cmd/charon/`, `nous/cmd/charon-security/`, and `nous/internal/charon/` in commit landing M1 of `nous#14`.

## Entries

- [Charon overview](charon.md) — what charon is, the proxy + provider-auth surface
- [Security audit](security-audit.md) — threat model + audit log conventions

## Future location

`nous#14` plans to disassemble `internal/charon/` into domain-organized libs in M2 (`lib/provider`, `lib/agent`, `lib/tui`, etc.). After M2, this index gets refreshed to point at the new homes; entries here will likely be archived as historical references.

## Cross-refs

- `nous/atlas/nous/brain-conflict-resolution.md` — sibling skill (`/nous-resolve`) that depends on the same gpg-agent + tui foundations charon helped pioneer
- `brain/atlas/threat-model-shared-brain.md` — the brain-side counterpart; `nous#14` adds a delegation-boundary subsection that pairs with charon's identity model

---
id: 000021
status: open
created: 2026-05-10
updated: 2026-05-10
estimate_hours: 2
---

# Audit: promote terminals + unanchored DRs to Critical on charon-namespace ACLs

## Problem

Today `lib/security/check_charon.go`'s trusted-app classifier
(`classifyOneFor`, lines 462-494) recognizes four states:

- `verdictExpected` — `identifier "com.charon.cli"` or `/charon"`;
  silent
- `verdictBenign` — 7 Apple system services on a hardcoded list
  (CertificateAssistant, keychainaccess, SecurityAgent,
  systempreferences, racoon, etc.); hygiene
- `verdictCatastrophic` — `/usr/bin/codesign`, `/usr/bin/security`,
  their bundle IDs; Critical
- `verdictUnknown` (default) — everything else; **Important** (NOT
  Critical)

Two attack shapes fall through as `verdictUnknown` even though their
blast radius is the same as the catastrophic class:

### Gap 1 — Terminal apps on the trust list

If a charon-namespace keychain entry trusts `com.apple.Terminal`,
`com.googlecode.iterm2`, or any other terminal, then **every shell
command you (or an agent running as you) execute** can do:

```
security find-generic-password -s charon -a google:user@gmail.com -w
```

…and silently exfiltrate the raw OAuth token. The proxy's audit
trail is bypassed entirely.

The terminal app legitimately needs to read keychain entries for
*other* things (ssh-agent, git credentials, etc.) — there are plenty
of valid reasons it might already be on Apple-managed trust lists.
But on a `charon` / `charon-dev` namespace entry, terminal trust is
identical-in-blast-radius to trusting `/usr/bin/security` (already
catastrophic).

`lib/security/knownapps.go` already enumerates 10 terminal bundle IDs
under `CatTerminal` (Terminal, iTerm2, Ghostty, Warp, Hyper,
Alacritty, WezTerm, Kitty, Tabby, cmux). Currently wired into TCC
checks only — not consulted by the keychain ACL classifier.

### Gap 2 — DRs with no real cert anchor

`classifyOneFor` extracts the identifier but ignores the **anchor
clause** of the DR predicate. DRs without `anchor apple`,
`anchor apple generic`, or `anchor trusted` are forgeable by any
local user. Three sub-shapes all qualify:

- Ad-hoc binaries: `cdhash H"..."`-only DR (no identifier or
  anchor at all)
- Self-signed cert binaries: `identifier "com.foo" and
  certificate root H"<non-apple-root-hash>"` (anchored to a CA
  the user controls)
- Bare identifier-only DRs: `identifier "com.foo"` with no
  anchor clause at all

For any of these, an attacker (or a misbehaving agent that landed
shell code on the machine) can produce a Mach-O whose DR matches
the trust list, place it anywhere on disk, and silently read the
keychain entry. Same A10 blast radius as catastrophic.

Today these surface as `verdictUnknown` → Important. They should
be Critical. The detection is structural (DR text shape) — no
need to enumerate "bad apps", the **absence** of an anchor clause
is the tell.

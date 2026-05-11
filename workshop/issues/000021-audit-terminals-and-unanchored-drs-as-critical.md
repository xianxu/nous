---
id: 000021
status: open
deps: []
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

## Spec

Two additions to `classifyOneFor` in
`lib/security/check_charon.go`. Order in the function matters
because the first matching rule wins:

1. **Catastrophic: terminals.** After the `expectedPatterns`
   check, before the existing catastrophic list, add a sweep over
   `knownapps.go`'s `CatTerminal` bundle IDs. If the DR contains
   `identifier "<bundle-id>"`, return `verdictCatastrophic` with
   reason: *"terminal app — any shell command running here can
   read this entry via `security find-generic-password`."*

2. **Catastrophic: unanchored DRs.** After the catastrophic-list
   sweep, before falling through to benign / unknown, add an
   anchor-shape check. If the DR (a) doesn't contain `anchor apple`,
   `anchor apple generic`, or `anchor trusted`, AND (b) isn't
   already matched as `verdictExpected` (charon's own DR for
   ad-hoc-signed dev binaries doesn't have `anchor apple` either —
   that's intentional and matched earlier), return
   `verdictCatastrophic` with reason: *"DR lacks a cert anchor —
   any local user can forge a binary matching this requirement."*

Subtlety: the `verdictExpected` short-circuit MUST run first.
Otherwise a `nous-dev`-style ad-hoc binary with `identifier
"com.charon.cli"` (cdhash-only DR) would catch on the anchor-shape
check. The current ordering already runs expected first; preserve
that.

Detection precision for terminals: match on bundle ID, not path.
Terminal apps may live at `/Applications/Utilities/Terminal.app`,
`/Applications/iTerm.app`, or homebrew-cask paths. The bundle ID
is the stable identifier.

Detection precision for anchors: regex `\banchor (apple|trusted)\b`
covers `anchor apple`, `anchor apple generic`,
`anchor apple generic and ...`, `anchor trusted`. `\b` word
boundary avoids matching `anchor_apple_lookalike`.

## Plan

- [ ] M1: Tests first — fixture each new pattern.
  `check_charon_drift_test.go` already has a DR-classification
  test infrastructure; extend it. Cases:
  - `identifier "com.apple.Terminal" and anchor apple` → Critical
    (terminal beats Apple-anchor — terminals are catastrophic
    regardless of how they were signed)
  - `identifier "com.googlecode.iterm2" and anchor apple` → Critical
  - `identifier "com.attacker.local"` (no anchor) → Critical
  - `identifier "com.attacker.local" and certificate root H"abc..."`
    (self-signed cert) → Critical
  - `cdhash H"abc..."` (ad-hoc, no identifier) → Critical
  - `identifier "com.charon.cli"` (charon's own ad-hoc DR) →
    Expected (regression guard for the ordering invariant)
  - `identifier "com.apple.Terminal" and anchor apple` while
    also on the *expected* pattern list → Expected (defensive;
    not a real shape but pin the ordering)
- [ ] M2: Implement `classifyOneFor` additions per the spec.
  Promote terminal classification ahead of the existing
  catastrophic list; add the anchor-shape regex check after.
- [ ] M3: Wire up `knownapps.go`'s `CatTerminal` table to be
  consumed by `classifyOneFor`. Today the table is private to
  the file; export or add a helper that returns the bundle IDs.
- [ ] M4: Update `remedy.go` — add remediation text for both
  new finding shapes:
  - terminal-trust → walk the operator through Keychain Access
    to remove the terminal from the ACL trust list; rewrite the
    entry through `nous provider` to re-attach a clean ACL.
  - unanchored-DR → same Keychain Access cleanup; the structural
    fix is to remove the entry and re-auth.
- [ ] M5: Smoke-test against the operator's actual keychain —
  `nous-security` audit run, confirm no false positives on the
  current expected state (just charon's own DR on entries).
  Atlas: add a sentence to `atlas/charon/security-audit.md`
  about the new classification rules.

## Log

Filed 2026-05-10. Surfaced during the nous#20 keychain-ACL
debugging conversation — operator asked "does the audit catch
terminal trust or locally-signed binaries?" Reviewing
`classifyOneFor` revealed both shapes fall through as Unknown.
The threat shape (any-local-shell or any-local-binary can read
the entry) is identical to what's already classified as
Catastrophic; the classifier just doesn't know to look for it.

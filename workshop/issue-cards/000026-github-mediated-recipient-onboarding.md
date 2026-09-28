---
id: '000026'
status: done
created: 2026-05-19
updated: 2026-05-20
estimate_hours: 6
actual_hours: 7
---

# brain: GitHub-mediated recipient onboarding (invite/join/auto-admit/verify)

## Problem

Adding a new recipient to a shared brain today requires a manual
sneakernet ceremony even with #23 in place:

1. New user exports their pubkey (`gpg --armor --export`)
2. SCP / copy/paste to operator's host
3. Operator inspects fingerprint, verifies out-of-band
4. Operator runs `nous brain recipient add` with the `.pub` file
5. Operator separately runs `gh api PUT collaborators/<login>` (or
   web UI) to grant GitHub access
6. New user accepts the GitHub invite via web UI

Six steps across two humans, three trust handoffs (pubkey transfer,
fingerprint compare, GitHub invitation accept), and zero of them are
required by the actual threat model. The trust anchor is "operator
chose to invite this GitHub identity" — same model as WhatsApp ("you
chose to add this phone number"). Everything else is mechanism that
can be automated.

---
id: '000023'
status: done
created: 2026-05-19
updated: 2026-05-19
estimate_hours: 6
actual_hours: N/A
---

# Automate pubkey exchange via a plain `keys` branch on the gcrypt repo

## Problem

The shared-brain dogfood (`nous#12`) currently requires a manual
bidirectional pubkey sneakernet between every pair of recipients,
because gcrypt signs every manifest with the producer's GPG key and
the consumer must have that pubkey in their keyring to verify before
decryption. The failure mode is loud — `gpg: Can't check signature:
No public key` mid-clone — but the recovery is annoying:

```
on operator's machine:    nous identity export > xianxu.pub
                          (sneakernet xianxu.pub to peer)
                          (voice/in-person: read fingerprint last-8)
on peer's machine:        nous identity import xianxu.pub
                          (type last-8 to confirm)
                          git clone gcrypt::ssh://...
```

For an N-recipient brain, this is N(N-1)/2 manual exchanges. For a
two-person family brain that's tolerable (one exchange). For anything
larger — or for adding the third+ recipient to an existing brain —
it's a real friction tax that compounds with every addition.

The cost lands disproportionately on adoption: an operator who's
just admitted a peer and pushed shouldn't have to instruct them
through a separate file-exchange ceremony to make `git clone` work.

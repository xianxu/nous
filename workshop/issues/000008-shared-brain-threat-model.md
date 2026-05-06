---
id: 000008
status: working
deps: []
created: 2026-05-05
updated: 2026-05-05
estimate_hours: 2
---

# shared-brain threat model

## Done when

- A threat model document lives at `brain/atlas/threat-model-shared-brain.md` (or equivalent location agreed during M1) and is referenced from issues `#3`, `#6`, and the `shared-brain` project file.
- The document enumerates trust boundaries, what's defended at each, what isn't, and the explicit non-goals.
- The brain-private privilege concentration is named and its mitigations + accepted SPOF are written down.
- The "all agents on the machine can read all decrypted brains" posture is explicit, and agent sandboxing is parked as future work with a pointer to whatever issue picks it up later.
- Per-recipient implications (no per-file revoke; revocation = re-encrypt + assume past content leaked) are written down so anyone added to a brain understands the commitment they're entering.
- Reviewed with my wife before `brain-shared-family` is provisioned, so consent is informed.

## Spec

This issue precedes implementation. Writing the threat model is what tells us whether the gcrypt design in `#3` is the right one before migration; the conclusions inform `#6`'s skip-with-hint behavior; the posture statements gate the wife/me consent conversation before `brain-shared-family` exists. Cheap to author (an afternoon's prose), expensive to discover post-hoc.

The architectural reality the document has to be honest about:

**The security boundary is at decryption-on-disk, not at agent-level inside brain.** Once a brain repo is checked out and decrypted, every process on that machine that can read the filesystem can read the content. There is no per-file ACL, no per-agent gate, no read-only mode. This is a deliberate simplification — making it otherwise would require sandboxing every agent's filesystem access, which is nous-runtime work and out of scope for this project.

**Sharp consequences that follow from that boundary:**

1. **Decryption = total admission.** Cloning and decrypting a brain is the only access-control event. After that, all bytes are equally readable to anything running as the user.
2. **brain-private is a privileged target.** It holds the GPG private key, so compromise of brain-private = compromise of every `brain-shared-*` the user is a recipient on. Brain-shared compromise is scoped to that brain only. The asymmetry is by design (sharing is a sensitivity downgrade), but it concentrates risk.
3. **A hijacked agent on the local machine is a bigger threat than the host.** GitHub sees only ciphertext. An attacker with code-execution on my laptop reads brain-private, exfiltrates the GPG key, owns every shared brain. Mitigation lives at the agent-runtime layer (sandboxing, per-tool-call permissions), not the encryption layer.
4. **Per-recipient revocation is heavy.** Adding a GPG recipient is cheap (push a new commit with the updated recipient list). Removing one is expensive: rotate the keys, re-encrypt the history, and assume everything they had before is leaked. There is no "ungrant access to past content" — that's a property of any encryption-at-rest scheme, not a brain-specific issue, but worth naming so the social commitment is understood.
5. **All agents on a shared machine see all decrypted brains.** If wife and I share `brain-shared-family`, anything she puts in it is readable by *any* agent running as her user on her laptop, and vice versa. The privacy boundary between her private brain and the shared brain is enforced by *which repo each artifact is in*, not by anything inside a single repo.

**Passphrase storage for brain-private.** The pensive's "one passphrase to memorize" framing is the purist position; what an agent-driven workflow actually needs is a fetcher that runs fifty times a day without prompting. The threat model has to be explicit about which mode is in use, because the brain-private compromise surface is materially different across them:

- *Memorize only* — SPOF is human memory; compromise requires coercion or shoulder-surfing during entry. Highest friction; only viable if push/pull is rare.
- *macOS Keychain (`security find-generic-password`)* — unlocked when login keychain is unlocked, so brain-private becomes implicitly available to anything running as the user. Effectively zero friction; widens surface to "anything that can prompt keychain access on this machine."
- *1Password CLI (`op read`)* — requires the vault to be unlocked at fetch time. Moderate friction; best auditability (each fetch is a logged op-call).
- *gpg-agent + pinentry-mac* — the canonical pattern for asymmetric flows. Less natural for brain-private's *symmetric* passphrase; fits the `brain-shared-*` repos better, where the GPG private key (held inside brain-private) is the unlock primitive and gpg-agent caches its passphrase normally.

The threat model should name all four, document the tradeoff, and state the user's selected default. The implementation lives in `#3` M1 as a small wrapper script with a configurable source (tty / keychain / 1password / env). No per-mode issue; one knob, one set of consequences in the threat-model doc.

**Out of scope for this issue (parked, not denied):**

- Agent sandboxing (per-agent FS permissions). Belongs in nous-runtime; track separately if/when it becomes a priority.
- Cloud-hosted shared brains with multi-tenant key escrow. The current design assumes each recipient holds their own GPG private key; centralized escrow is a different threat model.
- Hardware-backed key storage (Secure Enclave, YubiKey-backed GPG). Worth considering as an upgrade path for brain-private; not gating MVP.
- Defense against a fully-compromised local OS. Mitigated socially (FDE, lock-on-sleep, don't run sketchy software) rather than architecturally.

## Estimate

Range: **1.5–3 hr**. Best guess: **~2 hr**.

*Produced via `brain/data/life/42shots/velocity/estimate-logic-v2.1.md` against `baseline-v2.1.md`. Method A only.*

| Milestone | Primitive | Design (×0.5) | Impl | Total |
|---|---|---|---|---|
| M1 — write doc | Pensive (long-form thinking) | 0.15–0.5 | 0.1–0.3 | 0.25–0.8 |
| M2 — wire into #3 / #6 / project | Atlas/docs (3 small edits) | ~0 | 0.15–0.4 | 0.15–0.4 |
| M3 — wife review (focused work only) | Capture + writeup | ~0 | 0.2–0.4 | 0.2–0.4 |
| **Subtotal** | | 0.15–0.5 | 0.45–1.1 | 0.6–1.6 |
| **+30% design buffer** | | +0.05–0.15 | n/a | +0.05–0.15 |
| **Total** | | | | **0.7–1.8** focused-hr |

Buffer rounded up to ~1.5–3 hr to absorb the doc's "what does the threat model actually claim" iteration cost (more design surprises in security prose than the milestone count suggests). M3 wall-clock includes ~30 min meeting with wife — that's not in the focused-hour budget.

## Plan

### M1 — write the threat model document

- [ ] Decide doc location: `brain/atlas/threat-model-shared-brain.md` is the leading candidate (atlas is for "big picture pointers, terminologies"; brain is the subject; cross-cutting state belongs in brain). Confirm or pick alternative during authoring.
- [ ] Trust boundary enumeration: host (GitHub), device (my laptop / wife's laptop), agent (Claude Code instance, parley.nvim), human recipient.
- [ ] For each boundary: what's defended, what isn't, with concrete attack examples for the "isn't" cases.
- [ ] Privilege-concentration section on brain-private (the keyring) and accepted SPOF mitigations.
- [ ] Agent-access posture section: "all agents on the machine read all decrypted brains; sandboxing is future work."
- [ ] Per-recipient commitments: granting access is easy, revoking is heavy; document the revocation procedure (re-keyed remote, assume leaked) so it's not a surprise.
- [ ] Passphrase-storage subsection: enumerate the four modes (memorize / Keychain / 1Password CLI / gpg-agent), the compromise-surface tradeoff for each, and pick a default. Cross-link to `#3` M1's implementation knob.
- [ ] Out-of-scope list with pointers (where future work would live).

### M2 — wire the document into the issues that depend on it

- [ ] Update `#3` lede / spec to reference the threat model and gate M1 on this issue's M1 being done.
- [ ] Update `#6` to reference the threat model's "skip-with-hint, don't try to decrypt" posture as the rationale for resolver behavior.
- [ ] Update `shared-brain` project file lede to point at the threat-model doc.

### M3 — review with wife before `brain-shared-family` is provisioned

- [ ] Walk through the document together, paying particular attention to the "all agents on her laptop read all of brain-shared-family" implication.
- [ ] Capture any concerns / desired posture changes back in the document.
- [ ] Confirm informed consent before `#4` M4 (the trip-planning dogfood) provisions a real shared brain.

## Log



- 2026-05-05: closed M2 — #3 Spec now references the threat-model doc and names macOS Keychain default; #6 Spec adds skip-with-hint rationale paragraph; project file lede links the doc
- 2026-05-05: closed M1 — threat model written at brain/atlas/threat-model-shared-brain.md (8 sections + open questions + versioning) and indexed in brain/atlas/index.md
### 2026-05-05

- Issue created after thread-level discussion on whether shared-brain's encryption boundary is per-file/per-agent or at-decryption-on-disk. Conclusion: at-decryption-on-disk, deliberately, and that posture has consequences worth writing down before any migration happens. Gates `#3` M1.

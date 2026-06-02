---
id: 000012
status: open
deps: [000004]
created: 2026-05-08
updated: 2026-05-08
estimate_hours:
---

# shared-brain dogfood — wife/me forcing-function

## Problem

The shared-brain project's `done_when` criterion is wife and me co-authoring `data/life/travel/2026-08-01-paris.md` end-to-end via `brain-shared-family` over ≥2 weeks of daily use, with ≥3 conflicts resolved by `/brain-resolve` and human-confirmed without data loss. That validation gate is the milestone that proves the substrate (`nous#4`) and the merge skill (`nous#5`) actually work in practice — not synthetic tests. Carved out of `nous#4 M4` so #4 can close on shipping the daemon while the dogfood is tracked as its own portfolio item.

## Spec

The MVP validation slot for the shared-brain project. Three operational pieces:

1. **Provision `brain-shared-family`** — gcrypt-encrypted brain admitted to two recipients (me + wife). Same flow as `nous#3`'s `brain-private` provisioning but multi-recipient. Place an initial `data/life/travel/2026-08-01-paris.md` (or shared-family equivalent) seeded with whatever notes already exist.

2. **Onboard wife's machine** — `make nous-bootstrap` on her Mac; her GPG keypair via `make identity` (or import if she already has one); admit her recipient to `brain-shared-family`; install the brain-sync daemon. This is the first real cold-start of `nous#11`'s bootstrap toolchain on a *different operator's machine* — surfaces friction that the VM dry-run (`nous#10`) couldn't.

3. **Run the dogfood for ≥2 weeks** — both peers edit the Paris plan as natural use evolves. Log every conflict that surfaces, how it was resolved (manual until `nous#5 M1` lands, then `/brain-resolve`), and whether the resolution preserved intent.

The dogfood is the load-bearing experiment. It tells us:
- Whether `nous#4`'s file-level-conflict-only semantics hold up against real edit patterns. If one of us routinely overwrites the other's section because we read a stale file before editing, the convention is wrong.
- Whether `nous#5`'s whole-file AI-prose merge handles the bulk of real conflicts gracefully (load-bearing v1 claim).
- Whether `nous#5 M4` (declarative section merges) is actually needed, or if the AI path covers everything we hit.
- Whether `nous#7`'s lock primitive is necessary, or if daily verbal coordination + good semantic merge obviates it.

## Done when

- `brain-shared-family` exists with both me and wife as recipients, syncing on both machines.
- The Paris trip plan is co-authored over ≥2 weeks of daily use.
- At least 3 conflicts have been resolved by `/brain-resolve` (i.e. after `nous#5 M1` ships) and human-confirmed without data loss.
- A log of every conflict that arose during the window — root cause, resolution path, verdict (clean / acceptable / wrong) — exists in this issue's `## Log` or in a linked artifact under `brain/data/`. This log is the evidence that informs whether to ship `nous#5 M4` and `nous#7`.

## Plan

### M1 — provision `brain-shared-family` + place initial trip plan

- [ ] Create `brain-shared-family` via `nous brain new --fingerprint $WIFE_FP` (after wife's pubkey is imported on operator's machine via the verify-fingerprint ceremony).
- [ ] Round-trip clone-and-decrypt verified end-to-end on at least one machine.
- [ ] Seed `data/life/travel/2026-08-01-paris.md` with whatever notes already exist (or an empty travel-plan instance if starting fresh).
- [ ] `.brain/config.md` declares `recipients: [me, wife]`. (No `mode:` field — the M4c schema cleanup derives shared-vs-private from `len(recipients)`.)

### M2 — onboard wife's machine

- [ ] Wife's Mac runs `make nous-bootstrap` to completion.
- [ ] Wife's GPG keypair set up via `nous identity init`.
- [ ] Wife's fingerprint sneakernet'd to operator and admitted via `nous identity import` (verify-fingerprint ceremony) — already in M1's recipient list since M1 needs it before provisioning.
- [ ] Wife's machine clones `brain-shared-family` via gcrypt; round-trip edit + commit + sync verified.
- [ ] brain-sync daemon installed on her machine via `nous service install`.

### M3 — dogfood ≥2 weeks

- [ ] Both peers edit the Paris plan over ≥2 weeks of natural use.
- [ ] Every conflict logged: timestamp, files involved, root cause (e.g. simultaneous edit, stale read, etc.), resolution path (manual / `/brain-resolve`), verdict (clean / acceptable / wrong), preservation outcome (any data loss?).
- [ ] After window closes, evaluate the log: did file-level convention hold? Does `/brain-resolve` cover the failure modes? Are locks (`nous#7`) needed?

## Test plan

The two-machine walkthrough that exercises the M4 surface end-to-end. This is the first time `nous brain new --recipient`, the verify-fingerprint ceremony, and gcrypt's multi-recipient re-key flow run against humans + a real GitHub remote rather than synthetic tests.

Keep a session log as you go (timestamps + observations + anything surprising). Any step that doesn't go as expected is data — write it down and either fix-forward in the same session (small) or file an issue (real).

### Phase 0 — prereqs

**Operator (this machine):**
- [ ] `nous` binary built +on PATH (signed if testing the agent-threat boundary; ad-hoc fine for a first pass). Smoke: `nous identity list` shows my fingerprint annotated `(self)`.
- [ ] `gh auth status` is logged in to GitHub with `xianxu` (or whichever account owns shared-brain repos).
- [ ] `nous service doctor` returns 9/9 green. Anything red blocks the rest.
- [ ] **Decide install posture before Phase 7.** Two valid choices:
  - **Dev-mode (`make nous-dev`)** — unsigned binaries, `charon-dev` keychain namespace, foreground process killed on Ctrl-C. Lower friction; good for iterating on nous itself.
  - **Installed (`make nous-install`)** — ad-hoc signed (or Developer ID via `NOUS_CODESIGN_IDENTITY`), `charon` namespace with ACL'd entries, launchd-managed `com.42shots.nous` running in background. Required for the *agent-as-threat* boundary that the proxy was built to enforce (see `atlas/nous/dev-vs-runtime-mode.md`).

**Wife's Mac (fresh-ish):**
- [ ] Apple ID signed in, Xcode CLT installed (`xcode-select --install`), Homebrew installed.
- [ ] git, gh installed (`brew install git gh`).
- [ ] gh auth done — she signs in with her own GitHub account.
- [ ] No prior nous setup. We're testing the cold-start path.

**Out-of-band channel:** decide before starting which channel we'll use to communicate her fingerprint's last 8 hex chars (voice call, FaceTime, in-person — NOT the same channel as the pubkey transfer). The verify-fingerprint ceremony only works if the channel is independent.

**Calendar:** ~45 min for Phases 1–6. Phase 7 (install daemon) is another ~5 min. Keep her in the loop on what we're doing — this is meaningful time, not a sneak-it-in moment.

### Phase 1 — wife's machine: identity + export

Run on her Mac. She drives; I narrate.

```sh
git clone https://github.com/xianxu/nous ~/workspace/nous
cd ~/workspace/nous
make nous-bootstrap                       # one-time toolchain: Homebrew deps, gh auth, GPG config
make build                                # build binaries to cmd/<name>/bin/<name>
export PATH="$PWD/cmd/nous/bin:$PATH"     # transient; phase-only convenience
nous identity init                        # TTY-only; pinentry-mac will prompt for passphrase
nous identity list                        # confirm her fingerprint shows up, annotated (self)
nous identity export > ~/Desktop/wife.pub
```

**Expected:**
- `identity init` opens a pinentry-mac dialog; she sets a passphrase she'll remember (or stash in 1Password). The passphrase is hers — I don't see it.
- `identity list` shows one secret key with her name + email + fingerprint (the `(self)` annotation fires via the only-one-secret-key implicit-primary fallback; no need to run `nous identity primary` until she has multiple keys).
- `wife.pub` is ~3 KB of `-----BEGIN PGP PUBLIC KEY BLOCK-----`.

**Why `make build` and not `make nous-install`:** the install path registers a launchd service that runs `nous serve`. `nous serve` errors out if no shared brains exist yet (the brain-sync goroutine refuses an empty workspace), and launchd's `KeepAlive=true` will then retry-backoff-loop. Cleaner to defer the install until Phase 7, after `brain-shared-family` exists locally.

**Watch for:**
- pinentry-mac not launching (gpg-agent config issue). If so: `gpgconf --launch gpg-agent` then re-run.
- `identity init` says "no $NOUS_DIR" or fails to find scripts/identity.sh. Worth filing if it happens.
- Wall-clock time. If `identity init` takes >2 min, that's surprising.

### Phase 2 — sneakernet

AirDrop `wife.pub` from her Mac to mine. (USB stick or signed-message-via-iMessage are alternatives if AirDrop misbehaves.)

**Note her fingerprint's last 8 hex chars.** She reads them aloud over an OUT-OF-BAND channel while I'm watching the import prompt. NOT via iMessage / Slack / email — those could be MitM'd in principle. Voice call or in-person.

### Phase 3 — operator: import wife's pubkey (verify-fingerprint ceremony)

```sh
nous identity import ~/Downloads/wife.pub
```

**Expected interaction:**
```
Pubkey to admit:
  fingerprint: ABCDEF... (40 hex)
  last-8:      XXXXXXXX
  uid:         Wife <wife@example.com>

Verify the last 8 hex chars match what the peer sent you OUT OF BAND
...

Type the last-8 to confirm (attempt 1/3): _
```

I type what wife reads aloud. If it matches → import commits, prints `Imported XXXXXXXX.`. If not → mismatch hint, re-try, abort after 3.

**Smoke after:**
```sh
nous identity list
```
Should now show wife's pubkey under "Public keys (peers admitted to brains)" annotated `(peer)`. No brain assignment yet (none have her as recipient).

**Watch for:**
- Multi-key armor refusal (M4 review fix #3) — shouldn't fire for a single-key export, but if it does, something's weird with her keygen.
- Case sensitivity in the prompt. M4 implementation is case-insensitive; verify in practice.
- Wife's fingerprint visible to me earlier in the flow — does the ceremony still feel meaningful? (Honest UX feedback worth logging.)

### Phase 4 — operator: provision brain-shared-family

```sh
WIFE_FP=ABCDEF...           # her full 40-char fingerprint, copied from `nous identity list`
nous brain new ~/workspace/brain-shared-family --fingerprint $WIFE_FP
```

**Expected interaction:**
1. Verify-fingerprint ceremony for wife's key (one more confirmation — admitting to a brain is a separate trust event from importing).
2. Confirms list of recipients (me + wife), then runs scripts/new-brain.sh:
   - prompts for GitHub repo name + visibility (private)
   - creates the repo via `gh repo create`
   - git init, sets remote, writes single-recipient manifest, single-recipient gcrypt push.
3. nous re-keys: rewrites manifest to multi-recipient (me + wife, no `mode:`), updates gcrypt-participants, commits, pushes.
4. Final line: `Brain provisioned.`

**Smoke after:**
```sh
nous brain list                                    # should show brain-shared-family with 2 recipients
nous brain recipient list ~/workspace/brain-shared-family
# Expect:
#   Shared: true (2 recipients)
#   Recipients (manifest): both fingerprints, annotated (self) and (peer)
#   gcrypt-participants: same two
#   No mismatch warning
```

**Watch for:**
- The two-push pattern's wall-clock impact. If it's >30 seconds, log it.
- gcrypt errors during the second push (re-encryption to wife's key). If `gpg: encryption failed: Unusable public key`, her pubkey is missing the encryption-capable subkey — Phase 1 went wrong.
- Manifest `mode:` field present after provisioning — should be absent. (M4c schema cleanup verifies.)

### Phase 5 — wife's machine: clone the shared brain

She runs:

```sh
cd ~/workspace
nous brain clone gcrypt::ssh://git@github.com/xianxu/brain-shared-family.git
cd brain-shared-family
ls -la                              # should see .brain/config.md + the seed content
cat .brain/config.md                # should show both recipients, no mode:
```

**Expected:**
- `nous brain clone` first fetches operator's pubkey from the brain's `keys` branch and gpg-imports it (post-`nous#23`), then runs the gcrypt clone. Without this auto-import step the gcrypt clone would fail with `gpg: Can't check signature: No public key` — gcrypt signs every manifest with the producer's GPG key, and the consumer must have it to verify.
- Clone takes a beat (gcrypt has to decrypt the pack). pinentry-mac may pop up asking for her passphrase if gpg-agent's cache is cold.
- `.brain/config.md` is plaintext on her side — gcrypt is at the remote, working tree is decrypted.
- Manifest matches what I provisioned.

**Pre-`nous#23` fallback** (only relevant if she's cloning a brain provisioned before the keys-branch design landed):
```sh
nous identity import /path/to/xianxu.pub   # one-time sneakernet hand-off
git clone gcrypt::ssh://git@github.com/xianxu/brain-shared-family.git
```
`nous brain clone` detects the missing keys branch and falls through silently to this flow; the operator's pubkey still needs to arrive OOB once.

**Optional verification ceremony** (`nous#23` opt-in): after the clone, she can confirm operator's pubkey wasn't tampered with on the keys branch:
```sh
nous brain recipient verify ~/workspace/brain-shared-family $XIANXU_FP
```
Renders fingerprint + UID, prompts for the last-8 he sent her OOB (phone/voice, NOT the same channel as the brain access), reports match/mismatch. Skippable; recommended once when she joins a new family member's brain.

**Watch for:**
- `gcrypt: cannot decrypt for any of the recipients` → her keyring doesn't have her own secret key (Phase 1 didn't actually generate it) OR my `--recipient $WIFE_FP` was wrong.
- `gpg: Can't check signature: No public key` → keys branch missing AND no manual pubkey import done. `nous brain clone`'s fallback hint should have surfaced before this.
- pinentry-mac silent failures (her macOS Keychain not configured). If she has to type her passphrase every operation, log it for `nous#3` follow-up.

### Phase 6 — round-trip edit

Both sides exercise commit + push + pull.

**Operator (me):**
```sh
cd ~/workspace/brain-shared-family
mkdir -p data/life/travel
$EDITOR data/life/travel/2026-08-01-paris.md   # whatever placeholder content; date + 2-3 ideas
git add -A && git commit -m "seed: paris trip plan"
git push
```

**Wife:**
```sh
cd ~/workspace/brain-shared-family
git pull --ff-only
cat data/life/travel/2026-08-01-paris.md         # should see what I wrote
$EDITOR data/life/travel/2026-08-01-paris.md     # add a few of her own ideas
git add -A && git commit -m "+ wife's paris ideas"
git push
```

**Operator:**
```sh
git pull --ff-only
cat data/life/travel/2026-08-01-paris.md         # should see her additions
```

**Watch for:**
- Wall-clock for push/pull (gcrypt overhead). If pulls take >20s on Wi-Fi, that's friction worth logging.
- Authorship in `git log` — should show her email on her commits, mine on mine.

### Phase 7 — install the unified nous daemon on wife's machine

One target rebuilds, signs (ad-hoc by default), installs to
`~/.local/bin/`, and registers + starts the `com.42shots.nous`
launchd service (proxy + brain-sync as goroutines in one process):

```sh
cd ~/workspace/nous
make nous-install
```

She should add `~/.local/bin` to her PATH if it isn't already
(`echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc` then
re-source).

**Smoke after:**

```sh
nous service status               # com.42shots.nous: running (PID …)
nous service doctor               # green across the board
launchctl list | grep 42shots     # single line for com.42shots.nous
tail ~/Library/Logs/nous.log      # proxy started + brainsync watching brain-shared-family
```

**Why `make nous-install` defers from Phase 1:** brain-shared-family
needs to exist locally (Phase 5's clone) before `nous serve`'s
brain-sync goroutine has anything to watch. Trying to install
earlier triggers a launchd retry-backoff loop on "no shared
brains."

**Signing posture:**
- Default is ad-hoc — the binary identifies as `com.charon.cli` and
  the keychain namespace flips to `charon` (production) with ACLs
  bound to that identifier. Sufficient for the agent-as-threat
  bypass-prevention boundary that the proxy was built to enforce
  (a different binary, even one signed by her or by an agent-
  written script, can't read the ACL'd entries).
- For cert-leaf-bound ACLs (strongest), run with
  `NOUS_CODESIGN_IDENTITY="Developer ID Application: …"` once a
  real identity is set up. Not blocking for the M1 dogfood — the
  ad-hoc boundary is meaningful by itself.
- Note: the signed binary uses the `charon` keychain namespace.
  Any provider OAuth tokens stored before signing (via `make
  nous-dev` runs in `charon-dev`) are not visible to the signed
  binary; she'll need to re-auth providers if she'd been testing
  any.

**Watch for:**
- Whether brain-sync auto-discovers `brain-shared-family` (it should — `lib/workspace.Root()` resolution + multi-recipient = shared).
- launchd respawn loop on first install if no shared brains were present (shouldn't fire after Phase 5; flag if it does).
- pinentry-mac prompting from the launchd-spawned process. The unified plist sets PATH including `/opt/homebrew/bin` so gpg-agent should hand off cleanly; if it doesn't, the brain-sync goroutine will block waiting for the passphrase. Workaround: `nous identity agent prewarm` (caches passphrase in agent for the session).
- Whether the proxy listens on `127.0.0.1:8230` (default). She doesn't need to drive the proxy yet — it's idle on her machine until an agent is wired through it.

### Phase 8 — conflict exercise (when natural conflict shows up)

Don't manufacture a synthetic conflict — wait for one to happen during real co-editing. When it does (probably mid-trip-plan):

```sh
git pull
# … merge conflict on 2026-08-01-paris.md …
/nous-resolve ~/workspace/brain-shared-family    # via Claude Code skill
```

**Log:**
- What was the conflict (sentence-level disagreement, structural reorg, both adding new sections, …)
- How `/nous-resolve` handled it
- Whether the resolution preserved both intents
- Any data loss

This is the load-bearing dogfood evidence. Aim for ≥3 real conflicts before evaluating M3.

### Things to log over the ≥2-week window

In `workshop/issues/000012-shared-brain-dogfood.md`'s `## Log` section, append entries as observations come in:

- Every conflict (date, files, cause, resolution, verdict, data-loss verdict).
- Every recipient operation (add, remove, who, why) — should be rare.
- Every push that took unusually long, every pull that hit gcrypt errors.
- Any time pinentry-mac surprised either of us (cache-flush, passphrase re-prompt, keychain access).
- Wife's UX feedback verbatim — her perspective is the ground truth on whether this scales beyond a sample size of one operator.
- Any place the docs (`atlas/`, `nous instructions`) sent us wrong.

### Failure-mode triage table

Quick reference for common things that go wrong. If hit, decide on the spot: fix-forward (small, same session) vs file an issue.

| Symptom | Likely cause | Quick check |
|---|---|---|
| `nous identity import` says "armored input contains 2 keys" | Wife's `gpg --export` picked up an old key by accident | She runs `gpg --list-secret-keys` — should be 1 key |
| `nous brain new` errors at `gh repo create` | gh auth on operator missing or wrong account | `gh auth status` |
| Wife's clone errors `cannot decrypt for any of the recipients` | Her keyring missing her own private key | Re-run `nous identity init` |
| Wife's clone errors `gpg: encryption failed: Unusable public key` | Operator's `nous brain new --recipient` got the wrong fingerprint | `nous identity list` — confirm her pubkey is in operator's keyring + matches what was passed |
| `nous brain recipient list` shows "WARNING: manifest and gcrypt-participants disagree" | Hand-edit drift, or interrupted re-key | `nous brain recipient add/remove` reconciles via the canonical writers |
| `nous service doctor` red on "recipient fingerprints in keyring" | Wife's pubkey not yet imported on operator | `nous identity import` |
| brain-sync log shows repeated `gcrypt: Repository not found` | `gh` repo wasn't created or branch is wrong | Visit the GH URL |
| pinentry-mac doesn't launch | gpg-agent stale | `gpgconf --launch gpg-agent` |

### Done-when (for this test plan, not the issue)

- Phases 1–6 walked through without filing a Critical/Important bug, OR all such bugs filed + addressed.
- Wife and operator have both pushed + pulled the trip plan at least once.
- brain-sync running on both machines.
- ≥1 honest piece of UX feedback captured (positive or negative).
- The above logged in `## Log`.

That marks the M1+M2 milestones as substantively walked. M3 (≥2 weeks dogfood) is then a calendar gate, not a code gate.

## Notes

- **Soft dep on `nous#5` M1+M2**: ideally `/brain-resolve` ships *before* the dogfood window starts so the very first conflict gets exercised through the skill. If M3 starts before #5 M1 lands, conflicts are resolved by hand for the interim — usable but less informative for skill calibration.
- **Out of scope**: lock primitive (`nous#7`); cross-brain reference syntax (`nous#6`); brain-shared-* beyond family (e.g. brain-shared-work). All deferred until family-brain is real.
- **Forking risk**: if wife or I prefer not to dogfood for the full ≥2 weeks (e.g. trip planning concludes earlier), shorten the window but require ≥3 real conflicts. The conflict count is the load-bearing evidence; calendar duration is a secondary gate.

## Log

### 2026-05-08 — created
Carved out of `nous#4 M4` after recognizing the dogfood is a portfolio milestone with its own provisioning + onboarding + multi-week-window structure, distinct from #4's daemon-ships scope. Tracking it here lets `nous#4` close cleanly on M1–M3 (substrate + daemon + convention + manual resolve) while the dogfood is the standalone validation gate for the project's `done_when`.

### 2026-05-15 — side-quest: `make new-brain` UX against pre-existing remote with stale gcrypt state

Dry-run from a fresh tart VM (`make tart` → `make nous-bootstrap` → `make identity` → `make new-brain ../brain`) failed on the push:

```
gcrypt: Decrypting manifest
gpg: public key decryption failed: Wrong secret key used
gcrypt: Failed to decrypt manifest!
```

Root cause: `emmatest42/brain` on GitHub already had gcrypt refs from a prior run encrypted to a different identity (earlier VM iteration with a different GPG key). gcrypt always reads the existing manifest before push to chain protocol state. Manifest is encrypted to the *previous* recipient set; new VM's key can't decrypt; abort happens *before* any Git push, so the `git push --force` semantics never come into play.

UX bug in `scripts/new-brain.sh:79-94`: when the GH repo already exists, the prompt offers "Force-push to replace its contents? [y/N]" — misleading because force-push can't reach the failure point. Same bug in `scripts/cloneto.sh:71-77`.

Fix shape: when the existing repo has any branches, prompt instead "Delete `$GH_FULL` and recreate it fresh? [y/N]" → on yes, `gh repo delete --yes` + `gh repo create`. Empty placeholder repos pass through unchanged. Matches the actual recovery path; surfaces the destructive intent honestly.

Implemented in this session: `scripts/new-brain.sh` + `scripts/cloneto.sh`, plus docstring touch-ups. No new tests — script is interactive + side-effectful at the GitHub API layer, covered by the M1 walkthrough re-run.

### 2026-06-01 — scope clarification: the gap is a *durable* daily-use brain, not one-shot sync

Reframed after nous#36 landed the *scriptable* headless-VM path. Manual sync between shared brains has already been exercised (one-shot). What's still **missing — and is the real `done_when`** — is a **durable, real, day-to-day** shared brain: one actually used instead of the personal brain for certain tasks, continuously over several days, so conflicts/merges arise from real use rather than synthetic edits. Operator will stand up a fresh durable VM for this (next: 2026-06-02). nous#36 makes setup repeatable; the durable continuous-use gate is the remaining work here.

Onboarding-mechanism findings from today's setup (feed the durable run):
- **Two pubkey-distribution paths — don't mix them.** GitHub-mediated (`nous brain invite` → invitee `nous brain join` publishes `<login>.asc` to the keys branch → operator's brain-sync `ImportAllPubkeys` + `AutoAdmitFromKeysBranch` auto-import/admit) is the path for a GitHub-backed brain — no manual export/import. Sneakernet (`nous identity export` → `import --verified-last8` → `recipient add`) is the GitHub-free / explicit-verify path (what the file:// e2e uses).
- **Local key deletion is undone by the remote.** A recipient `fp` lives in manifest + keys branch + `verified.yaml`; `gpg --delete-keys` locally gets re-imported/re-admitted on the next sync/invite. True removal needs system-wide revocation → filed as **nous#37**.
- **Never reuse a brain repo across keys.** A repo ever encrypted to a now-gone key gives `Failed to decrypt manifest!` (same class as the 2026-05-15 note). Reset = `gh repo delete && recreate` (or a fresh brain name), until nous#37.
- **Shim ordering** (nous#36): on a fresh VM the GPG-unattended hook runs before `make nous-bootstrap` installs gpg, so it defers; it self-heals on the first boot after gpg exists (or re-run `scripts/brain-vm-setup.sh` once).

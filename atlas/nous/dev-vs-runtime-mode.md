# Dev mode vs runtime user mode

A distinction worth pinning before scope decisions: nous (and ariadne)
has two operator personas, and most of the current codebase implicitly
addresses one of them.

## The two personas

**Dev mode operator** — the engineer who is iteratively *building*
nous (or building features on top of nous). They:

- Rebuild binaries multiple times a day.
- Run `make nous-dev` to iterate; foreground daemons, ctrl-c, rebuild.
- Don't need code signing — their binaries are ephemeral.
- Don't need notarized menubar apps — they read logs and CLI output.
- Live in the `charon-dev` keychain namespace because their binaries
  are unsigned (per `lib/provider/vault/keychain/service.go`).
- Tolerate friction (manual setup, env vars, occasional broken state)
  in exchange for fast inner-loop iteration.

**Runtime user mode operator** — someone who *uses* nous as a brain
companion (planning a trip, journaling, asking the AI about their
calendar). They:

- Install once, use daily.
- Don't iterate on the code; expect a stable binary that doesn't
  surprise them across days.
- Need real notifications, menubar surfaces, signed `.app` bundles
  (because macOS Gatekeeper + UserNotifications require it).
- Should live in the production keychain namespace (`charon`) so
  their credentials are ACL-bound to the specific signed binary
  and won't leak to other dev-mode binaries that happen to share
  the operator's account.
- Need a packaging story: homebrew bottle, .pkg installer, .dmg —
  something that handles install + first-run + updates without
  asking them to clone a repo.

## Where the codebase sits today

Aimed predominantly at **dev mode**. Specifically:

- `make build`, `make nous-dev`, `make nous-install` all produce
  unsigned binaries. The codesigning self-check in
  `keychain/codesign_darwin.go` exists in the code but routes
  every dev binary into `charon-dev`.
- The verify-fingerprint ceremony, the TTY-only delegation
  boundary, the agent-vs-human help split — all assume a developer
  who reads cobra help and types into a terminal.
- `nous-security` is the partial exception: it knows about the
  bundle-vs-bare distinction and has notification machinery. But
  it's not actually built as a `.app` bundle today (see nous#19).
- `ariadne`'s base layer — which seeds the workflow conventions
  (`AGENTS.md`, `workshop/`, `atlas/`, `construct/`) — is similarly
  dev-mode-shaped. The `make issue-sync` / `make close-issue`
  primitives assume an engineer iterating on issues; a
  travel-plan-companion runtime user wouldn't touch them.

## Implications for ongoing work

1. **Don't conflate the two in install flows.** nous#16's
   `make nous-install` is a dev-mode install (unsigned, foreground-
   capable). nous#19's `nous-security.app` packaging is a
   runtime-mode artifact (signed, notarized, installable to
   `/Applications/`). Mixing them produces a flow that's neither
   convenient for dev nor solid enough for daily use.

2. **Keychain namespace stays dormant for now.** The runtime check
   in `keychain/service.go` (`ServiceProd` vs `ServiceDev`) is
   coded for the future where some binaries are signed and some
   aren't. Today every install-flow binary lands in `ServiceDev`,
   so the namespace split is effectively a no-op. Keep the
   machinery — it's the cheap stub for runtime-mode arrival —
   but don't pretend it's load-bearing.

3. **Runtime-mode packaging is its own scope.** When the operator
   says "the wife is using this daily to plan our trip," the
   forcing function is real, and the work is: notarized
   `nous-security.app`, homebrew bottle (or .pkg), bootstrapped
   directory structure (where does `~/.config/nous/` live for a
   non-engineer who didn't `git clone`?), first-run UX (no `make
   nous-bootstrap`; some other onboarding). That's not a single
   issue — it's a project. File when needed; don't pre-build.

4. **Ariadne base-layer assumption.** A meaningful chunk of
   ariadne's `construct/` machinery + the workflow conventions
   only make sense to a developer-operator. For a brain-companion
   runtime user, those conventions are dead weight. When the
   forcing function arrives, the question to answer is whether
   ariadne grows a runtime-mode subset, or whether nous forks the
   relevant pieces. Don't try to answer that today; just don't
   let dev-mode conveniences calcify into "this is how nous
   works" assumptions.

## See also

- `nous/workshop/issues/000016-unified-nous-serve-dev-prod-workflow.md`
  — the dev-mode install flow (`make nous-dev`, `make nous-install`)
- `nous/workshop/issues/000019-nous-security-app-packaging.md` — the
  first runtime-mode artifact (signed + notarized menubar)
- `lib/provider/vault/keychain/service.go` — `ServiceProd` /
  `ServiceDev` namespace split (dormant today; load-bearing once
  signed binaries exist)
- `cmd/nous-security/notify_darwin.go` — bundle-vs-bare detection +
  osascript fallback (the current dev-mode workaround)

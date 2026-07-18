---
type: project
name: charon-launch-push
goal: "Get charon launch-ready as my personal AI gateway — usable across LLM providers AND safe to leave running unattended."
done_when: "Charon runs as my sole credential proxy for OpenAI + Anthropic + Vertex daily traffic for ≥1 week, disarmed-by-default at boot, kept armed by continued activity after I rearm via Charon Security.app, and auto-disarms once idle (e.g., laptop closed) for ≥1h — no fallback."
status: done
charon_13_status: done  # OpenAI provider chain + TUI flows + proxy data plane closed 2026-04-30
charon_14_status: done  # Google AI providers + GCP project mgmt + AI Studio mint/route/revoke; M7 docs landed 2026-05-01
charon_16_status: done  # runtime consent + caller ID + stats + menubar; all 8 phases + 2 review passes closed 2026-05-03
charon_15_status: done  # all milestones + both review chunks closed 2026-05-04; M4 e2e verified, M4b e2e verified during review followups
operator: xianxu
mvp_scope: [charon#13, charon#14, charon#15, charon#16]
explicitly_out: [charon#2, charon#8, charon#9]
created: 2026-04-30
updated: 2026-05-04
closed: 2026-05-04
sources:
  - /Users/xianxu/workspace/charon/workshop/issues/000013-api-key-providers-openai-anthropic.md
  - /Users/xianxu/workspace/charon/workshop/issues/000014-google-ai-providers.md
  - /Users/xianxu/workspace/charon/workshop/issues/000015-provider-catalog.md
  - /Users/xianxu/workspace/charon/workshop/issues/000016-runtime-consent-and-stats.md
  - /Users/xianxu/workspace/brain/data/project/charon-release-push.md
---

# charon-launch-push

A focused push to make charon launch-ready as my personal AI gateway: multi-provider coverage (#13 OpenAI/Anthropic, #14 Google AI, #15 long-tail catalog) **plus** runtime consent and observability (#16) so the gateway is safe to leave running unattended. Supersedes `charon-release-push` by folding #16 back in. **Out of MVP:** Linux secret service (#2 — Mac-only launch), 403→denial synthesis (#8 — UX polish), cloud-scalable vault (#9 — later phase).

**Estimates (v2 update, 2026-04-30):** ~37 hr remaining best-guess. #13 closed at ~5 hr actual (v1 estimate was 41–78 hr; v1 over-estimated ~10× for AI-paired work). v2 estimator authored to split design + impl per primitive — see `data/life/42shots/velocity/estimate-logic-v2.md`. Anthropic moved from #13 to #15 mid-issue (Admin API can't programmatically create keys). Per-issue v2 best-guesses: #14 ~5 hr, #15 ~12 hr, #16 ~20 hr. Originally estimated ~210h under v1.

**#14 update (2026-05-01):** Spec amended mid-stream — original 6-milestone list grew a new M3 (full GCP project management) once smoke-testing surfaced that OAuth alone doesn't carry project_id/region. Spec estimate revised from 2.4–7.6 hr to 3.9–11 hr (~7.5 hr midpoint). Actual hours not tracked — the session included substantial side-quests (`charon manifest` reshape, embedded `charon instructions`, runtime-discovery file, `make dev`, multi-account UX polish, Refresh-drops-sidecar bug fix, code-review followups) so actuals are not a clean velocity signal. M7 docs landed 2026-05-01 in `1466777`; #14 fully closed.

**#15 closed (2026-05-04):** All 8 milestones (M1–M7 plus M4b) + both review chunks done. Final shape:
- M1 schema + Anthropic seed (`cba90fc`); M2 catalog picker (`8bcf871`); M3 routing + AuthHeader/AuthQuery (`5ed9a87`); chunk-1 review followups (`6f0baf4`).
- M4 paste flow (`fd4daf2`), e2e verified through prod proxy with real Anthropic key.
- M4b revoke dispatcher + list/manage TUI (`f71a605`) + 5 e2e-driven UX followups (`2633b20`, `802c1bf`, `125fd60`, `17b4403`, `cb98234`, `48c3b5b`, `a7d31f4`) — revoke posture flip to default-preserve, OSC 8 hyperlinks, action-hint visibility, enter-on-account no-op, in-session per-screen cursor memory across drill-out → drill-in.
- Chunk-2 review followups (`0489db3`): 3 Important (vault.Delete dead-end, dead AuthSource field, http timeout backstop) + 3 Minor + posture pinned in issue Notes.
- M5 verify-on-paste + M7 onboarding cursor (`a79e99f`); M6 docs + issue close (`dc79f1f`).

Originally scoped 13 LLM-inference providers; reduced mid-stream to **Anthropic-only** as a generic API-key paste-and-revoke mechanism (not LLM-specific) since API-key auth covers many use cases beyond inference. Plan at `workshop/plans/000015-provider-catalog-plan.md`. Revised midpoint estimate ~7–8 hr (vs. v2 best-guess of 12 hr); actual delivery was higher because of UX iteration during e2e and the broader-than-planned M4b scope, but in the same ballpark.

**#16 update (2026-05-03):** Closed in two sittings. v2 estimate was ~20 hr; actuals were *roughly* in that ballpark but, as with #14, padded by side-quests (the menubar UX polish that triggered Phase D refinements — native UserNotifications via cgo Obj-C, adaptive countdown polling, and the disarmed-request audit fix — wasn't on the milestone list). Two review passes (A+B+E+F → `9803c11`; C+D+G → `bb42b79`); both clean of Critical, all Important findings addressed. Five Minor findings (wire versioning, listen→chmod race, sync click handlers, pollLoop shutdown, Close() path) logged for future cleanup but did not block closure. The cooldown-on-oracle behaviour is spec'd in the issue and deferred — no MVP-blocker. The single load-bearing decision worth re-reading is in `docs/threat-model.md` defense layer 7 + threat A1b: the gate explicitly does not defend against an actively-using user; it defends against agent activity while the user is away.

## tasks

- [x] TUI design sketch (cross-cutting for the provider chain) [charon#13 sketch]
- [x] provider interface skeleton [charon#13 M1]
- [x] OpenAI provider impl + threat-model amendment [charon#13 M2]
- [~] Anthropic provider (mirror of M2) [charon#13 M3] → demoted to catalog (#15); Admin API can't create keys
- [x] OpenAI/Anthropic TUI flows [charon#13 M4]
- [x] proxy injection per-host routing [charon#13 M5]
- [x] account-level rm refactor (lift to list level) [charon#13 M6]
- [x] docs — agent-protocol, README, threat-model [charon#13 M7]
- [x] code review chunk 1 [charon#13 review-1]
- [x] code review chunk 2 [charon#13 review-2]

- [x] add cloud-platform scope to Google catalog [charon#14 M1]
- [x] Vertex routing + smoke test [charon#14 M2] — zero new code; existing `.googleapis.com` suffix rule already covered Vertex regional hosts. Regression tests added.
- [x] GCP project management — pick/create/enable/billing/region [charon#14 M3-new] — added mid-stream; was not on this list. OAuth alone is insufficient for Vertex (URL needs project+region) and AI Studio mint (needs project_id). Surfaced billing-block stop-and-wait, project-pin (for fresh-create eventual-consistency), and integrates with AI Studio mint below.
- [x] AI Studio key mint flow [charon#14 M4] — auto-minted at end of M3 setup; one key per account; non-fatal failure surfaced via persistent applyStatus.
- [x] AI Studio routing [charon#14 M5] — first non-header auth in proxy (URL-param `?key=`); `Provider.VaultProvider` lets the routing entry piggyback on existing google credential; cred.AIStudio.KeyMaterial attached transparently.
- [x] Vertex / AI Studio revoke flow [charon#14 M6] — `apikeys.googleapis.com DELETE` on `accounts rm`; ordered before OAuth revoke (DELETE needs the bearer); failure non-fatal with status note. Project preserved per lifecycle rule.
- [x] docs — agent-protocol / README / threat-model touch-up [charon#14 M7]
- [x] code review [charon#14 review] — subagent run mid-session, surfaced 4 important items (cache flush parity, broader sidecar test coverage, billing-block test coverage), all fixed.

- [x] catalog schema + Anthropic seed YAML [charon#15 M1] — scope reduced from 13 LLM providers to Anthropic-only seed; mechanism designed to grow. Schema (Entry/Auth/Revoke/ListEndpoint) + load.go validation + 16 tests landed in `cba90fc`.
- [x] catalog loader + TUI picker [charon#15 M2] — eager `catalog.Load()` at TUI startup; cursor-based list mirroring providerPickerModel (n=1 today; bubbles/list filter is overkill until ~5+ entries); + add provider transitions instantly. Landed in `8bcf871`.
- [x] generic metadata-driven per-host router [charon#15 M3] — added AuthHeader, renamed AuthURLParamKey→AuthQuery, plumbed HeaderName/HeaderPrefix/ExtraHeaders, registered Anthropic catalog entry into proxy routing, added `vault set --type catalog` testing path. End-to-end verified 2026-05-03: real Claude request through proxy with x-api-key + anthropic-version injection. Landed in `5ed9a87`.
- [x] TUI add-account flow ([Open] + paste + Anthropic e2e) [charon#15 M4] — `catalogPasteModel` (account-name → masked key paste; ctrl+o opens key URL via `open`/`xdg-open`); on success, success hint includes ready-to-run `charon run -- curl …` for the entry's first hostname. 7 teatest cases. E2e verified against prod proxy: real key → 200 with content from `claude-opus-4-7`. Landed in `fd4daf2`.
- [x] catalog revoke dispatcher + list/manage TUI [charon#15 M4b] — scope broadened from original plan: M4b also ships the picker row + per-provider account list deferred from M4. (a) `Entry.RevokeKey(ctx, key)` dispatcher: list-then-deactivate (Anthropic shape) + direct revoke; partial-key-hint suffix matcher; bearer/header/query auth dispatch; ErrNoRevokeEndpoint + ErrKeyNotFound sentinels. (b) provider-picker TypeCatalog row per entry with stored creds (green ●, "API key" label, count). (c) `catalogAccountListModel` mirroring admin_key_list shape — one row per stored credential + "+ add account". (d) `catalogRevokeModel` confirm/in-progress/upstream-failed state machine; on upstream-fail, default key (esc/n/enter) preserves credential, `[d]` is explicit force-delete affordance — flipped from original "any-key falls back to local-delete" posture during e2e because catalog credentials are charon's *handle* on the upstream key. 17 new tests. Landed in `f71a605` + 5 followups during e2e (revoke posture flip, OSC 8 hyperlinks, action hint visibility, enter-on-account no-op, in-session per-screen cursor memory across drill-out → drill-in) through `a7d31f4`.
- [x] --verify flag (health-check post-paste) [charon#15 M5] — `Entry.Verify(ctx, key)` returns VerifyOK / VerifyRejected / VerifyEndpointError; TUI gains a brief "verifying..." state between key paste and store. 401/403 sends user back to retype; 5xx/network stores with degraded "verify inconclusive" note; 2xx stores with "verified" note. Anthropic's `verify_url` is `/v1/models`; entries without `verify_url` skip the probe. 6 dispatcher tests + 4 paste-flow integration tests. Landed in `a79e99f`.
- [x] docs — README / providers.md / threat-model notes [charon#15 M6] — README catalog bullet alongside OAuth and admin-key; new `docs/providers.md` carries the full catalog reference (schema, validation, verify+revoke postures, how to add a new entry); `docs/threat-model.md` Catalog asset row + Catalog (Tier-3) trust boundary subsection (SSRF surface bounded by embedded curated YAML); `atlas/charon.md` Catalog providers subsection; `atlas/index.md` surfaces `docs/providers.md`. Landed in `dc79f1f`.
- [x] onboarding polish (default to catalog when empty) [charon#15 M7] — when vault has 0 credentials, provider-picker initial cursor lands on `+ add provider` so first-run users land on an actionable row. One-line check at the bottom of `newProviderPickerModel`; cursor-preservation across drill-out → drill-in (added pre-chunk-2) overrides on subsequent rebuilds. Implementation simpler than the plan's auto-jump (composes with cursor memory and doesn't trap users who want OAuth). Landed in `a79e99f`.
- [x] code review chunk 1 [charon#15 review-1] — superpowers-code-reviewer subagent on M1+M2+M3 (BASE 9843ab4 → HEAD 8bcf871). 0 Critical, 4 Important, 8 Minor. All Important addressed in `6f0baf4`: suffix-collision detection in `catalog.Register`, duplicate-hostname load-time rejection, atlas doc post-rename fixup, defensive `t.Cleanup` on the compiled-host precedence test. Plus AI-Studio-vs-catalog disambiguation test added to lock the riskiest dispatch invariant. Minors deferred (cosmetic / DRY).
- [x] code review chunk 2 [charon#15 review-2] — general-purpose subagent w/ requesting-code-review template on M4b + 5 followups (BASE 6f0baf4 → HEAD a7d31f4). 0 Critical, 3 Important, 9 Minor. All 3 Important addressed in `0489db3`: (#1) `[d]`-on-vault-failure dead-end fixed — clean exit with status note instead of stuck loop; (#2) dropped dead `Revoke.AuthSource` field (validator enforced single value but dispatcher ignored it — YAGNI cleanup); (#3) package-local httpClient with 30s timeout backstop in revoke.go. Three Minors landed (parseResultPath error msg, vault.List cost comment, restrict any-key→explicit-keys on revoke overlay). Reviewer's load-bearing recommendation about `auth_source: admin_key_ref` for non-admin pasted Anthropic keys filed as Open Question for future schema evolution.

- [x] proxy session state — runtime consent skeleton [charon#16 A]
- [x] caller identification + audit-log enrichment [charon#16 B]
- [x] security.app trust edge (unix socket + peer DR) [charon#16 C]
- [x] security.app menubar + consent UI [charon#16 D] — MVP via fyne.io/systray; refinements layered on (native UserNotifications cgo, adaptive 1 s/10 s countdown polling, disarmed-request audit logging)
- [x] stats: Tier 1 + Tier 2 (sizes, content-type, JSON counts) [charon#16 E]
- [x] CLI surfaces — `charon who` / `charon stats` [charon#16 F]
- [x] atlas + threat-model docs [charon#16 G]
- [x] code review (A+B+E+F → 9803c11; C+D+G → bb42b79) [charon#16 review]

## details

<a id="charon-13-sketch"></a>
### charon#13 sketch — TUI design sketch (cross-cutting)

**est:** 1–2h

Cross-cutting work upfront for the provider chain — TUI scaling sketch covering OAuth (Google), admin-key (OpenAI/Anthropic), and Tier 3 (catalog) provider types. First deliverable of the chain since it locks patterns for #13 + #14 + #15. Validates before TUI implementation in #13 M4.

<a id="charon-13-m1"></a>
### charon#13 M1 — provider interface skeleton

**est:** 2–4h

`internal/providers/` package skeleton. New `Provider` interface separate from existing `internal/oauth/`. Establishes the scaffolding that #14 and #15 reuse.

<a id="charon-13-m2"></a>
### charon#13 M2 — OpenAI provider impl + threat-model amendment

**est:** 10–16h

Admin API client (Bearer auth) + Keychain storage of admin key + per-account minted keys + project-list / mint / revoke operations. Includes amending `docs/threat-model.md` to add the admin-key asset class as the highest-blast-radius credential charon holds.

<a id="charon-13-m3"></a>
### charon#13 M3 — Anthropic provider (mirror of M2)

**est:** 4–8h

Mirror M2 with Anthropic's `x-api-key` header convention. Pattern reuse — should be much smaller than M2 once the shape is established.

<a id="charon-13-m4"></a>
### charon#13 M4 — OpenAI/Anthropic TUI flows

**est:** 8–14h

Provider picker + admin-key paste-and-store + account add/rm flows. Validates the TUI sketch from `charon#13 sketch` works end-to-end.

<a id="charon-13-m5"></a>
### charon#13 M5 — proxy injection per-host routing

**est:** 4–8h

Per-host routing for `api.openai.com` and `api.anthropic.com` with the right header shape per provider.

<a id="charon-13-m6"></a>
### charon#13 M6 — account-level rm refactor

**est:** 2–4h

Lift account revoke from "go into account → revoke" to "select on account list → revoke." Applies to both OAuth and admin-key providers. Could ship independently if M4 is delayed.

<a id="charon-13-m7"></a>
### charon#13 M7 — docs

**est:** 1–3h

Update agent-protocol.md (header semantics for provider types), README (new "What it does" section), threat-model.md (admin-key asset class — already covered by M2's amendment but reconcile here).

<a id="charon-14-m3"></a>
### charon#14 M3 — AI Studio key mint flow

**est:** 5–8h

Google API Keys API client + Keychain storage + project resolution + API-enablement detection. Reuses #13's mint scaffolding.

<a id="charon-15-m1"></a>
### charon#15 M1 — catalog schema + 13-provider seed

**est:** 2–5h

Define catalog YAML schema; seed 13 providers (Groq, Cohere, Mistral, xAI, Perplexity, Together, Fireworks, DeepInfra, Replicate, OpenRouter, Voyage, Jina, Anyscale). Per-provider URL/auth-shape research — light per-provider but adds up.

<a id="charon-15-m3"></a>
### charon#15 M3 — generic metadata-driven per-host router

**est:** 8–14h

The meat of #15. Reads catalog at startup, compiles hostname patterns, dispatches per-request: hostname → provider entry → keychain lookup by `X-Charon-Account` → attach auth header per `auth.style`. Lives alongside #13's compiled-provider routing.

<a id="charon-15-m4"></a>
### charon#15 M4 — TUI add-account flow

**est:** 6–12h

Pick from catalog → `[Open]` buttons (subprocess `open <url>` on macOS) for signup / key URLs → paste key → store. End-to-end test against 3 providers. The 60-second add-provider UX target lives here.

<a id="charon-16-a"></a>
### charon#16 A — proxy session state (runtime consent skeleton)

**est:** ~12h

`armed bool` + `expires time.Time` proxy state under mutex. CONNECT handler returns structured 407 when disarmed. Idle + absolute timers. `/session/{arm,disarm,status}` HTTP endpoints. CLI: `charon arm` / `charon disarm` / `charon status`. Tests: state transitions with mock clock, drain-vs-RST for in-flight requests.

<a id="charon-16-b"></a>
### charon#16 B — caller identification + audit-log enrichment

**est:** ~10h

`internal/proxy/peerinfo_darwin.go` using `proc_listpids` + `proc_pidinfo` keyed on local TCP 4-tuple. Parent-chain walk via `PROC_PIDT_BSDINFO.ppid`. Resolve once per CONNECT, cache for tunnel lifetime. Extend `audit.Record` with `peer_*` fields.

<a id="charon-16-c"></a>
### charon#16 C — security.app trust edge

**est:** ~12h

Unix-domain socket at `~/Library/Caches/charon/runtime.sock`, perms 0600. Proxy rejects connections whose peer DR ≠ `com.charon.security`. Signed approval token, key in keychain ACL-pinned to security.app's DR. Small JSON protocol over the socket (`Arm`, `Disarm`, `Status`, `RecentActivity`).

<a id="charon-16-d"></a>
### charon#16 D — security.app menubar + consent UI

**est:** ~14h

`LSUIElement` mode for the bundle. Status glyph + click-to-arm panel. Live connection count via streaming `RecentActivity`. Disarm-on-idle notification. Blocked-CONNECT toast with rate cap. Audit log viewer with search.

<a id="charon-16-e"></a>
### charon#16 E — stats: Tier 1 + Tier 2

**est:** ~14h

Tier 1: `req_bytes`, `resp_bytes`, `resp_content_type` plumbed through existing streams. Tier 2: size-capped JSON tee, top-level array count, streaming-detection skip path (chunked + SSE/NDJSON). Update `docs/threat-model.md` with content-sampling posture shift. Tests on Google's standard list shapes.

<a id="charon-16-f"></a>
### charon#16 F — CLI surfaces

**est:** ~6h

`charon who` (live + `--since 1h`). `charon stats --since 1h` aggregator. Human-readable default, `--json` for scripts.

<a id="charon-16-g"></a>
### charon#16 G — atlas + threat-model docs

**est:** ~5h

`atlas/charon.md` session model section. `atlas/security-audit.md` runtime-consent role for security.app. `docs/threat-model.md` armed/disarmed gate, content-sampling posture, cooldown-on-oracle rationale.

<a id="charon-16-review"></a>
### charon#16 review — milestone code reviews

**est:** ~12h

Three review chunks at phase boundaries (A+B, C+D, E+F+G) via `superpowers:requesting-code-review` → `superpowers:code-reviewer`. Address Critical/Important findings before next phase.

[charon#13 sketch]: #charon-13-sketch
[charon#13 M1]: #charon-13-m1
[charon#13 M2]: #charon-13-m2
[charon#13 M3]: #charon-13-m3
[charon#13 M4]: #charon-13-m4
[charon#13 M5]: #charon-13-m5
[charon#13 M6]: #charon-13-m6
[charon#13 M7]: #charon-13-m7
[charon#14 M3]: #charon-14-m3
[charon#15 M1]: #charon-15-m1
[charon#15 M3]: #charon-15-m3
[charon#15 M4]: #charon-15-m4
[charon#16 A]: #charon-16-a
[charon#16 B]: #charon-16-b
[charon#16 C]: #charon-16-c
[charon#16 D]: #charon-16-d
[charon#16 E]: #charon-16-e
[charon#16 F]: #charon-16-f
[charon#16 G]: #charon-16-g
[charon#16 review]: #charon-16-review

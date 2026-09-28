---
id: 000025
status: done
deps: []
created: 2026-05-19
updated: 2026-05-19
estimate_hours: 0.5
actual_hours: 0.3
---

# scripts/new-brain.sh: use REST repo endpoint, not GraphQL — fresh-account lag

## Problem

Bootstrapping a brain under a brand-new GitHub account (`yingtest42`,
created ~30 min before retry) failed at `gh repo create`:

```
HTTP 404: Not Found (https://api.github.com/users/yingtest42)
```

Even after manually creating `yingtest42/brain` via the web UI, the
script's `gh repo view "$GH_FULL"` check (line 151) returned non-zero,
so the script fell into the else branch and tried `gh repo create`,
which then 404'd again on `/users/<login>`.

Empirical from the VM:

| call                                  | result                            |
|---------------------------------------|-----------------------------------|
| `gh api user --jq .login`             | `yingtest42` (auth token works)   |
| `gh api users/yingtest42`             | 404                               |
| `gh api repos/yingtest42/brain`       | **200 — full repo JSON**          |
| `gh repo view yingtest42/brain`       | "Could not resolve" (GraphQL)     |
| `gh repo create yingtest42/brain ...` | 404 on /users/yingtest42          |

The pattern: GitHub's GraphQL and `/users/<login>` lookup caches lag
for very-new accounts (~minutes to hours), but the REST repo endpoint
`/repos/<owner>/<name>` resolves immediately via the repo's own ID.

## Insight

`gh repo view` uses GraphQL under the hood; `gh repo create` validates
the owner via REST `/users/<login>`. Both have propagation-cache
sensitivity that the script doesn't need to inherit — the existence
check can use the more-direct REST repo endpoint that already works.

`gh repo create` is harder to bypass (its owner-validation is internal),
but it's only called when the repo doesn't exist. With a more-robust
existence check, the create path is skipped for the common "user
already made the repo via web UI as a workaround" flow.

## Done when

- `scripts/new-brain.sh` line 151: `gh repo view "$GH_FULL"` becomes
  `gh api "repos/$GH_FULL" --silent`. Functionally equivalent for the
  existence-detection use case, but resolves via REST/repo-ID instead
  of GraphQL/login-lookup.
- A `SKIP_REPO_CREATE=1` env-var escape hatch lets the operator force-
  skip both `gh repo view` and `gh repo create` entirely, for cases
  where even the REST existence check fails but the operator knows
  the repo exists. Documented in the script's `--help` text.
- When `gh repo create` fails with a 404 mentioning `/users/<login>`
  AND the authenticated user matches `$GH_OWNER`, print a clear
  diagnostic: "your account's /users/ endpoint hasn't propagated yet;
  wait ~30-60 min, or create the repo manually at github.com/new and
  rerun with `SKIP_REPO_CREATE=1`."

## Spec

```bash
SKIP_REPO_CREATE="${SKIP_REPO_CREATE:-0}"

# Existence check via REST repo endpoint (resolves by repo ID, not by
# /users/<login> + GraphQL — robust against brand-new-account cache lag).
repo_exists() {
    gh api "repos/$GH_FULL" --silent 2>/dev/null
}

if [ "$SKIP_REPO_CREATE" = "1" ]; then
    warn "SKIP_REPO_CREATE=1 — skipping repo creation/verification."
    warn "  Assuming $GH_FULL exists. Push will fail if not."
elif repo_exists; then
    BRANCH_COUNT=$(gh api "repos/$GH_FULL/branches" --jq 'length' 2>/dev/null || echo 0)
    if [ "${BRANCH_COUNT:-0}" -eq 0 ]; then
        ok "$GH_FULL exists but is empty — using it."
    else
        # existing recreate-with-confirmation flow
    fi
else
    info "Creating GitHub repo $GH_FULL (private, no issues, no wiki)..."
    if ! create_repo 2>/tmp/new-brain-create.err; then
        if grep -q "users/$GH_OWNER" /tmp/new-brain-create.err 2>/dev/null \
           && [ "$(gh api user --jq .login 2>/dev/null)" = "$GH_OWNER" ]; then
            warn ""
            warn "  GitHub's /users/$GH_OWNER endpoint hasn't propagated yet."
            warn "  Your account is recent enough that gh repo create can't"
            warn "  validate the owner. Two options:"
            warn "    1. Wait 30-60 min and retry."
            warn "    2. Create $GH_FULL manually at https://github.com/new"
            warn "       (empty, no README), then rerun with:"
            warn "         SKIP_REPO_CREATE=1 make new-brain"
            warn ""
        fi
        cat /tmp/new-brain-create.err >&2
        die "gh repo create failed."
    fi
    ok "Created https://github.com/$GH_FULL"
fi
```

## Plan

- [x] M1: Replaced `gh repo view "$GH_FULL"` with
      `gh api "repos/$GH_FULL" --silent`. REST repo endpoint
      resolves via repo ID, sidesteps GraphQL + `/users/<login>` lag.
- [x] M2: Added `SKIP_REPO_CREATE` env var with inline comment
      explaining when to use it (after manual web-UI creation).
- [x] M3: Added `/users/<login>` 404 detection in the create-fails
      branch; prints the manual workaround + `SKIP_REPO_CREATE=1`
      retry instruction before re-emitting the gh error.
- [x] M4: Pending verification on VM (`yingtest42/brain` already
      exists manually). Sandbox can't simulate GraphQL lag, so the
      gating test is operator-side.

## Out of scope

- Pre-validating GH_OWNER before the prompt (would have caught this
  earlier but adds latency to every run for marginal benefit; the
  better fix is making the lower-level call robust).
- Custom auth-flow for fresh accounts. The bearer token works fine
  for everything the script needs; the issue is purely a cache-lag
  artifact in two specific gh CLI paths.

## Log

### 2026-05-19 — landed

Two changes in `scripts/new-brain.sh` section 3:

1. **Existence check switched to REST** (`gh api repos/$GH_FULL
   --silent`). The previous `gh repo view` call went through
   GraphQL, which has its own cache lag for new repos under new
   accounts; the REST `/repos/<owner>/<name>` endpoint resolves
   immediately via repo ID. Caught by inspection — the operator
   manually demonstrated `gh api repos/yingtest42/brain` returns
   200 while `gh repo view yingtest42/brain` says "Could not
   resolve to a Repository."

2. **`SKIP_REPO_CREATE` escape hatch + lag diagnostic.** Even
   with the REST switch, `gh repo create` itself validates the
   owner via `/users/<login>` which can lag too — bypassing it
   requires either waiting or pre-creating the repo manually
   then setting the env var. The script now detects the
   "users/$GH_OWNER mentioned in error" + "auth'd user matches"
   pattern and prints the workaround inline before re-emitting
   the gh error.

Operator-side verification: rerun on the VM with `yingtest42/brain`
already manually created. The REST existence check should find it,
report "exists but is empty — using it," and proceed straight to
sections 4-10 (GPG identity → git init → push). No `SKIP_REPO_CREATE`
needed for this specific repro because the REST switch alone is
enough.

The fallback `SKIP_REPO_CREATE=1` remains useful for the harder
case where REST is also lagging (haven't seen that empirically but
plausible for the first few minutes of an account's life).

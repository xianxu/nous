---
id: '000025'
status: done
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

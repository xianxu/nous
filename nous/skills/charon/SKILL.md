---
name: charon
description: "How to talk to APIs through the Charon credential proxy. Bootstrap by running `charon instructions` — that prints the canonical guide embedded in the installed charon binary, so this skill stays in sync automatically. Use when writing or debugging any tool that makes outbound HTTP calls to OAuth-protected APIs (Gmail, Drive, Vertex AI, etc.)."
---

# Charon (agent side)

Charon is a forward HTTP proxy that injects OAuth tokens and API keys
into outbound requests. Your tool never sees the secret material; you
declare which account / scopes via headers and charon handles the
attachment.

## Always run this first

```bash
charon instructions
```

That command prints the canonical agent-facing guide: bootstrap,
manifest shape, `X-Charon-Account` / `X-Charon-Scope` semantics,
per-provider URL conventions (Google Workspace, Vertex AI, AI Studio,
OpenAI, Anthropic), and error handling (407, BILLING_DISABLED, …).

The content is **embedded in the charon binary**, so it always
matches the version installed on this machine. Follow what it
says — do not rely on cached knowledge of charon's protocol.

## When to re-read

- Before adding a new tool that calls a previously-untouched API.
- When charon returns something unexpected (especially 407 / 403).
- After upgrading charon — the embedded guide may have new sections.

## Fallback

If `charon instructions` is missing or errors, the installed charon
is too old or absent. Tell the user to update from
https://github.com/xianxu/charon. Do not guess at the protocol from
memory — that's exactly what this skill is designed to prevent.

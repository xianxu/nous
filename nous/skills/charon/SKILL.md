---
name: charon
description: "How to talk to APIs through the Charon credential proxy: required headers, the 407 dance for missing scopes, and how to discover what scope your call needs. Use when writing or debugging any tool that makes outbound HTTP calls to OAuth-protected APIs (Gmail, Google Drive, etc.)."
---

# Charon (agent side)

Charon is a forward HTTP proxy that injects OAuth bearer tokens into your
outbound requests. Your tool never sees the token; you declare *which
account* and *which scopes* via headers, charon handles the rest.

The **canonical spec** for the agent-side protocol lives in the charon
repo: [`charon/docs/agent-protocol.md`](https://github.com/xianxu/charon/blob/main/docs/agent-protocol.md).
Read that for the full contract, especially when adding a new tool or
adding to an existing one.

## Cheat sheet

On every HTTP request your tool makes through charon, set:

```
X-Charon-Account: <email>      # which account's tokens to use
X-Charon-Scope: <scopes>       # comma-separated short names or full URLs
```

If charon returns **HTTP 407** (Proxy Authentication Required), it means
the account doesn't have one or more scopes you declared. The response
body tells you exactly what's missing and how to fix it:

```json
{
  "error": "scope_missing",
  "missing": ["gmail.readonly"],
  "account": "user@gmail.com",
  "provider": "google",
  "fix": "charon auth google grant user@gmail.com gmail.readonly"
}
```

In this case, ask user to grant permission in `charon auth` (interactive TUI), and the failed 407 permission should be marked in the UI, for user to confirm granting permission.

## Discovering required scopes (Google)

For programmatic lookup, charon ships its catalog and current grants
as JSON:

```bash
charon scopes                              # what's POSSIBLE per provider
charon permissions                         # what's GRANTED per account
charon permissions google user@gmail.com   # one account's scopes (array)
```

Use `scopes` at code-write time to map operations to scope short
names. Use `permissions` at runtime if your tool needs to know what
the user has actually granted (e.g., to decide whether to attempt an
optional capability).

```bash
charon scopes | jq 'keys'                                    # ["google"]
charon scopes | jq '.google[] | select(.short | startswith("gmail"))'
charon permissions google user@gmail.com | jq '.'
```

Each entry has `short`, `full`, `description`, `required`. Use `short`
or `full` in `X-Charon-Scope`; charon accepts either.

Common ones (snapshot — the catalog above is canonical):

| You want to... | Scope short name |
|---|---|
| Search/read Gmail | `gmail.readonly` |
| Send mail | `gmail.send` |
| Read calendar | `calendar.readonly` |
| Write calendar | `calendar` |
| Read Drive | `drive.readonly` |
| Write Drive | `drive` |
| Read Sheets | `spreadsheets.readonly` |
| Write Sheets | `spreadsheets` |

Full table and other providers in the canonical doc.

If you're not sure: declare what you intend, let charon's 407 tell you
what's actually missing. Don't guess defensively (over-broad scopes are
worse UX for the user than a one-time 407 retry).

## What goes wrong if you skip `X-Charon-Scope`

You'll get a generic HTTP 403 from the upstream API instead of charon's
407 with a structured fix command. The TUI won't surface the missing
scope as a badge either. Always set the header.

## When to read the canonical doc

- Adding a new tool that calls a new API → read the per-provider section
- Charon returns something unexpected → check if the protocol changed
- Adding support for a non-Google provider → that provider's section
  has the specifics

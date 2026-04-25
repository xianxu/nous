---
name: gmail
description: "Search Gmail and read threads via Charon proxy. Use when the user asks to search email, check Gmail, find threads, or enrich data from email correspondence."
compatibility: "Go, macOS/Linux, requires charon proxy for auth"
---

# Gmail

Search Gmail and read full threads. Auth is handled by Charon — no credentials in this tool.

## Commands

```bash
# Search threads for an account
charon run -- go run ./cmd/gmail search --account user@gmail.com "cobalt solar"

# Search with custom result limit
charon run -- go run ./cmd/gmail search --account user@gmail.com --max-results 20 "invoice"

# Read a full thread
charon run -- go run ./cmd/gmail thread --account user@gmail.com <thread_id>
```

Note: flags must come before the positional argument (Go convention).

## Output

JSON to stdout. Structured for programmatic use.

**Search** returns `[{id, subject, sender, date, snippet, message_count}, ...]`

**Thread** returns `{id, messages: [{sender, to, date, subject, body}, ...]}`

## Multi-Account Search

List accounts with `charon accounts`, then search each:

```bash
for acct in $(charon accounts); do
  charon run -- go run ./cmd/gmail search --account "$acct" "query"
done
```

## Setup

1. Install charon and authenticate: `charon auth google user@gmail.com`
2. Use directly: `charon run -- go run ./cmd/gmail search --account user@gmail.com "query"`
3. Or build: `make build` then `charon run -- cmd/gmail/bin/gmail ...`

## Architecture

- **CLI**: `cmd/gmail/main.go` — thin subcommand dispatcher, JSON output
- **Library**: `lib/gmail/` — Go package with `SearchThreads` and `GetThread`, pure HTTP through Charon proxy
- **Auth**: Charon proxy injects bearer tokens via `X-Charon-Account` header — no credentials in this repo

## Security

- No tokens, secrets, or credentials in this codebase
- All auth handled by Charon sidecar (separate process, separately managed)
- `X-Charon-Account` header selects account; Charon injects the bearer token

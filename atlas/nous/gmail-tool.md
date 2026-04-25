# Gmail Tool

Search Gmail across multiple authenticated accounts. First Go tool in nous, establishing the pattern for future tools.

## Origin

Built to support contractor enrichment — finding email threads related to people in personal data, identifying who actually interacted via email.

## Architecture

- **`lib/gmail/`** — Go package: `SearchThreads`, `GetThread`. Raw HTTPS through Charon proxy. No credential management.
- **`cmd/gmail/`** — Go binary + `SKILL.md`. Subcommands: `search`, `thread`. JSON output.
- **Auth** — Charon proxy sidecar. `X-Charon-Account` header selects account; Charon injects bearer tokens.

## Key Design Decisions

- **No credentials in this repo** — all OAuth handled by Charon
- **Raw HTTP to Gmail API** — no client library, just `GET /gmail/v1/users/me/threads` with JSON parsing
- **Parallel metadata fetches** — search fires N goroutines with HTTP keep-alive for speed
- **SSL_CERT_FILE** — explicitly loaded for Go on macOS (Go doesn't always respect the env var)
- **JSON output** — structured for programmatic consumption by agents and scripts

## Usage

```bash
charon run -- go run ./cmd/gmail search --account user@gmail.com "query"
charon run -- go run ./cmd/gmail thread --account user@gmail.com <thread_id>
```

## Future Direction

- Extend to other Google APIs (Calendar, Drive) using same Charon proxy pattern
- Use `lib/gmail` directly in Go scripts for enrichment workflows

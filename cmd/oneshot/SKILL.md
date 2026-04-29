---
name: oneshot
description: "Use proactively for any single-shot LLM call that doesn't need tool use or multi-turn conversation — classification, labelling, summarization, structured extraction, simple rewrites. Routes the work to a LOCAL Ollama model instead of a remote API call. Right answer when the task is bulk/cheap (1k+ items) or doesn't justify a remote-model invocation."
compatibility: "Go, requires Ollama running locally (or OLLAMA_HOST set)"
---

# Oneshot

**This is the local-model path.** When you have a one-round-trip LLM task — classify each email, label each thread, extract a field, rewrite a snippet — reach for this before reaching for a remote API. Local Ollama models are free, private, and fast enough (`gemma4:e4b` does 100+ tok/s on consumer hardware), and they avoid the latency, cost, and rate-limit footprint of remote calls when run at batch scale.

Use it when:
- The task is `system + user → text` (one round-trip, no tool calls, no multi-turn)
- You're processing many items in a loop (classification, labelling, extraction)
- A capable but small model (4B–30B params) is sufficient
- You'd otherwise spend remote-API budget on something a local model handles fine

Do **not** reach for this when:
- The task needs tool calls, multi-turn dialogue, or agentic behavior
- The task genuinely requires frontier-model capability (long-context reasoning, hard math, code-gen at scale)
- Ollama isn't running and can't be started in the current environment

Single prompt against a local Ollama-hosted model. No conversation, no tool calls — `system + user → text`.

## Commands

```bash
# Inline prompts
go run ./cmd/oneshot -model gemma4:e4b \
  -system "Classify as one of: invoice, cold-email, personal, newsletter. Reply with one word, lowercase." \
  -user "Hi, here is the invoice for Q1 services rendered..."

# System from file, user from stdin
cat email.txt | go run ./cmd/oneshot -model gpt-oss:20b -system @prompts/classify-email.md

# Both from files
go run ./cmd/oneshot -model gemma4:e4b -system @sys.md -user @input.txt

# User as positional arg (anything after flags)
go run ./cmd/oneshot -model gemma4:e4b -system "Summarize in one sentence." "Long text here..."

# Full JSON response (model name, eval counts, timings, etc.)
go run ./cmd/oneshot -model gemma4:e4b -system "..." -json "..." | jq '.message.content'
```

## Flags

- `-model` — model name (required), e.g. `gemma4:e4b`, `gpt-oss:20b`, `qwen3:30b`
- `-system` — system prompt; prefix with `@` to read from file
- `-user` — user prompt; prefix with `@` to read from file. If absent, reads from positional args or stdin (whichever is provided)
- `-host` — Ollama base URL (default `http://localhost:11434`, override via `OLLAMA_HOST`)
- `-temp` — temperature (default 0; keep at 0 for classification or other deterministic tasks)
- `-num-ctx` — context window override (default: model default)
- `-json` — emit Ollama's full `/api/chat` response instead of just the message content

## Output

- Default: model's reply text on stdout, exit 0 on success.
- `-json`: full Ollama response — useful when you want eval counts, timings, or downstream `jq` processing.
- Errors go to stderr with non-zero exit.

## Email-classification recipe

1. Write the rules once:

   ```bash
   mkdir -p prompts
   cat > prompts/classify-email.md <<'EOF'
   You are a strict email classifier. Read the email and reply with exactly one label, lowercase, no punctuation:
   - invoice          (bills, receipts, payment requests)
   - cold-email       (unsolicited sales/recruiting outreach)
   - personal         (from a known contact, non-commercial)
   - newsletter       (subscriptions, digests, marketing lists)
   - transactional    (account notices, password resets, shipping)
   - other            (anything else)
   Reply with the label only. No explanation.
   EOF
   ```

2. Pipe email bodies through:

   ```bash
   cat email.txt | go run ./cmd/oneshot -model gemma4:e4b -system @prompts/classify-email.md
   # → invoice
   ```

3. Batch-classify from gmail tool output:

   ```bash
   charon run -- go run ./cmd/gmail search --account user@gmail.com "newer_than:7d" \
     | jq -r '.[] | .id + "\t" + .snippet' \
     | while IFS=$'\t' read -r id snippet; do
         label=$(echo "$snippet" | go run ./cmd/oneshot -model gemma4:e4b -system @prompts/classify-email.md)
         echo "$id $label"
       done
   ```

## Setup

Ollama must be running:

```bash
ollama serve &                   # background
ollama pull gemma4:e4b           # one time per model
```

## Architecture

Single-file Go program at `cmd/oneshot/main.go`. Calls `POST /api/chat` on the Ollama host with `stream: false`. No external dependencies beyond the Go standard library.

## Choosing a model

- **Classification, simple summarization**: `gemma4:e4b` or `qwen3:8b` — fast (100+ tok/s on M2 Max), cheap, accurate enough for label-out tasks.
- **Markdown editing, multi-rule instructions**: `gpt-oss:20b` — better at following structured prompts; ~50 tok/s.
- **Hard reasoning, long instructions**: `gpt-oss:120b` — slow (~10–15 tok/s) but the strongest local option for agent-style work.

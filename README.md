# Brain

A personal AI extension, your own cognitive infrastructure — Claude/ChatGPT on steroids.

Chat with a coding agent that has access to your personal data, persists conversation state, and builds durable artifacts that make AI work in *your* style. Not a chatbot — a collaborator that remembers, learns, and acts.

## What this is

Brain is a monorepo where you and AI work together. It holds your data (email, house projects, contacts), tools to access that data, and the accumulated context that makes the AI increasingly useful over time. The more you use it, the more it understands your workflows and preferences.

## Prerequisites

1. **A coding agent subscription.** Developed with [Claude Code](https://claude.ai), but Codex, Gemini, or any coding agent should work.

2. **[Charon](https://github.com/xianxu/charon)** for credential management. A single self-contained binary that acts as a proxy sidecar — your AI tools never touch raw tokens. Install it, authenticate your accounts once, and forget about it.

   ```bash
   # Authenticate your Gmail
   charon auth google you@gmail.com

   # Start the proxy
   charon serve
   ```

## Getting started

Fire up `claude` (or your coding agent of choice) in this directory and start asking questions.

```
> Tell me about my last 10 gmails
> Search my email for cobalt solar
> Who are the contractors I've worked with?
```

The agent reads the SKILL.md files in this repo, discovers available tools, and uses them to answer. No setup beyond Charon auth.

## Directory structure

### Ariadne base layer

Most of the root directory structure comes from [Ariadne](https://github.com/xianxu/ariadne) — a base layer for agentic development. It provides issue tracking, plan management, and workflow conventions that make AI collaboration structured and repeatable.

```
atlas/              # Map of the codebase — onboarding pointers for humans and agents
workshop/           # Where building happens
  issues/           # Active work items
  plans/            # Detailed designs
  history/          # Archived completed work
  lessons.md        # Patterns of what went wrong, rules to prevent repeating
docs/vision/        # Pensives, brainstorms, product thinking
CLAUDE.md           # Agent instructions (loaded automatically by Claude Code)
```

### Go applications

`cmd/` and `lib/` hold Go applications — deterministic scripts that work alongside AI to extend your workflows.

```
cmd/                # Go binaries — each is also an agent skill
  gmail/            # Search and read Gmail threads (SKILL.md inside)
lib/                # Reusable Go libraries
  gmail/            # Gmail API client via Charon proxy
```

Each `cmd/<name>/` directory is both a Go binary and an agent skill. The binary *is* the skill's implementation — `SKILL.md` describes how agents invoke it, `main.go` is the code. No wrapper scripts.

The whole purpose of this project is for you to extend these tools — adding access to your calendar, notes, finances, whatever makes your AI collaborator more useful. Brain grows with you.

## Example: Gmail

```bash
# Search across an authenticated account
charon run -- go run ./cmd/gmail search --account you@gmail.com "query"

# Read a full email thread
charon run -- go run ./cmd/gmail thread --account you@gmail.com <thread_id>
```

Or just ask your agent — it knows how to use the tool.

# Nous

A personal AI extension, your own cognitive infrastructure — Claude/ChatGPT/Gemini on steroids.

Work with an agent that has access to your personal data, persists conversation state, and builds durable artifacts and workflows that make AI work in your style for you. Not a chatbot — a companion that remembers, learns, teaches, and acts. It is the extension of your mind. 

## The trio: charon, nous, and brain

Three repos, layered:

```
                Cloud (Gmail, Calendar, Drive, …)
                              ▲ ▼
        ┌──────────────────────────────────────────┐
        │  charon (public)  <── this is /charon    │  outbound, secrets
        │  OAuth-aware proxy: manages permssions   │  managed by OS keychain
        └──────────────────────────────────────────┘
                              ▲ ▼
        ┌──────────────────────────────────────────┐
        │  nous   (public)  <── this is /nous      │  task capability
        │  Skills + tools the agent uses           │  no secrets — pure code
        └──────────────────────────────────────────┘
                              ▲ ▼
        ┌──────────────────────────────────────────┐
        │  brain  (private) <── kept separate      │  private state
        │  Memory, thoughts, workflows, context    │  yours alone
        └──────────────────────────────────────────┘
                              ▲ ▼
                         Personal Agent
                              ▲ ▼
                              You
```

- **charon** ([github.com/xianxu/charon](https://github.com/xianxu/charon)) is the outbound capability layer. It speaks OAuth and mediates the OS keychain — the keychain is the actual vault, charon just routes requests through it and attach Bearer tokens when available. Useful to anyone building agents that need to call third-party authenticated APIs.

- **nous** (this repo, [github.com/xianxu/nous](https://github.com/xianxu/nous)) is the task capability layer — skills, tools, and the Go binaries the agent invokes to do work. Calls outbound APIs through charon. Holds no secrets and no personal state; reusable across users. In truth, many of those small scripts can easily be done by coding agents in **brain** itself.

- **brain** is the private state layer — accumulated memory, derived thoughts, designed workflows, and the agent's evolving understanding of you. Stays on your hardware, no need to be published. The personal half of the architecture.

**Sharing strategy**: charon and nous are open infrastructure (this repo and charon are both public). Brain is yours — it's where the personal context lives, and can't be shared without leaking who you are. The trust boundaries are at the OS keychain (below charon) and at brain (above nous): everything between is general-purpose code anyone can use.

## Prerequisites

1. **A coding agent subscription.** Developed with [Claude Code](https://claude.ai), but intend to make Codex, Gemini, or any coding agent work as well.

2. **[Charon](https://github.com/xianxu/charon)** for credential management. A single self-contained binary that acts as a proxy sidecar — your AI tools never touch raw tokens. Install it, authenticate your accounts once, and forget about it.

Typical directory structure:

    ```
    ~/workspace/
        charon/
        nous/
        brain/
    ```
## Getting started

If you want to access your gmail and other private information

1. build charon: `cd ../charon/; make service install`
2. authenticate to some gmail accounts: `charon auth`
3. fire up `claude` in this directory and start asking questions. e.g. 

```
> Tell me about my last 10 gmails
> Search my email about solar installation
> Who are the contractors I've worked with?
```

The agent reads the SKILL.md files in this repo, discovers available tools, and uses them to answer. No setup beyond charon (both install as a service for proxy, and as command line for auth).

## Directory structure

### Ariadne base layer

Most of the root directory structure comes from `Ariadne` — a base layer for agentic development. It provides issue tracking, plan management, and workflow conventions that make AI collaboration structured and repeatable.

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

### The vendoring system

Ariadne right now is private. The way `nous` uses it is through vendoring. Basically `../ariadne/construct/setup.sh --vendor` is ran to set up the base layer in `nous`. 

`nous` provides the same way its derivative and private `brain` repo should do, basically run `../nous/nous/setup.sh` in your `brain` folder. Note in this example, since everyone have access to `nous` and since `brain` is intended to be private, we used symlink version, e.g. without the `--vendor` flag. This requires `nous` and `brain` to be sibling in some folder.

### Go applications

`cmd/` and `lib/` hold Go applications — deterministic scripts that work alongside AI to extend your workflows.

```
cmd/                # Go binaries — each is also an agent skill
  gmail/            # Search and read Gmail threads (SKILL.md inside)
lib/                # Reusable Go libraries
  gmail/            # Gmail API client via Charon proxy
```

Each `cmd/<name>/` directory is both a Go binary and an agent skill (i.e. how it's supposed to be used by agent). The binary *is* the skill's implementation — `SKILL.md` describes how agents invoke it, `main.go` is the code. No wrapper scripts. All capabilities should be implemented as subcommands, e.g. `gmail search`.

The whole purpose of this project is for you to extend these tools easily — adding access to your calendar, notes, finances, whatever makes your AI collaborator more useful. Brain grows with you. The gmail is just a simple example and starting point for me to tinker. 

## Example: Gmail

```bash
# Search across an authenticated account
charon run -- go run ./cmd/gmail search --account you@gmail.com "query"

# Read a full email thread
charon run -- go run ./cmd/gmail thread --account you@gmail.com <thread_id>
```
Here, `charon run` provides all the necessary settings for the proxy.

Or just ask your agent — it knows how to use the tool and create new tools.

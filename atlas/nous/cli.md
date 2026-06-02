# nous CLI — agent-vs-human surface

The single `nous` binary serves two audiences simultaneously without
mixing their UX:

- **Agents** read `nous --help`, `nous <cluster> --help`, and
  `nous <cluster> <verb> --help` (cobra subcommand help). Help text is
  the agent's manual: dense, self-contained, source-of-truth for
  procedures (verify-fingerprint ceremony, safeguards, what files
  get touched).
- **Humans** drive the TUIs (`nous brain`, `nous provider`) for the
  domains where browse-and-act is the natural shape, and use
  sequenced CLI prompts for the small interactive surfaces
  (`nous identity init`, `nous identity import`,
  `nous brain recipient add`).

Both surfaces wrap the same `lib/` operations — the seam is purely
rendering. Logic lives in lib, where it can be tested and reused;
cmd is the cobra/bubbletea glue.

## Cluster map

| Cluster      | Top-level command            | Subcommands                                                                                                          |
| ------------ | ---------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| identity     | `nous identity`              | `init` (h) · `export` (a) · `import` (h) · `list` (a) · `primary` (b) · `agent {prewarm,flush,status}` (a)           |
| brain        | `nous brain`                 | bare cmd launches TUI on TTY (h); subcommands: `new` (h) · `list` (a) · `recipient {list,add,remove}` (a/h/h) · `resolve` (a) |
| provider     | `nous provider`              | bare cmd launches TUI (h); subcommand: `manifest` (a)                                                                |
| service      | `nous service`               | `install` · `uninstall` · `start` · `stop` · `status` · `doctor` · `audit` (all (a), scriptable)                     |
| top-level    | `nous instructions [topic]`  | canonical agent guide (a, with topic narrowing)                                                                       |
|              | `nous manifest [topic[:filter]]` | machine-readable state (a)                                                                                        |

## Audience tags

Every command in the subcommand tree carries an audience tag:

- **(a)** — agent-facing primarily. Scriptable, idempotent, exits
  with a structured error on misuse. No TUI rendering, no
  interactive prompts. Read-paths default to JSON-or-text shapes
  agents can parse.
- **(h)** — human-facing primarily. Either a TUI launch, an
  interactive CLI prompt, or a guided multi-step flow. TTY-only
  when the action is identity-and-access (verify-fingerprint
  ceremony, recipient changes — the agent-as-threat boundary;
  see `brain/atlas/threat-model-shared-brain.md` Revisions).
  The identity-and-access commands carry explicit scripted
  escape-hatch flags for test/automation (nous#36) that lift the
  TTY gate, the operator asserting the OOB check already ran:
  `--verified-last8` on `identity import` / `brain recipient add`,
  `--name`/`--email` (or `IDENTITY_*`) on `identity init`, and
  `--force` on `brain recipient remove`. See
  `atlas/nous/e2e-integration-testing.md` (headless-VM section).
- **(b)** — both. Reads like (a) when non-interactive; renders a
  prompt or TUI when invoked on a TTY. Currently:
  `nous identity primary` (no-args TTY runs the heuristic resolver
  and prompts to persist; non-TTY just prints the resolved value).

Tags are visible in each cobra command's `Short` / `Long` text so
they survive into `--help` output. The agent's manual stays honest
about which surfaces it can drive.

## Per-cluster TUI choice

Only two clusters render a full-screen TUI; the choice is
deliberate, not symmetric:

- **`nous brain`** — TUI. Operators "go check on their brains"
  the way they check on git working trees: list the workspace, drill
  into one, see recipients / sync state / conflicts, take an action.
  Browse-and-act fits the domain. Subcommands remain for the agent
  surface and for scripted automation.
- **`nous provider`** — TUI (inherited from charon's `auth` TUI via
  `lib/charoncli.AuthCmd`). Credential management is similarly
  browse-and-act: list providers, drill into an account, OAuth dance
  or paste an admin key, rotate. `nous provider manifest` is the
  scriptable read.
- **`nous identity`** — **no full TUI**. Identity ops happen a few
  times in a brain's lifetime (keygen, import a peer, set primary).
  Humans don't browse keys; they perform one act and leave.
  Sequenced CLI prompts (`nous identity import file.asc` →
  verify-fingerprint ceremony) are the right shape; a bubbletea
  screen would be inventory pressure without payoff.
- **`nous service`** — **no TUI**. Service ops are mechanical and
  scripted (install, start, stop, status, doctor, audit). Operators
  pipe `nous service audit --grep ...` and chain into other tools;
  a TUI would obstruct that flow.

Bare `nous` prints help (cobra default). It is *never* a TUI entry —
the disambiguation between bare-cluster-as-TUI (`nous brain`,
`nous provider`) and bare-binary-as-help (`nous`) is also
deliberate. Agents calling `nous --help` need the manual, not a
screen.

## Cross-refs

- `nous/workshop/issues/000014-absorb-charon-unified-nous-cli.md` — full design history, audience-tag rationale, sub-milestone notes
- `brain/atlas/threat-model-shared-brain.md` — TTY-only delegation boundary; identity-and-access commands refuse non-TTY invocation
- `atlas/nous/lib-layout.md` — where each cluster's underlying ops live in `lib/`
- `atlas/charon/index.md` — the absorbed-from project; provider TUI is its direct descendant

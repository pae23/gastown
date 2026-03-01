# Federation Capabilities

> Mini-spec for environment-aware molecule steps across a Gas Town federation.

**Status**: Draft
**Related**: [federation.md](federation.md) | [model-aware-molecules.md](model-aware-molecules.md) | [agent-provider-interface.md](agent-provider-interface.md)

---

## 1. Problem and Scope

A molecule step already declares *which model* it wants to run on. This design extends that principle to *which environment* it runs in: what tools are available, what network policy applies, which secrets are visible.

**This document does not cover** how an environment is created — container, VM, bare metal. That is the responsibility of the town that hosts it. This document covers only:

- the data model for describing an environment
- how a town advertises its capabilities to the federation
- how a step declares its requirements
- how the federation routes a step to the right town

---

## 2. Core Concept: Environment Profile

An `EnvProfile` is a declaration of what a town can provide to execute a step. It is defined locally by the town and advertised to its federated peers.

```toml
# ~/.gt/envs.toml  (declared by each town)

[envs.python-isolated]
description = "Python 3.12 with no network access"
tools       = ["git", "python3.12", "uv", "make"]
network     = "isolated"
secrets     = []                      # nothing injected by default
tags        = ["python", "isolated"]
agent       = "claude"                # agent preset running in this environment

[envs.node-web]
description = "Node.js with access to npm registry and GitHub"
tools       = ["git", "node22", "npm", "pnpm"]
network     = "restricted:registry.npmjs.org,github.com"
secrets     = ["GITHUB_TOKEN"]
tags        = ["node", "web"]
agent       = "claude"

[envs.secure-sandbox]
description = "Empty environment, no network, no secrets"
tools       = ["git"]
network     = "isolated"
secrets     = []
tags        = ["sandbox", "untrusted"]
agent       = "gemini"                # any agent that supports non-interactive mode

[envs.full]
description = "Standard town environment (default)"
tools       = []                      # empty = whatever is on the machine
network     = "full"
secrets     = ["ANTHROPIC_API_KEY", "GITHUB_TOKEN"]
tags        = ["default"]
agent       = "claude"
```

### Profile Fields

| Field | Type | Description |
|---|---|---|
| `description` | string | Human-readable label |
| `tools` | []string | Available executables. Empty = no constraint |
| `network` | string | `"isolated"` · `"full"` · `"restricted:<allowlist>"` |
| `secrets` | []string | Names of env vars injected into the step |
| `tags` | []string | Free-form labels used for capability matching |
| `agent` | string | Agent preset name (`"claude"`, `"gemini"`, `"codex"`, …). Empty = any available agent |
| `resources` | table | Optional: cpu, memory, timeout |
| `federated` | bool | Whether this profile is advertised to federation peers (default: false) |

The `agent` field maps to an entry in `builtinPresets` (`internal/config/agents.go`). It determines which CLI binary is launched, how readiness is detected, and which capabilities (hooks, non-interactive mode, session resume) are available for the step. See [agent-provider-interface.md](agent-provider-interface.md) for the full capability matrix.

---

## 3. Step Environment Constraints

Natural extension of the step schema (see `model-aware-molecules.md`):

```toml
[[steps]]
id    = "run-tests"
title = "Run test suite"
model = "auto"

# Option A: exact profile name (resolved locally first, then in the federation)
env = "python-isolated"

# Option B: capability-based matching (runtime finds a compatible profile)
env_tools   = ["python3", "make"]
env_network = "isolated"
env_tags    = ["python"]

# Option C: agent constraint (implicit from model, or explicit)
# Most commonly inferred: model = "gemini-2.0-flash" implies env_agent = "gemini"
env_agent = "gemini"
```

`env` and `env_tools/env_network/env_tags` are mutually exclusive.
`env_agent` may be combined with either option.
A step with no environment constraint uses the local town's `"full"` profile — identical to current behaviour.

The model constraint (`model`, `min_swe`, etc.) implicitly drives agent selection: a step requiring `claude-sonnet-4-5` can only execute on a town where the `claude` preset is available. The router resolves this automatically — `env_agent` is only needed when the agent matters independently of the model (e.g. "run this in Codex regardless of which model it uses").

### Resolution Priority

```
1. env = "exact-name"  found in local town              → local execution
2. env = "exact-name"  found in a federated town        → delegation
3. env_tools/env_tags  matched in local town            → local execution
4. env_tools/env_tags  matched in federated town        → delegation
5. No match anywhere                                     → step blocked, error
```

---

## 4. Capability Manifest in the Federation

Each town publishes a **capability manifest** in its beads — a single bead of type `town-capabilities`. This bead is synchronised to peers via the existing Dolt remotes.

```
Bead: hq-capabilities
Type: town-capabilities
Slots:
  hop_id   = "hop://alice@example.com/main-town"
  profiles = <JSON: name, tags, tools, network, agent, agent_caps for each federated profile>
  updated  = <timestamp>
```

The manifest does **not** include secrets or internal profile details — only names, tags, tools, network policy, and agent capabilities. Secrets are never transmitted.

`agent_caps` is a subset of the capability matrix from [agent-provider-interface.md](agent-provider-interface.md), summarised as tier flags:

| Flag | Meaning |
|---|---|
| `non_interactive` | Agent supports headless execution (`-p` flag or `exec` subcommand) |
| `hooks` | Agent supports lifecycle hooks (session_start, tool guards) |
| `resume` | Agent supports `--resume` / session continuation |

```bash
# Inspect a federated town's capabilities
gt remote capabilities hop://alice@example.com/main-town

# Output:
# Profiles:
#   python-isolated  [python, isolated]  agent: claude  caps: non_interactive,hooks,resume  tools: git, python3.12, uv
#   node-web         [node, web]         agent: claude  caps: non_interactive,hooks,resume  tools: git, node22, npm
#   secure-sandbox   [sandbox]           agent: gemini  caps: non_interactive,resume        tools: git
```

---

## 5. Federated Step Routing

Routing happens in two passes, with no new protocol — built on the existing delegation and mail systems.

### Pass 1: Local Resolution

At `gt mol execute` time (or when the refinery dispatches a step), the router:

1. Loads `EnvProfile` entries from `~/.gt/envs.toml`
2. Checks whether the step can run locally
3. If yes → normal local execution (existing tmux agent)

### Pass 2: Federation Delegation

If no local profile satisfies the constraints:

1. Query the capability manifests of known federation peers (`gt remote list`)
2. Select the best-matching town (tags + tools + network; tiebreak: declared load, latency)
3. Create a delegation bead on the remote town via the existing `AddDelegation` mechanism
4. Send a mail message to the remote town's mayor with the step to execute
5. The remote town instantiates the step in its own beads, runs it, and notifies on completion
6. The local town receives the notification and marks the step as done in the molecule

```
Local town                          Remote town
    │                                    │
    │── mail: "execute step X" ─────────▶│
    │                                    │── create step bead
    │                                    │── spawn agent
    │                                    │── step executes
    │                                    │── step completes
    │◀─ mail: "step X done" ─────────────│
    │                                    │
    │── step marked done in molecule     │
```

The delegated step appears in the local molecule like any other step — it simply carries a `delegated_to` attribute pointing to the remote town's HOP URI.

---

## 6. Agent Execution on Remote Towns

When a step is delegated, the remote town executes it using its local `AgentPresetInfo` machinery — the same infrastructure used for all local agent orchestration. There is no special federation execution path.

### Execution Modes

Federated steps are always headless. The remote town selects an execution mode based on the agent preset's capabilities:

| Agent has `non_interactive`? | Execution mode | Mechanism |
|---|---|---|
| Yes (`claude`, `gemini`, `codex exec`, …) | Direct headless call | `command -p "…"` or `exec` subcommand |
| No (`auggie`, `amp`, …) | Tmux send-keys | Spawn tmux session, deliver via `send-keys`, poll output via `capture-pane` |

The **tmux shim** is the universal execution floor — any CLI agent that runs in a terminal can execute federated steps, even without a non-interactive API. This is the "zero API" guarantee: participation in federation requires only that an agent can be launched and receive input.

### Readiness Detection

Once the agent is spawned, the remote town uses `AgentPresetInfo`'s two readiness strategies before delivering the step:

1. **Prompt-prefix scan** — poll `tmux capture-pane` for the agent's ready prompt (e.g. `❯` for Claude). Reliable for agents with stable prompt characters.
2. **Delay fallback** — wait `ReadyDelayMs` milliseconds. Used for TUI agents (OpenCode, Codex) whose prompts can't be scanned.

### Step Delivery

After the agent is ready, the step's instructions are delivered via:
- **Hooks** (`session_start` callback) — for Claude, OpenCode, Pi. Reliable, no timing dependency.
- **Tmux `send-keys`** — universal fallback. The step description and prompt are sent as text.

The `GT_AGENT` env var is set in the remote tmux session, identifying the agent preset. This is used by Phase 2 of the model router (`ResolveSession`) to verify that the correct agent is handling the step.

### Graceful Degradation

Every capability has a fallback, so the remote town never hard-blocks on a missing agent feature:

- No hooks → startup fallback via `gt prime && gt mail check --inject` sent over tmux
- No non-interactive mode → full tmux session with send-keys delivery
- No resume → fresh session with handoff mail containing prior context
- No process API → liveness via `tmux pane_current_command` against `ProcessNames`

---

## 7. Security Model

**Principle**: the remote town is sovereign over its environment. The local town cannot inspect, modify, or bypass the constraints of a remote profile.

| Property | Guarantee |
|---|---|
| **Network isolation** | Enforced by the remote town; not a matter of trust |
| **Secrets** | Never transmitted over federation. Pre-provisioned on the remote town |
| **Tooling** | The remote town certifies its manifest; the local town trusts it |
| **Step content** | The step description and instructions are transmitted. Credentials are not |
| **Results** | Returned via Dolt sync + mail. No direct channel |

A town explicitly chooses which profiles it exposes to the federation via the `federated` field. Profiles default to internal-only.

```toml
[envs.secure-sandbox]
# ...
federated = true    # visible to federation peers

[envs.internal-gpu]
# ...
federated = false   # internal only (default)
```

---

## 8. Step Schema Extension

Addition to the existing `Step` struct (see `internal/formula/types.go`):

```go
// Execution environment constraints (all optional).
// Env is a named profile: resolved locally first, then across the federation.
Env        string   `toml:"env"`
// EnvTools requires specific executables to be present in the environment.
EnvTools   []string `toml:"env_tools"`
// EnvNetwork requires a specific network policy.
// Values: "isolated", "full", "restricted:<host1>,<host2>"
EnvNetwork string   `toml:"env_network"`
// EnvTags requires an environment that carries all listed tags.
EnvTags    []string `toml:"env_tags"`
// EnvAgent requires a specific agent preset (e.g. "claude", "gemini", "codex").
// Usually inferred from the model constraint; set explicitly only when the agent
// matters independently of the model.
EnvAgent   string   `toml:"env_agent"`
```

`Env` and `EnvTools/EnvNetwork/EnvTags` are mutually exclusive (parser error if both are set).
`EnvAgent` may be combined with either form.

---

## 9. Multi-Town Molecule Example

```toml
formula = "mol-secure-pipeline"
version = 1

# Step 1: code analysis — runs anywhere
[[steps]]
id    = "analyze"
title = "Analyze codebase"
model = "claude-sonnet-4-5"

# Step 2: tests in an isolated sandbox
# → delegated to a remote town if unavailable locally
[[steps]]
id      = "test"
title   = "Run tests in isolation"
needs   = ["analyze"]
env     = "python-isolated"
model   = "auto"
min_swe = 50

# Step 3: synthesis — back on the local town
[[steps]]
id    = "report"
title = "Synthesize results"
needs = ["test"]
model = "claude-sonnet-4-5"
```

---

## 10. Open Questions

| Question | Discussion |
|---|---|
| **Remote town selection** | On what criteria to choose between two towns satisfying the same constraints? Declared load, latency, organisational affinity (HOP entity)? |
| **Delegated step cancellation** | If a molecule is `burn`ed locally, how is the remote town notified to cancel an in-progress step? |
| **Structured results** | Step results currently live in beads (completed bead description). Is that sufficient for cross-town cases, or is a dedicated slot needed? |
| **Profile versioning** | When a profile changes (tool updated), how are cached manifests in peer towns invalidated? |
| **Transitive delegation** | Town A delegates to Town B which delegates to Town C — should chained delegation be allowed, and to what depth? |
| **Agent version pinning** | Should a step be able to require a minimum agent version (e.g. `claude >= 1.2`)? How is agent version advertised in the capability manifest? |
| **Model↔agent mismatch** | If a step declares `model = "claude-opus-4-6"` but the remote town's profile has `agent = "gemini"`, the router should reject the delegation. Where is this cross-validation performed — at dispatch time or at manifest query time? |
| **Hook portability** | Claude hooks (`settings.json`) and OpenCode hooks (plugin JS) are agent-specific. If a step relies on a `session_start` hook for context injection, does that constraint propagate to the federation? |

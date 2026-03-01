# Wasteland Environments

> Mini-spec for environment-aware molecule steps across the Gas Town Wasteland.

**Status**: Draft
**Related**: [federation.md](federation.md) | [model-aware-molecules.md](model-aware-molecules.md) | [agent-provider-interface.md](agent-provider-interface.md)
**Implementation**: `internal/wasteland/` · `internal/doltserver/wl_commons.go` · `internal/cmd/wl_*.go`

---

## 1. Problem and Scope

A molecule step already declares *which model* it wants to run on. This design extends that principle to *which environment* it runs in: what tools are available, what network policy applies, which secrets are visible.

**This document does not cover** how an environment is created — container, VM, bare metal. That is the responsibility of the rig that hosts it. This document covers only:

- the data model for describing an environment (`EnvProfile`)
- how a rig advertises its capabilities to the **Wasteland** (`internal/wasteland/`)
- how a step declares its requirements
- how the Wasteland routes a step to the right rig via `wl post / claim / done`

---

## 2. Core Concept: Environment Profile

An `EnvProfile` is a declaration of what a rig can provide to execute a step. It is defined locally by the rig and advertised to Wasteland peers.

```toml
# ~/.gt/envs.toml  (declared by each rig)

[envs.python-isolated]
description = "Python 3.12 with no network access"
tools       = ["git", "python3.12", "uv", "make"]
network     = "isolated"
secrets     = []                      # nothing injected by default
tags        = ["python", "isolated"]
agent       = "claude"                # agent preset running in this environment
shared      = true                    # visible to Wasteland peers

[envs.node-web]
description = "Node.js with access to npm registry and GitHub"
tools       = ["git", "node22", "npm", "pnpm"]
network     = "restricted:registry.npmjs.org,github.com"
secrets     = ["GITHUB_TOKEN"]
tags        = ["node", "web"]
agent       = "claude"
shared      = true

[envs.secure-sandbox]
description = "Empty environment, no network, no secrets"
tools       = ["git"]
network     = "isolated"
secrets     = []
tags        = ["sandbox", "untrusted"]
agent       = "gemini"                # any agent that supports non-interactive mode
shared      = true

[envs.full]
description = "Standard rig environment (default)"
tools       = []                      # empty = whatever is on the machine
network     = "full"
secrets     = ["ANTHROPIC_API_KEY", "GITHUB_TOKEN"]
tags        = ["default"]
agent       = "claude"
shared      = false                   # internal only (default)
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
| `shared` | bool | Whether this profile is advertised to Wasteland peers (default: false) |

The `agent` field maps to an entry in `builtinPresets` (`internal/config/agents.go`). It determines which CLI binary is launched, how readiness is detected, and which capabilities (hooks, non-interactive mode, session resume) are available for the step. See [agent-provider-interface.md](agent-provider-interface.md) for the full capability matrix.

---

## 3. Step Environment Constraints

Natural extension of the step schema (see `model-aware-molecules.md`):

```toml
[[steps]]
id    = "run-tests"
title = "Run test suite"
model = "auto"

# Option A: exact profile name (resolved locally first, then via the Wasteland)
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
A step with no environment constraint uses the local rig's `"full"` profile — identical to current behaviour.

The model constraint (`model`, `min_swe`, etc.) implicitly drives agent selection: a step requiring `claude-sonnet-4-5` can only execute on a rig where the `claude` preset is available. The router resolves this automatically — `env_agent` is only needed when the agent matters independently of the model (e.g. "run this in Codex regardless of which model it uses").

### Resolution Priority

```
1. env = "exact-name"  found on local rig              → local execution
2. env = "exact-name"  found on a Wasteland peer       → Wasteland delegation
3. env_tools/env_tags  matched on local rig            → local execution
4. env_tools/env_tags  matched on a Wasteland peer     → Wasteland delegation
5. No match anywhere                                    → step blocked, error
```

---

## 4. Capability Manifest in the Wasteland

The Wasteland (`internal/wasteland/`) is the Gas Town federation: each rig holds a sovereign fork of the shared **`wl-commons`** DoltHub database, synchronised via fork/PR/merge.

Rig registration already writes a row to `wl-commons.rigs`:

```sql
-- existing columns
handle, display_name, dolthub_org, hop_uri, owner_email, gt_version,
trust_level, registered_at, last_seen, rig_type, parent_rig
```

The capability manifest extends this row with an `env_profiles` JSON column. Each entry is a shared `EnvProfile` (profiles with `shared = false` are excluded):

```json
{
  "env_profiles": [
    {
      "name": "python-isolated",
      "tags": ["python", "isolated"],
      "tools": ["git", "python3.12", "uv"],
      "network": "isolated",
      "agent": "claude",
      "agent_caps": ["non_interactive", "hooks", "resume"]
    },
    {
      "name": "secure-sandbox",
      "tags": ["sandbox"],
      "tools": ["git"],
      "network": "isolated",
      "agent": "gemini",
      "agent_caps": ["non_interactive", "resume"]
    }
  ]
}
```

`agent_caps` is the agent's capability tier flags (see [agent-provider-interface.md](agent-provider-interface.md)):

| Flag | Meaning |
|---|---|
| `non_interactive` | Agent supports headless execution (`-p` or `exec` subcommand) |
| `hooks` | Agent supports lifecycle hooks (session_start, tool guards) |
| `resume` | Agent supports `--resume` / session continuation |

Secrets are never advertised. The manifest is updated by `gt wl sync` whenever `~/.gt/envs.toml` changes.

```bash
# Inspect a Wasteland peer's environments
gt wl caps <rig-handle>

# Output:
#   python-isolated  [python, isolated]  agent: claude  caps: non_interactive,hooks,resume
#   secure-sandbox   [sandbox]           agent: gemini  caps: non_interactive,resume
```

---

## 5. Wasteland Step Routing

Routing happens in two passes, with no new protocol — built entirely on existing **Wasteland** primitives (`gt wl post / claim / done / sync`).

### Pass 1: Local Resolution

At `gt mol execute` time (or when the refinery dispatches a step), the router:

1. Loads `EnvProfile` entries from `~/.gt/envs.toml`
2. Checks whether the step can run locally
3. If yes → normal local execution (existing tmux agent)

### Pass 2: Wasteland Delegation

If no local profile satisfies the constraints, the step is distributed as a **wanted item** on the `wl-commons` board:

1. Query `wl-commons.rigs` for peers whose `env_profiles` satisfy the step's constraints (tags + tools + network + agent)
2. Select the best-matching rig (tiebreak: `trust_level`, `last_seen`, `gt_version`)
3. Post a wanted item with `sandbox_required = 1` and the step payload in `sandbox_scope`:

```sql
-- wanted row for a delegated molecule step
id               = "w-<hash>"
title            = "mol: <formula-id>/<step-id>"
type             = "mol-step"
posted_by        = "<local-rig-handle>"
sandbox_required = 1
sandbox_scope    = '{"env":"python-isolated","mol_id":"...","step_id":"..."}'
sandbox_min_tier = "isolated"
status           = "open"
```

4. The target rig runs `gt wl sync` and sees the matching wanted item (`claimed_by` is empty, `sandbox_scope.env` matches a local profile)
5. Target rig runs `gt wl claim w-<hash>` → status: `claimed`
6. Target rig executes the step in the declared environment (see §6)
7. On completion, target rig runs `gt wl done w-<hash> --evidence <bead-uri-or-commit>` → status: `in_review`
8. Local rig syncs (`gt wl sync`) and sees `in_review`; marks the molecule step done

```
Local rig                          wl-commons                  Target rig
    │                                   │                           │
    │── gt wl post (sandbox_required) ─▶│                           │
    │                                   │◀── gt wl sync ────────────│
    │                                   │── claim ─────────────────▶│
    │                                   │                           │── execute in env
    │                                   │                           │── gt wl done
    │◀── gt wl sync ────────────────────│                           │
    │── step marked done in molecule    │                           │
```

The delegated step appears in the local molecule like any other step — it carries a `delegated_to` attribute with the target rig's `hop_uri` from the `rigs` table, and an `evidence_url` pointing to the result bead or commit on the remote rig.

> **Phase 1 note**: In the current wild-west mode, `gt wl post` writes directly to the local clone of `wl-commons`. Full cross-rig visibility requires `gt wl sync` (DoltHub pull) on both sides. PR-mode delegation (Phase 2) will make this atomic.

---

## 6. Agent Execution on Remote Rigs

When a step is delegated, the remote rig executes it using its local `AgentPresetInfo` machinery — the same infrastructure used for all local agent orchestration. There is no special Wasteland execution path.

### Execution Modes

Wasteland-delegated steps are always headless. The remote rig selects an execution mode based on the agent preset's capabilities:

| Agent has `non_interactive`? | Execution mode | Mechanism |
|---|---|---|
| Yes (`claude`, `gemini`, `codex exec`, …) | Direct headless call | `command -p "…"` or `exec` subcommand |
| No (`auggie`, `amp`, …) | Tmux send-keys | Spawn tmux session, deliver via `send-keys`, poll output via `capture-pane` |

The **tmux shim** is the universal execution floor — any CLI agent that runs in a terminal can execute Wasteland-delegated steps, even without a non-interactive API. This is the "zero API" guarantee: participation in the Wasteland requires only that an agent can be launched and receive input.

### Readiness Detection

Once the agent is spawned, the remote rig uses `AgentPresetInfo`'s two readiness strategies before delivering the step:

1. **Prompt-prefix scan** — poll `tmux capture-pane` for the agent's ready prompt (e.g. `❯` for Claude). Reliable for agents with stable prompt characters.
2. **Delay fallback** — wait `ReadyDelayMs` milliseconds. Used for TUI agents (OpenCode, Codex) whose prompts can't be scanned.

### Step Delivery

After the agent is ready, the step's instructions are delivered via:
- **Hooks** (`session_start` callback) — for Claude, OpenCode, Pi. Reliable, no timing dependency.
- **Tmux `send-keys`** — universal fallback. The step description and prompt are sent as text.

The `GT_AGENT` env var is set in the remote tmux session, identifying the agent preset. This is used by Phase 2 of the model router (`ResolveSession`) to verify that the correct agent is handling the step.

### Graceful Degradation

Every capability has a fallback, so the remote rig never hard-blocks on a missing agent feature:

- No hooks → startup fallback via `gt prime && gt mail check --inject` sent over tmux
- No non-interactive mode → full tmux session with send-keys delivery
- No resume → fresh session with handoff mail containing prior context
- No process API → liveness via `tmux pane_current_command` against `ProcessNames`

---

## 7. Security Model

**Principle**: the remote rig is sovereign over its environment. The local rig cannot inspect, modify, or bypass the constraints of a remote profile.

| Property | Guarantee |
|---|---|
| **Network isolation** | Enforced by the remote rig; not a matter of trust |
| **Secrets** | Never transmitted over the Wasteland. Pre-provisioned on the remote rig |
| **Tooling** | The remote rig certifies its manifest; the local rig trusts it |
| **Step content** | The step description and instructions are transmitted. Credentials are not |
| **Results** | Returned via `wl done --evidence` + Dolt sync. No direct channel |

A rig explicitly chooses which profiles it exposes to the Wasteland via the `shared` field. Profiles default to internal-only.

```toml
[envs.secure-sandbox]
# ...
shared = true     # visible to Wasteland peers

[envs.internal-gpu]
# ...
shared = false    # internal only (default)
```

---

## 8. Step Schema Extension

Addition to the existing `Step` struct (see `internal/formula/types.go`):

```go
// Execution environment constraints (all optional).
// Env is a named profile: resolved locally first, then via the Wasteland.
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

## 9. Multi-Rig Molecule Example

```toml
formula = "mol-secure-pipeline"
version = 1

# Step 1: code analysis — runs anywhere
[[steps]]
id    = "analyze"
title = "Analyze codebase"
model = "claude-sonnet-4-5"

# Step 2: tests in an isolated sandbox
# → delegated to a Wasteland peer if unavailable locally
[[steps]]
id      = "test"
title   = "Run tests in isolation"
needs   = ["analyze"]
env     = "python-isolated"
model   = "auto"
min_swe = 50

# Step 3: synthesis — back on the local rig
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
| **Rig selection tiebreaking** | When multiple rigs satisfy the same constraints, on what criteria to pick one? `trust_level` and `last_seen` already exist in `wl-commons.rigs`. Should rigs self-report load or queue depth? |
| **Directed vs open wanted** | Current Wasteland wanted items are open (any rig can claim). Molecule step delegation needs directed assignment (only the matched rig should claim). Should we add a `target_rig` column to `wanted`, or use a separate `mol_steps` table? |
| **Delegated step cancellation** | If a molecule is `burn`ed locally while a wanted item is `claimed` by a remote rig, how is the cancellation communicated? A `status = 'cancelled'` transition doesn't exist in the current schema. |
| **Structured results** | `wl done --evidence` currently takes a free-form URL/string. For molecule steps, the result is a bead URI. Should `completions.evidence` be structured (JSON with bead-uri, model-id, token-counts)? |
| **Profile versioning** | `env_profiles` in the `rigs` row is updated on each `gt wl sync`. Peers read stale manifests until their next sync. Is eventual consistency sufficient, or does capability routing need fresher data? |
| **Transitive delegation** | Rig A delegates to Rig B which delegates to Rig C — should chained delegation be allowed? The `parent_completion_id` column in `completions` hints at this, but the lifecycle isn't defined. |
| **`sandbox_scope` schema** | The `wanted.sandbox_scope` column (JSON) already exists. What is the canonical schema for a molecule step payload? At minimum: `mol_id`, `step_id`, `env`, `instructions`, `result_bead_prefix`. |
| **Agent version pinning** | Should a step be able to require a minimum agent version (e.g. `claude >= 1.2`)? `gt_version` is in `rigs` but refers to the Gas Town binary, not the agent. |
| **Model↔agent mismatch** | If a step declares `model = "claude-opus-4-6"` but the matched rig has `agent = "gemini"`, the router should reject. Is this validated at `wl post` time or at claim time? |
| **Hook portability** | Claude hooks (`settings.json`) and OpenCode hooks (plugin JS) are agent-specific. If a step depends on a `session_start` hook for context injection, does that constraint propagate to the capability manifest? |

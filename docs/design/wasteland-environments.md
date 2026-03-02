# Wasteland Environments

> Spec for environment-aware beads and molecule steps across the Gas Town Wasteland.

**Status**: Draft
**Related**: [federation.md](federation.md) | [model-aware-molecules.md](model-aware-molecules.md) | [agent-provider-interface.md](agent-provider-interface.md)
**Implementation**: `internal/wasteland/` · `internal/doltserver/wl_commons.go` · `internal/cmd/wl_*.go`

---

## 1. Problem and Scope

A molecule step already declares *which model* it wants to run on. This design extends that principle to *which environment* it runs in: what tools are available, what network policy applies, which secrets are visible.

**Key terminology**:
- **Town** — a Gas Town instance. A single `gt` daemon process runs per town. Multiple towns can coexist on the same machine, each sovereign with its own configuration and environment. Rigs live as subdirectories inside the town.
- **Rig** — a git repository (subdirectory) managed by the town daemon. All rigs in a town share the same environment; capabilities are a town-level concern.
- **Bead** — a work item managed by `bd`. A bead can carry environment requirements that express what the work needs to execute, independently of any molecule formula.
- **Wasteland** — a federation of towns, coordinated via the shared `wl-commons` DoltHub database.

Environment routing operates at **two levels**:

| Level | When | Semantics |
|---|---|---|
| **Bead** | At `gt sling` / `wl post` | "This whole work item requires this environment" |
| **Step** | At molecule execution | "This specific step requires this environment" |

Step-level constraints refine bead-level constraints; they cannot contradict them. A bead routed to a Python town can still delegate an individual step to a GPU town via the normal step-delegation path.

Environment capabilities are declared at the **town** level (`~/.gt/envs.toml`) and are uniform across all rigs in that town.

**This document does not cover** how an environment is created — container, VM, bare metal. That is the responsibility of the town that hosts it. This document covers only:

- the data model for describing a town's environment (`EnvProfile`)
- how a bead or step declares its environment requirements
- how a town advertises its capabilities to the Wasteland
- how the Wasteland routes a bead or step to the right town via `wl post / claim / done`

---

## 2. Core Concept: Environment Profile

An `EnvProfile` is a declaration of what a town can provide to execute a bead or step. It is defined locally by the town and optionally advertised to Wasteland peers.

```toml
# ~/.gt/envs.toml  (declared by each town instance)

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
description = "Standard town environment (default)"
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

The `agent` field maps to an entry in `builtinPresets` (`internal/config/agents.go`). It determines which CLI binary is launched, how readiness is detected, and which capabilities (hooks, non-interactive mode, session resume) are available. See [agent-provider-interface.md](agent-provider-interface.md) for the full capability matrix.

---

## 3. Bead Environment Requirements

A bead can declare environment requirements directly in its `bd` fields. This is independent of any attached molecule formula and applies to the work as a whole.

### Fields

```
env         string   Named profile: resolved locally first, then via the Wasteland.
env_tools   []string Executables required in the environment.
env_network string   Network policy: "isolated", "full", "restricted:<host1>,<host2>".
env_tags    []string Tags the environment must carry (all required).
env_agent   string   Agent preset required (e.g. "claude", "gemini", "codex").
```

`env` and `env_tools/env_network/env_tags` are mutually exclusive.
`env_agent` may be combined with either form.
A bead with no environment fields runs in the local town's `"full"` profile — identical to current behaviour.

### Bead routing in the Wasteland

When a bead is posted as a Wasteland wanted item (`gt wl post`), its environment requirements populate the existing sandbox columns in `wl-commons.wanted`:

```sql
sandbox_required = 1
sandbox_scope    = '{"env":"python-isolated","bead_id":"gt-abc123"}'
sandbox_min_tier = "isolated"   -- derived from env_network
```

Any town whose `env_profiles` manifest satisfies the requirements can claim the item (`gt wl claim`). The env requirement **is** the claim filter — no `target_town` column is needed at bead level. This resolves the "directed vs open wanted" ambiguity for whole-bead routing: routing is open but gated by capability matching.

### Resolution

```
1. No env fields              → local execution in "full" profile (current behaviour)
2. env = "name" found locally → local execution
3. env constraints match locally → local execution
4. No local match            → gt wl post with sandbox_required=1
5. Wasteland peer matches    → peer claims and executes the whole bead
6. No match anywhere         → bead blocked, error surfaced to poster
```

### `sandbox_min_tier` values

Derived automatically from `env_network` when not set explicitly:

| `env_network` | `sandbox_min_tier` |
|---|---|
| `"isolated"` | `"isolated"` |
| `"restricted:<allowlist>"` | `"restricted"` |
| `"full"` | `"none"` |

---

## 4. Step Environment Constraints

Within a molecule formula, individual steps can declare environment constraints. Step-level constraints refine bead-level constraints; a step that needs a stricter environment than the bead's declared profile triggers a nested delegation (§6).

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
A step with no environment constraint inherits the bead's environment (or the local `"full"` profile if the bead has none).

The model constraint (`model`, `min_swe`, etc.) implicitly drives agent selection: a step requiring `claude-sonnet-4-5` can only execute on a town where the `claude` preset is available. The router resolves this automatically — `env_agent` is only needed when the agent matters independently of the model.

### Step resolution priority

```
1. env = "exact-name"  found in current execution town  → local step execution
2. env = "exact-name"  found in a Wasteland peer town   → step delegation
3. env_tools/env_tags  matched in current town          → local step execution
4. env_tools/env_tags  matched in a Wasteland peer town → step delegation
5. No match anywhere                                     → step blocked, error
```

---

## 5. Capability Manifest in the Wasteland

The Wasteland (`internal/wasteland/`) is the Gas Town federation: each town holds a sovereign fork of the shared **`wl-commons`** DoltHub database, synchronised via fork/PR/merge.

Town registration writes one row to `wl-commons.rigs` per town. The capability manifest extends this row with an `env_profiles` JSON column. Each entry is a shared `EnvProfile` (profiles with `shared = false` are excluded):

```sql
-- Migration required: not yet in schema/commons.sql
ALTER TABLE rigs ADD COLUMN env_profiles JSON;
```

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
# Inspect a Wasteland peer town's environments
gt wl caps <town-handle>

# Output:
#   python-isolated  [python, isolated]  agent: claude  caps: non_interactive,hooks,resume
#   secure-sandbox   [sandbox]           agent: gemini  caps: non_interactive,resume
```

> **Schema note**: `gt wl caps` and the `env_profiles` column are not yet implemented. The `WantedItem` Go struct in `/internal/commons/commons.go` also needs `SandboxScope string` and `SandboxMinTier string` fields added to match the existing SQL columns.

---

## 6. Wasteland Routing

Routing happens in three passes. No new protocol is introduced — everything is built on existing Wasteland primitives (`gt wl post / claim / done / sync`).

### Pass 0 — Bead-level routing (at `gt sling` / `wl post`)

If the bead carries environment requirements:

1. Check whether the local town satisfies them
2. If yes → normal local execution
3. If no → post the bead as a wanted item with `sandbox_required = 1`:

```sql
id               = "w-<hash>"
title            = "<bead-title>"
type             = "mol-step"        -- see note below
posted_by        = "<local-town-handle>"
sandbox_required = 1
sandbox_scope    = '{"env":"python-isolated","bead_id":"gt-abc123"}'
sandbox_min_tier = "isolated"
status           = "open"
```

> **Schema note**: `type = "mol-step"` is not yet in the `wanted.type` enum. To be added alongside `sandbox_scope` semantics documentation.

Any Wasteland peer town whose `env_profiles` matches the `sandbox_scope` constraints can claim the item. The claiming town executes the whole bead (including its attached molecule, if any) in the matched environment.

### Pass 1 — Local step resolution (at molecule execution)

At `gt mol execute` time, for each step:

1. Loads `EnvProfile` entries from `~/.gt/envs.toml`
2. Checks whether the step can run in the current execution town
3. If yes → normal local execution

### Pass 2 — Step-level Wasteland delegation

If no local profile satisfies a step's constraints:

1. Query `wl-commons.rigs` for peer towns whose `env_profiles` satisfy the step's constraints
2. Select the best-matching town (tiebreak: `trust_level`, `last_seen`, `gt_version`)
3. Post a wanted item with the step payload in `sandbox_scope`:

```sql
id               = "w-<hash>"
title            = "mol: <formula-id>/<step-id>"
type             = "mol-step"
posted_by        = "<local-town-handle>"
sandbox_required = 1
sandbox_scope    = '{"env":"python-isolated","mol_id":"...","step_id":"...","instructions":"..."}'
sandbox_min_tier = "isolated"
status           = "open"
```

4. The target town runs `gt wl sync` and sees the matching wanted item
5. Target town runs `gt wl claim w-<hash>` → status: `claimed`
6. Target town executes the step in the declared environment (see §7)
7. On completion, target town runs `gt wl done w-<hash> --evidence <bead-uri-or-commit>` → status: `in_review`
8. Local town syncs (`gt wl sync`) and sees `in_review`; marks the molecule step done

```
Local town                         wl-commons                  Target town
    │                                   │                           │
    │── gt wl post (sandbox_required) ─▶│                           │
    │                                   │◀── gt wl sync ────────────│
    │                                   │── claim ─────────────────▶│
    │                                   │                           │── execute in env
    │                                   │                           │── gt wl done
    │◀── gt wl sync ────────────────────│                           │
    │── step marked done in molecule    │                           │
```

The delegated step carries a `delegated_to` attribute with the target town's `hop_uri` and an `evidence_url` pointing to the result bead or commit on the remote town.

> **Phase 1 note**: In the current wild-west mode, `gt wl post` writes directly to the local clone of `wl-commons`. Full cross-town visibility requires `gt wl sync` (DoltHub pull) on both sides. PR-mode delegation (Phase 2) will make this atomic.

---

## 7. Agent Execution on Remote Towns

When a bead or step is delegated, the remote town executes it using its local `AgentPresetInfo` machinery — the same infrastructure used for all local agent orchestration. There is no special Wasteland execution path.

### Execution Modes

Wasteland-delegated work is always headless. The remote town selects an execution mode based on the agent preset's capabilities:

| Agent has `non_interactive`? | Execution mode | Mechanism |
|---|---|---|
| Yes (`claude`, `gemini`, `codex exec`, …) | Direct headless call | `command -p "…"` or `exec` subcommand |
| No (`auggie`, `amp`, …) | Tmux send-keys | Spawn tmux session, deliver via `send-keys`, poll output via `capture-pane` |

The **tmux shim** is the universal execution floor — any CLI agent that runs in a terminal can execute Wasteland-delegated work, even without a non-interactive API.

### Readiness Detection

Once the agent is spawned, the remote town uses `AgentPresetInfo`'s two readiness strategies before delivering the work:

1. **Prompt-prefix scan** — poll `tmux capture-pane` for the agent's ready prompt (e.g. `❯` for Claude). Reliable for agents with stable prompt characters.
2. **Delay fallback** — wait `ReadyDelayMs` milliseconds. Used for TUI agents (OpenCode, Codex) whose prompts can't be scanned.

### Work Delivery

After the agent is ready, the work is delivered via:
- **Hooks** (`session_start` callback) — for Claude, OpenCode, Pi. Reliable, no timing dependency.
- **Tmux `send-keys`** — universal fallback. The step description and prompt are sent as text.

The `GT_AGENT` env var is set in the remote tmux session, identifying the agent preset. This is used by Phase 2 of the model router (`ResolveSession`) to verify that the correct agent is handling the work.

### Graceful Degradation

Every capability has a fallback, so the remote town never hard-blocks on a missing agent feature:

- No hooks → startup fallback via `gt prime && gt mail check --inject` sent over tmux
- No non-interactive mode → full tmux session with send-keys delivery
- No resume → fresh session with handoff mail containing prior context
- No process API → liveness via `tmux pane_current_command` against `ProcessNames`

---

## 8. Security Model

**Principle**: the remote town is sovereign over its environment. The local town cannot inspect, modify, or bypass the constraints of a remote profile.

| Property | Guarantee |
|---|---|
| **Network isolation** | Enforced by the remote town; not a matter of trust |
| **Secrets** | Never transmitted over the Wasteland. Pre-provisioned on the remote town |
| **Tooling** | The remote town certifies its manifest; the local town trusts it |
| **Step content** | The step description and instructions are transmitted. Credentials are not |
| **Results** | Returned via `wl done --evidence` + Dolt sync. No direct channel |

A town explicitly chooses which profiles it exposes to the Wasteland via the `shared` field. Profiles default to internal-only.

```toml
[envs.secure-sandbox]
# ...
shared = true     # visible to Wasteland peers

[envs.internal-gpu]
# ...
shared = false    # internal only (default)
```

---

## 9. Schema Extensions

### 9a. Bead fields (`bd`)

New optional fields on `bd` work items, parallel to the Step fields:

```go
// Environment requirements for the bead as a whole (all optional).
// Env is a named profile: resolved locally first, then via the Wasteland.
Env        string   `bd:"env"`
// EnvTools requires specific executables in the execution environment.
EnvTools   []string `bd:"env_tools"`
// EnvNetwork requires a specific network policy.
// Values: "isolated", "full", "restricted:<host1>,<host2>"
EnvNetwork string   `bd:"env_network"`
// EnvTags requires an environment carrying all listed tags.
EnvTags    []string `bd:"env_tags"`
// EnvAgent requires a specific agent preset.
// Usually inferred from the model constraint.
EnvAgent   string   `bd:"env_agent"`
```

### 9b. Step fields (`internal/formula/types.go`)

Addition to the existing `Step` struct:

```go
// Execution environment constraints (all optional).
Env        string   `toml:"env"`
EnvTools   []string `toml:"env_tools"`
EnvNetwork string   `toml:"env_network"`
EnvTags    []string `toml:"env_tags"`
EnvAgent   string   `toml:"env_agent"`
```

`Env` and `EnvTools/EnvNetwork/EnvTags` are mutually exclusive (parser error if both are set).
`EnvAgent` may be combined with either form.

### 9c. `wl-commons` schema migrations

```sql
-- 1. Capability manifest on towns
ALTER TABLE rigs ADD COLUMN env_profiles JSON;

-- 2. Directed routing for step delegation (optional, see open questions)
ALTER TABLE wanted ADD COLUMN target_town VARCHAR(255);
```

The `WantedItem` Go struct in `internal/commons/commons.go` also needs:

```go
SandboxScope    string  // maps to sandbox_scope JSON column (already in SQL)
SandboxMinTier  string  // maps to sandbox_min_tier column (already in SQL)
```

---

## 10. Multi-Town Example

```toml
formula = "mol-secure-pipeline"
version = 1

# Step 1: code analysis — runs anywhere (inherits bead env or local "full")
[[steps]]
id    = "analyze"
title = "Analyze codebase"
model = "claude-sonnet-4-5"

# Step 2: tests in an isolated sandbox
# → delegated to a Wasteland peer town if unavailable locally
[[steps]]
id      = "test"
title   = "Run tests in isolation"
needs   = ["analyze"]
env     = "python-isolated"
model   = "auto"
min_swe = 50

# Step 3: synthesis — back on the local (or bead-routed) town
[[steps]]
id    = "report"
title = "Synthesize results"
needs = ["test"]
model = "claude-sonnet-4-5"
```

Bead with a pre-declared environment requirement that routes the whole item upfront:

```
# posted via gt wl post or gt sling
bead gt-abc123:
  title       = "Fix the Python parser regression"
  env         = "python-isolated"
  env_network = "isolated"
```

Routing flow:
```
1. Local town has no "python-isolated" profile
2. gt wl post → sandbox_required=1, sandbox_scope={"env":"python-isolated","bead_id":"gt-abc123"}
3. Remote town (python-isolated available) → gt wl claim
4. Remote town executes full bead + molecule in python-isolated environment
5. gt wl done --evidence <bead-uri> → local town marks bead complete
```

---

## 11. Open Questions

| Question | Discussion |
|---|---|
| **`target_town` for step delegation** | Bead-level routing is open (any matching town can claim — env IS the filter). Step delegation within a running molecule needs to reach a specific town. Options: `target_town VARCHAR(255)` in `wanted`, or encode it in `sandbox_scope.target`. |
| **`type = "mol-step"` enum** | Not yet in `wanted.type` enum (current values: feature, bug, design, rfc, docs, research, community, inference). Add `"mol-step"` or reuse `"inference"`? |
| **Structured results** | `wl done --evidence` currently takes a free-form URL/string. For molecule steps and beads, the result is a bead URI. Should `completions.evidence` be structured JSON (`bead_uri`, `model_id`, `token_counts`)? |
| **Bead env vs step env conflict** | A step declaring `env = "gpu-farm"` inside a bead already routed to a `python-isolated` town is a mismatch. Validate at `wl post` time (step envs must be satisfiable on any town that satisfies the bead env) or at execution time? |
| **Profile versioning** | `env_profiles` in the `rigs` row is updated on each `gt wl sync`. Peer towns read stale manifests until their next sync. Is eventual consistency sufficient? |
| **Transitive delegation** | Town A delegates to Town B which delegates to Town C — should chained delegation be allowed? The `parent_completion_id` column in `completions` hints at this, but the lifecycle isn't defined. |
| **Town selection tiebreaking** | When multiple towns satisfy the same constraints, tiebreak by `trust_level` + `last_seen`. Should towns self-report queue depth? |
| **Agent version pinning** | Should a bead or step require a minimum agent version (e.g. `claude >= 1.2`)? `gt_version` is in `rigs` but refers to the Gas Town binary, not the agent. |
| **Model↔agent mismatch** | If a step declares `model = "claude-opus-4-6"` but the matched town's profile has `agent = "gemini"`, the router should reject. Validated at `wl post` or at claim time? |
| **Hook portability** | Claude hooks (`settings.json`) and OpenCode hooks (plugin JS) are agent-specific. If a step depends on a `session_start` hook for context injection, does that constraint propagate to the capability manifest? |

# Wasteland Environments — Capability Matchmaking

> Spec for capability-aware routing of beads and molecule steps across the Gas Town Wasteland.

**Status**: Draft
**Related**: [federation.md](federation.md) | [model-aware-molecules.md](model-aware-molecules.md) | [agent-provider-interface.md](agent-provider-interface.md)
**Implementation**: `internal/wasteland/` · `internal/doltserver/wl_commons.go` · `internal/cmd/wl_*.go`

---

## 1. Problem and Scope

A molecule step already declares *which model* it wants to run on. This design extends that principle to *which environment* it runs in — and introduces **capability matchmaking** as the mechanism by which the Wasteland connects beads that declare requirements with towns that satisfy them.

The Wasteland is not just a work queue: it is a marketplace where capability supply (town profiles) meets capability demand (bead and step constraints). A bead that needs a GPU, a data lake, or a HIPAA-compliant sandbox is routed to the right town automatically, without the poster knowing which town that is.

**Key terminology**:
- **Town** — a Gas Town instance. A single `gt` daemon process runs per town. Multiple towns can coexist on the same machine, each sovereign with its own configuration and environment. Rigs live as subdirectories inside the town.
- **Rig** — a git repository (subdirectory) managed by the town daemon. All rigs in a town share the same environment; capabilities are a town-level concern.
- **Bead** — a work item managed by `bd`. A bead can carry capability requirements that express what the work needs to execute, independently of any molecule formula.
- **Wasteland** — a federation of towns, coordinated via the shared `wl-commons` DoltHub database.
- **Capability manifest** — a town's public advertisement of what it can provide: compute (GPU, RAM, storage), data access (lakes, databases), network policy, security posture, and agent availability.
- **Matchmaking** — the process of finding the town whose capability manifest best satisfies a bead or step's requirements.

Capability routing operates at **two levels**:

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

An `EnvProfile` is a declaration of what a town can provide to execute a bead or step. It is defined locally by the town and optionally advertised to Wasteland peers as part of the town's **capability manifest**.

Profiles cover five capability dimensions: **tools**, **network**, **compute**, **data**, and **security**. A bead or step declares requirements in any subset of these dimensions; the matchmaker finds the town whose profile satisfies all of them.

```toml
# ~/.gt/envs.toml  (declared by each town instance)

[envs.python-isolated]
description = "Python 3.12 with no network access"
tools       = ["git", "python3.12", "uv", "make"]
network     = "isolated"
secrets     = []
tags        = ["python", "isolated"]
agent       = "claude"
shared      = true

[envs.gpu-training]
description  = "NVIDIA A100 node for ML fine-tuning"
tools        = ["git", "python3.12", "cuda12", "torch", "huggingface-cli"]
network      = "restricted:huggingface.co,files.pythonhosted.org"
secrets      = ["HF_TOKEN"]
tags         = ["gpu", "ml", "training"]
agent        = "claude"
shared       = true

[compute]
gpu          = "nvidia-a100"
gpu_memory   = "40GB"
cpu_cores    = 32
ram          = "128GB"
storage      = "2TB"
storage_type = "nvme"

[envs.datalake-analyst]
description = "Read-only access to S3 data lake + Athena"
tools       = ["git", "python3.12", "aws-cli", "dbt"]
network     = "restricted:s3.amazonaws.com,athena.us-east-1.amazonaws.com"
secrets     = ["AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"]
tags        = ["data", "analytics", "s3"]
agent       = "claude"
shared      = true

[data]
lakes        = ["s3://corp-datalake/"]
databases    = ["athena:corp-warehouse"]
access       = "read-only"

[envs.hipaa-sandbox]
description = "HIPAA-compliant isolated sandbox for health data"
tools       = ["git", "python3.12"]
network     = "isolated"
secrets     = []
tags        = ["hipaa", "healthcare", "isolated"]
agent       = "gemini"
shared      = true

[security]
compliance   = ["hipaa"]
clearance    = "internal"
audit_log    = true

[envs.full]
description = "Standard town environment (default)"
tools       = []                      # empty = whatever is on the machine
network     = "full"
secrets     = ["ANTHROPIC_API_KEY", "GITHUB_TOKEN"]
tags        = ["default"]
agent       = "claude"
shared      = false                   # internal only, never advertised
```

### Profile Fields

**Core fields** (apply to every profile):

| Field | Type | Description |
|---|---|---|
| `description` | string | Human-readable label |
| `tools` | []string | Available executables. Empty = no constraint |
| `network` | string | `"isolated"` · `"full"` · `"restricted:<host1>,<host2>"` |
| `secrets` | []string | Env var names injected into the execution environment. Never advertised to Wasteland |
| `tags` | []string | Free-form labels used for capability matching |
| `agent` | string | Agent preset (`"claude"`, `"gemini"`, `"codex"`, …). Empty = any available |
| `shared` | bool | Whether this profile is advertised to Wasteland peers (default: false) |

**`[compute]` sub-table** (optional):

| Field | Type | Description |
|---|---|---|
| `gpu` | string | GPU model (e.g. `"nvidia-a100"`, `"nvidia-l4"`) or `"any"` |
| `gpu_memory` | string | Minimum GPU VRAM (e.g. `"40GB"`) |
| `cpu_cores` | int | Minimum CPU core count |
| `ram` | string | Minimum RAM (e.g. `"64GB"`) |
| `storage` | string | Minimum disk space (e.g. `"1TB"`) |
| `storage_type` | string | `"ssd"` · `"nvme"` · `"hdd"` |

**`[data]` sub-table** (optional):

| Field | Type | Description |
|---|---|---|
| `lakes` | []string | Accessible data lake URIs (S3, GCS, ADLS) |
| `databases` | []string | Accessible databases (`"athena:db"`, `"bigquery:project"`, `"pg:host/db"`) |
| `access` | string | `"read-only"` · `"read-write"` |

**`[security]` sub-table** (optional):

| Field | Type | Description |
|---|---|---|
| `compliance` | []string | Compliance frameworks enforced (`"hipaa"`, `"soc2"`, `"gdpr"`, `"pci-dss"`) |
| `clearance` | string | Minimum data clearance level (`"public"`, `"internal"`, `"confidential"`, `"secret"`) |
| `audit_log` | bool | Whether all agent actions are audit-logged |

The `agent` field maps to an entry in `builtinPresets` (`internal/config/agents.go`). See [agent-provider-interface.md](agent-provider-interface.md) for the full capability matrix.

### Resource field units

The `[compute]` sub-table uses **Kubernetes resource quantity syntax** for cpu/memory/storage — the same units used by Docker, Nomad, and any K8s-backed sandbox runtime:

- **cpu**: cores as a string (`"8"`) or millicores (`"8000m"`) — 1000m = 1 core
- **memory / gpu_memory**: binary SI suffixes — `"64Gi"`, `"40Gi"` (not `"64GB"`)
- **storage**: same — `"1Ti"`, `"500Gi"`

The field names are inspired by the **devcontainer `hostRequirements`** spec ([devcontainerjson-reference.md](https://github.com/devcontainers/spec/blob/main/docs/specs/devcontainerjson-reference.md)), a CNCF-maintained standard used by VS Code Dev Containers, GitHub Codespaces, and Cursor. `hostRequirements` uses the same vocabulary (`cpus`, `memory`, `storage`, `gpu`) for declaring host-level compute needs in developer tooling contexts — the closest existing standard to what Gas Town profiles need.

### Sandbox Backend (optional)

A profile can declare a `sandbox_type` to specify the enforcement mechanism for its isolation constraints. When present, the town uses the named backend to create and manage the execution container, turning declarative constraints (`network = "isolated"`, compliance tags) into actual OS-level guarantees via Linux namespaces and container isolation.

```toml
[envs.secure-sandbox]
tools         = ["git"]
network       = "isolated"
tags          = ["sandbox", "untrusted"]
agent         = "gemini"
shared        = true
sandbox_type  = "docker"        # or "opensandbox", "nix", "firecracker", …
sandbox_image = "ubuntu:24.04"

[security]
compliance = ["soc2"]
audit_log  = true
```

**Candidate backends** (not yet evaluated — to be confirmed before implementation):

| Backend | Description | Notes |
|---|---|---|
| **Docker** | Standard container runtime | Available on Linux + macOS (Docker Desktop). The baseline. |
| **[OpenSandbox](https://github.com/alibaba/OpenSandbox)** | AI-agent sandbox platform (Alibaba) | Docker/K8s runtimes, per-sandbox egress controls, agents (Claude Code, Gemini CLI, Codex) listed as use cases. Worth evaluating — API maturity and macOS support TBC. |
| **Firecracker** | MicroVM (AWS) | Strong isolation, Linux only, no macOS. Good for `clearance = "secret"` profiles. |
| **gVisor** | Kernel sandbox (Google) | Syscall interception, runs on Linux. Intermediate between Docker and Firecracker. |
| **Nix shell** | Reproducible dev environments | No network isolation, but hermetic tooling. Suitable for `tools`-only constraints without security requirements. |

The choice of backend is a town-level implementation detail. Gas Town defines the interface (`sandbox_type`, `sandbox_image`); the town is responsible for having the backend available. A town that advertises `sandbox_type = "opensandbox"` but does not have the OpenSandbox lifecycle server running will fail at claim time.

Profiles without `sandbox_type` rely on the town's native environment (bare metal, existing VM, `nix-shell`, etc.) — the town certifies compliance by reputation, not by technical enforcement.

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
      "agent_caps": ["non_interactive", "hooks", "resume"],
      "sandbox_type": "docker"
    },
    {
      "name": "gpu-training",
      "tags": ["gpu", "ml", "training"],
      "tools": ["git", "python3.12", "cuda12", "torch"],
      "network": "restricted:huggingface.co",
      "agent": "claude",
      "agent_caps": ["non_interactive", "hooks", "resume"],
      "compute": {
        "gpu": "nvidia-a100",
        "gpu_memory": "40GB",
        "cpu_cores": 32,
        "ram": "128GB"
      }
    },
    {
      "name": "datalake-analyst",
      "tags": ["data", "analytics", "s3"],
      "tools": ["git", "python3.12", "aws-cli", "dbt"],
      "network": "restricted:s3.amazonaws.com,athena.us-east-1.amazonaws.com",
      "agent": "claude",
      "agent_caps": ["non_interactive", "hooks", "resume"],
      "data": {
        "lakes": ["s3://corp-datalake/"],
        "databases": ["athena:corp-warehouse"],
        "access": "read-only"
      }
    },
    {
      "name": "hipaa-sandbox",
      "tags": ["hipaa", "healthcare", "isolated"],
      "tools": ["git", "python3.12"],
      "network": "isolated",
      "agent": "gemini",
      "agent_caps": ["non_interactive", "resume"],
      "security": {
        "compliance": ["hipaa"],
        "clearance": "confidential",
        "audit_log": true
      },
      "sandbox_type": "docker"
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

## 6. Capability Matchmaking

Matchmaking is the process of selecting the best town for a bead or step. It runs locally, against the `env_profiles` manifests cached in `wl-commons.rigs`, before any Wasteland item is posted.

### Matching Rules

A town profile **satisfies** a requirement if all of the following hold:

| Requirement field | Satisfied when |
|---|---|
| `env = "name"` | Profile `name` equals the declared profile name |
| `env_tools = [...]` | Every listed tool is present in `profile.tools` (or `profile.tools` is empty) |
| `env_network` | Profile `network` is equal to or stricter than the requirement |
| `env_tags = [...]` | Every listed tag is present in `profile.tags` |
| `env_agent` | Profile `agent` equals the declared agent (or requirement is empty) |
| `compute.gpu` | Profile advertises the requested GPU model, or `"any"` |
| `compute.gpu_memory` | Profile `gpu_memory` ≥ required value |
| `compute.cpu_cores` | Profile `cpu_cores` ≥ required value |
| `compute.ram` | Profile `ram` ≥ required value |
| `data.lakes` | Every required lake URI prefix appears in `profile.data.lakes` |
| `data.databases` | Every required database appears in `profile.data.databases` |
| `security.compliance` | Every required compliance tag appears in `profile.security.compliance` |
| `security.clearance` | Profile `clearance` ≥ required clearance level |

Network strictness order: `isolated` > `restricted` > `full`. A bead requiring `isolated` will not match a `full` profile.

Clearance level order: `secret` > `confidential` > `internal` > `public`.

### Scoring

When multiple towns satisfy the same requirements, they are ranked by:

```
score = trust_level × 40
      + recency(last_seen) × 30     # decays to 0 over 7 days of silence
      + profile_fit × 20            # exact name match > superset match
      - queue_depth × 10            # self-reported; omitted = 0 assumed
```

The highest-scoring town is selected. Ties are broken by `last_seen` (most recent wins).

### Partial matches

A town profile that satisfies *some but not all* requirements is not a match. There is no partial routing. If no single town satisfies all requirements, the bead or step is blocked with an error listing the unsatisfied dimensions — e.g.:

```
no town satisfies: compute.gpu=nvidia-a100, security.compliance=[hipaa]
  town-alice: missing compute.gpu
  town-bob:   missing security.compliance
```

This fail-fast behaviour prevents silent capability degradation.

---

## 7. Wasteland Routing

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

## 8. Agent Execution on Remote Towns

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

## 9. Security Model

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

## 10. Schema Extensions

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

## 11. Multi-Town Example

### Example A — Isolated test pipeline

```toml
formula = "mol-secure-pipeline"
version = 1

# Step 1: code analysis — runs anywhere
[[steps]]
id    = "analyze"
title = "Analyze codebase"
model = "claude-sonnet-4-5"

# Step 2: tests in an isolated sandbox
# → matchmaker delegates to a Wasteland peer town if unavailable locally
[[steps]]
id          = "test"
title       = "Run tests in isolation"
needs       = ["analyze"]
env_tools   = ["python3", "make"]
env_network = "isolated"
env_tags    = ["python"]
model       = "auto"
min_swe     = 50

# Step 3: synthesis — runs on the local (or bead-routed) town
[[steps]]
id    = "report"
title = "Synthesize results"
needs = ["test"]
model = "claude-sonnet-4-5"
```

### Example B — GPU fine-tuning + datalake bead

Bead that routes the entire work item upfront to a town with GPU and S3 access:

```
bead gt-def456:
  title       = "Fine-tune classifier on Q1 dataset"
  env_tags    = ["gpu", "ml"]
  env_network = "restricted:s3.amazonaws.com,huggingface.co"

  [compute]
  gpu        = "any"
  gpu_memory = "16GB"

  [data]
  lakes = ["s3://corp-datalake/datasets/q1/"]
```

Matchmaking + routing flow:
```
1. Local town: no GPU profile → no match
2. Matchmaker queries wl-commons.rigs env_profiles
   → town-alice: gpu-training profile (A100, 40GB, S3 access) → score 87
   → town-bob:   gpu-training profile (L4, 24GB, S3 access)   → score 74
3. town-alice selected (higher score)
4. gt wl post → sandbox_required=1, sandbox_scope={"env_tags":["gpu","ml"],...}
5. town-alice gt wl claim
6. town-alice executes full bead in gpu-training environment
7. gt wl done --evidence <bead-uri> → local town marks bead complete
```

### Example C — HIPAA-compliant data bead

```
bead gt-ghi789:
  title = "Analyze patient outcome data"
  env_tags = ["hipaa", "healthcare"]
  env_network = "isolated"

  [security]
  compliance = ["hipaa"]
  clearance  = "confidential"
```

Matchmaking rejects towns without `security.compliance = ["hipaa"]` regardless of all other capabilities. The compliance requirement is a hard filter, not a scoring dimension.

---

## 12. Open Questions

| Question | Discussion |
|---|---|
| **Scoring weights** | The scoring formula (trust×40, recency×30, fit×20, queue×10) is a first guess. Should weights be configurable per town, or learned from historical completion quality? |
| **Queue depth self-reporting** | Scoring penalises busy towns, but towns self-report their queue depth. A dishonest or misconfigured town can game the score. Should queue depth be omitted from the formula, or cross-validated via `last_seen` activity? |
| **Partial capability matching** | Currently fail-fast: all requirements must be satisfied. Should the matchmaker allow "best-effort" matching (ignoring optional requirements flagged with `required = false`)? |
| **`target_town` for step delegation** | Bead-level routing is open (any matching town can claim — env IS the filter). Step delegation within a running molecule has already selected a specific target via matchmaking. Options: `target_town VARCHAR(255)` in `wanted`, or encode it in `sandbox_scope.target`. |
| **`type = "mol-step"` enum** | Not yet in `wanted.type` enum (current values: feature, bug, design, rfc, docs, research, community, inference). Add `"mol-step"` or reuse `"inference"`? |
| **Structured results** | `wl done --evidence` currently takes a free-form URL/string. For molecule steps and beads, the result is a bead URI. Should `completions.evidence` be structured JSON (`bead_uri`, `model_id`, `token_counts`)? |
| **Bead env vs step env conflict** | A step declaring `env_tags = ["gpu"]` inside a bead already routed to a `python-isolated` town is a mismatch. Validate at `wl post` time, or at execution time? |
| **Profile versioning** | `env_profiles` in the `rigs` row is updated on each `gt wl sync`. Peer towns read stale manifests until their next sync. Is eventual consistency sufficient, or does compute/data routing need fresher capability data? |
| **Transitive delegation** | Town A delegates to Town B which delegates to Town C — should chained delegation be allowed? The `parent_completion_id` column in `completions` hints at this, but the lifecycle isn't defined. |
| **Sandbox backend selection** | No backend has been formally evaluated yet. OpenSandbox, Firecracker, gVisor, and plain Docker are candidates. Criteria: macOS support (Docker Desktop is a hard dep for non-Linux towns), API maturity, agent compatibility (Claude Code, Gemini CLI, Codex), security audit trail. Needs a dedicated evaluation before implementation. |
| **Compliance certification** | `security.compliance = ["hipaa"]` is a self-certification. Nothing prevents a town from lying. Should compliance-tagged profiles require an out-of-band attestation (signed document, badge in `wl-commons.badges`)? |
| **Agent version pinning** | Should a bead or step require a minimum agent version (e.g. `claude >= 1.2`)? `gt_version` is in `rigs` but refers to the Gas Town binary, not the agent. |
| **Model↔agent mismatch** | If a step declares `model = "claude-opus-4-6"` but the matched town's profile has `agent = "gemini"`, the router should reject. Validated at `wl post` or at claim time? |
| **Hook portability** | Claude hooks (`settings.json`) and OpenCode hooks (plugin JS) are agent-specific. If a step depends on a `session_start` hook, does that constraint propagate to the capability manifest? |

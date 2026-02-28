# OpenTelemetry Data Model

Complete schema of all telemetry events emitted by Gas Town. Each event consists of:

1. **Log record** (→ any OTLP v1.x+ backend, defaults to VictoriaLogs) with full structured attributes
2. **Metric counter** (→ any OTLP v1.x+ backend, defaults to VictoriaMetrics) for aggregation

All events automatically carry `run.id` from context or `GT_RUN` environment variable for waterfall correlation.

---

## Event Index

| Event | Category | Status |
|-------|----------|--------|
| `agent.instantiate` | Session | ✅ Main |
| `session.start` | Session | ✅ Main |
| `session.stop` | Session | ✅ Main |
| `agent.event` | Agent | ✅ Available in main |
| `agent.usage` | Agent | ✅ Available in main |
| `agent.state_change` | Agent | ✅ Main |
| `bd.call` | Work | ✅ Main |
| `mail` | Work | ✅ Main |
| `prime` | Workflow | ✅ Main |
| `prime.context` | Workflow | ✅ Main |
| `prompt.send` | Workflow | ✅ Main |
| `nudge` | Workflow | ✅ Main |
| `sling` | Workflow | ✅ Main |
| `done` | Workflow | ✅ Main |
| `polecat.spawn` | Lifecycle | ✅ Main |
| `polecat.remove` | Lifecycle | ✅ Main |
| `daemon.restart` | Lifecycle | ✅ Main |
| `mol.cook` | Molecule | ✅ Main |
| `mol.wisp` | Molecule | ✅ Main |
| `mol.squash` | Molecule | ✅ Main |
| `mol.burn` | Molecule | ✅ Main |
| `bead.create` | Molecule | ✅ Main |
| `formula.instantiate` | Molecule | ✅ Main |
| `convoy.create` | Molecule | ✅ Main |

---

## 1. Identity hierarchy

### 1.1 Instance

The outermost grouping. Derived at agent spawn time from the machine hostname
and the town root directory basename.

| Attribute | Type | Description |
|---|---|---|
| `instance` | string | `hostname:basename(town_root)` — e.g. `"laptop:gt"` |
| `town_root` | string | absolute path to the town root — e.g. `"/Users/pa/gt"` |

### 1.2 Run

Each agent spawn generates one `run.id` UUID. All OTel records for that
session carry the same `run.id`.

| Attribute | Type | Source |
|---|---|---|
| `run.id` | string (UUID v4) | generated at spawn; propagated via `GT_RUN` |
| `instance` | string | `hostname:basename(town_root)` |
| `town_root` | string | absolute town root path |
| `agent_type` | string | `"claudecode"`, `"opencode"`, … |
| `role` | string | `polecat` · `witness` · `mayor` · `refinery` · `crew` · `deacon` · `dog` · `boot` |
| `agent_name` | string | specific name within the role (e.g. `"wyvern-Toast"`); equals role for singletons |
| `session_id` | string | tmux pane name |
| `rig` | string | allocation rig at session creation; **empty for generic polecats**. Not the work rig — see `work_rig` on `prime` events. |

---

## 2. Events

### `agent.instantiate`

Emitted once per agent spawn. Anchors all subsequent events for that run.

| Attribute | Type | Description |
|---|---|---|
| `run.id` | string | run UUID |
| `instance` | string | `hostname:basename(town_root)` |
| `town_root` | string | absolute town root path |
| `agent_type` | string | `"claudecode"` · `"opencode"` · … |
| `role` | string | Gastown role |
| `agent_name` | string | agent name |
| `session_id` | string | tmux pane name |
| `rig` | string | allocation rig (empty for generic polecats — not the work rig) |
| `issue_id` | string | bead ID passed at spawn via `--issue`; empty if none |
| `git_branch` | string | git branch of the working directory at spawn time |
| `git_commit` | string | HEAD SHA of the working directory at spawn time |

> **Note on `rig`**: for generic polecats this field is empty or reflects the allocation pool, not the rig being worked on. Use `work_rig` from subsequent `prime` events for accurate work attribution.

**Example log record (generic polecat, no rig at spawn):**
```json
{
  "run.id": "a3f8c21d-4b6e-4f10-9c32-e5d7a8f9b0c1",
  "instance": "laptop:gt",
  "town_root": "/Users/pa/gt",
  "agent_type": "claudecode",
  "role": "polecat",
  "agent_name": "Toast",
  "session_id": "gt-Toast",
  "rig": "",
  "issue_id": "",
  "git_branch": "main",
  "git_commit": "d4e5f6a7b8c9d0e1"
}
```

---

### `session.start` / `session.stop`

tmux session lifecycle events.

| Attribute | Type | Description |
|---|---|---|
| `run.id` | string | run UUID |
| `session_id` | string | tmux pane name |
| `role` | string | Gastown role |
| `status` | string | `"ok"` · `"error"` |

---

### `prime`

Emitted on each `gt prime` invocation. The rendered formula is emitted
separately as `prime.context` (same attributes plus `formula`).

For generic polecats, `gt prime` is the moment work context becomes known. The `work_*` attributes below are planned (see roadmap P0) — once implemented, they will also be persisted in the tmux session environment so all subsequent events (`bd.call`, `mail`, `sling`, `done`) carry them automatically until the next `prime`.

| Attribute | Type | Description |
|---|---|---|
| `run.id` | string | run UUID |
| `role` | string | Gastown role |
| `hook_mode` | bool | true when invoked from a hook |
| `status` | string | `"ok"` · `"error"` |
| `work_rig` | string | ⚠️ **Planned** — rig whose bead is on the hook |
| `work_bead` | string | ⚠️ **Planned** — bead ID currently hooked |
| `work_mol` | string | ⚠️ **Planned** — molecule ID if the bead is a molecule step; empty otherwise |

---

### `prime.context`

Companion to `prime`, emitted in the same invocation. Carries the full rendered formula text.

| Attribute | Type | Description |
|---|---|---|
| `run.id` | string | run UUID |
| `role` | string | Gastown role |
| `hook_mode` | bool | true when invoked from a hook |
| `formula` | string | full rendered formula text |
| `status` | string | `"ok"` · `"error"` |

---

### `prompt.send`

Each `gt sendkeys` dispatch to an agent's tmux pane.

| Attribute | Type | Description |
|---|---|---|
| `run.id` | string | run UUID |
| `session` | string | tmux pane name |
| `keys` | string | full prompt text |
| `keys_len` | int | prompt length in bytes |
| `debounce_ms` | int | applied debounce delay |
| `status` | string | `"ok"` · `"error"` |

---

### `agent.event`

One record per content block in the agent's conversation log.
Full content, no truncation.

| Attribute | Type | Description |
|---|---|---|
| `run.id` | string | run UUID |
| `session` | string | tmux pane name |
| `native_session_id` | string | agent-native session UUID (native AI agent: e.g., Claude Code, OpenCode JSONL filename UUID) |
| `agent_type` | string | adapter name |
| `event_type` | string | `"text"` · `"tool_use"` · `"tool_result"` · `"thinking"` |
| `role` | string | `"assistant"` · `"user"` |
| `content` | string | full content — LLM text, tool JSON input, tool output |

For `tool_use`: `content = "<tool_name>: <full_json_input>"`
For `tool_result`: `content = <full tool output>`

---

### `agent.usage`

One record per assistant turn (not per content block, to avoid
double-counting). Only emitted when `GT_LOG_AGENT_OUTPUT=true`.

| Attribute | Type | Description |
|---|---|---|
| `run.id` | string | run UUID |
| `session` | string | tmux pane name |
| `native_session_id` | string | agent-native session UUID |
| `input_tokens` | int | `input_tokens` from the API usage field |
| `output_tokens` | int | `output_tokens` from the API usage field |
| `cache_read_tokens` | int | `cache_read_input_tokens` |
| `cache_creation_tokens` | int | `cache_creation_input_tokens` |

---

### `bd.call`

Each invocation of the `bd` CLI, whether by the Go daemon or by the agent
in a shell.

| Attribute | Type | Description |
|---|---|---|
| `run.id` | string | run UUID |
| `subcommand` | string | bd subcommand (`"ready"`, `"update"`, `"create"`, …) |
| `args` | string | full argument list |
| `duration_ms` | float | wall-clock duration in milliseconds |
| `stdout` | string | full stdout (opt-in: `GT_LOG_BD_OUTPUT=true`) |
| `stderr` | string | full stderr (opt-in: `GT_LOG_BD_OUTPUT=true`) |
| `status` | string | `"ok"` · `"error"` |

---

### `mail`

All operations on the Gastown mail system.

| Attribute | Type | Description |
|---|---|---|
| `run.id` | string | run UUID |
| `operation` | string | `"send"` · `"read"` · `"archive"` · `"list"` · `"delete"` · … |
| `msg.id` | string | message identifier |
| `msg.from` | string | sender address |
| `msg.to` | string | recipient(s), comma-separated |
| `msg.subject` | string | subject |
| `msg.body` | string | full message body — no truncation |
| `msg.thread_id` | string | thread ID |
| `msg.priority` | string | `"high"` · `"normal"` · `"low"` |
| `msg.type` | string | message type (`"work"`, `"notify"`, `"queue"`, …) |
| `status` | string | `"ok"` · `"error"` |

Use `RecordMailMessage(ctx, operation, MailMessageInfo{…}, err)` for operations
where the message is available (send, read). Use `RecordMail(ctx, operation, err)`
for content-less operations (list, archive-by-id).

---

### `agent.state_change`

Emitted whenever an agent transitions to a new state (idle → working, etc.).

| Attribute | Type | Description |
|---|---|---|
| `run.id` | string | run UUID |
| `agent_id` | string | agent identifier |
| `new_state` | string | new state (`"idle"`, `"working"`, `"done"`, …) |
| `hook_bead` | string | bead ID the agent is currently processing; empty if none |
| `status` | string | `"ok"` · `"error"` |

---

### `mol.cook` / `mol.wisp` / `mol.squash` / `mol.burn`

Molecule lifecycle events emitted at each stage of the formula workflow.

**`mol.cook`** — formula compiled to a proto (prerequisite for wisp creation):

| Attribute | Type | Description |
|---|---|---|
| `run.id` | string | run UUID |
| `formula_name` | string | formula name (e.g. `"mol-polecat-work"`) |
| `status` | string | `"ok"` · `"error"` |

**`mol.wisp`** — proto instantiated as a live wisp (ephemeral molecule instance):

| Attribute | Type | Description |
|---|---|---|
| `run.id` | string | run UUID |
| `formula_name` | string | formula name |
| `wisp_root_id` | string | root bead ID of the created wisp |
| `bead_id` | string | base bead bonded to the wisp; empty for standalone formula slinging |
| `status` | string | `"ok"` · `"error"` |

**`mol.squash`** — molecule execution completed and collapsed to a digest:

| Attribute | Type | Description |
|---|---|---|
| `run.id` | string | run UUID |
| `mol_id` | string | molecule root bead ID |
| `done_steps` | int | number of steps completed |
| `total_steps` | int | total steps in the molecule |
| `digest_created` | bool | false when `--no-digest` flag was set |
| `status` | string | `"ok"` · `"error"` |

**`mol.burn`** — molecule destroyed without creating a digest:

| Attribute | Type | Description |
|---|---|---|
| `run.id` | string | run UUID |
| `mol_id` | string | molecule root bead ID |
| `children_closed` | int | number of descendant step beads closed |
| `status` | string | `"ok"` · `"error"` |

---

### `bead.create`

Emitted for each child bead created during molecule instantiation
(`bd mol pour` / `InstantiateMolecule`). Allows tracing the full
parent → child bead graph for a given molecule.

| Attribute | Type | Description |
|---|---|---|
| `run.id` | string | run UUID |
| `bead_id` | string | newly created child bead ID |
| `parent_id` | string | parent (wisp root / base) bead ID |
| `mol_source` | string | molecule proto bead ID that drove the instantiation |

---

### Other events

All carry `run.id`.

| Event body | Key attributes | Metric |
|---|---|---|
| `sling` | `bead`, `target`, `status`, `error` | `gastown.sling.dispatches.total` |
| `nudge` | `target`, `status`, `error` | `gastown.nudge.total` |
| `done` | `exit_type` (`COMPLETED` · `ESCALATED` · `DEFERRED`), `status`, `error` | `gastown.done.total` |
| `polecat.spawn` | `name`, `status`, `error` | `gastown.polecat.spawns.total` |
| `polecat.remove` | `name`, `status`, `error` | `gastown.polecat.removes.total` |
| `formula.instantiate` | `formula_name`, `bead_id`, `status`, `error` (top-level formula-on-bead result) | `gastown.formula.instantiations.total` |
| `convoy.create` | `bead_id`, `status`, `error` | `gastown.convoy.creates.total` |
| `daemon.restart` | `agent_type` | `gastown.daemon.agent_restarts.total` |

---

## 3. Metrics Reference

| Metric | Type | Labels | Status |
|--------|------|--------|--------|
| `gastown.agent.instantiations.total` | Counter | `agent_type`, `role`, `rig` | ✅ Main |
| `gastown.session.starts.total` | Counter | `status`, `role` | ✅ Main |
| `gastown.session.stops.total` | Counter | `status` | ✅ Main |
| `gastown.agent.events.total` | Counter | `session`, `event_type`, `role` | ✅ Main |
| `gastown.agent.state_changes.total` | Counter | `status`, `new_state` | ✅ Main |
| `gastown.bd.calls.total` | Counter | `status`, `subcommand` | ✅ Main |
| `gastown.bd.duration_ms` | Histogram | `subcommand` | ✅ Main |
| `gastown.mail.operations.total` | Counter | `status`, `operation` | ✅ Main |
| `gastown.prime.total` | Counter | `status`, `role`, `hook_mode` | ✅ Main |
| `gastown.prompt.sends.total` | Counter | `status` | ✅ Main |
| `gastown.nudge.total` | Counter | `status` | ✅ Main |
| `gastown.sling.dispatches.total` | Counter | `status` | ✅ Main |
| `gastown.done.total` | Counter | `status`, `exit_type` | ✅ Main |
| `gastown.polecat.spawns.total` | Counter | `status` | ✅ Main |
| `gastown.polecat.removes.total` | Counter | `status` | ✅ Main |
| `gastown.daemon.agent_restarts.total` | Counter | `agent_type` | ✅ Main |
| `gastown.formula.instantiations.total` | Counter | `status`, `formula` | ✅ Main |
| `gastown.convoy.creates.total` | Counter | `status` | ✅ Main |
| `gastown.mol.cooks.total` | Counter | `status`, `formula` | ✅ Main |
| `gastown.mol.wisps.total` | Counter | `status`, `formula` | ✅ Main |
| `gastown.mol.squashes.total` | Counter | `status` | ✅ Main |
| `gastown.mol.burns.total` | Counter | `status` | ✅ Main |
| `gastown.bead.creates.total` | Counter | `mol_source` | ✅ Main |

---

## 4. Recommended indexed attributes

```
run.id, instance, town_root, session_id, rig, role, agent_type,
event_type, msg.thread_id, msg.from, msg.to
```

---

## 5. Environment variables

| Variable | Set by | Description |
|---|---|---|
| `GT_RUN` | tmux session env + subprocess | Run UUID; correlation key across all events |
| `GT_OTEL_LOGS_URL` | daemon startup | OTLP logs endpoint URL |
| `GT_OTEL_METRICS_URL` | daemon startup | OTLP metrics endpoint URL |
| `GT_LOG_AGENT_OUTPUT` | operator | Set to `true` to enable agent conversation event streaming. Requires `GT_OTEL_LOGS_URL` to be set. |
| `GT_LOG_BD_OUTPUT` | operator | Set to `true` to include bd stdout/stderr in `bd.call` log records |

`GT_RUN` is also surfaced as `gt.run_id` in `OTEL_RESOURCE_ATTRIBUTES` for `bd`
subprocesses, correlating their own telemetry to the parent run.

---

## 6. Status Field Semantics

All events include a `status` field:

| Value | Meaning |
|-------|---------|
| "ok" | Operation completed successfully |
| "error" | Operation failed |

When status is "error", the `error` field contains the error message. When status is "ok", `error` is an empty string.

---

## 7. Backend Compatibility

This data model is **backend-agnostic** — any OTLP v1.x+ compatible backend can consume these events:

- **VictoriaMetrics/VictoriaLogs** — Default for local development. Override with `GT_OTEL_METRICS_URL`/`GT_OTEL_LOGS_URL` to use any OTLP-compatible backend.
- **Prometheus** — Via remote_write receiver
- **Grafana Mimir** — Via write endpoint
- **OpenTelemetry Collector** — Universal forwarder to any backend

The schema uses standard OpenTelemetry Protocol (OTLP) with protobuf encoding, which is universally supported.

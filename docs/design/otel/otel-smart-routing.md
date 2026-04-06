# Smart Model Routing via OTel Signals

Extend Gas Town's OpenTelemetry instrumentation to capture the signals needed
for automatic model selection at sling time, with cascade on refinery rejection.

**Prerequisite**: All instrumentation in this spec is opt-in via the existing
`GT_OTEL_METRICS_URL` / `GT_OTEL_LOGS_URL` activation. When OTel is off, these
code paths are no-ops. Smart routing itself degrades gracefully: without OTel
data, `gt sling` falls back to the existing `role_agents` resolution.

---

## Problem

Gas Town assigns the same model to all polecats regardless of task complexity.
A typo fix and a cross-module refactor both get Opus. This wastes money and
capacity. The inverse — always using a cheap model — produces failures that
the Refinery catches but cannot act on intelligently.

**Goal**: Route simple tasks to cheaper models, escalate to capable models on
failure, and feed outcomes back to improve future routing.

---

## Architecture

```
                    ┌─────────────────────┐
                    │   Routing Table     │
                    │  (VictoriaMetrics)  │
                    │                     │
                    │  success_rate by:   │
                    │   model × task_type │
                    │   × priority        │
                    └────────┬────────────┘
                             │ query
                             ▼
 bd create ──► gt sling ──► model_select() ──► polecat (agent=sonnet)
                                                   │
                                                gt done
                                                   │
                                                   ▼
                                              Refinery
                                             ┌────┴────┐
                                        pass │         │ fail
                                             ▼         ▼
                                          merge    reopen bead
                                                   attempt_count++
                                                   re-sling (agent=opus)
                                                       │
                                              ┌────────┘
                                              ▼
                                    refinery.merge_outcome
                                    ──► VictoriaMetrics
                                    (closes the feedback loop)
```

---

## Phase 1: New OTel Events

Three new events, two new metrics. All follow the existing `Record*` pattern
in `internal/telemetry/recorder.go`.

### 1.1 `refinery.test_run`

Emitted by the Refinery after running quality gates on a branch before merge.
One event per test suite invocation (not per test case).

| Attribute | Type | Description |
|---|---|---|
| `mr_id` | string | merge request bead ID |
| `source_bead` | string | original work bead ID |
| `branch` | string | source branch |
| `agent` | string | agent alias used by the polecat (e.g. `"claude-sonnet"`) |
| `model` | string | resolved model ID (e.g. `"claude-sonnet-4-6"`) |
| `suite` | string | test suite name or command (e.g. `"go test ./..."`) |
| `result` | string | `"pass"` · `"fail"` · `"error"` · `"skip"` |
| `duration_ms` | float | wall-clock time of the test run |
| `failure_summary` | string | first 1024 chars of failure output; empty on pass |
| `is_preexisting` | bool | `true` if the failure also exists on the target branch |
| `status` | string | `"ok"` · `"error"` (OTel emission status, not test result) |
| `error` | string | error message; empty when `"ok"` |

**Metric**: `gastown.refinery.test_runs.total` — Counter, labels: `result`, `agent`, `status`

**Recorder**:
```go
func RecordTestRun(ctx context.Context, info TestRunInfo) {
    // info is a struct — more than 4 params
}
```

### 1.2 `refinery.merge_outcome`

Emitted by the Refinery when an MR reaches a terminal state. One event per MR
close (not per retry attempt).

| Attribute | Type | Description |
|---|---|---|
| `mr_id` | string | merge request bead ID |
| `source_bead` | string | original work bead ID |
| `branch` | string | source branch |
| `target` | string | target branch (`"main"`, `"integration/..."`) |
| `agent` | string | agent alias used by the polecat |
| `model` | string | resolved model ID |
| `rig` | string | rig where the work was done |
| `outcome` | string | `"merged"` · `"rejected"` · `"conflict"` · `"superseded"` |
| `reject_reason` | string | `"test_failure"` · `"build_failure"` · `"conflict"` · `""` |
| `attempt_count` | int | number of times this bead was attempted (1 = first try) |
| `task_type` | string | bead type (`"bug"`, `"feature"`, `"task"`, `"chore"`) |
| `task_priority` | int | bead priority (0–4) |
| `duration_ms` | float | wall-clock from MR creation to close |
| `status` | string | `"ok"` · `"error"` |
| `error` | string | error message; empty when `"ok"` |

**Metric**: `gastown.refinery.merge_outcomes.total` — Counter, labels: `outcome`, `agent`, `task_type`, `task_priority`

**Metric**: `gastown.refinery.merge_duration_ms` — Histogram, labels: `outcome`, `agent`

**Recorder**:
```go
func RecordMergeOutcome(ctx context.Context, info MergeOutcomeInfo) {
    // info is a struct — many fields
}
```

### 1.3 `sling.model_select`

Emitted by `gt sling` when smart routing is active. Records which model was
chosen and why.

| Attribute | Type | Description |
|---|---|---|
| `bead` | string | bead ID being slung |
| `target` | string | target rig |
| `task_type` | string | bead type |
| `task_priority` | int | bead priority (0–4) |
| `selected_agent` | string | agent alias chosen |
| `selected_model` | string | resolved model ID |
| `selection_reason` | string | `"heuristic"` · `"history"` · `"escalation"` · `"override"` · `"default"` |
| `attempt_count` | int | attempt number (1 = first try, 2+ = escalation) |
| `confidence` | float | routing confidence 0.0–1.0 (from historical success rate) |
| `fallback_agent` | string | next agent if this one fails; empty if already at top tier |
| `status` | string | `"ok"` · `"error"` |
| `error` | string | error message; empty when `"ok"` |

**Metric**: `gastown.sling.model_selections.total` — Counter, labels: `selected_agent`, `selection_reason`, `task_type`

**Recorder**:
```go
func RecordModelSelect(ctx context.Context, info ModelSelectInfo) {
    // info is a struct
}
```

---

## Phase 2: Bead Schema Extensions

Two new optional fields on beads, used by the routing and cascade logic.

### 2.1 `attempt_count`

Integer field on the bead. Defaults to 0 (unset). Incremented by the Refinery
when it rejects an MR and reopens the source bead.

**Flow**:
1. `gt sling` reads `attempt_count` to decide escalation tier
2. Polecat does work, runs `gt done`
3. Refinery rejects → `bd update <bead> --field attempt_count=<n+1>`
4. Bead is reopened → available for re-sling with escalated model

### 2.2 `last_agent`

String field on the bead. Set by `gt sling` to record which agent was assigned.
Used by the Refinery to tag `refinery.merge_outcome` events and by subsequent
slings to avoid retrying with the same agent.

---

## Phase 3: Pluggable Router Architecture

Model selection uses the **Strategy pattern** via the `ModelRouter` interface
(`internal/routing/`). Multiple routers compose into a **chain of responsibility**
where the first non-declining router wins.

### 3.1 ModelRouter interface

```go
// internal/routing/router.go

type ModelRouter interface {
    Name() string
    Route(ctx context.Context, input RoutingInput, tiers AgentTiers,
          cfg *config.SmartRoutingConfig) RoutingDecision
}

type RoutingInput struct {
    BeadID, Title, Description, TaskType string
    Priority, AttemptCount, DepCount     int
    LastAgent                            string
    Labels                               []string
}

type RoutingDecision struct {
    Agent         string  // selected agent alias; empty = decline
    Reason        string  // machine tag for telemetry ("static", "history", ...)
    Confidence    float64 // 0.0–1.0
    FallbackAgent string  // next tier if this one fails
    Explanation   string  // human-readable one-liner
}

type AgentTiers struct {
    Cheap, Capable, Escalated string // resolved from role_agents
}
```

### 3.2 Built-in routers

| Router | Strategy name | What it does | When it declines |
|--------|--------------|--------------|-----------------|
| `EscalationRouter` | `escalation` | Forces tier upgrade when `attempt_count > 0` | First attempt (count=0) |
| `StaticRouter` | `static` | Rule-based heuristic on type × priority | Never (always decides) |
| `HistoryRouter` | `history` | Queries VictoriaMetrics for success rates | VM unreachable or < `min_samples` data |
| `ClassifyRouter` | `classify` | Calls an LLM to evaluate bead complexity | No `classify_model` configured, LLM error, or confidence < 0.5 |

**EscalationRouter always runs first** — it's prepended to every chain.
A prior rejection overrides all heuristics.

### 3.3 Chain composition

The `strategy` config field defines the chain:

```
"strategy": "static"              → escalation → static
"strategy": "history,static"      → escalation → history → static
"strategy": "classify,static"     → escalation → classify → static
"strategy": "classify,history,static" → full pipeline
```

Built via `routing.BuildRouter(strategy)`. Unknown names return nil (fail-safe).

### 3.4 Custom routers

Register custom routers via `routing.RegisterRouter()`:

```go
func init() {
    routing.RegisterRouter("my-ml-model", func() routing.ModelRouter {
        return &MyMLRouter{endpoint: os.Getenv("ML_ROUTING_URL")}
    })
}

type MyMLRouter struct{ endpoint string }
func (r *MyMLRouter) Name() string { return "my-ml-model" }
func (r *MyMLRouter) Route(ctx context.Context, input routing.RoutingInput,
    tiers routing.AgentTiers, cfg *config.SmartRoutingConfig) routing.RoutingDecision {
    // Call your ML model, return a decision or decline
}
```

Then configure:
```json
"smart_routing": { "strategy": "my-ml-model,static" }
```

### 3.5 Configuration

```json
{
  "role_agents": {
    "polecat": "claude-sonnet",
    "polecat_cheap": "claude-haiku",
    "polecat_escalated": "claude-opus"
  },
  "smart_routing": {
    "enabled": true,
    "strategy": "history,static",
    "success_threshold": 0.85,
    "min_samples": 20,
    "max_attempts": 3,
    "classify_model": "claude-haiku",
    "classify_prompt": ""
  }
}
```

### 3.6 Decision flow

```
gt sling <bead> <rig>
  │
  ├─ --agent flag set? → use it (override, skip routing)
  │
  ├─ smart_routing.enabled = false? → use role_agents["polecat"] (default)
  │
  ├─ attempt_count >= max_attempts? → leave for human (no routing)
  │
  └─ BuildRouter(strategy) → ChainRouter
       │
       ├─ EscalationRouter: attempt_count > 0? → escalate
       ├─ HistoryRouter: VM has data? → route by success rate
       ├─ ClassifyRouter: LLM confident? → route by complexity
       └─ StaticRouter: type × priority → heuristic
            │
            └─ RecordModelSelect(agent, reason, confidence)
                 │
                 └─ resolveTarget(agent=selected)
```

---

## Phase 4: Cascade on Rejection

Changes to the Refinery's rejection flow.

### Current flow

```
Refinery rejects MR
  → bd update <source_bead> --status=open --assignee=""
  → send MERGE_FAILED to Witness
  → same model retries
```

### New flow

```
Refinery rejects MR
  → bd update <source_bead> --status=open --assignee=""
  → bd update <source_bead> --field attempt_count=<n+1>
  → bd update <source_bead> --field last_agent=<agent>
  → RecordMergeOutcome(outcome="rejected", agent, task_type, attempt_count)
  → send MERGE_FAILED to Witness (includes attempt_count)
  → Witness re-slings bead
  → gt sling reads attempt_count → selects escalated agent
  → RecordModelSelect(selection_reason="escalation", attempt_count)
```

### Safety limits

- `max_attempts` (default 3): after N failures, bead is not auto-re-slung.
  Instead it's left open with label `gt:needs-human` for manual triage.
- `RecordMergeOutcome` is emitted regardless — the data feeds the routing
  table even when human intervention is needed.

---

## Phase 5: Recommended Indexed Attributes

Add to the existing recommended index list:

```
agent, model, outcome, result, task_type, task_priority,
attempt_count, selection_reason, suite
```

---

## Phase 6: Queries for Dashboards

### Success rate by model × task type

```promql
sum by (agent, task_type) (
  gastown_refinery_merge_outcomes_total{outcome="merged"}
)
/ sum by (agent, task_type) (
  gastown_refinery_merge_outcomes_total
)
```

### Escalation rate (how often cheap fails)

```promql
sum(gastown_sling_model_selections_total{selection_reason="escalation"})
/ sum(gastown_sling_model_selections_total)
```

### Cost efficiency

```promql
# Cost per successful merge by model
sum by (agent) (gastown_refinery_merge_duration_ms{outcome="merged"})
# Cross-reference with gt costs data for USD attribution
```

### Test failure hotspots

```logsql
_msg:"refinery.test_run" AND result:"fail" AND is_preexisting:"false"
| stats by (agent, suite) count() as failures
| sort by (failures) desc
```

---

## Implementation Order

| Step | What | Depends on |
|------|------|------------|
| 1 | `RecordTestRun` + `refinery.test_run` event | Nothing — instrument existing Refinery test runner |
| 2 | `RecordMergeOutcome` + `refinery.merge_outcome` event | Nothing — instrument existing Refinery close flow |
| 3 | `attempt_count` + `last_agent` bead fields | bd schema change |
| 4 | Refinery cascade: increment `attempt_count`, re-sling | Steps 2, 3 |
| 5 | `RecordModelSelect` + `sling.model_select` event | Nothing — instrument `gt sling` |
| 6 | Static routing heuristic in `gt sling` | Steps 3, 5 |
| 7 | History-based routing (VictoriaMetrics query) | Steps 1, 2, 6 + sufficient data |

Steps 1, 2, and 5 are independent and can be implemented in parallel.
Steps 3–4 are the cascade logic. Step 6 is the routing. Step 7 is the
data-driven refinement.

---

## Graceful Degradation

| Condition | Behavior |
|---|---|
| OTel disabled (`GT_OTEL_*` unset) | No `Record*` calls fire. Routing falls back to `role_agents`. No cascade (existing behavior). |
| OTel enabled but VictoriaMetrics unreachable | `Record*` calls fail silently (best-effort). Static heuristic routing only. |
| OTel enabled, no history yet | Static heuristic routing (Phase 3.2). Events accumulate for future queries. |
| OTel enabled, sufficient history | Full data-driven routing (Phase 3.3). |
| Smart routing disabled in config | `role_agents.polecat` used for all slings. Events still emitted for observability. |

# PRD: Resilience Patterns from tokio-prompt-orchestrator

> Production-grade resilience primitives adapted from TPO's aerospace-reliability architecture to harden Gastown's multi-agent orchestration.

**Status**: Draft  
**Owner**: Daemon / Witness / Refinery subsystems  
**Related**: [architecture.md](architecture.md) | [convoy/spec.md](convoy/spec.md)  
**Inspiration**: `tokio-prompt-orchestrator` (Rust, Tokio-based LLM inference orchestrator)

---

## 1. Overview

Gastown coordinates 20–30+ Claude agents across tmux sessions, git worktrees, and beads databases. Several failure modes — cascading restarts, duplicate convoy checks, unbounded queue growth, and stuck-agent loops — are addressed ad-hoc today. TPO solves analogous problems in its LLM inference pipeline with battle-tested patterns.

This PRD specifies **8 resilience patterns** adapted from TPO to Gastown's Go codebase. Each pattern references the specific Gastown subsystem it hardens, the TPO implementation it draws from, and concrete acceptance criteria.

---

## 2. Goals

- Prevent cascading failures when Claude API or Dolt become unavailable.
- Eliminate redundant work from duplicate convoy checks and mail fan-out.
- Protect the Refinery merge queue and mail system from unbounded growth.
- Formalize module contracts to prevent responsibility drift across 50+ packages.
- Provide adaptive agent scaling based on measured load, not static config.
- Enforce test coverage discipline across multi-agent commits.
- Optimize API cost by routing simple work to cheaper execution paths.

---

## 3. Quality Gates

- `go test ./...` passes
- `golangci-lint run` passes
- Each pattern has ≥3 unit tests and ≥1 integration test
- No new `panic()` calls in production paths

---

## 4. Patterns

### Pattern 1: Circuit Breaker

#### 4.1.1 TPO Reference

TPO implements a 3-state FSM (Closed → Open → Half-Open) in `src/enhanced/circuit_breaker.rs`:
- **Closed**: Normal operation; tracks recent failures in a sliding window.
- **Open**: All calls fail-fast for `timeout` duration (default 60s). Prevents cascading load.
- **Half-Open**: After timeout, allows one probe call. Success → Closed; Failure → Open.
- Config: `failure_threshold=5`, `success_rate_threshold=0.8`, `timeout=60s`.
- Latency: ~0.4μs on closed (hot) path.

#### 4.1.2 Gastown Application

| Call Site | Current Behavior | With Circuit Breaker |
|-----------|-----------------|---------------------|
| `tmux.CheckSessionHealth()` | Retries on every heartbeat even if tmux is down | Opens after 3 failures, probes every 30s |
| `beadsdk.Storage.GetAllEventsSince()` | Logs error, retries next 5s tick | Opens after 5 failures, probes every 60s |
| `exec.Command("bd", ...)` in mail/convoy | Each call can hang for `BdCommandTimeout` | Opens after 3 timeouts, skips for 30s |
| `exec.Command("gt", "sling", ...)` in convoy feed | Failures logged, continues next issue | Opens after 3 failures, waits before retry |

#### 4.1.3 Proposed API

```go
// Package resilience provides production-grade resilience primitives.
package resilience

type CircuitState int
const (
    StateClosed   CircuitState = iota // Normal operation
    StateOpen                         // Failing fast
    StateHalfOpen                     // Probing recovery
)

type CircuitBreakerConfig struct {
    Name             string        // For metrics/logging (e.g., "tmux", "beads-hq")
    FailureThreshold int           // Consecutive failures to open (default: 5)
    Timeout          time.Duration // Time in Open before probing (default: 60s)
    SuccessThreshold int           // Successes in HalfOpen to close (default: 2)
}

type CircuitBreaker struct { /* ... */ }

func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker
func (cb *CircuitBreaker) Execute(fn func() error) error  // Returns ErrCircuitOpen if open
func (cb *CircuitBreaker) State() CircuitState
func (cb *CircuitBreaker) Reset()                          // Manual reset (for tests/admin)
```

#### 4.1.4 Acceptance Criteria

- [ ] Circuit breaker opens after `FailureThreshold` consecutive failures.
- [ ] All calls during Open state return `ErrCircuitOpen` without executing `fn`.
- [ ] After `Timeout`, exactly one probe call is allowed (Half-Open).
- [ ] Successful probe transitions to Closed; failed probe resets Open timer.
- [ ] Thread-safe: concurrent `Execute()` calls from daemon heartbeat + convoy manager.
- [ ] State is observable via `State()` for daemon status reporting.

#### 4.1.5 Integration Points

- `internal/daemon/daemon.go`: Wrap `ensureDeaconRunning`, `ensureWitnessRunning`, `checkPolecatSessionHealth` in per-subsystem breakers.
- `internal/daemon/convoy_manager.go`: Wrap `pollStore()` beads calls in per-store breakers.
- `internal/mail/router.go`: Wrap `runBdCommand()` calls in a shared `bd-write` breaker.

---

### Pattern 2: Request Deduplication

#### 4.2.1 TPO Reference

TPO uses a concurrent `DashMap<RequestHash, broadcast::Sender>` in `src/enhanced/dedup.rs`:
- Hash incoming requests; check map for existing entry.
- **New**: Insert sender, process request, broadcast result to all waiters.
- **InProgress**: Subscribe to existing sender's broadcast channel, wait for result.
- **Cached**: Return cached result immediately.
- Cleanup task runs every 60s to evict expired entries.
- Production impact: 67.2% dedup collapse rate.
- Latency: ~1.5μs per check.

#### 4.2.2 Gastown Application

| Operation | Current Duplication Source | Dedup Key |
|-----------|--------------------------|-----------|
| `CheckConvoysForIssue()` | Same close event from multiple stores (GH #1798) | `convoy-check:{convoyID}` |
| `FeedStranded()` | Overlapping daemon heartbeat + deacon patrol cycles | `feed-stranded:{convoyID}` |
| Mail fan-out notifications | Same message triggers multiple `notifyRecipient` goroutines | `notify:{messageID}:{recipient}` |
| `dispatchIssue()` | Race between event-driven feed and stranded scan | `dispatch:{issueID}` |

Note: `ConvoyManager.pollStore()` already has `processedCloses` sync.Map for cross-cycle dedup of close events (GH #1798). Pattern 2 generalizes this to all idempotent operations.

#### 4.2.3 Proposed API

```go
type DeduplicatorConfig struct {
    TTL             time.Duration // How long to remember completed operations (default: 5m)
    CleanupInterval time.Duration // Expired entry eviction interval (default: 60s)
    MaxEntries      int           // Hard cap to prevent unbounded growth (default: 10000)
}

type DeduplicateResult int
const (
    ResultNew        DeduplicateResult = iota // First time seeing this key — caller should proceed
    ResultInProgress                          // Another goroutine is processing — caller should wait
    ResultCompleted                           // Already completed within TTL — caller should skip
)

type Deduplicator struct { /* sync.Map + cleanup goroutine */ }

func NewDeduplicator(cfg DeduplicatorConfig) *Deduplicator
func (d *Deduplicator) Check(key string) DeduplicateResult
func (d *Deduplicator) Complete(key string)  // Mark operation as done
func (d *Deduplicator) Cancel(key string)    // Remove in-progress entry (on failure)
func (d *Deduplicator) Stop()                // Stop cleanup goroutine
```

#### 4.2.4 Acceptance Criteria

- [ ] First `Check()` for a key returns `ResultNew`.
- [ ] Concurrent `Check()` for same key returns `ResultInProgress`.
- [ ] After `Complete()`, subsequent `Check()` within TTL returns `ResultCompleted`.
- [ ] After TTL expiry, `Check()` returns `ResultNew` again.
- [ ] `Cancel()` removes in-progress entry so next caller can proceed.
- [ ] Cleanup goroutine evicts expired entries without blocking `Check()`.
- [ ] `MaxEntries` hard cap prevents unbounded memory growth.
- [ ] Replaces `ConvoyManager.processedCloses` sync.Map with unified dedup.

---

### Pattern 3: Backpressure & Graceful Shedding

#### 4.3.1 TPO Reference

TPO's 5-stage pipeline uses bounded MPSC channels with explicit buffer sizes:
- `RAG(512) → Assemble(512) → Inference(1024) → Post(512) → Stream(256)`
- Largest buffer (1024) placed before slowest stage (inference).
- When a channel is full, new requests are **dropped** (not blocked).
- Shedding is logged with structured tracing for observability.

#### 4.3.2 Gastown Application

| Queue | Current Behavior | Risk |
|-------|-----------------|------|
| Refinery merge queue | Unbounded beads query; all open MRs loaded | Memory growth with 100+ MRs |
| Mail delivery (`notifyWg`) | Unbounded goroutine fan-out | Goroutine explosion on `@rig` sends |
| Nudge queue (filesystem) | Unbounded JSON files per session | Disk exhaustion if agent stuck |
| Convoy stranded scan | Dispatches up to `maxPerCycle=3` dogs | Fixed limit but no feedback from dog capacity |
| `dispatchQueuedWork()` | Shells to `gt scheduler run` with 5m timeout | No awareness of active polecat count |

#### 4.3.3 Proposed API

```go
type BoundedQueueConfig struct {
    Name     string // For metrics/logging
    Capacity int    // Max items before shedding
    OnShed   func(item interface{}) // Optional callback for shed items (logging, metrics)
}

type BoundedQueue[T any] struct { /* ... */ }

func NewBoundedQueue[T any](cfg BoundedQueueConfig) *BoundedQueue[T]
func (q *BoundedQueue[T]) TryEnqueue(item T) bool  // Returns false if full (item shed)
func (q *BoundedQueue[T]) Dequeue() (T, bool)      // Non-blocking dequeue
func (q *BoundedQueue[T]) Len() int
func (q *BoundedQueue[T]) Cap() int
```

#### 4.3.4 Acceptance Criteria

- [ ] `TryEnqueue` returns false when queue is at capacity (no blocking).
- [ ] Shed items trigger `OnShed` callback for structured logging.
- [ ] Mail fan-out capped at configurable concurrency (default: 10 goroutines).
- [ ] Nudge queue capped at 50 pending nudges per session; oldest shed on overflow.
- [ ] Refinery `Queue()` returns at most `MaxConcurrent * 3` MRs (configurable).
- [ ] Metrics: shed count, queue depth, high-water mark per named queue.

---

### Pattern 4: Module Contracts

#### 4.4.1 TPO Reference

Every TPO module declares a formal contract in its `mod.rs`:
- **Responsibility**: Single-sentence purpose.
- **Guarantees**: Thread-safety, determinism, performance budgets (e.g., dedup <1ms hot path).
- **NOT Responsible For**: Explicit boundaries with other modules.

#### 4.4.2 Gastown Application

Gastown's 50+ internal packages have implicit contracts enforced by convention and code review. Formalizing contracts would prevent:
- Witness doing convoy work (should only detect zombies and nudge).
- Deacon doing merge operations (should only ensure patrol agents are running).
- Mail router doing session management (should only route messages).

#### 4.4.3 Proposed Format

Each package's `doc.go` (or top of main file) gets a structured contract comment:

```go
// Package witness monitors polecat health and detects zombie sessions.
//
// CONTRACT:
//   Responsibility: Detect zombie polecats (session-dead, agent-dead, agent-hung)
//                   and report findings. Nudge stuck polecats.
//   Guarantees:     - All detection functions are idempotent and safe to call concurrently.
//                   - DetectZombiePolecats completes within 30s for up to 50 polecats.
//                   - No side effects beyond logging and nudging (no kills, no restarts).
//   NOT Responsible For:
//                   - Killing zombie sessions (daemon's job via checkPolecatSessionHealth).
//                   - Feeding convoy work (convoy manager's job).
//                   - Restarting polecats (daemon heartbeat's job).
//                   - Processing merge requests (refinery's job).
```

#### 4.4.4 Packages to Document

| Package | Responsibility | Key Boundary |
|---------|---------------|-------------|
| `internal/witness` | Detect zombie polecats, nudge stuck agents | Does NOT kill or restart |
| `internal/deacon` | Ensure patrol agents are running, dispatch plugins | Does NOT merge or feed convoys |
| `internal/refinery` | Process merge queue, run quality gates | Does NOT assign work to polecats |
| `internal/convoy` | Check convoy completion, feed ready issues | Does NOT spawn polecats |
| `internal/mail` | Route messages between agents | Does NOT manage sessions |
| `internal/daemon` | Heartbeat, restart dead agents, orchestrate subsystems | Does NOT make judgment calls (ZFC) |
| `internal/polecat` | Manage polecat lifecycle (add/remove/start/stop) | Does NOT decide what work to assign |
| `internal/session` | Unified session startup lifecycle | Does NOT manage agent-specific logic |

#### 4.4.5 Acceptance Criteria

- [ ] All 8 packages above have CONTRACT comments.
- [ ] Each contract has Responsibility, Guarantees, and NOT Responsible For sections.
- [ ] Guarantees include at least one measurable property (latency, concurrency-safety, or idempotency).
- [ ] Contracts are reviewed for accuracy against current code behavior.

---

### Pattern 5: Self-Tuning Control Loop

#### 4.5.1 TPO Reference

TPO runs a PID controller (`src/self_tune/controller.rs`) that continuously adjusts 12 runtime parameters based on telemetry snapshots collected every 5 seconds:
- **Telemetry Bus**: Broadcasts snapshots (queue depths, latencies, error rates) to subscribers.
- **Anomaly Detector**: Z-score (2.0σ warning, 3.0σ critical) + CUSUM (drift detection) on sliding windows.
- **PID Controller**: Proportional-Integral-Derivative adjustments with anti-windup and rollback.
- Each tunable parameter has: `current`, `min`, `max`, `step`, `cooldown`, `rollback_threshold`.
- Tuning changes are validated (cargo test) before applying; rollback on regression.

#### 4.5.2 Gastown Application

The Daemon currently uses **fixed intervals** for all operations:
- Recovery heartbeat: fixed interval (currently 3 minutes).
- Dolt health check: fixed cadence.
- Convoy stranded scan: fixed `scanInterval`.
- Plugin cooldowns: static duration strings in TOML.

A control loop could dynamically adjust:

| Parameter | Current | Adaptive Range | Signal |
|-----------|---------|---------------|--------|
| `recoveryHeartbeatInterval` | 3m fixed | 30s–5m | Active zombie count, restart frequency |
| `ConvoyManager.scanInterval` | 30s fixed | 10s–2m | Stranded convoy count, feed success rate |
| `FeedStranded.maxPerCycle` | 3 fixed | 1–10 | Available idle dogs, dispatch success rate |
| Nudge `IdleNotifyTimeout` | Default | 500ms–5s | Session busy rate, nudge queue depth |
| Plugin dispatch concurrency | 1 dog at a time | 1–5 | Idle dog count, plugin backlog |

#### 4.5.3 Proposed API

```go
type TunableParam struct {
    Name     string
    Current  float64
    Min      float64
    Max      float64
    Step     float64
    Cooldown time.Duration // Min time between adjustments
    lastAdj  time.Time
}

type ControlLoopConfig struct {
    SnapshotInterval time.Duration // How often to sample metrics (default: 10s)
    AnomalyZScore    float64      // Z-score threshold for anomaly (default: 2.0)
    Params           []TunableParam
}

type Snapshot struct {
    Timestamp          time.Time
    ActivePolecats     int
    ZombieCount        int
    StrandedConvoys    int
    MergeQueueDepth    int
    NudgeQueueDepth    int
    HeartbeatDuration  time.Duration
    RestartCount       int // In last snapshot window
}

type ControlLoop struct { /* ... */ }

func NewControlLoop(cfg ControlLoopConfig, snapshotFn func() Snapshot) *ControlLoop
func (cl *ControlLoop) Start(ctx context.Context)
func (cl *ControlLoop) GetParam(name string) float64
func (cl *ControlLoop) Snapshot() Snapshot // Latest snapshot for dashboard
```

#### 4.5.4 Acceptance Criteria

- [ ] Snapshot function called at `SnapshotInterval` without blocking daemon heartbeat.
- [ ] Parameters only adjusted after their individual `Cooldown` has elapsed.
- [ ] Adjustments are bounded by `Min`/`Max` (never exceed).
- [ ] Anomaly detection flags when a metric exceeds 2σ from rolling mean.
- [ ] Parameters revert (rollback) if adjustment causes metric degradation within 2 snapshot cycles.
- [ ] All tuning events are logged with: param name, old value, new value, triggering metric.
- [ ] Control loop is optional: disabled by default, enabled via config flag.

---

### Pattern 6: Feature-Gated Builds

#### 4.6.1 TPO Reference

TPO uses Cargo features to enable/disable entire subsystems at compile time:
```toml
default = []
metrics-server = ["axum", "tower"]
self-tune = []
self-improving = ["self-tune", "self-modify", "intelligence"]
tui = ["ratatui", "crossterm"]
mcp = ["rmcp", "async-stream"]
```
This reduces binary size from ~50MB (full) to ~8MB (minimal) and compile time by 60%.

#### 4.6.2 Gastown Application

Gastown already uses Go build tags for `integration` tests. This pattern extends that to optional subsystems:

| Build Tag | Subsystem | Dependencies Excluded |
|-----------|-----------|----------------------|
| `gt_web` | Web dashboard (`internal/web/`) | htmx templates, HTTP server |
| `gt_tui` | Terminal UI (`internal/tui/`) | bubbletea, bubbles, glamour |
| `gt_otel` | OpenTelemetry (`internal/telemetry/`) | OTEL SDK, exporters |
| `gt_metrics` | Prometheus metrics | prometheus client |
| `gt_full` | All of the above | Everything |

#### 4.6.3 Implementation Approach

```go
// internal/web/server.go
//go:build gt_web || gt_full

package web
// ... full implementation ...

// internal/web/stub.go
//go:build !(gt_web || gt_full)

package web

// Stub implementations that compile but do nothing
func StartServer(_ string) error { return nil }
```

#### 4.6.4 Acceptance Criteria

- [ ] `go build ./cmd/gt` (no tags) produces a minimal binary with core functionality.
- [ ] `go build -tags gt_full ./cmd/gt` produces the current full binary.
- [ ] Each tagged subsystem has a stub file that satisfies the interface when excluded.
- [ ] CI builds and tests with both `gt_full` and no tags.
- [ ] Binary size without tags is ≥20% smaller than with `gt_full`.
- [ ] README documents available build tags.

---

### Pattern 7: Test Ratio Enforcement

#### 4.7.1 TPO Reference

TPO enforces a **1.5:1 test-to-production LOC ratio** at every commit via `scripts/ratio_check.sh`:
- Counts test lines (`#[cfg(test)]` blocks + `tests/` directory).
- Counts production lines (everything else in `src/`).
- Fails CI if ratio drops below 1.5.
- Also runs: `cargo fmt`, `cargo clippy -D warnings`, `cargo test`, `cargo audit`, `panic_scan.sh`.

#### 4.7.2 Gastown Application

Gastown has tests but no ratio enforcement. With 24+ agents committing in parallel, test coverage can silently degrade.

#### 4.7.3 Proposed Script: `scripts/test_ratio_check.sh`

```bash
#!/bin/bash
# Enforce minimum test-to-production LOC ratio.
# Usage: scripts/test_ratio_check.sh [--min-ratio 0.8]

MIN_RATIO="${1:-0.8}"  # Gastown starts at 0.8 (grow toward 1.0+)

TEST_LOC=$(find internal/ -name '*_test.go' | xargs wc -l 2>/dev/null | tail -1 | awk '{print $1}')
PROD_LOC=$(find internal/ -name '*.go' ! -name '*_test.go' | xargs wc -l 2>/dev/null | tail -1 | awk '{print $1}')

RATIO=$(echo "scale=2; $TEST_LOC / $PROD_LOC" | bc)

echo "Test LOC:       $TEST_LOC"
echo "Production LOC: $PROD_LOC"
echo "Ratio:          $RATIO (minimum: $MIN_RATIO)"

if (( $(echo "$RATIO < $MIN_RATIO" | bc -l) )); then
    echo "FAIL: Test ratio $RATIO is below minimum $MIN_RATIO"
    exit 1
fi

echo "PASS: Test ratio is acceptable"
```

#### 4.7.4 Acceptance Criteria

- [ ] Script runs in <5s on the full codebase.
- [ ] Initial minimum ratio set to 0.8 (current baseline, measured).
- [ ] Script is added to CI pipeline (GitHub Actions).
- [ ] Makefile target: `make check-test-ratio`.
- [ ] Ratio failure produces actionable output: which packages are below threshold.
- [ ] Per-package breakdown available via `--verbose` flag.
- [ ] Ratio target increases to 1.0 within 3 months, 1.2 within 6 months.

---

### Pattern 8: Complexity-Based Routing

#### 4.8.1 TPO Reference

TPO's `src/routing/scorer.rs` scores prompt complexity using 5 heuristics:
- Token count (+0.3 weight)
- Code blocks present (+0.2)
- Multi-step reasoning required (+0.2)
- Ambiguous references (+0.15)
- Domain-specific terms (+0.15)

Scores route to: local llama.cpp (free, score <0.3) vs cloud API (paid, score ≥0.3). Same prompt always produces the same routing (deterministic).

`src/routing/cost_tracker.rs` tracks per-model cost with budget awareness.

#### 4.8.2 Gastown Application

Gastown dispatches all work to full polecat sessions (Claude Code agents), regardless of task complexity. Simple tasks (closing beads, running `bd sync`, status checks) consume the same resources as complex multi-file implementations.

| Task Type | Current Execution | Proposed Execution | Cost Difference |
|-----------|------------------|-------------------|-----------------|
| `bd close <id>` | Full polecat session (~2min startup) | Wisp (ephemeral, <10s) | ~90% reduction |
| Status check / `gt doctor` | Dog or polecat | Direct CLI (no agent) | ~100% reduction |
| Multi-file feature | Polecat (correct) | Polecat (unchanged) | Baseline |
| Code review | Polecat (expensive) | Formula with cheaper model | ~50% reduction |
| Beads triage | Polecat | Wisp with `bv --robot-triage` | ~80% reduction |

#### 4.8.3 Proposed API

```go
type ComplexityScore struct {
    Score       float64            // 0.0 (trivial) to 1.0 (complex)
    Factors     map[string]float64 // Individual heuristic contributions
    Recommended ExecutionTier      // Derived routing decision
}

type ExecutionTier int
const (
    TierCLI    ExecutionTier = iota // Direct CLI execution, no agent needed
    TierWisp                        // Ephemeral wisp (short-lived agent)
    TierDog                         // Deacon dog (medium-lived agent)
    TierPolecat                     // Full polecat session (long-lived agent)
)

type ComplexityScorer struct { /* ... */ }

func NewComplexityScorer() *ComplexityScorer
func (s *ComplexityScorer) Score(issue *beadsdk.Issue) ComplexityScore
```

Scoring heuristics for Gastown:
- **Dependency count**: Issues with 3+ dependencies → higher complexity.
- **Description length**: >500 chars → likely multi-step.
- **Label signals**: `type:bug` (variable), `type:docs` (simple), `type:feature` (complex).
- **File scope**: Issues referencing multiple packages → higher complexity.
- **Priority**: P0/P1 → always polecat (reliability over cost).

#### 4.8.4 Acceptance Criteria

- [ ] Scorer is deterministic: same issue always produces same score.
- [ ] P0/P1 issues always route to TierPolecat regardless of score.
- [ ] TierCLI tasks execute without spawning any tmux session.
- [ ] TierWisp tasks complete within 60s or escalate to TierDog.
- [ ] Cost tracking: log estimated cost per dispatch tier for observability.
- [ ] Scorer can be overridden per-rig via configuration.
- [ ] Formula system respects tier: `bd cook` for wisps, `bd mol pour` for polecats.

---

## 5. Non-Goals

- **Distributed clustering**: Gastown is single-machine (tmux-based). TPO's NATS/Redis clustering does not apply.
- **Self-modifying code**: TPO's agent loop that generates and validates code changes is out of scope.
- **Prompt optimization**: TPO's semantic dedup and prompt optimization assume direct LLM API access; Gastown delegates to Claude Code.
- **A/B experimentation**: TPO's evolution module is out of scope for this PRD.

---

## 6. File Map

| Pattern | New Files | Modified Files |
|---------|-----------|---------------|
| Circuit Breaker | `internal/resilience/circuit_breaker.go`, `*_test.go` | `daemon.go`, `convoy_manager.go`, `router.go` |
| Deduplication | `internal/resilience/dedup.go`, `*_test.go` | `convoy_manager.go`, `operations.go`, `router.go` |
| Backpressure | `internal/resilience/bounded_queue.go`, `*_test.go` | `router.go`, `nudge/queue.go`, `engineer.go` |
| Module Contracts | — | `doc.go` in 8 packages |
| Self-Tuning | `internal/resilience/control_loop.go`, `*_test.go` | `daemon.go` |
| Feature Gates | `*_stub.go` per subsystem | `web/`, `tui/`, `telemetry/` |
| Test Ratio | `scripts/test_ratio_check.sh` | `Makefile`, `.github/workflows/` |
| Complexity Routing | `internal/resilience/scorer.go`, `*_test.go` | `convoy/operations.go`, `sling.go` |

---

## 7. Priority Order

| Priority | Pattern | Rationale |
|----------|---------|-----------|
| P1 | Circuit Breaker | Prevents cascading failures — highest blast radius |
| P1 | Request Deduplication | Eliminates known duplicate work (GH #1798 was symptomatic) |
| P2 | Backpressure | Protects against unbounded growth under load |
| P2 | Module Contracts | Zero code change, high documentation value, prevents drift |
| P3 | Self-Tuning Control Loop | Requires metric collection infrastructure first |
| P3 | Complexity Routing | Requires formula system changes for tier-aware dispatch |
| P4 | Feature-Gated Builds | Optimization — no correctness impact |
| P4 | Test Ratio Enforcement | CI improvement — no runtime impact |

---

## 8. Success Metrics

| Metric | Current | Target |
|--------|---------|--------|
| Duplicate convoy checks per hour | Unknown (suspected ~30%) | <5% |
| Daemon restart cascades per day | 2–5 (observed) | 0 |
| Mail notification goroutine peak | Unbounded | ≤10 concurrent |
| Nudge queue files per session | Unbounded | ≤50 |
| Mean time to zombie detection | ~3min (heartbeat) | <30s (adaptive) |
| Binary size (minimal build) | N/A | 20% smaller than full |
| Test-to-production LOC ratio | Unknown | ≥0.8 (growing) |

---

## 9. Open Questions

1. **Circuit breaker persistence**: Should breaker state survive daemon restart, or always start Closed?
2. **Dedup across daemon restart**: Should completed-operation TTL data persist to disk, or is in-memory sufficient?
3. **Control loop safety**: How to prevent oscillation when multiple parameters interact (e.g., heartbeat interval affects zombie detection which affects heartbeat interval)?
4. **Build tag adoption**: Should `gt_full` be the default for `make install`, with minimal build opt-in?
5. **Complexity scoring training data**: How to calibrate heuristic weights without historical dispatch cost data?
6. **Feature flag vs build tag**: Should some features use runtime flags (config) instead of compile-time tags for easier toggling?


// Package routing implements the strategy pattern for smart model selection.
//
// A ModelRouter evaluates a bead and returns a routing decision — which agent
// tier to use and why. Multiple routers can be composed into a chain where the
// first router that returns a non-empty decision wins.
//
// Built-in routers:
//   - StaticRouter:    rule-based heuristic on task type × priority
//   - HistoryRouter:   queries VictoriaMetrics for success rates (TODO: implement query)
//   - ClassifyRouter:  calls an LLM to evaluate bead complexity (TODO: implement call)
//   - EscalationRouter: forces escalation when attempt_count > 0
//   - ChainRouter:     tries routers in sequence until one decides
//
// Adding a new router:
//  1. Implement the ModelRouter interface
//  2. Register it in RouterRegistry
//  3. Users enable it via smart_routing.strategy in settings
package routing

import (
	"context"
	"strings"
	"sync"

	"github.com/steveyegge/gastown/internal/config"
)

// RoutingInput is the normalized bead data passed to routers.
// Routers should not call bd or read files — all data is pre-fetched.
type RoutingInput struct {
	BeadID       string // bead identifier
	Title        string // bead title
	Description  string // bead description (may contain custom fields)
	TaskType     string // "bug", "feature", "task", "chore", "epic"
	Priority     int    // 0 (critical) – 4 (backlog)
	AttemptCount int    // number of prior failed attempts (0 = first try)
	LastAgent    string // agent used in the last failed attempt; empty on first try
	Labels       []string
	DepCount     int // number of dependencies
}

// RoutingDecision is the output of a router.
// A zero-value decision (empty Agent) means "I decline — try the next router".
type RoutingDecision struct {
	// Agent is the selected agent alias (e.g. "claude-haiku"). Empty = decline.
	Agent string

	// Reason is a machine-readable tag for telemetry.
	// Convention: the router name (e.g. "static", "history", "classify").
	Reason string

	// Confidence is the router's confidence in this decision (0.0–1.0).
	// Used for telemetry and for future weighted-ensemble routing.
	Confidence float64

	// FallbackAgent is the next agent to try if this one fails at the refinery.
	// Empty means the selected agent is already the top tier.
	FallbackAgent string

	// Explanation is a human-readable one-liner for logging/debugging.
	// Not stored in beads — only emitted to OTel and stdout.
	Explanation string
}

// Declined returns true when the router chose not to make a decision.
func (d RoutingDecision) Declined() bool { return d.Agent == "" }

// AgentTiers holds the three model tiers resolved from role_agents config.
// Routers pick from these — they don't invent agent names.
type AgentTiers struct {
	Cheap     string // role_agents["polecat_cheap"]     — e.g. "claude-haiku"
	Capable   string // role_agents["polecat"]           — e.g. "claude-sonnet"
	Escalated string // role_agents["polecat_escalated"] — e.g. "claude-opus"
}

// Valid returns true when at least cheap and escalated are configured.
func (t AgentTiers) Valid() bool { return t.Cheap != "" && t.Escalated != "" }

// ModelRouter is the strategy interface for model selection.
// Implementations must be safe for concurrent use.
type ModelRouter interface {
	// Name returns a short identifier for telemetry and config (e.g. "static").
	Name() string

	// Route evaluates the input and returns a decision.
	// Return a zero RoutingDecision to decline (let the next router try).
	// ctx carries the OTel run.id for telemetry correlation.
	Route(ctx context.Context, input RoutingInput, tiers AgentTiers, cfg *config.SmartRoutingConfig) RoutingDecision
}

// ---------------------------------------------------------------------------
// Registry: maps strategy names to router constructors
// ---------------------------------------------------------------------------

// RouterFactory creates a ModelRouter instance. Called once at chain build time.
type RouterFactory func() ModelRouter

var (
	registryMu sync.RWMutex
	registry   = map[string]RouterFactory{
		"static":     func() ModelRouter { return &StaticRouter{} },
		"escalation": func() ModelRouter { return &EscalationRouter{} },
		"history":    func() ModelRouter { return &HistoryRouter{} },
		"classify":   func() ModelRouter { return &ClassifyRouter{} },
	}
)

// RegisterRouter adds a custom router to the global registry.
// Call this from an init() in your router package.
func RegisterRouter(name string, factory RouterFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[name] = factory
}

// ---------------------------------------------------------------------------
// ChainRouter: the composite
// ---------------------------------------------------------------------------

// ChainRouter tries routers in order; first non-declined decision wins.
type ChainRouter struct {
	routers []ModelRouter
}

func (c *ChainRouter) Name() string { return "chain" }

func (c *ChainRouter) Route(ctx context.Context, input RoutingInput, tiers AgentTiers, cfg *config.SmartRoutingConfig) RoutingDecision {
	for _, r := range c.routers {
		d := r.Route(ctx, input, tiers, cfg)
		if !d.Declined() {
			return d
		}
	}
	return RoutingDecision{} // all declined
}

// BuildRouter constructs a ModelRouter from a strategy string.
//
// The strategy string is either a single name ("static") or a
// comma-separated chain ("history,static"). The EscalationRouter is
// always prepended — escalation on retry trumps everything.
//
// Returns nil if the strategy string references unknown routers.
func BuildRouter(strategy string) ModelRouter {
	if strategy == "" {
		strategy = "static"
	}

	names := strings.Split(strategy, ",")
	routers := []ModelRouter{&EscalationRouter{}} // always first

	registryMu.RLock()
	defer registryMu.RUnlock()

	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || name == "escalation" {
			continue // already prepended
		}
		factory, ok := registry[name]
		if !ok {
			return nil // unknown router
		}
		routers = append(routers, factory())
	}

	if len(routers) == 1 {
		// Only escalation — append static as final fallback.
		routers = append(routers, &StaticRouter{})
	}

	return &ChainRouter{routers: routers}
}

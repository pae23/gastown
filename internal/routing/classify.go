package routing

import (
	"context"
	"fmt"

	"github.com/steveyegge/gastown/internal/config"
)

// ClassifyRouter calls an LLM to evaluate bead complexity and decide which
// model tier to use. This is the most accurate but slowest router — it adds
// one LLM call at sling time.
//
// The classifier receives the bead title, description, type, and priority
// and outputs a structured JSON decision:
//
//	{"tier": "cheap"|"capable"|"escalated", "confidence": 0.0-1.0, "reason": "..."}
//
// Declines when:
//   - ClassifyModel is not configured
//   - The LLM call fails or returns invalid JSON
//   - The classifier's confidence is below 0.5
//
// Usage in config:
//
//	"smart_routing": {
//	  "enabled": true,
//	  "strategy": "classify,static",
//	  "classify_model": "claude-haiku"
//	}
type ClassifyRouter struct{}

func (r *ClassifyRouter) Name() string { return "classify" }

func (r *ClassifyRouter) Route(ctx context.Context, input RoutingInput, tiers AgentTiers, cfg *config.SmartRoutingConfig) RoutingDecision {
	model := cfg.ClassifyModel
	if model == "" {
		return RoutingDecision{} // decline — no classifier configured
	}

	tier, confidence, reason, err := classifyBead(ctx, model, cfg.ClassifyPrompt, input)
	if err != nil || confidence < 0.5 {
		return RoutingDecision{} // decline — classifier failed or low confidence
	}

	var agent, fallback string
	switch tier {
	case "cheap":
		agent = tiers.Cheap
		fallback = tiers.Capable
	case "capable":
		agent = tiers.Capable
		fallback = tiers.Escalated
	case "escalated":
		agent = tiers.Escalated
		fallback = ""
	default:
		return RoutingDecision{} // decline — unknown tier
	}

	return RoutingDecision{
		Agent:         agent,
		Reason:        "classify",
		Confidence:    confidence,
		FallbackAgent: fallback,
		Explanation:   reason,
	}
}

// classifyBead calls an LLM to classify the bead complexity.
//
// TODO: Implement actual LLM call. The implementation should:
//  1. Build a prompt from the bead data (title, description, type, priority)
//  2. Call the specified model via the Anthropic API (or via gt agent)
//  3. Parse the JSON response: {"tier": "...", "confidence": 0.0-1.0, "reason": "..."}
//
// The built-in prompt should explain the tier semantics:
//   - "cheap": typo fixes, dependency bumps, simple config changes, doc updates
//   - "capable": single-file bugs, straightforward features, test additions
//   - "escalated": cross-module refactors, architecture changes, complex bugs
//
// For now, always returns an error to force decline.
func classifyBead(_ context.Context, _ string, _ string, _ RoutingInput) (tier string, confidence float64, reason string, err error) {
	return "", 0, "", fmt.Errorf("LLM classifier not yet implemented")
}

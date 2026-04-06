package routing

import (
	"context"
	"fmt"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/langfuse"
)

// LangfuseClassifyRouter wraps ClassifyRouter and logs the classification
// to Langfuse for evaluation, scoring, and dataset building.
//
// When Langfuse is not configured, falls through to plain ClassifyRouter.
// When ClassifyRouter declines (no model configured, etc.), this also declines.
//
// Usage: "strategy": "langfuse-classify,static"
type LangfuseClassifyRouter struct {
	inner ClassifyRouter
}

func (r *LangfuseClassifyRouter) Name() string { return "langfuse-classify" }

func (r *LangfuseClassifyRouter) Route(ctx context.Context, input RoutingInput, tiers AgentTiers, cfg *config.SmartRoutingConfig) RoutingDecision {
	model := cfg.ClassifyModel
	if model == "" {
		return RoutingDecision{} // decline
	}

	// Call the classifier (when implemented, this returns real results).
	tier, confidence, reason, err := classifyBead(ctx, model, cfg.ClassifyPrompt, input)
	if err != nil || confidence < 0.5 {
		return RoutingDecision{} // decline
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
		return RoutingDecision{} // decline
	}

	// Log to Langfuse for evaluation and dataset building.
	// This captures the prompt, response, and decision for scoring.
	if langfuse.IsActive() {
		prompt := fmt.Sprintf("Classify complexity: [%s] %s (P%d) — %s",
			input.TaskType, input.Title, input.Priority, truncate(input.Description, 500))
		langfuse.TraceClassification(
			input.BeadID, model, prompt, reason,
			0, 0, // token counts filled when classifier is implemented
			0,    // duration filled when classifier is implemented
			tier, confidence,
		)
	}

	return RoutingDecision{
		Agent:         agent,
		Reason:        "langfuse-classify",
		Confidence:    confidence,
		FallbackAgent: fallback,
		Explanation:   reason,
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func init() {
	RegisterRouter("langfuse-classify", func() ModelRouter {
		return &LangfuseClassifyRouter{}
	})
}

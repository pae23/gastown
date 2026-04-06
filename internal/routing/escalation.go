package routing

import (
	"context"
	"fmt"

	"github.com/steveyegge/gastown/internal/config"
)

// EscalationRouter forces model escalation when a bead has failed before.
// Always runs first in any chain — a prior rejection overrides all heuristics.
type EscalationRouter struct{}

func (r *EscalationRouter) Name() string { return "escalation" }

func (r *EscalationRouter) Route(_ context.Context, input RoutingInput, tiers AgentTiers, _ *config.SmartRoutingConfig) RoutingDecision {
	if input.AttemptCount == 0 {
		return RoutingDecision{} // decline — first attempt, let other routers decide
	}

	switch {
	case input.AttemptCount == 1:
		return RoutingDecision{
			Agent:         tiers.Capable,
			Reason:        "escalation",
			Confidence:    1.0,
			FallbackAgent: tiers.Escalated,
			Explanation:   fmt.Sprintf("attempt %d: escalating from %s to capable tier", input.AttemptCount+1, input.LastAgent),
		}
	default:
		return RoutingDecision{
			Agent:         tiers.Escalated,
			Reason:        "escalation",
			Confidence:    1.0,
			FallbackAgent: "",
			Explanation:   fmt.Sprintf("attempt %d: escalating to max tier", input.AttemptCount+1),
		}
	}
}

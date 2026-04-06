package routing

import (
	"context"

	"github.com/steveyegge/gastown/internal/config"
)

// StaticRouter applies rule-based heuristics on task type and priority.
// This is the default router and the final fallback in any chain.
type StaticRouter struct{}

func (r *StaticRouter) Name() string { return "static" }

func (r *StaticRouter) Route(_ context.Context, input RoutingInput, tiers AgentTiers, _ *config.SmartRoutingConfig) RoutingDecision {
	switch {
	// Chores with low priority → cheap
	case input.TaskType == "chore" && input.Priority >= 3:
		return RoutingDecision{
			Agent:         tiers.Cheap,
			Reason:        "static",
			Confidence:    0.9,
			FallbackAgent: tiers.Capable,
			Explanation:   "low-priority chore → cheap tier",
		}

	// Tasks with medium+ priority → cheap
	case input.TaskType == "task" && input.Priority >= 2:
		return RoutingDecision{
			Agent:         tiers.Cheap,
			Reason:        "static",
			Confidence:    0.8,
			FallbackAgent: tiers.Capable,
			Explanation:   "medium-priority task → cheap tier",
		}

	// High-priority bugs → capable
	case input.TaskType == "bug" && input.Priority <= 1:
		return RoutingDecision{
			Agent:         tiers.Capable,
			Reason:        "static",
			Confidence:    0.7,
			FallbackAgent: tiers.Escalated,
			Explanation:   "high-priority bug → capable tier",
		}

	// Features → capable
	case input.TaskType == "feature":
		return RoutingDecision{
			Agent:         tiers.Capable,
			Reason:        "static",
			Confidence:    0.7,
			FallbackAgent: tiers.Escalated,
			Explanation:   "feature → capable tier",
		}

	// Epics → capable
	case input.TaskType == "epic":
		return RoutingDecision{
			Agent:         tiers.Capable,
			Reason:        "static",
			Confidence:    0.6,
			FallbackAgent: tiers.Escalated,
			Explanation:   "epic → capable tier",
		}

	// Default: cheap for low priority, capable for high priority
	default:
		if input.Priority >= 3 {
			return RoutingDecision{
				Agent:         tiers.Cheap,
				Reason:        "static",
				Confidence:    0.7,
				FallbackAgent: tiers.Capable,
				Explanation:   "low-priority default → cheap tier",
			}
		}
		return RoutingDecision{
			Agent:         tiers.Capable,
			Reason:        "static",
			Confidence:    0.7,
			FallbackAgent: tiers.Escalated,
			Explanation:   "high-priority default → capable tier",
		}
	}
}

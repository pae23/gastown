package routing

import (
	"context"
	"fmt"

	"github.com/steveyegge/gastown/internal/config"
)

// HistoryRouter queries VictoriaMetrics for historical success rates and
// routes to the cheapest model that passes the success threshold for the
// given task type.
//
// Declines when:
//   - VictoriaMetrics is unreachable or GT_OTEL_METRICS_URL is unset
//   - Insufficient data (< MinSamples outcomes for the task type bucket)
//
// When it declines, the next router in the chain takes over (typically static).
type HistoryRouter struct{}

func (r *HistoryRouter) Name() string { return "history" }

func (r *HistoryRouter) Route(ctx context.Context, input RoutingInput, tiers AgentTiers, cfg *config.SmartRoutingConfig) RoutingDecision {
	threshold := cfg.SuccessThreshold
	if threshold <= 0 {
		threshold = 0.85
	}
	minSamples := cfg.MinSamples
	if minSamples <= 0 {
		minSamples = 20
	}

	// Query success rate for cheap model on this task type.
	cheapRate, cheapN, err := querySuccessRate(ctx, tiers.Cheap, input.TaskType)
	if err != nil || cheapN < minSamples {
		return RoutingDecision{} // decline — insufficient data
	}

	if cheapRate >= threshold {
		return RoutingDecision{
			Agent:         tiers.Cheap,
			Reason:        "history",
			Confidence:    cheapRate,
			FallbackAgent: tiers.Capable,
			Explanation:   fmt.Sprintf("cheap model success rate %.0f%% (%d samples) ≥ %.0f%% threshold", cheapRate*100, cheapN, threshold*100),
		}
	}

	// Cheap model below threshold — check capable model.
	capableRate, capableN, err := querySuccessRate(ctx, tiers.Capable, input.TaskType)
	if err != nil || capableN < minSamples {
		// Not enough data for capable either — decline.
		return RoutingDecision{}
	}

	if capableRate >= threshold {
		return RoutingDecision{
			Agent:         tiers.Capable,
			Reason:        "history",
			Confidence:    capableRate,
			FallbackAgent: tiers.Escalated,
			Explanation:   fmt.Sprintf("capable model success rate %.0f%% (%d samples), cheap was %.0f%%", capableRate*100, capableN, cheapRate*100),
		}
	}

	// Both below threshold — route to escalated.
	return RoutingDecision{
		Agent:         tiers.Escalated,
		Reason:        "history",
		Confidence:    1.0,
		FallbackAgent: "",
		Explanation:   fmt.Sprintf("all models below threshold: cheap=%.0f%% capable=%.0f%%", cheapRate*100, capableRate*100),
	}
}

// querySuccessRate queries VictoriaMetrics for the success rate of a given
// agent on a given task type. Returns (rate, sampleCount, error).
//
// TODO: Implement actual VictoriaMetrics query via:
//
//	GET {GT_OTEL_METRICS_URL}/api/v1/query?query=
//	  sum(gastown_refinery_merge_outcomes_total{outcome="merged",agent="<agent>",task_type="<taskType>"})
//	  / sum(gastown_refinery_merge_outcomes_total{agent="<agent>",task_type="<taskType>"})
//
// For now, always returns an error to force decline (falls through to static).
func querySuccessRate(_ context.Context, _ string, _ string) (rate float64, count int, err error) {
	return 0, 0, fmt.Errorf("VictoriaMetrics query not yet implemented")
}

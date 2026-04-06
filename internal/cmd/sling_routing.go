// Package cmd — sling_routing.go
// Smart model routing for gt sling: auto-selects agent tier based on bead
// complexity and historical success rates. See docs/design/otel/otel-smart-routing.md.
//
// Routing is pluggable via the ModelRouter strategy interface in internal/routing.
// Built-in strategies: static, history, classify. Custom routers can be registered
// via routing.RegisterRouter(). Strategy is configured in smart_routing.strategy.
package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/routing"
	"github.com/steveyegge/gastown/internal/telemetry"
	"github.com/steveyegge/gastown/internal/workspace"
)

// smartSelectAgent evaluates a bead and returns the agent alias to use,
// or empty string to fall back to the default role_agents resolution.
//
// It builds a ModelRouter chain from the configured strategy, feeds it
// the bead data, and emits telemetry for the decision.
func smartSelectAgent(ctx context.Context, beadID string, info *beadInfo, townRoot string) string {
	if townRoot == "" {
		var err error
		townRoot, err = workspace.FindFromCwd()
		if err != nil {
			return ""
		}
	}

	// Load town settings to check if smart routing is enabled.
	settings, err := config.LoadOrCreateTownSettings(config.TownSettingsPath(townRoot))
	if err != nil || settings.SmartRouting == nil || !settings.SmartRouting.Enabled {
		return ""
	}
	cfg := settings.SmartRouting

	// Resolve agent tiers from role_agents config.
	tiers := routing.AgentTiers{
		Cheap:     settings.RoleAgents["polecat_cheap"],
		Capable:   settings.RoleAgents["polecat"],
		Escalated: settings.RoleAgents["polecat_escalated"],
	}
	if !tiers.Valid() {
		// Smart routing needs at least cheap + escalated tiers configured.
		return ""
	}
	if tiers.Capable == "" {
		tiers.Capable = tiers.Escalated
	}

	// Safety limit: if max_attempts reached, don't auto-route — leave for human.
	maxAttempts := cfg.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	attemptCount := parseAttemptCount(info)
	if attemptCount >= maxAttempts {
		return ""
	}

	// Build the router chain from the configured strategy.
	router := routing.BuildRouter(cfg.Strategy)
	if router == nil {
		return "" // unknown strategy
	}

	// Prepare routing input.
	input := routing.RoutingInput{
		BeadID:       beadID,
		AttemptCount: attemptCount,
	}
	if info != nil {
		input.Title = info.Title
		input.Description = info.Description
		input.TaskType = info.IssueType
		input.Priority = info.Priority
		input.Labels = info.Labels
		input.DepCount = len(info.Dependencies)
		input.LastAgent = slingDescField(info.Description, "last_agent")
	}

	// Route.
	decision := router.Route(ctx, input, tiers, cfg)
	if decision.Declined() {
		return ""
	}

	// Emit telemetry for the routing decision.
	telemetry.RecordModelSelect(ctx, telemetry.ModelSelectInfo{
		Bead:            beadID,
		TaskType:        input.TaskType,
		TaskPriority:    input.Priority,
		SelectedAgent:   decision.Agent,
		SelectionReason: decision.Reason,
		AttemptCount:    attemptCount + 1, // 1-based for display
		Confidence:      decision.Confidence,
		FallbackAgent:   decision.FallbackAgent,
	}, nil)

	return decision.Agent
}

// parseAttemptCount reads the attempt_count from bead description.
func parseAttemptCount(info *beadInfo) int {
	if info == nil || info.Description == "" {
		return 0
	}
	countStr := slingDescField(info.Description, "attempt_count")
	if countStr == "" {
		return 0
	}
	var n int
	fmt.Sscanf(countStr, "%d", &n)
	return n
}

// slingDescField extracts a "key: value" field from a description string.
func slingDescField(description, key string) string {
	prefix := key + ":"
	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
		if strings.HasPrefix(line, "- "+prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, "- "+prefix))
		}
	}
	return ""
}

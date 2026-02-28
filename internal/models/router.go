package models

import (
	"fmt"
	"sort"
	"strings"
)

// StepConstraints holds the model routing constraints declared on a formula step.
// All fields are optional; zero values mean "no constraint".
type StepConstraints struct {
	// Model is an exact model ID or "auto". Empty means any model.
	Model string
	// Provider filters by provider name.
	Provider string
	// AccessType filters by access method: "subscription" or "api_key".
	AccessType string
	// MinMMLU requires a minimum MMLU benchmark score.
	MinMMLU float64
	// MinSWE requires a minimum SWE-bench score.
	MinSWE float64
	// Requires lists required model capabilities.
	Requires []string
	// MaxCost caps the combined cost in USD per 1K tokens.
	MaxCost float64
	// SubscriptionActive is true when a subscription access is currently available.
	// Filled by the caller from env/config, not from the formula itself.
	SubscriptionActive bool
}

// RoutingDecision is the result of SelectModel.
type RoutingDecision struct {
	// ModelID is the selected model identifier.
	ModelID string
	// Provider is the model's provider.
	Provider string
	// AccessType is the recommended access method.
	AccessType string
	// Reason is a human-readable explanation of why this model was chosen.
	Reason string
	// CostPer1KIn is the input cost (USD per 1K tokens).
	CostPer1KIn float64
	// CostPer1KOut is the output cost (USD per 1K tokens).
	CostPer1KOut float64
	// MMLUScore is the model's known MMLU score, or 0 if unknown.
	MMLUScore float64
	// SWEScore is the model's known SWE-bench score, or 0 if unknown.
	SWEScore float64
}

// SelectModel applies constraints to the model database and returns the best
// matching model using heuristic scoring. No LLM calls are made.
//
// Selection priority (highest first):
//  1. Exact model match (constraints.Model set and not "auto")
//  2. Subscription access (cost = $0), when SubscriptionActive is true
//  3. Constraint satisfaction (min_mmlu, min_swe, requires, max_cost, provider)
//  4. Score: MMLU weight + SWE weight + cost savings
func SelectModel(constraints StepConstraints, db []ModelEntry) (*RoutingDecision, error) {
	// Fast path: exact model specified
	if constraints.Model != "" && constraints.Model != "auto" {
		return exactModel(constraints.Model, constraints, db)
	}

	// Filter candidates
	candidates := filter(db, constraints)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no model satisfies constraints: %s", describeConstraints(constraints))
	}

	// Score and rank
	sort.Slice(candidates, func(i, j int) bool {
		return score(candidates[i], constraints) > score(candidates[j], constraints)
	})

	best := candidates[0]
	accessType := "api_key"
	if constraints.SubscriptionActive && best.SubscriptionEligible {
		accessType = "subscription"
	}

	return &RoutingDecision{
		ModelID:      best.ID,
		Provider:     best.Provider,
		AccessType:   accessType,
		Reason:       routingReason(best, constraints, accessType),
		CostPer1KIn:  best.CostPer1KIn,
		CostPer1KOut: best.CostPer1KOut,
		MMLUScore:    best.MMLUScore,
		SWEScore:     best.SWEScore,
	}, nil
}

// exactModel returns a decision for an explicitly named model.
func exactModel(modelID string, constraints StepConstraints, db []ModelEntry) (*RoutingDecision, error) {
	entry := GetModel(db, modelID)

	accessType := "api_key"
	if constraints.SubscriptionActive && entry != nil && entry.SubscriptionEligible {
		accessType = "subscription"
	}

	d := &RoutingDecision{
		ModelID:    modelID,
		AccessType: accessType,
		Reason:     "exact model specified in formula",
	}
	if entry != nil {
		d.Provider = entry.Provider
		d.CostPer1KIn = entry.CostPer1KIn
		d.CostPer1KOut = entry.CostPer1KOut
		d.MMLUScore = entry.MMLUScore
		d.SWEScore = entry.SWEScore
	}
	return d, nil
}

// filter returns all models that satisfy the hard constraints.
func filter(db []ModelEntry, c StepConstraints) []ModelEntry {
	var out []ModelEntry
	for _, m := range db {
		if c.Provider != "" && m.Provider != c.Provider {
			continue
		}
		if c.AccessType == "subscription" {
			// Hard requirement: subscription must be both declared and active.
			if !m.SubscriptionEligible || !c.SubscriptionActive {
				continue
			}
		}
		if c.MinMMLU > 0 && m.MMLUScore < c.MinMMLU {
			continue
		}
		if c.MinSWE > 0 && m.SWEScore < c.MinSWE {
			continue
		}
		if c.MaxCost > 0 {
			combined := m.CombinedCostPer1K()
			// If cost is zero but subscription isn't active, we don't know the API cost —
			// skip the model unless subscription is active (avoids hiding costs).
			if combined == 0 && !(c.SubscriptionActive && m.SubscriptionEligible) {
				continue
			}
			if combined > 0 && combined > c.MaxCost {
				continue
			}
		}
		missing := false
		for _, req := range c.Requires {
			if !m.HasCapability(req) {
				missing = true
				break
			}
		}
		if missing {
			continue
		}
		out = append(out, m)
	}
	return out
}

// score computes a heuristic routing score for a model (higher is better).
// Weights:
//
//	subscription access (free): +40 points
//	MMLU score (normalized):    up to 30 points
//	SWE score (normalized):     up to 20 points
//	cost savings (inverse):     up to 10 points
func score(m ModelEntry, c StepConstraints) float64 {
	var s float64

	// Subscription bonus: already paid, zero incremental cost
	if c.SubscriptionActive && m.SubscriptionEligible {
		s += 40
	}

	// Quality scores (normalized to 0–1 scale)
	s += (m.MMLUScore / 100) * 30
	s += (m.SWEScore / 100) * 20

	// Cost savings: cheaper = better, scored relative to a $0.10/1K ceiling
	combined := m.CombinedCostPer1K()
	const costCeiling = 0.10
	if combined == 0 {
		s += 10 // free (subscription)
	} else if combined < costCeiling {
		s += (1 - combined/costCeiling) * 10
	}

	return s
}

// routingReason builds a short explanation of the selection.
func routingReason(m ModelEntry, c StepConstraints, accessType string) string {
	var parts []string

	if accessType == "subscription" {
		parts = append(parts, "subscription access (no incremental cost)")
	} else if m.CostPer1KIn > 0 {
		parts = append(parts, fmt.Sprintf("cost $%.5f/1K in", m.CostPer1KIn))
	}

	if m.MMLUScore > 0 {
		parts = append(parts, fmt.Sprintf("MMLU %.1f", m.MMLUScore))
	}
	if m.SWEScore > 0 {
		parts = append(parts, fmt.Sprintf("SWE %.1f", m.SWEScore))
	}

	if len(parts) == 0 {
		return "best available match"
	}
	return strings.Join(parts, ", ")
}

// describeConstraints produces a compact string for error messages.
func describeConstraints(c StepConstraints) string {
	var parts []string
	if c.Provider != "" {
		parts = append(parts, "provider="+c.Provider)
	}
	if c.AccessType != "" {
		parts = append(parts, "access_type="+c.AccessType)
	}
	if c.MinMMLU > 0 {
		parts = append(parts, fmt.Sprintf("min_mmlu=%.0f", c.MinMMLU))
	}
	if c.MinSWE > 0 {
		parts = append(parts, fmt.Sprintf("min_swe=%.0f", c.MinSWE))
	}
	if c.MaxCost > 0 {
		parts = append(parts, fmt.Sprintf("max_cost=%.4f", c.MaxCost))
	}
	if len(c.Requires) > 0 {
		parts = append(parts, "requires=["+strings.Join(c.Requires, ",")+"]")
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, " ")
}

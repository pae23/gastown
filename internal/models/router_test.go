package models

import (
	"testing"
)

var testDB = []ModelEntry{
	{
		ID:                   "claude-opus-4-5",
		Provider:             "anthropic",
		MMLUScore:            87.0,
		SWEScore:             72.5,
		Vision:               true,
		CostPer1KIn:          0.015,
		CostPer1KOut:         0.075,
		SubscriptionEligible: true,
		GoodFor:              []string{"reasoning", "coding"},
	},
	{
		ID:                   "claude-haiku-4-5",
		Provider:             "anthropic",
		MMLUScore:            75.0,
		SWEScore:             40.0,
		Vision:               true,
		CostPer1KIn:          0.00025,
		CostPer1KOut:         0.00125,
		SubscriptionEligible: true,
		GoodFor:              []string{"fast", "cheap"},
	},
	{
		ID:            "gpt-4o",
		Provider:      "openai",
		MMLUScore:     88.0,
		SWEScore:      49.0,
		Vision:        true,
		CodeExecution: true,
		CostPer1KIn:   0.0025,
		CostPer1KOut:  0.01,
	},
	{
		ID:           "gemini-2.0-flash",
		Provider:     "google",
		MMLUScore:    85.0,
		Vision:       true,
		CostPer1KIn:  0.000075,
		CostPer1KOut: 0.0001,
	},
}

func TestSelectModel_ExactModel(t *testing.T) {
	c := StepConstraints{Model: "claude-opus-4-5"}
	d, err := SelectModel(c, testDB)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.ModelID != "claude-opus-4-5" {
		t.Errorf("got %q, want claude-opus-4-5", d.ModelID)
	}
}

func TestSelectModel_SubscriptionPreferred(t *testing.T) {
	c := StepConstraints{
		Model:              "auto",
		SubscriptionActive: true,
	}
	d, err := SelectModel(c, testDB)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.AccessType != "subscription" {
		t.Errorf("expected subscription access, got %q", d.AccessType)
	}
	// Should pick the best subscription-eligible model (opus has highest score)
	if d.ModelID != "claude-opus-4-5" {
		t.Errorf("expected claude-opus-4-5, got %q", d.ModelID)
	}
}

func TestSelectModel_ProviderFilter(t *testing.T) {
	c := StepConstraints{Provider: "openai"}
	d, err := SelectModel(c, testDB)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Provider != "openai" {
		t.Errorf("got provider %q, want openai", d.Provider)
	}
}

func TestSelectModel_MinMMLU(t *testing.T) {
	// min_mmlu=86 should exclude haiku (75) and gemini (85), leaving opus (87) and gpt-4o (88)
	c := StepConstraints{MinMMLU: 86}
	d, err := SelectModel(c, testDB)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.MMLUScore < 86 {
		t.Errorf("selected model MMLU %.1f < min 86", d.MMLUScore)
	}
}

func TestSelectModel_RequiresCodeExecution(t *testing.T) {
	c := StepConstraints{Requires: []string{"code_execution"}}
	d, err := SelectModel(c, testDB)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.ModelID != "gpt-4o" {
		t.Errorf("expected gpt-4o (only model with code_execution), got %q", d.ModelID)
	}
}

func TestSelectModel_MaxCost(t *testing.T) {
	// max_cost=0.001 excludes opus (0.0263/1K combined) and gpt-4o (0.00813/1K combined)
	// haiku combined = (0.00025 + 0.00125*3)/4 = 0.000031; gemini = (0.000075 + 0.0001*3)/4 = 0.000094
	c := StepConstraints{MaxCost: 0.001, SubscriptionActive: false}
	d, err := SelectModel(c, testDB)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entry := GetModel(testDB, d.ModelID)
	if entry == nil {
		t.Fatalf("selected model %q not in DB", d.ModelID)
	}
	if entry.CombinedCostPer1K() > 0.001 {
		t.Errorf("selected model combined cost %.6f > max 0.001", entry.CombinedCostPer1K())
	}
}

func TestSelectModel_NoMatch(t *testing.T) {
	// min_mmlu=99 — nothing satisfies this
	c := StepConstraints{MinMMLU: 99}
	_, err := SelectModel(c, testDB)
	if err == nil {
		t.Error("expected error for unsatisfiable constraint, got nil")
	}
}

func TestSelectModel_SubscriptionRequiredNoActive(t *testing.T) {
	// access_type=subscription but SubscriptionActive=false → no eligible models pass filter
	c := StepConstraints{AccessType: "subscription", SubscriptionActive: false}
	_, err := SelectModel(c, testDB)
	if err == nil {
		t.Error("expected error when subscription required but not active")
	}
}

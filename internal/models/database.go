// Package models provides a model capability database with pricing sourced from
// OpenRouter and benchmark scores from static data. The database supports
// per-step model routing in molecule formulas.
package models

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// ModelEntry describes an AI model's capabilities, benchmark scores, and cost.
type ModelEntry struct {
	// ID is the canonical model identifier used with --model flags (e.g. "claude-sonnet-4-5").
	ID string

	// Provider is the AI provider: "anthropic", "openai", "google", "deepseek", "mistral", etc.
	Provider string

	// Name is the human-readable display name.
	Name string

	// OpenRouterID is the OpenRouter model ID used for pricing lookups
	// (e.g. "anthropic/claude-sonnet-4-5"). Empty if not listed on OpenRouter.
	OpenRouterID string

	// Benchmark scores from published evaluations (0–100). Zero means unknown.
	MMLUScore float64 // MMLU (general knowledge)
	SWEScore  float64 // SWE-bench (software engineering)

	// Capabilities
	Vision        bool
	CodeExecution bool
	ContextWindow int // tokens

	// Cost in USD per 1K tokens. Zero means included in subscription or unknown.
	CostPer1KIn  float64
	CostPer1KOut float64

	// SubscriptionEligible is true when this model can be accessed via a subscription
	// (e.g. Claude Code subscription for Anthropic models).
	SubscriptionEligible bool

	// GoodFor is a list of task tags this model excels at.
	// Common values: "coding", "code_review", "reasoning", "vision", "fast", "cheap".
	GoodFor []string
}

// HasCapability returns true if the model supports the named capability.
func (m *ModelEntry) HasCapability(cap string) bool {
	switch cap {
	case "vision":
		return m.Vision
	case "code_execution":
		return m.CodeExecution
	default:
		return false
	}
}

// CombinedCostPer1K returns the combined input+output cost per 1K tokens,
// assuming a 1:3 input:output ratio (common for coding tasks).
func (m *ModelEntry) CombinedCostPer1K() float64 {
	return (m.CostPer1KIn + m.CostPer1KOut*3) / 4
}

// staticDB is the built-in model database with benchmark scores.
// Pricing is intentionally left at zero here and filled in from OpenRouter or ~/.gt/models.toml.
// Benchmark data from published evaluations (approximate; update via ~/.gt/models.toml if stale).
var staticDB = []ModelEntry{
	// === Anthropic ===
	{
		ID:                   "claude-opus-4-5",
		Provider:             "anthropic",
		Name:                 "Claude Opus 4.5",
		OpenRouterID:         "anthropic/claude-opus-4-5",
		MMLUScore:            87.0,
		SWEScore:             72.5,
		Vision:               true,
		ContextWindow:        200000,
		SubscriptionEligible: true,
		GoodFor:              []string{"complex_reasoning", "coding", "code_review", "vision"},
	},
	{
		ID:                   "claude-sonnet-4-5",
		Provider:             "anthropic",
		Name:                 "Claude Sonnet 4.5",
		OpenRouterID:         "anthropic/claude-sonnet-4-5",
		MMLUScore:            83.0,
		SWEScore:             49.0,
		Vision:               true,
		ContextWindow:        200000,
		SubscriptionEligible: true,
		GoodFor:              []string{"coding", "code_review", "reasoning", "vision"},
	},
	{
		ID:                   "claude-haiku-4-5",
		Provider:             "anthropic",
		Name:                 "Claude Haiku 4.5",
		OpenRouterID:         "anthropic/claude-haiku-4-5",
		MMLUScore:            75.0,
		SWEScore:             40.0,
		Vision:               true,
		ContextWindow:        200000,
		SubscriptionEligible: true,
		GoodFor:              []string{"fast", "cheap", "simple_tasks"},
	},
	// === OpenAI ===
	{
		ID:            "gpt-4o",
		Provider:      "openai",
		Name:          "GPT-4o",
		OpenRouterID:  "openai/gpt-4o",
		MMLUScore:     88.0,
		SWEScore:      49.0,
		Vision:        true,
		CodeExecution: true,
		ContextWindow: 128000,
		GoodFor:       []string{"coding", "vision", "code_execution", "reasoning"},
	},
	{
		ID:            "gpt-4o-mini",
		Provider:      "openai",
		Name:          "GPT-4o Mini",
		OpenRouterID:  "openai/gpt-4o-mini",
		MMLUScore:     82.0,
		SWEScore:      23.0,
		Vision:        true,
		ContextWindow: 128000,
		GoodFor:       []string{"fast", "cheap", "simple_tasks"},
	},
	{
		ID:            "o3-mini",
		Provider:      "openai",
		Name:          "o3-mini",
		OpenRouterID:  "openai/o3-mini",
		MMLUScore:     87.0,
		SWEScore:      49.3,
		ContextWindow: 200000,
		GoodFor:       []string{"reasoning", "coding"},
	},
	// === Google ===
	{
		ID:            "gemini-2.0-flash",
		Provider:      "google",
		Name:          "Gemini 2.0 Flash",
		OpenRouterID:  "google/gemini-2.0-flash-001",
		MMLUScore:     85.0,
		SWEScore:      49.1,
		Vision:        true,
		ContextWindow: 1048576,
		GoodFor:       []string{"fast", "cheap", "vision", "coding"},
	},
	{
		ID:            "gemini-2.5-pro",
		Provider:      "google",
		Name:          "Gemini 2.5 Pro",
		OpenRouterID:  "google/gemini-2.5-pro-preview",
		MMLUScore:     90.0,
		SWEScore:      63.0,
		Vision:        true,
		ContextWindow: 1048576,
		GoodFor:       []string{"reasoning", "coding", "vision"},
	},
	// === DeepSeek ===
	{
		ID:            "deepseek-chat",
		Provider:      "deepseek",
		Name:          "DeepSeek V3",
		OpenRouterID:  "deepseek/deepseek-chat",
		MMLUScore:     88.5,
		SWEScore:      49.2,
		ContextWindow: 131072,
		GoodFor:       []string{"coding", "reasoning", "cheap"},
	},
	{
		ID:            "deepseek-r1",
		Provider:      "deepseek",
		Name:          "DeepSeek R1",
		OpenRouterID:  "deepseek/deepseek-r1",
		MMLUScore:     90.8,
		SWEScore:      49.2,
		ContextWindow: 131072,
		GoodFor:       []string{"reasoning", "coding"},
	},
	// === Mistral ===
	{
		ID:            "mistral-large",
		Provider:      "mistral",
		Name:          "Mistral Large",
		OpenRouterID:  "mistralai/mistral-large",
		MMLUScore:     84.0,
		SWEScore:      40.0,
		ContextWindow: 131072,
		GoodFor:       []string{"reasoning", "coding"},
	},
	// === ZAI (Zhipu AI) ===
	{
		ID:            "glm-4-plus",
		Provider:      "zai",
		Name:          "GLM-4 Plus",
		OpenRouterID:  "zhipuai/glm-4-plus",
		MMLUScore:     79.0,
		SWEScore:      35.0,
		Vision:        true,
		ContextWindow: 128000,
		GoodFor:       []string{"coding", "vision"},
	},
}

// openRouterModel is the minimal OpenRouter API response shape we care about.
type openRouterModel struct {
	ID            string `json:"id"`
	ContextLength int    `json:"context_length"`
	Pricing       struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
	} `json:"pricing"`
	Architecture struct {
		InputModalities []string `json:"input_modalities"`
	} `json:"architecture"`
}

type openRouterResponse struct {
	Data []openRouterModel `json:"data"`
}

// pricingCache is the on-disk cache structure for OpenRouter pricing.
type pricingCache struct {
	FetchedAt time.Time               `json:"fetched_at"`
	Pricing   map[string][2]float64   `json:"pricing"` // openrouter_id → [per1Kin, per1Kout]
}

const (
	openRouterURL    = "https://openrouter.ai/api/v1/models"
	pricingCacheName = "models_pricing_cache.json"
	cacheMaxAge      = 24 * time.Hour
	fetchTimeout     = 5 * time.Second
)

// LoadDatabase returns the merged model database:
//  1. Static entries (benchmarks, capabilities)
//  2. OpenRouter pricing applied on top (cached locally for 24h)
//  3. User overrides from ~/.gt/models.toml
func LoadDatabase(gtDir string) []ModelEntry {
	db := make([]ModelEntry, len(staticDB))
	copy(db, staticDB)

	// Apply OpenRouter pricing (best-effort; fall through on any error)
	pricing := loadPricing(gtDir)
	for i := range db {
		if db[i].OpenRouterID == "" {
			continue
		}
		if p, ok := pricing[db[i].OpenRouterID]; ok {
			db[i].CostPer1KIn = p[0]
			db[i].CostPer1KOut = p[1]
		}
	}

	// Apply user overrides from ~/.gt/models.toml
	db = applyOverrides(db, gtDir)

	return db
}

// GetModel looks up a model by ID in the database.
// Returns nil if the model is not found.
func GetModel(db []ModelEntry, id string) *ModelEntry {
	for i := range db {
		if db[i].ID == id {
			return &db[i]
		}
	}
	return nil
}

// loadPricing returns a map of openrouter_id → [per1Kin, per1Kout].
// It reads from the local cache if fresh, otherwise fetches from OpenRouter.
func loadPricing(gtDir string) map[string][2]float64 {
	cachePath := filepath.Join(gtDir, pricingCacheName)

	// Try cache first
	if data, err := os.ReadFile(cachePath); err == nil {
		var cache pricingCache
		if json.Unmarshal(data, &cache) == nil && time.Since(cache.FetchedAt) < cacheMaxAge {
			return cache.Pricing
		}
	}

	// Fetch from OpenRouter
	pricing, err := fetchOpenRouterPricing()
	if err != nil {
		// Non-fatal: continue with zero pricing
		return nil
	}

	// Persist cache
	cache := pricingCache{FetchedAt: time.Now(), Pricing: pricing}
	if data, err := json.Marshal(cache); err == nil {
		_ = os.MkdirAll(gtDir, 0o700)
		_ = os.WriteFile(cachePath, data, 0o600)
	}

	return pricing
}

// fetchOpenRouterPricing calls the OpenRouter models endpoint and returns
// a map of openrouter_id → [per1Kin, per1Kout] in USD.
func fetchOpenRouterPricing() (map[string][2]float64, error) {
	client := &http.Client{Timeout: fetchTimeout}
	resp, err := client.Get(openRouterURL)
	if err != nil {
		return nil, fmt.Errorf("openrouter fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openrouter returned %d", resp.StatusCode)
	}

	var or openRouterResponse
	if err := json.NewDecoder(resp.Body).Decode(&or); err != nil {
		return nil, fmt.Errorf("openrouter decode: %w", err)
	}

	result := make(map[string][2]float64, len(or.Data))
	for _, m := range or.Data {
		in, _ := strconv.ParseFloat(strings.TrimSpace(m.Pricing.Prompt), 64)
		out, _ := strconv.ParseFloat(strings.TrimSpace(m.Pricing.Completion), 64)
		// OpenRouter pricing is per-token; convert to per-1K
		result[m.ID] = [2]float64{in * 1000, out * 1000}
	}
	return result, nil
}

// modelOverride is the TOML shape for ~/.gt/models.toml entries.
type modelOverride struct {
	Provider      string   `toml:"provider"`
	Name          string   `toml:"name"`
	OpenRouterID  string   `toml:"openrouter_id"`
	MMLUScore     float64  `toml:"mmlu"`
	SWEScore      float64  `toml:"swe"`
	Vision        *bool    `toml:"vision"`
	CodeExecution *bool    `toml:"code_execution"`
	ContextWindow int      `toml:"context_window"`
	CostPer1KIn   *float64 `toml:"cost_per_1k_in"`
	CostPer1KOut  *float64 `toml:"cost_per_1k_out"`
	SubscriptionEligible *bool   `toml:"subscription_eligible"`
	GoodFor       []string `toml:"good_for"`
}

type modelsToml struct {
	Models map[string]modelOverride `toml:"models"`
}

// applyOverrides merges user-provided ~/.gt/models.toml into the database.
// Known models have their fields overridden selectively; unknown models are appended.
func applyOverrides(db []ModelEntry, gtDir string) []ModelEntry {
	path := filepath.Join(gtDir, "models.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return db // file optional
	}

	var cfg modelsToml
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return db // malformed; ignore silently
	}

	// Index existing entries for fast lookup
	index := make(map[string]int, len(db))
	for i := range db {
		index[db[i].ID] = i
	}

	for id, ov := range cfg.Models {
		if i, found := index[id]; found {
			// Override existing fields where the override is non-zero
			e := &db[i]
			if ov.Provider != "" {
				e.Provider = ov.Provider
			}
			if ov.Name != "" {
				e.Name = ov.Name
			}
			if ov.OpenRouterID != "" {
				e.OpenRouterID = ov.OpenRouterID
			}
			if ov.MMLUScore != 0 {
				e.MMLUScore = ov.MMLUScore
			}
			if ov.SWEScore != 0 {
				e.SWEScore = ov.SWEScore
			}
			if ov.Vision != nil {
				e.Vision = *ov.Vision
			}
			if ov.CodeExecution != nil {
				e.CodeExecution = *ov.CodeExecution
			}
			if ov.ContextWindow != 0 {
				e.ContextWindow = ov.ContextWindow
			}
			if ov.CostPer1KIn != nil {
				e.CostPer1KIn = *ov.CostPer1KIn
			}
			if ov.CostPer1KOut != nil {
				e.CostPer1KOut = *ov.CostPer1KOut
			}
			if ov.SubscriptionEligible != nil {
				e.SubscriptionEligible = *ov.SubscriptionEligible
			}
			if len(ov.GoodFor) > 0 {
				e.GoodFor = ov.GoodFor
			}
		} else {
			// New model not in static DB
			entry := ModelEntry{
				ID:           id,
				Provider:     ov.Provider,
				Name:         ov.Name,
				OpenRouterID: ov.OpenRouterID,
				MMLUScore:    ov.MMLUScore,
				SWEScore:     ov.SWEScore,
				ContextWindow: ov.ContextWindow,
				GoodFor:      ov.GoodFor,
			}
			if ov.Vision != nil {
				entry.Vision = *ov.Vision
			}
			if ov.CodeExecution != nil {
				entry.CodeExecution = *ov.CodeExecution
			}
			if ov.CostPer1KIn != nil {
				entry.CostPer1KIn = *ov.CostPer1KIn
			}
			if ov.CostPer1KOut != nil {
				entry.CostPer1KOut = *ov.CostPer1KOut
			}
			if ov.SubscriptionEligible != nil {
				entry.SubscriptionEligible = *ov.SubscriptionEligible
			}
			db = append(db, entry)
			index[id] = len(db) - 1
		}
	}

	return db
}

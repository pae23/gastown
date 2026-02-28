package models

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const usageFileName = "usage.jsonl"

// UsageEntry records a single model invocation.
type UsageEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	ModelID    string    `json:"model_id"`
	Provider   string    `json:"provider"`
	AccessType string    `json:"access_type"` // "subscription" | "api_key"
	TaskType   string    `json:"task_type"`   // free-form label, e.g. "coding", "review"
	TokensIn   int       `json:"tokens_in"`
	TokensOut  int       `json:"tokens_out"`
	CostUSD    float64   `json:"cost_usd"`
	Success    bool      `json:"success"`
	LatencyMs  int       `json:"latency_ms"`
	Reason     string    `json:"reason,omitempty"` // routing reason
}

// RecordUsage appends a usage entry to ~/.gt/usage.jsonl.
// The file is created if it does not exist.
func RecordUsage(gtDir string, entry UsageEntry) error {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal usage entry: %w", err)
	}

	path := filepath.Join(gtDir, usageFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open usage file: %w", err)
	}
	defer f.Close()

	_, err = fmt.Fprintf(f, "%s\n", line)
	return err
}

// LoadUsage reads all usage entries from ~/.gt/usage.jsonl recorded at or after since.
// Pass the zero time to load all entries.
func LoadUsage(gtDir string, since time.Time) ([]UsageEntry, error) {
	path := filepath.Join(gtDir, usageFileName)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open usage file: %w", err)
	}
	defer f.Close()

	var entries []UsageEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e UsageEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip malformed lines
		}
		if !since.IsZero() && e.Timestamp.Before(since) {
			continue
		}
		entries = append(entries, e)
	}
	return entries, scanner.Err()
}

// ModelStats holds aggregated statistics for a single model.
type ModelStats struct {
	ModelID    string
	Provider   string
	Invocations int
	TokensIn   int64
	TokensOut  int64
	TotalCost  float64
	Successes  int
	SubscriptionUses int
}

// SuccessRate returns the fraction of successful invocations (0–1).
func (s ModelStats) SuccessRate() float64 {
	if s.Invocations == 0 {
		return 0
	}
	return float64(s.Successes) / float64(s.Invocations)
}

// MonthlyStats aggregates usage entries by model for a given month.
// Pass the first day of the month as month (time.Month, year).
func MonthlyStats(entries []UsageEntry, year int, month time.Month) map[string]*ModelStats {
	start := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)

	stats := make(map[string]*ModelStats)
	for _, e := range entries {
		if e.Timestamp.Before(start) || !e.Timestamp.Before(end) {
			continue
		}
		s, ok := stats[e.ModelID]
		if !ok {
			s = &ModelStats{ModelID: e.ModelID, Provider: e.Provider}
			stats[e.ModelID] = s
		}
		s.Invocations++
		s.TokensIn += int64(e.TokensIn)
		s.TokensOut += int64(e.TokensOut)
		s.TotalCost += e.CostUSD
		if e.Success {
			s.Successes++
		}
		if e.AccessType == "subscription" {
			s.SubscriptionUses++
		}
	}
	return stats
}

// TotalCost returns the sum of all USD costs in the given entries.
func TotalCost(entries []UsageEntry) float64 {
	var total float64
	for _, e := range entries {
		total += e.CostUSD
	}
	return total
}

// EstimateCost computes the estimated cost for a model invocation.
// tokensIn and tokensOut are token counts; model must be in the database.
func EstimateCost(entry *ModelEntry, tokensIn, tokensOut int) float64 {
	if entry == nil {
		return 0
	}
	return entry.CostPer1KIn*float64(tokensIn)/1000 +
		entry.CostPer1KOut*float64(tokensOut)/1000
}

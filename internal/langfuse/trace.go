package langfuse

import (
	"fmt"
	"time"
)

// TraceBody is the Langfuse trace creation payload.
type TraceBody struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Input    interface{}       `json:"input,omitempty"`
	Output   interface{}       `json:"output,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Tags     []string          `json:"tags,omitempty"`
	UserID   string            `json:"userId,omitempty"`
	Release  string            `json:"release,omitempty"`
}

// GenerationBody is the Langfuse generation (LLM call) payload.
type GenerationBody struct {
	ID                string            `json:"id"`
	TraceID           string            `json:"traceId"`
	Name              string            `json:"name"`
	Model             string            `json:"model,omitempty"`
	Input             interface{}       `json:"input,omitempty"`
	Output            interface{}       `json:"output,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	StartTime         string            `json:"startTime,omitempty"`
	EndTime           string            `json:"endTime,omitempty"`
	CompletionStartMs float64           `json:"completionStartTime,omitempty"`
	Usage             *UsageBody        `json:"usage,omitempty"`
}

// UsageBody tracks token usage for a generation.
type UsageBody struct {
	Input  int `json:"input,omitempty"`
	Output int `json:"output,omitempty"`
	Total  int `json:"total,omitempty"`
}

// ScoreBody is the Langfuse score creation payload.
type ScoreBody struct {
	ID            string  `json:"id"`
	TraceID       string  `json:"traceId"`
	ObservationID string  `json:"observationId,omitempty"`
	Name          string  `json:"name"`
	Value         float64 `json:"value"`
	DataType      string  `json:"dataType,omitempty"` // "NUMERIC", "CATEGORICAL", "BOOLEAN"
	Comment       string  `json:"comment,omitempty"`
}

// ---------------------------------------------------------------------------
// Public API — called by routing and refinery
// ---------------------------------------------------------------------------

// TraceRouting creates a Langfuse trace for a smart routing decision.
// Called by gt sling after the ModelRouter chain returns a decision.
func TraceRouting(beadID, taskType string, priority, attemptCount int, agent, reason string, confidence float64) string {
	c := Global()
	if c == nil {
		return ""
	}
	traceID := fmt.Sprintf("routing-%s-%d", beadID, time.Now().UnixNano())
	c.enqueue(kindTrace, &TraceBody{
		ID:   traceID,
		Name: "smart-routing",
		Input: map[string]interface{}{
			"bead_id":       beadID,
			"task_type":     taskType,
			"priority":      priority,
			"attempt_count": attemptCount,
		},
		Output: map[string]interface{}{
			"agent":      agent,
			"reason":     reason,
			"confidence": confidence,
		},
		Metadata: map[string]string{
			"bead_id":   beadID,
			"task_type": taskType,
		},
		Tags: []string{"routing", reason, taskType},
	})
	return traceID
}

// TraceClassification creates a Langfuse trace + generation for an LLM
// classification call (ClassifyRouter). This captures the prompt, response,
// and token usage for evaluation and dataset building.
func TraceClassification(beadID, model, prompt, response string, inputTokens, outputTokens int, durationMs float64, tier string, confidence float64) string {
	c := Global()
	if c == nil {
		return ""
	}
	traceID := fmt.Sprintf("classify-%s-%d", beadID, time.Now().UnixNano())
	genID := fmt.Sprintf("gen-%s-%d", beadID, time.Now().UnixNano())

	now := time.Now().UTC()
	start := now.Add(-time.Duration(durationMs) * time.Millisecond)

	c.enqueue(kindTrace, &TraceBody{
		ID:   traceID,
		Name: "classify-complexity",
		Input: map[string]interface{}{
			"bead_id": beadID,
			"prompt":  prompt,
		},
		Output: map[string]interface{}{
			"tier":       tier,
			"confidence": confidence,
			"response":   response,
		},
		Tags: []string{"classify", tier},
	})

	c.enqueue(kindGeneration, &GenerationBody{
		ID:        genID,
		TraceID:   traceID,
		Name:      "complexity-classifier",
		Model:     model,
		Input:     prompt,
		Output:    response,
		StartTime: start.Format(time.RFC3339Nano),
		EndTime:   now.Format(time.RFC3339Nano),
		Usage: &UsageBody{
			Input:  inputTokens,
			Output: outputTokens,
			Total:  inputTokens + outputTokens,
		},
		Metadata: map[string]string{
			"bead_id": beadID,
			"tier":    tier,
		},
	})

	return traceID
}

// ScoreMergeOutcome attaches a score to a routing trace based on the
// refinery's merge outcome. This closes the feedback loop:
// routing decision → polecat work → refinery verdict → score.
//
// Scoring scheme:
//
//	merged                → 1.0 (correct routing)
//	rejected + escalated  → 0.5 (wrong tier but recoverable)
//	rejected + max tier   → 0.0 (hard failure)
//	conflict              → 0.3 (not a model quality issue)
func ScoreMergeOutcome(beadID, outcome, agent string, attemptCount int) {
	c := Global()
	if c == nil {
		return
	}

	// Find the routing trace for this bead. Convention: trace ID starts with "routing-{beadID}".
	// Since we can't query Langfuse for the exact trace, we reconstruct the pattern.
	// In practice, the trace ID would be stored on the bead or passed through the pipeline.
	// For now, create a score-only trace that Langfuse will correlate by bead_id tag.
	traceID := fmt.Sprintf("outcome-%s-%d", beadID, time.Now().UnixNano())

	var score float64
	var comment string
	switch outcome {
	case "merged":
		score = 1.0
		comment = fmt.Sprintf("merged on attempt %d with %s", attemptCount, agent)
	case "rejected":
		if attemptCount <= 1 {
			score = 0.5
			comment = fmt.Sprintf("rejected on attempt %d, escalation available", attemptCount)
		} else {
			score = 0.0
			comment = fmt.Sprintf("rejected on attempt %d at top tier", attemptCount)
		}
	case "conflict":
		score = 0.3
		comment = "merge conflict (not model quality)"
	default:
		score = 0.2
		comment = fmt.Sprintf("outcome: %s", outcome)
	}

	// Create a trace for the outcome (so the score has context).
	c.enqueue(kindTrace, &TraceBody{
		ID:   traceID,
		Name: "merge-outcome",
		Input: map[string]interface{}{
			"bead_id":       beadID,
			"agent":         agent,
			"attempt_count": attemptCount,
		},
		Output: map[string]interface{}{
			"outcome": outcome,
			"score":   score,
		},
		Tags:   []string{"outcome", outcome, agent},
		UserID: agent,
	})

	c.enqueue(kindScore, &ScoreBody{
		ID:       fmt.Sprintf("score-%s-%d", beadID, time.Now().UnixNano()),
		TraceID:  traceID,
		Name:     "routing-quality",
		Value:    score,
		DataType: "NUMERIC",
		Comment:  comment,
	})

	// Also score cost-efficiency: cheaper model + success = better score.
	c.enqueue(kindScore, &ScoreBody{
		ID:       fmt.Sprintf("cost-%s-%d", beadID, time.Now().UnixNano()),
		TraceID:  traceID,
		Name:     "cost-efficiency",
		Value:    costEfficiencyScore(outcome, attemptCount),
		DataType: "NUMERIC",
		Comment:  fmt.Sprintf("attempts=%d outcome=%s", attemptCount, outcome),
	})
}

// costEfficiencyScore penalizes retries: each attempt costs money.
// First-attempt merge = 1.0, two attempts = 0.5, three = 0.33, etc.
func costEfficiencyScore(outcome string, attemptCount int) float64 {
	if outcome != "merged" {
		return 0.0
	}
	if attemptCount <= 0 {
		attemptCount = 1
	}
	return 1.0 / float64(attemptCount)
}

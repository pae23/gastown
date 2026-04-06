package langfuse

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClient_Inactive(t *testing.T) {
	// No env vars → Global() returns nil.
	if IsActive() {
		t.Skip("GT_LANGFUSE_PUBLIC_KEY is set in env, skipping inactive test")
	}

	// All public functions should be safe no-ops on nil client.
	TraceRouting("bd-123", "bug", 1, 0, "claude-haiku", "static", 0.9)
	ScoreMergeOutcome("bd-123", "merged", "claude-haiku", 1)
	traceID := TraceClassification("bd-123", "haiku", "prompt", "response", 100, 50, 200, "cheap", 0.8)
	if traceID != "" {
		t.Errorf("expected empty traceID when inactive, got %q", traceID)
	}
}

func TestClient_SendsBatch(t *testing.T) {
	var received atomic.Int32
	var lastBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/public/ingestion" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// Verify basic auth.
		user, pass, ok := r.BasicAuth()
		if !ok || user != "pk-test" || pass != "sk-test" {
			t.Errorf("bad auth: user=%q pass=%q ok=%v", user, pass, ok)
		}
		body, _ := io.ReadAll(r.Body)
		lastBody = body
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newClient(srv.URL, "pk-test", "sk-test")

	// Enqueue a trace and a score.
	c.enqueue(kindTrace, &TraceBody{
		ID:   "test-trace-1",
		Name: "test",
		Tags: []string{"test"},
	})
	c.enqueue(kindScore, &ScoreBody{
		ID:      "test-score-1",
		TraceID: "test-trace-1",
		Name:    "quality",
		Value:   0.95,
	})

	// Wait for flush (2s ticker + margin).
	time.Sleep(3 * time.Second)
	c.Shutdown()

	if received.Load() == 0 {
		t.Fatal("expected at least one batch sent")
	}

	// Verify the batch structure.
	var batch ingestionBatch
	if err := json.Unmarshal(lastBody, &batch); err != nil {
		t.Fatalf("unmarshal batch: %v", err)
	}
	if len(batch.Batch) == 0 {
		t.Error("expected non-empty batch")
	}

	// Check event types.
	types := map[string]bool{}
	for _, ev := range batch.Batch {
		types[ev.Type] = true
	}
	if !types["trace-create"] {
		t.Error("expected trace-create in batch")
	}
	if !types["score-create"] {
		t.Error("expected score-create in batch")
	}
}

func TestClient_QueueFull(t *testing.T) {
	// Create client with tiny queue.
	c := &Client{
		baseURL:    "http://localhost:0",
		publicKey:  "pk",
		secretKey:  "sk",
		httpClient: &http.Client{Timeout: 1 * time.Second},
		queue:      make(chan event, 1),
	}
	c.wg.Add(1)
	go c.flushLoop()

	// Fill the queue.
	c.enqueue(kindTrace, &TraceBody{ID: "fill"})
	// This should not block (drops silently).
	c.enqueue(kindTrace, &TraceBody{ID: "overflow"})

	c.Shutdown()
	// No panic = pass.
}

func TestCostEfficiencyScore(t *testing.T) {
	tests := []struct {
		outcome  string
		attempts int
		want     float64
	}{
		{"merged", 1, 1.0},
		{"merged", 2, 0.5},
		{"merged", 3, 1.0 / 3.0},
		{"rejected", 1, 0.0},
		{"conflict", 2, 0.0},
	}
	for _, tc := range tests {
		got := costEfficiencyScore(tc.outcome, tc.attempts)
		if got != tc.want {
			t.Errorf("costEfficiency(%q, %d) = %f, want %f", tc.outcome, tc.attempts, got, tc.want)
		}
	}
}

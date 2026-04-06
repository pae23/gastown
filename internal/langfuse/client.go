// Package langfuse provides an optional, best-effort Langfuse integration
// for the smart model routing system. It traces routing decisions, logs
// merge outcomes as scores, and builds datasets for classifier training.
//
// Activation: set GT_LANGFUSE_PUBLIC_KEY and GT_LANGFUSE_SECRET_KEY.
// Both unset = completely disabled (no HTTP calls, no goroutines).
//
// This is a thin HTTP client against the Langfuse REST API — no external
// SDK dependency. All calls are fire-and-forget with a background flush
// queue, matching the OTel best-effort pattern.
package langfuse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

// Default Langfuse API endpoint (self-hosted or cloud).
const defaultBaseURL = "https://cloud.langfuse.com"

// Client is a best-effort Langfuse API client.
// Safe for concurrent use. All methods are no-ops when not active.
type Client struct {
	baseURL    string
	publicKey  string
	secretKey  string
	httpClient *http.Client

	// Background flush queue.
	queue chan event
	wg    sync.WaitGroup
}

var (
	globalOnce   sync.Once
	globalClient *Client
)

// Global returns the singleton client, initializing from env on first call.
// Returns nil when Langfuse is not configured (no env vars).
func Global() *Client {
	globalOnce.Do(func() {
		pub := os.Getenv("GT_LANGFUSE_PUBLIC_KEY")
		sec := os.Getenv("GT_LANGFUSE_SECRET_KEY")
		if pub == "" || sec == "" {
			return // not configured
		}
		base := os.Getenv("GT_LANGFUSE_URL")
		if base == "" {
			base = defaultBaseURL
		}
		globalClient = newClient(base, pub, sec)
	})
	return globalClient
}

// IsActive returns true when Langfuse is configured and the client is running.
func IsActive() bool { return Global() != nil }

func newClient(baseURL, publicKey, secretKey string) *Client {
	c := &Client{
		baseURL:   baseURL,
		publicKey: publicKey,
		secretKey: secretKey,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		queue: make(chan event, 256),
	}
	c.wg.Add(1)
	go c.flushLoop()
	return c
}

// Shutdown drains the queue and stops the background goroutine.
// Safe to call multiple times or on a nil client.
func (c *Client) Shutdown() {
	if c == nil {
		return
	}
	close(c.queue)
	c.wg.Wait()
}

// ---------------------------------------------------------------------------
// Internal: event queue + flush loop
// ---------------------------------------------------------------------------

type eventKind int

const (
	kindTrace eventKind = iota
	kindGeneration
	kindScore
)

type event struct {
	kind    eventKind
	payload interface{}
}

func (c *Client) enqueue(kind eventKind, payload interface{}) {
	if c == nil {
		return
	}
	select {
	case c.queue <- event{kind: kind, payload: payload}:
	default:
		// Queue full — drop (best-effort).
	}
}

func (c *Client) flushLoop() {
	defer c.wg.Done()

	batch := make([]ingestionEvent, 0, 32)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case ev, ok := <-c.queue:
			if !ok {
				// Channel closed — flush remaining and exit.
				if len(batch) > 0 {
					c.sendBatch(batch)
				}
				return
			}
			batch = append(batch, toIngestionEvent(ev))
			if len(batch) >= 32 {
				c.sendBatch(batch)
				batch = batch[:0]
			}

		case <-ticker.C:
			if len(batch) > 0 {
				c.sendBatch(batch)
				batch = batch[:0]
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Langfuse ingestion API types
// ---------------------------------------------------------------------------

// ingestionEvent is a single event in the batch ingestion API.
type ingestionEvent struct {
	ID        string      `json:"id"`
	Type      string      `json:"type"`
	Timestamp string      `json:"timestamp"`
	Body      interface{} `json:"body"`
}

type ingestionBatch struct {
	Batch    []ingestionEvent `json:"batch"`
	Metadata interface{}     `json:"metadata,omitempty"`
}

func toIngestionEvent(ev event) ingestionEvent {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	ie := ingestionEvent{
		Timestamp: now,
		Body:      ev.payload,
	}
	switch ev.kind {
	case kindTrace:
		ie.Type = "trace-create"
		if t, ok := ev.payload.(*TraceBody); ok {
			ie.ID = t.ID
		}
	case kindGeneration:
		ie.Type = "generation-create"
		if g, ok := ev.payload.(*GenerationBody); ok {
			ie.ID = g.ID
		}
	case kindScore:
		ie.Type = "score-create"
		if s, ok := ev.payload.(*ScoreBody); ok {
			ie.ID = s.ID
		}
	}
	if ie.ID == "" {
		ie.ID = fmt.Sprintf("gt-%d", time.Now().UnixNano())
	}
	return ie
}

func (c *Client) sendBatch(batch []ingestionEvent) {
	body := ingestionBatch{Batch: batch}
	data, err := json.Marshal(body)
	if err != nil {
		return // best-effort
	}

	req, err := http.NewRequest("POST", c.baseURL+"/api/public/ingestion", bytes.NewReader(data))
	if err != nil {
		return
	}
	req.SetBasicAuth(c.publicKey, c.secretKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return // best-effort
	}
	resp.Body.Close()
}

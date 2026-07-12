package daemon

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	beadsdk "github.com/steveyegge/beads"
)

// fakeEventStore serves a fixed event list and no dependencies. Only the methods
// the convoy event poll actually touches are implemented; the embedded interface
// is nil, so any other call panics loudly rather than passing silently.
type fakeEventStore struct {
	beadsdk.Storage
	events []*beadsdk.Event
}

func (f *fakeEventStore) GetAllEventsSince(_ context.Context, since time.Time) ([]*beadsdk.Event, error) {
	var out []*beadsdk.Event
	for _, e := range f.events {
		if !e.CreatedAt.Before(since) {
			out = append(out, e)
		}
	}
	return out, nil
}

// No convoy tracks anything in these tests — the poll stops after logging the
// close it detected, which is what the assertions look at.
func (f *fakeEventStore) GetDependentsWithMetadata(_ context.Context, _ string) ([]*beadsdk.IssueWithDependencyMetadata, error) {
	return nil, nil
}

func closedEvent(id, issueID string, at time.Time) *beadsdk.Event {
	return &beadsdk.Event{
		ID:        id,
		IssueID:   issueID,
		EventType: beadsdk.EventClosed,
		CreatedAt: at,
	}
}

// A store that first appears after the daemon has been polling — Dolt still
// opening at boot, a rig unparked mid-run, a rig docked later — has no
// high-water mark, so its query reaches back to the epoch. Its pre-existing
// events are history, not news: a warm-up poll must absorb them.
//
// With a single global seeded flag, only the very first cycle was a warm-up, so
// any store that showed up later had its entire event history processed as if it
// were new. That is how long-dead synthetic events kept re-firing across daemon
// restarts (gt-ousq).
func TestEventPoll_LateArrivingStoreDoesNotReplayHistory(t *testing.T) {
	old := time.Now().UTC().Add(-72 * time.Hour)

	hq := &fakeEventStore{}
	rig := &fakeEventStore{
		events: []*beadsdk.Event{closedEvent("ev-stale", "gt-stale1", old)},
	}

	var logged []string
	logger := func(format string, args ...interface{}) {
		logged = append(logged, fmt.Sprintf(format, args...))
	}
	loggedContains := func(substr string) bool {
		for _, s := range logged {
			if strings.Contains(s, substr) {
				return true
			}
		}
		return false
	}

	m := NewConvoyManager(t.TempDir(), logger, "gt", 10*time.Minute,
		map[string]beadsdk.Storage{"hq": hq}, nil, nil)

	// Cycle 1: only hq is open — the rig's store isn't available yet.
	m.pollStoresSnapshot(map[string]beadsdk.Storage{"hq": hq})

	// Cycle 2: the rig store appears, carrying history.
	stores := map[string]beadsdk.Storage{"hq": hq, "gastown": rig}
	m.pollStoresSnapshot(stores)

	if loggedContains("gt-stale1") {
		t.Errorf("late-arriving store replayed its history: %v", logged)
	}

	// Cycle 3: a genuinely new close in that store is still processed.
	rig.events = append(rig.events, closedEvent("ev-fresh", "gt-fresh1", time.Now().UTC()))
	m.pollStoresSnapshot(stores)

	if !loggedContains("gt-fresh1") {
		t.Errorf("expected close detection for gt-fresh1 after warm-up, got: %v", logged)
	}
}

// The warm-up is per-store, so a store present from the first cycle keeps the
// original behaviour: its history is absorbed, later closes are processed.
func TestEventPoll_FirstCycleStoreIsStillWarmedUp(t *testing.T) {
	old := time.Now().UTC().Add(-72 * time.Hour)

	hq := &fakeEventStore{
		events: []*beadsdk.Event{closedEvent("ev-stale", "gt-stale1", old)},
	}

	var logged []string
	logger := func(format string, args ...interface{}) {
		logged = append(logged, fmt.Sprintf(format, args...))
	}

	m := NewConvoyManager(t.TempDir(), logger, "gt", 10*time.Minute,
		map[string]beadsdk.Storage{"hq": hq}, nil, nil)

	stores := map[string]beadsdk.Storage{"hq": hq}
	m.pollStoresSnapshot(stores)

	for _, s := range logged {
		if strings.Contains(s, "gt-stale1") {
			t.Fatalf("first-cycle store replayed history: %v", logged)
		}
	}

	hq.events = append(hq.events, closedEvent("ev-fresh", "gt-fresh1", time.Now().UTC()))
	m.pollStoresSnapshot(stores)

	found := false
	for _, s := range logged {
		if strings.Contains(s, "gt-fresh1") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected close detection for gt-fresh1 after warm-up, got: %v", logged)
	}
}

package server

import (
	"context"
	"fmt"

	"github.com/Pcapchu/Pcapchu/internal/storage"
)

// bufferedEvent holds an SSE event in memory until flush.
type bufferedEvent struct {
	seq       int
	eventType string
	data      string
}

// txCollector accumulates events and round data in memory during an SSE
// stream. Nothing is persisted until flush() is called, ensuring the DB
// never contains partial transaction state.
type txCollector struct {
	sessionID string
	round     int
	events    []bufferedEvent
	rounds    []storage.Round
}

func newTxCollector(sessionID string, round int) *txCollector {
	return &txCollector{sessionID: sessionID, round: round}
}

// bufferEvent records an SSE event for later persistence.
func (c *txCollector) bufferEvent(seq int, eventType, data string) {
	c.events = append(c.events, bufferedEvent{
		seq:       seq,
		eventType: eventType,
		data:      data,
	})
}

// collectRound is an investigation.OnRoundDone callback that buffers round data.
func (c *txCollector) collectRound(_ context.Context, r storage.Round) error {
	c.rounds = append(c.rounds, r)
	return nil
}

// flush persists all accumulated data to the database:
//  1. events → session_events (tagged with round)
//  2. rounds → rounds table
//  3. query → round_queries table
//  4. session title (first round only) + status
func (c *txCollector) flush(ctx context.Context, store *storage.Store, query, status string) error {
	// 1. Persist events.
	if len(c.events) > 0 {
		evts := make([]storage.SessionEvent, len(c.events))
		for i, e := range c.events {
			evts[i] = storage.SessionEvent{
				Seq:       e.seq,
				Round:     c.round,
				EventType: e.eventType,
				Data:      e.data,
			}
		}
		if err := store.SaveEvents(ctx, c.sessionID, evts); err != nil {
			return fmt.Errorf("save events: %w", err)
		}
	}

	// 2. Persist round data.
	for _, r := range c.rounds {
		if err := store.SaveRound(ctx, c.sessionID, r); err != nil {
			return fmt.Errorf("save round %d: %w", r.Round, err)
		}
	}

	// 3. Persist round query.
	if query != "" {
		if err := store.SaveRoundQuery(ctx, c.sessionID, c.round, query); err != nil {
			return fmt.Errorf("save round query: %w", err)
		}
	}

	// 4. Update session title (use first-ever query) + status.
	if c.round == 1 && query != "" {
		_ = store.UpdateSessionTitle(ctx, c.sessionID, query)
	}

	if err := store.UpdateSessionStatus(ctx, c.sessionID, status); err != nil {
		return fmt.Errorf("update session status: %w", err)
	}

	return nil
}

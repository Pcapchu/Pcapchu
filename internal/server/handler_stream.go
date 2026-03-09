package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// handleStream serves an SSE stream for a session.
//
// Behavior:
//  1. Subscribe to live events first (to avoid gaps).
//  2. Load stored events from DB.
//  3. Send stored events (respecting Last-Event-ID for reconnect).
//  4. Stream live events, deduplicating by seq.
//  5. When the investigation completes (live channel closed) or client disconnects, stop.
//
// If the session is not actively running, only stored events are sent
// followed by a "done" event.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	sse := newSSEWriter(w)
	if sse == nil {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Parse Last-Event-ID for reconnection.
	lastEventID := 0
	if idStr := r.Header.Get("Last-Event-ID"); idStr != "" {
		if n, err := strconv.Atoi(idStr); err == nil {
			lastEventID = n
		}
	}

	// Step 1: subscribe to live channel (if session is active).
	ar := s.runner.GetActive(sessionID)
	var liveCh chan numberedEvent
	if ar != nil {
		liveCh = ar.addClient()
		defer ar.removeClient(liveCh)
	}

	// Step 2: load stored events from DB.
	pastEvents, err := s.store.LoadSessionEventsSince(r.Context(), sessionID, lastEventID)
	if err != nil {
		sse.writeEvent(0, "error", `{"message":"load events failed"}`)
		return
	}

	// Step 3: send stored events.
	maxSentSeq := lastEventID
	for _, ev := range pastEvents {
		sse.writeEvent(ev.Seq, ev.EventType, ev.Data)
		if ev.Seq > maxSentSeq {
			maxSentSeq = ev.Seq
		}
	}

	// Step 4: if not actively running, send "done" and return.
	if liveCh == nil {
		sse.writeEvent(0, "done", "{}")
		return
	}

	// Step 5: stream live events.
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepAlive.C:
			sse.writeComment("keepalive")
		case nev, ok := <-liveCh:
			if !ok {
				// Investigation finished.
				sse.writeEvent(0, "done", "{}")
				return
			}
			if nev.Seq <= maxSentSeq {
				continue // already sent from DB replay
			}
			data := "{}"
			if nev.Event.Data != nil {
				data = string(nev.Event.Data)
			}
			sse.writeEvent(nev.Seq, nev.Event.Type, data)
			maxSentSeq = nev.Seq
		}
	}
}

// handleEvents returns all stored events for a session as a JSON array.
// This is the non-streaming alternative for frontends that do not use SSE.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	evts, err := s.store.LoadSessionEvents(r.Context(), sessionID)
	if err != nil {
		http.Error(w, "load events: "+err.Error(), http.StatusInternalServerError)
		return
	}

	type eventJSON struct {
		Seq       int             `json:"seq"`
		Type      string          `json:"type"`
		Data      json.RawMessage `json:"data"`
		Timestamp string          `json:"timestamp"`
	}

	result := make([]eventJSON, 0, len(evts))
	for _, ev := range evts {
		result = append(result, eventJSON{
			Seq:       ev.Seq,
			Type:      ev.EventType,
			Data:      json.RawMessage(ev.Data),
			Timestamp: ev.CreatedAt.Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"events":     result,
	})
}

package server

import (
	"encoding/json"
	"net/http"
	"time"
)

// handleEvents returns all stored events for a session as a JSON array.
// This is the history endpoint — use POST /api/analyze for live SSE streaming.
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

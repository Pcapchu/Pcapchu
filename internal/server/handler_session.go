package server

import (
	"encoding/json"
	"net/http"
	"time"
)

// handleListSessions returns all sessions with round count and status.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListSessions(r.Context())
	if err != nil {
		http.Error(w, "list sessions: "+err.Error(), http.StatusInternalServerError)
		return
	}

	type sessionJSON struct {
		ID           string `json:"id"`
		SessionTitle string `json:"session_title"`
		RoundCount   int    `json:"round_count"`
		Status       string `json:"status"`
		PcapSource   string `json:"pcap_source"`
		CreatedAt    string `json:"created_at"`
		UpdatedAt    string `json:"updated_at"`
	}

	result := make([]sessionJSON, 0, len(items))
	for _, item := range items {
		status := item.Status
		if status == "" {
			status = "idle"
		}
		result = append(result, sessionJSON{
			ID:           item.ID,
			SessionTitle: item.SessionTitle,
			RoundCount:   item.RoundCount,
			Status:       status,
			PcapSource:   item.PcapSource(),
			CreatedAt:    item.CreatedAt.Format(time.RFC3339),
			UpdatedAt:    item.UpdatedAt.Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"sessions": result,
	})
}

// handleGetSession returns a single session with its history organized by round.
// Each round contains the user query for that round and all events that occurred
// during that round, assembled from session_events + round_queries tables.
func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	sess, err := s.store.GetSession(r.Context(), sessionID)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	evts, err := s.store.LoadSessionEvents(r.Context(), sessionID)
	if err != nil {
		http.Error(w, "load events: "+err.Error(), http.StatusInternalServerError)
		return
	}

	queries, err := s.store.LoadRoundQueries(r.Context(), sessionID)
	if err != nil {
		http.Error(w, "load round queries: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Build query lookup: round → user_query.
	queryMap := make(map[int]string, len(queries))
	for _, q := range queries {
		queryMap[q.Round] = q.UserQuery
	}

	// Group events by round.
	type eventJSON struct {
		Seq       int             `json:"seq"`
		Type      string          `json:"type"`
		Data      json.RawMessage `json:"data"`
		Timestamp string          `json:"timestamp"`
	}
	type roundJSON struct {
		Round     int         `json:"round"`
		UserQuery string      `json:"user_query"`
		Events    []eventJSON `json:"events"`
	}

	roundMap := make(map[int]*roundJSON)
	var roundOrder []int

	for _, ev := range evts {
		rj, ok := roundMap[ev.Round]
		if !ok {
			rj = &roundJSON{
				Round:     ev.Round,
				UserQuery: queryMap[ev.Round],
			}
			roundMap[ev.Round] = rj
			roundOrder = append(roundOrder, ev.Round)
		}
		rj.Events = append(rj.Events, eventJSON{
			Seq:       ev.Seq,
			Type:      ev.EventType,
			Data:      json.RawMessage(ev.Data),
			Timestamp: ev.CreatedAt.Format(time.RFC3339),
		})
	}

	rounds := make([]roundJSON, 0, len(roundOrder))
	for _, rn := range roundOrder {
		rounds = append(rounds, *roundMap[rn])
	}

	status := sess.Status
	if status == "" {
		status = "idle"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":            sess.ID,
		"session_title": sess.SessionTitle,
		"status":        status,
		"rounds":        rounds,
		"round_count":   len(rounds),
		"created_at":    sess.CreatedAt.Format(time.RFC3339),
		"updated_at":    sess.UpdatedAt.Format(time.RFC3339),
	})
}

// handleDeleteSession deletes a session and all its data.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	if err := s.store.DeleteSession(r.Context(), sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

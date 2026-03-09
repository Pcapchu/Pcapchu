package server

import (
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
		ID         string `json:"id"`
		UserQuery  string `json:"user_query"`
		RoundCount int    `json:"round_count"`
		Status     string `json:"status"`
		PcapSource string `json:"pcap_source"`
		CreatedAt  string `json:"created_at"`
		UpdatedAt  string `json:"updated_at"`
	}

	result := make([]sessionJSON, 0, len(items))
	for _, item := range items {
		status := item.Status
		if status == "" {
			status = "idle"
		}
		// If the runner thinks it's active, override the DB status.
		if s.runner.IsRunning(item.ID) {
			status = "running"
		}
		result = append(result, sessionJSON{
			ID:         item.ID,
			UserQuery:  item.UserQuery,
			RoundCount: item.RoundCount,
			Status:     status,
			PcapSource: item.PcapSource(),
			CreatedAt:  item.CreatedAt.Format(time.RFC3339),
			UpdatedAt:  item.UpdatedAt.Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"sessions": result,
	})
}

// handleGetSession returns a single session with its rounds.
func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	sess, err := s.store.GetSession(r.Context(), sessionID)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	rounds, err := s.store.LoadRounds(r.Context(), sessionID)
	if err != nil {
		http.Error(w, "load rounds: "+err.Error(), http.StatusInternalServerError)
		return
	}

	type roundJSON struct {
		Round          int    `json:"round"`
		Summary        string `json:"summary"`
		KeyFindings    string `json:"key_findings"`
		OpenQuestions   string `json:"open_questions"`
		MarkdownReport string `json:"markdown_report,omitempty"`
		CreatedAt      string `json:"created_at"`
	}

	roundList := make([]roundJSON, 0, len(rounds))
	for _, rd := range rounds {
		roundList = append(roundList, roundJSON{
			Round:          rd.Round,
			Summary:        rd.Summary,
			KeyFindings:    rd.KeyFindings,
			OpenQuestions:   rd.OpenQuestions,
			MarkdownReport: rd.MarkdownReport,
			CreatedAt:      rd.CreatedAt.Format(time.RFC3339),
		})
	}

	status := sess.Status
	if status == "" {
		status = "idle"
	}
	if s.runner.IsRunning(sessionID) {
		status = "running"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":          sess.ID,
		"user_query":  sess.UserQuery,
		"status":      status,
		"rounds":      roundList,
		"round_count": len(rounds),
		"created_at":  sess.CreatedAt.Format(time.RFC3339),
		"updated_at":  sess.UpdatedAt.Format(time.RFC3339),
	})
}

// handleDeleteSession deletes a session and all its data.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	// Cancel if currently running.
	s.runner.Cancel(sessionID)

	if err := s.store.DeleteSession(r.Context(), sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

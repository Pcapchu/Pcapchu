package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/Pcapchu/Pcapchu/internal/events"
	"github.com/Pcapchu/Pcapchu/internal/storage"

	"github.com/google/uuid"
)

// analyzeRequest is the JSON body for POST /api/analyze when not using multipart.
type analyzeRequest struct {
	PcapID   int64  `json:"pcap_id,omitempty"`
	Query    string `json:"query"`
	Rounds   int    `json:"rounds"`
	StorePcap bool  `json:"store_pcap,omitempty"`
}

// continueRequest is the JSON body for POST /api/sessions/{id}/continue.
type continueRequest struct {
	Query  string `json:"query"`
	Rounds int    `json:"rounds"`
}

// handleAnalyze starts a new analysis session.
//
// Accepts multipart/form-data with fields:
//
//	pcap (file)       — pcap file upload
//	pcap_id (string)  — OR reference a stored pcap by ID
//	query (string)    — analysis query
//	rounds (string)   — number of rounds (default "1")
//	store_pcap (string) — "true" to persist pcap in DB
//
// Returns 201 with {"session_id": "...", "status": "running"}.
func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	query := "Analyze this pcap file and identify any security concerns."
	rounds := 1
	var pcapID int64
	var pcapData []byte
	var pcapFilename string

	ct := r.Header.Get("Content-Type")
	if len(ct) >= 19 && ct[:19] == "multipart/form-data" {
		// Limit upload to 500 MB.
		if err := r.ParseMultipartForm(500 << 20); err != nil {
			http.Error(w, "parse multipart: "+err.Error(), http.StatusBadRequest)
			return
		}

		if q := r.FormValue("query"); q != "" {
			query = q
		}
		if n, err := strconv.Atoi(r.FormValue("rounds")); err == nil && n > 0 {
			rounds = n
		}
		if pidStr := r.FormValue("pcap_id"); pidStr != "" {
			if pid, err := strconv.ParseInt(pidStr, 10, 64); err == nil {
				pcapID = pid
			}
		}

		file, header, err := r.FormFile("pcap")
		if err == nil {
			defer file.Close()
			data, err := io.ReadAll(file)
			if err != nil {
				http.Error(w, "read pcap upload: "+err.Error(), http.StatusInternalServerError)
				return
			}
			pcapData = data
			pcapFilename = header.Filename
		}
	} else {
		// JSON body
		var req analyzeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Query != "" {
			query = req.Query
		}
		if req.Rounds > 0 {
			rounds = req.Rounds
		}
		pcapID = req.PcapID
	}

	if pcapData == nil && pcapID == 0 {
		http.Error(w, "either upload a pcap file or provide pcap_id", http.StatusBadRequest)
		return
	}

	sessionID := uuid.New().String()
	var sess storage.Session
	sess.ID = sessionID
	sess.UserQuery = query

	if pcapData != nil {
		// Always store uploaded pcap in DB so the runner can read it
		// without relying on temporary files.
		if pcapFilename == "" {
			pcapFilename = "upload.pcap"
		}
		pid, err := s.store.InsertPcapFile(ctx, pcapFilename, pcapData)
		if err != nil {
			http.Error(w, "store pcap: "+err.Error(), http.StatusInternalServerError)
			return
		}
		sess.PcapFileID = storage.NullInt64(pid)
	} else if pcapID > 0 {
		sess.PcapFileID = storage.NullInt64(pcapID)
	}

	if err := s.store.CreateSession(ctx, sess); err != nil {
		http.Error(w, "create session: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Emit session created event through the runner once it starts,
	// but also store it immediately so SSE replay can pick it up.
	ev := events.NewEvent(events.TypeSessionCreated, sessionID, events.SessionCreatedData{
		SessionID:  sessionID,
		UserQuery:  query,
		PcapSource: pcapSourceLabel(sess),
	})
	_ = s.store.SaveEvent(ctx, sessionID, 0, ev.Type, string(ev.Data))

	// Use Background context — the investigation must outlive this HTTP request.
	if err := s.runner.Start(context.Background(), sessionID, query, "", 1, rounds); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"session_id": sessionID,
		"status":     "running",
	})
}

// handleContinue adds more investigation rounds to an existing session.
func (s *Server) handleContinue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := r.PathValue("id")

	if s.runner.IsRunning(sessionID) {
		http.Error(w, "session is already running", http.StatusConflict)
		return
	}

	var req continueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Rounds <= 0 {
		req.Rounds = 1
	}

	sess, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	query := sess.UserQuery
	if req.Query != "" {
		query = req.Query
	}

	prevRounds, err := s.store.RoundCount(ctx, sessionID)
	if err != nil {
		http.Error(w, "count rounds: "+err.Error(), http.StatusInternalServerError)
		return
	}
	startRound := prevRounds + 1
	endRound := startRound + req.Rounds - 1

	// Use Background context — the investigation must outlive this HTTP request.
	if err := s.runner.Start(context.Background(), sessionID, query, "", startRound, endRound); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":  sessionID,
		"status":      "running",
		"start_round": startRound,
		"end_round":   endRound,
	})
}

// handleCancel cancels an active investigation.
func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if !s.runner.Cancel(sessionID) {
		http.Error(w, "session is not running", http.StatusNotFound)
		return
	}
	_ = s.store.UpdateSessionStatus(r.Context(), sessionID, "cancelled")
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"status":     "cancelled",
	})
}

func pcapSourceLabel(sess storage.Session) string {
	if sess.PcapFileID.Valid {
		return "db"
	}
	if sess.PcapPath.Valid {
		return sess.PcapPath.String
	}
	return "file"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}



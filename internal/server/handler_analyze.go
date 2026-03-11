package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Pcapchu/Pcapchu/internal/events"
	"github.com/Pcapchu/Pcapchu/internal/investigation"
	"github.com/Pcapchu/Pcapchu/middlewares/logger"
)

// analyzeRequest is the JSON body for POST /api/sessions/{id}/analyze.
type analyzeRequest struct {
	Query string `json:"query"`
}

// handleAnalyze runs one investigation round on an existing session and streams
// events via SSE. The session must already have a pcap attached (via
// POST /api/pcap/upload or PATCH /api/sessions/{id}/pcap).
// Call this endpoint repeatedly to run additional rounds — the start round is
// auto-detected from the database.
//
// Request: POST /api/sessions/{id}/analyze  {"query":"..."}
// Response: text/event-stream
func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := r.PathValue("id")

	var req analyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Load session, verify it exists and has a pcap.
	sess, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	if !sess.PcapFileID.Valid {
		http.Error(w, "session has no pcap file attached", http.StatusBadRequest)
		return
	}

	query := sess.UserQuery
	if req.Query != "" {
		query = req.Query
	}

	// Determine start/end rounds (auto-detect first-run vs continuation).
	prevRounds, err := s.store.RoundCount(ctx, sessionID)
	if err != nil {
		http.Error(w, "count rounds: "+err.Error(), http.StatusInternalServerError)
		return
	}
	startRound := prevRounds + 1
	endRound := startRound // always 1 round per request

	// --- SSE setup ---
	sse := newSSEWriter(w)
	if sse == nil {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Create per-request emitter + logger with emitFunc wired.
	emitter := events.NewChannelEmitter(1024)
	log := logger.NewLogger().
		WithSink(s.log.Sink()).
		WithEmit(func(eventType string, data any) {
			emitter.Emit(events.NewEvent(eventType, "", data))
		})

	rt, err := investigation.NewRuntime(r.Context(), log, emitter)
	if err != nil {
		sseError(sse, "init", fmt.Sprintf("create runtime: %v", err))
		return
	}
	defer rt.Close()

	_ = s.store.UpdateSessionStatus(ctx, sessionID, "running")

	// Save the user query on first analysis so it appears in session listings.
	if prevRounds == 0 && query != "" {
		_ = s.store.UpdateSessionQuery(ctx, sessionID, query)
	}

	// Emit session lifecycle event.
	if prevRounds == 0 {
		log.Emit(events.TypeSessionCreated, events.SessionCreatedData{
			SessionID:  sessionID,
			UserQuery:  query,
			PcapSource: "db",
		})
	} else {
		log.Emit(events.TypeSessionResumed, events.SessionResumedData{
			SessionID: sessionID,
			FromRound: startRound,
		})
	}

	// Copy pcap into container from DB.
	containerPcapPath, err := investigation.CopyPcapToContainer(rt.Ctx(), rt.Env(), sess, s.store, "")
	if err != nil {
		sseError(sse, "init", fmt.Sprintf("copy pcap: %v", err))
		_ = s.store.UpdateSessionStatus(ctx, sessionID, "error")
		return
	}

	rt.SetUserQuery(query)

	// Subscribe to events from the runtime's emitter.
	ch := rt.Emitter().Subscribe()

	// Run investigation in a goroutine; signal completion via channel.
	errCh := make(chan error, 1)
	go func() {
		err := investigation.RunInvestigation(
			rt.Ctx(), rt.Log(), rt.Planner(), rt.Exec(), rt.Compressor(),
			s.store, sessionID, query, containerPcapPath, startRound, endRound,
		)
		rt.Emitter().Close() // closes subscriber channels
		errCh <- err
	}()

	// Stream events to SSE + persist to DB.
	seq := 0
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-r.Context().Done():
			_ = s.store.UpdateSessionStatus(context.Background(), sessionID, "cancelled")
			return
		case <-keepAlive.C:
			sse.writeComment("keepalive")
		case ev, ok := <-ch:
			if !ok {
				runErr := <-errCh
				status := "completed"
				if runErr != nil {
					status = "error"
					sseError(sse, "investigation", runErr.Error())
				}
				// Emit analysis.completed before closing.
				seq++
				acData := marshalJSON(events.AnalysisData{
					SessionID:   sessionID,
					TotalRounds: endRound,
				})
				_ = s.store.SaveEvent(context.Background(), sessionID, seq, events.TypeAnalysisCompleted, acData)
				sse.writeEvent(seq, events.TypeAnalysisCompleted, acData)

				_ = s.store.UpdateSessionStatus(context.Background(), sessionID, status)
				sse.writeEvent(0, "done", marshalJSON(map[string]string{
					"session_id": sessionID,
					"status":     status,
				}))
				return
			}
			seq++
			data := "{}"
			if ev.Data != nil {
				data = string(ev.Data)
			}
			_ = s.store.SaveEvent(context.Background(), sessionID, seq, ev.Type, data)
			sse.writeEvent(seq, ev.Type, data)
		}
	}
}

// sseError sends an error event over the SSE stream.
func sseError(sse *sseWriter, phase, msg string) {
	sse.writeEvent(0, "error", marshalJSON(events.ErrorData{Phase: phase, Message: msg}))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func marshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}


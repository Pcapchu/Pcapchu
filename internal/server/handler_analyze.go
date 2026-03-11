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
// POST /api/pcap/upload).
//
// No data is persisted until the investigation completes successfully.
// A roundCollector buffers round results in memory; on success it flushes
// everything to the database atomically.
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

	sess, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if !sess.PcapFileID.Valid {
		http.Error(w, "session has no pcap file attached", http.StatusBadRequest)
		return
	}

	query := req.Query
	if query == "" {
		http.Error(w, "query is required", http.StatusBadRequest)
		return
	}

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

	// Per-request emitter + logger.
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

	// Copy pcap into container.
	containerPcapPath, err := investigation.CopyPcapToContainer(rt.Ctx(), rt.Env(), sess, s.store, "")
	if err != nil {
		sseError(sse, "init", fmt.Sprintf("copy pcap: %v", err))
		return
	}

	rt.SetUserQuery(query)

	// Collector buffers events + round data in memory — no DB writes during SSE.
	collector := newTxCollector(sessionID, startRound)

	ch := rt.Emitter().Subscribe()

	errCh := make(chan error, 1)
	go func() {
		err := investigation.RunInvestigation(
			rt.Ctx(), rt.Log(), rt.Planner(), rt.Exec(), rt.Compressor(),
			s.store, sessionID, query, containerPcapPath, startRound, endRound,
			collector.collectRound,
		)
		rt.Emitter().Close()
		errCh <- err
	}()

	// --- SSE streaming (no DB writes) ---
	seq := 0
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-r.Context().Done():
			// Client disconnected — do NOT flush to DB.
			return

		case <-keepAlive.C:
			sse.writeComment("keepalive")

		case ev, ok := <-ch:
			if !ok {
				// Investigation finished — determine outcome.
				runErr := <-errCh
				status := "completed"
				if runErr != nil {
					status = "error"
					sseError(sse, "investigation", runErr.Error())
				}

				// Emit analysis.completed as final buffered event.
				seq++
				acData := marshalJSON(events.AnalysisData{
					SessionID:   sessionID,
					TotalRounds: endRound,
				})
				collector.bufferEvent(seq, events.TypeAnalysisCompleted, acData)
				sse.writeEvent(seq, events.TypeAnalysisCompleted, acData)

				// Flush accumulated data to DB atomically.
				if flushErr := collector.flush(context.Background(), s.store, query, status); flushErr != nil {
					log.Error(ctx, "flush to DB failed", logger.A(logger.AttrError, flushErr.Error()))
				}

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
			collector.bufferEvent(seq, ev.Type, data)
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


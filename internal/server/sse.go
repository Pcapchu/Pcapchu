package server

import (
	"fmt"
	"net/http"
)

// sseWriter wraps an http.ResponseWriter that supports Server-Sent Events.
type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// newSSEWriter prepares the response for SSE and returns a writer.
// Returns nil if the client does not support streaming.
func newSSEWriter(w http.ResponseWriter) *sseWriter {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
	flusher.Flush()
	return &sseWriter{w: w, flusher: flusher}
}

// writeEvent sends a single SSE event with an optional id.
// id=0 means no id field is written.
func (s *sseWriter) writeEvent(id int, eventType, data string) {
	if id > 0 {
		fmt.Fprintf(s.w, "id: %d\n", id)
	}
	fmt.Fprintf(s.w, "event: %s\n", eventType)
	fmt.Fprintf(s.w, "data: %s\n\n", data)
	s.flusher.Flush()
}

// writeComment sends an SSE comment (used for keep-alive).
func (s *sseWriter) writeComment(text string) {
	fmt.Fprintf(s.w, ": %s\n\n", text)
	s.flusher.Flush()
}

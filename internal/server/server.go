package server

import (
	"context"
	"net/http"
	"time"

	"github.com/Pcapchu/Pcapchu/internal/storage"
	"github.com/Pcapchu/Pcapchu/middlewares/logger"
)

// Server is the HTTP server that exposes the SSE API for Pcapchu.
type Server struct {
	store *storage.Store
	log   *logger.Logger
	mux   *http.ServeMux
	addr  string
}

// New creates a new Server.
func New(store *storage.Store, log *logger.Logger, addr string) *Server {
	s := &Server{
		store: store,
		log:   log,
		mux:   http.NewServeMux(),
		addr:  addr,
	}
	s.routes()
	return s
}

// ListenAndServe starts the HTTP server and blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.addr,
		Handler:           s.cors(s.mux),
		ReadHeaderTimeout: 30 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	s.log.Info(context.Background(), "server listening", logger.A("addr", s.addr))
	return srv.ListenAndServe()
}

func (s *Server) routes() {
	// Analysis (SSE — response is text/event-stream)
	s.mux.HandleFunc("POST /api/sessions/{id}/analyze", s.handleAnalyze)

	// Session CRUD
	s.mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	s.mux.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	s.mux.HandleFunc("DELETE /api/sessions/{id}", s.handleDeleteSession)

	// Pcap management
	s.mux.HandleFunc("POST /api/pcap/upload", s.handlePcapUpload)
	s.mux.HandleFunc("GET /api/pcap", s.handleListPcap)
	s.mux.HandleFunc("DELETE /api/pcap/{id}", s.handleDeletePcap)


	// Serve embedded frontend (SPA with fallback to index.html)
	s.mux.Handle("/", spaHandler())
}

// cors wraps a handler with permissive CORS headers for local/dev frontend usage.
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Last-Event-ID")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

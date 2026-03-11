package server

import (
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Pcapchu/Pcapchu/internal/storage"

	"github.com/google/uuid"
)

// handlePcapUpload stores a pcap file and creates a new session bound to it.
// Accepts multipart/form-data with a "file" field.
// Returns {session_id, pcap_id, filename, size}.
func (s *Server) handlePcapUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Limit upload to 500 MB.
	if err := r.ParseMultipartForm(500 << 20); err != nil {
		http.Error(w, "parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing 'file' field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "read file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	pcapID, err := s.store.InsertPcapFile(ctx, header.Filename, data)
	if err != nil {
		http.Error(w, "store pcap: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Create a session bound to this pcap.
	sessionID := uuid.New().String()
	sess := storage.Session{
		ID:         sessionID,
		PcapFileID: storage.NullInt64(pcapID),
	}
	if err := s.store.CreateSession(ctx, sess); err != nil {
		http.Error(w, "create session: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"session_id": sessionID,
		"pcap_id":    pcapID,
		"filename":   header.Filename,
		"size":       len(data),
	})
}

// handleListPcap returns all stored pcap file metadata (no blob data).
func (s *Server) handleListPcap(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListPcapFiles(r.Context())
	if err != nil {
		http.Error(w, "list pcap: "+err.Error(), http.StatusInternalServerError)
		return
	}

	type pcapJSON struct {
		ID        int64  `json:"id"`
		Filename  string `json:"filename"`
		Size      int64  `json:"size"`
		SHA256    string `json:"sha256"`
		CreatedAt string `json:"created_at"`
	}

	result := make([]pcapJSON, 0, len(items))
	for _, item := range items {
		result = append(result, pcapJSON{
			ID:        item.ID,
			Filename:  item.Filename,
			Size:      item.Size,
			SHA256:    item.SHA256,
			CreatedAt: item.CreatedAt.Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"pcap_files": result,
	})
}

// handleDeletePcap removes a stored pcap file.
func (s *Server) handleDeletePcap(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid pcap id", http.StatusBadRequest)
		return
	}

	if err := s.store.DeletePcapFile(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

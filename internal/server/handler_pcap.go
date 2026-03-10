package server

import (
	"encoding/json"
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

// reattachPcapRequest is the JSON body for PATCH /api/sessions/{id}/pcap.
type reattachPcapRequest struct {
	PcapID int64 `json:"pcap_id"`
}

// handleReattachPcap re-binds a stored pcap file to an existing session.
//
// Accepts either:
//   - multipart/form-data with a "file" field (uploads + stores + binds)
//   - JSON body with {"pcap_id": 123}      (binds to existing stored pcap)
//
// Upload always does SHA-256 dedup. If the same file already exists, the
// existing pcap row is reused.
func (s *Server) handleReattachPcap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := r.PathValue("id")

	// Verify session exists.
	if _, err := s.store.GetSession(ctx, sessionID); err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	var pcapID int64

	ct := r.Header.Get("Content-Type")
	if len(ct) >= 19 && ct[:19] == "multipart/form-data" {
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
		// InsertPcapFile does SHA-256 dedup automatically.
		id, err := s.store.InsertPcapFile(ctx, header.Filename, data)
		if err != nil {
			http.Error(w, "store pcap: "+err.Error(), http.StatusInternalServerError)
			return
		}
		pcapID = id
	} else {
		var req reattachPcapRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.PcapID <= 0 {
			http.Error(w, "pcap_id is required", http.StatusBadRequest)
			return
		}
		// Verify the pcap exists.
		if _, err := s.store.GetPcapFileData(ctx, req.PcapID); err != nil {
			http.Error(w, "pcap file not found", http.StatusNotFound)
			return
		}
		pcapID = req.PcapID
	}

	if err := s.store.UpdateSessionPcap(ctx, sessionID, pcapID, ""); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"pcap_id":    pcapID,
	})
}

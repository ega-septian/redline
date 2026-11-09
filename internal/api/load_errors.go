package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"redline/internal/store"
	"redline/internal/triage"
)

// handleListLoadErrors: GET /api/load-errors?limit=50
func (s *Server) handleListLoadErrors(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 50, 1, 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items, err := s.Store.LoadErrors(r.Context(), limit)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// handleTriageLoadError: PUT /api/load-errors/{id}/triage
func (s *Server) handleTriageLoadError(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
		Owner  string `json:"owner"`
		Note   string `json:"note"`
		By     string `json:"by"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body harus JSON: {\"status\": \"...\", \"owner\": \"...\", \"note\": \"...\", \"by\": \"...\"}")
		return
	}
	body.Status = strings.TrimSpace(body.Status)
	body.Owner = strings.TrimSpace(body.Owner)
	body.Note = strings.TrimSpace(body.Note)
	if !store.ValidLoadErrorStatus(body.Status) {
		writeError(w, http.StatusBadRequest, "status harus salah satu dari: "+strings.Join(store.LoadErrorStatuses, ", "))
		return
	}
	if len(body.Owner) > 80 || len(body.Note) > 500 {
		writeError(w, http.StatusBadRequest, "owner maksimal 80 karakter dan note maksimal 500 karakter")
		return
	}
	by := s.actor(r, body.By)
	if err := s.Store.SetLoadErrorTriage(r.Context(), r.PathValue("id"), body.Status, body.Owner, body.Note, by); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "load error tidak ditemukan")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": body.Status, "owner": body.Owner, "note": body.Note, "by": by,
	})
}

// handleAnalyzeLoadError: POST /api/load-errors/{id}/analyze. Re-runs the deterministic rules for
// groups created before analysis was stored on ingest.
func (s *Server) handleAnalyzeLoadError(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := s.Store.LoadError(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "load error tidak ditemukan")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	analysis := triage.AnalyzeLoadError(item.Message)
	if err := s.Store.SetLoadErrorAnalysis(r.Context(), id, analysis); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "load error tidak ditemukan")
			return
		}
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, analysis)
}

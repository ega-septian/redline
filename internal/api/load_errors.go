package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"redline/internal/analysis"
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
		writeError(w, http.StatusBadRequest, "request body must be JSON: {\"status\": \"...\", \"owner\": \"...\", \"note\": \"...\", \"by\": \"...\"}")
		return
	}
	body.Status = strings.TrimSpace(body.Status)
	body.Owner = strings.TrimSpace(body.Owner)
	body.Note = strings.TrimSpace(body.Note)
	if !store.ValidLoadErrorStatus(body.Status) {
		writeError(w, http.StatusBadRequest, "status must be one of: "+strings.Join(store.LoadErrorStatuses, ", "))
		return
	}
	if len(body.Owner) > 80 || len(body.Note) > 500 {
		writeError(w, http.StatusBadRequest, "owner must be at most 80 characters and note at most 500 characters")
		return
	}
	by := s.actor(r, body.By)
	if err := s.Store.SetLoadErrorTriage(r.Context(), r.PathValue("id"), body.Status, body.Owner, body.Note, by); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "load error not found")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": body.Status, "owner": body.Owner, "note": body.Note, "by": by,
	})
}

// handleAnalyzeLoadError: POST /api/load-errors/{id}/analyze[?ai=1]. The default runs deterministic
// rules; ai=1 asks the model for cases the rules leave unclear.
func (s *Server) handleAnalyzeLoadError(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := s.Store.LoadError(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "load error not found")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	result := triage.AnalyzeLoadError(item.Message)
	if r.URL.Query().Get("ai") == "1" {
		if s.Analyzer == nil {
			writeError(w, http.StatusServiceUnavailable, analysis.ErrAIDisabled.Error())
			return
		}
		result, err = s.Analyzer.AnalyzeLoadError(r.Context(), item.Message)
		if errors.Is(err, analysis.ErrAIDisabled) || errors.Is(err, analysis.ErrBudgetExceeded) {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		if err != nil {
			writeError(w, http.StatusBadGateway, "AI analysis failed; check the server logs")
			return
		}
	}
	if err := s.Store.SetLoadErrorAnalysis(r.Context(), id, result); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "load error not found")
			return
		}
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

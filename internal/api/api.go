// Package api menyediakan HTTP API Redline.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"redline/internal/analysis"
	"redline/internal/report"
	"redline/internal/store"
)

// Store adalah bagian dari database yang dipakai API (interface supaya mudah dites).
type Store interface {
	IngestReport(ctx context.Context, rep *report.Report, meta store.RunMeta) (*store.IngestResult, error)
	ListRuns(ctx context.Context, limit int) ([]store.Run, error)
	ListGroups(ctx context.Context, statuses []string, limit int) ([]store.Group, error)
	GetGroup(ctx context.Context, fingerprint string) (store.Group, []store.Occurrence, error)
	GetAnalysis(ctx context.Context, fingerprint, promptVersion string) (store.Analysis, error)
	SetLabel(ctx context.Context, fingerprint, label, note string) error
}

// Analyzer menentukan penyebab kegagalan (aturan, cache, lalu AI).
type Analyzer interface {
	Analyze(ctx context.Context, fingerprint string, force bool) (*store.Analysis, bool, error)
}

type Server struct {
	Store       Store
	Analyzer    Analyzer
	Log         *slog.Logger
	MaxBodySize int64
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/runs", s.handleIngest)
	mux.HandleFunc("GET /api/runs", s.handleListRuns)
	mux.HandleFunc("GET /api/groups", s.handleListGroups)
	mux.HandleFunc("GET /api/groups/{fingerprint}", s.handleGetGroup)
	mux.HandleFunc("POST /api/groups/{fingerprint}/analyze", s.handleAnalyze)
	mux.HandleFunc("PUT /api/groups/{fingerprint}/label", s.handleLabel)
	return mux
}

// handleIngest menerima isi results.json di body (mentah, atau dibungkus reporter Redline
// sebagai {"playwright": ..., "test_hashes": ...}). Metadata run lewat query string:
//
//	POST /api/runs?source=local&branch=main&commit=abc123&app_version=sprint5-with-bugs
func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	limit := s.MaxBodySize
	if limit <= 0 {
		limit = 50 << 20
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "results.json terlalu besar (atur MAX_REPORT_MB)")
			return
		}
		writeError(w, http.StatusBadRequest, "gagal membaca body: "+err.Error())
		return
	}
	// Bisa results.json mentah (curl) atau bungkus dari reporter Redline (dengan sidik jari kode test).
	rep, err := report.ParseUpload(data)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	q := r.URL.Query()
	source := q.Get("source")
	if source != "" && source != "local" && source != "ci" {
		writeError(w, http.StatusBadRequest, "source harus 'local' atau 'ci'")
		return
	}
	meta := store.RunMeta{
		Source:     source,
		Branch:     q.Get("branch"),
		CommitSHA:  q.Get("commit"),
		AppVersion: q.Get("app_version"),
	}
	res, err := s.Store.IngestReport(r.Context(), rep, meta)
	if err != nil {
		s.Log.Error("ingest gagal", "err", err)
		writeError(w, http.StatusInternalServerError, "gagal menyimpan laporan, cek log server")
		return
	}
	s.Log.Info("run tersimpan", "run", res.RunID, "total", res.Total, "failed", res.Failed,
		"new", len(res.New), "regressed", len(res.Regressed), "resolved", len(res.Resolved))
	writeJSON(w, http.StatusCreated, res)
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := s.Store.ListRuns(r.Context(), limit)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// handleListGroups: GET /api/groups?status=open,regressed (default) atau status=all.
func (s *Server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	var statuses []string
	switch raw := r.URL.Query().Get("status"); raw {
	case "":
	case "all":
		statuses = []string{"open", "regressed", "resolved"}
	default:
		for _, st := range strings.Split(raw, ",") {
			st = strings.TrimSpace(st)
			if st != "open" && st != "regressed" && st != "resolved" {
				writeError(w, http.StatusBadRequest, "status harus open, regressed, resolved, atau all")
				return
			}
			statuses = append(statuses, st)
		}
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	groups, err := s.Store.ListGroups(r.Context(), statuses, limit)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

func (s *Server) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	g, occ, err := s.Store.GetGroup(r.Context(), r.PathValue("fingerprint"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "kelompok kegagalan tidak ditemukan")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	resp := map[string]any{"group": g, "occurrences": occ, "analysis": nil}
	if a, err := s.Store.GetAnalysis(r.Context(), g.Fingerprint, analysis.PromptVersion); err == nil {
		resp["analysis"] = a
	} else if !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleAnalyze: POST /api/groups/{fingerprint}/analyze?force=1
// Tanpa force, hasil cache dipakai kalau ada (tanpa biaya token).
func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if s.Analyzer == nil {
		writeError(w, http.StatusServiceUnavailable, analysis.ErrAIDisabled.Error())
		return
	}
	force := r.URL.Query().Get("force") == "1"
	res, cached, err := s.Analyzer.Analyze(r.Context(), r.PathValue("fingerprint"), force)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "kelompok kegagalan tidak ditemukan")
		return
	case errors.Is(err, analysis.ErrAIDisabled):
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	case err != nil:
		s.Log.Error("analisis gagal", "fingerprint", r.PathValue("fingerprint"), "err", err)
		writeError(w, http.StatusBadGateway, "analisis gagal, cek log server")
		return
	}
	if !cached && res.Source == "ai" {
		s.Log.Info("analisis AI", "fingerprint", res.Fingerprint, "category", res.Category,
			"input_tokens", res.InputTokens, "output_tokens", res.OutputTokens, "cost_usd", res.CostUSD)
	}
	writeJSON(w, http.StatusOK, map[string]any{"analysis": res, "cached": cached})
}

// handleLabel: PUT /api/groups/{fingerprint}/label {"label":"test_bug","note":"assertion salah"}
// Label kosong menghapus label. Label manual menimpa hasil aturan dan AI.
func (s *Server) handleLabel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label string `json:"label"`
		Note  string `json:"note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body harus JSON: {\"label\": \"...\", \"note\": \"...\"}")
		return
	}
	if body.Label != "" && !store.ValidCategory(body.Label) {
		writeError(w, http.StatusBadRequest, "label harus salah satu dari: "+strings.Join(store.Categories, ", "))
		return
	}
	err := s.Store.SetLabel(r.Context(), r.PathValue("fingerprint"), body.Label, strings.TrimSpace(body.Note))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "kelompok kegagalan tidak ditemukan")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"label": body.Label, "note": body.Note})
}

func (s *Server) serverError(w http.ResponseWriter, err error) {
	s.Log.Error("request gagal", "err", err)
	writeError(w, http.StatusInternalServerError, "terjadi kesalahan, cek log server")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

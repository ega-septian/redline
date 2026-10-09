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
	"time"

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
	SetLabel(ctx context.Context, fingerprint, label, note, by string) error
	SaveExperiment(ctx context.Context, e store.Experiment) (int64, error)
	ListExperiments(ctx context.Context, groupID string) ([]store.Experiment, error)
	ListRules(ctx context.Context, status string) ([]store.LearnedRule, error)
	DecideRule(ctx context.Context, id int64, status, by string) (store.LearnedRule, error)
	Scores(ctx context.Context, groupID string, limit int) (*store.Scoreboard, error)
}

// Analyzer menentukan penyebab kegagalan (aturan, cache, lalu AI).
type Analyzer interface {
	Analyze(ctx context.Context, fingerprint string, force bool) (*store.Analysis, bool, error)
	ProposeFix(ctx context.Context, fingerprint string, files []analysis.SourceFile, attempts []analysis.Attempt) (*analysis.Fix, error)
	ProposeRules(ctx context.Context) (*analysis.LearnResult, error)
	EmbedPending(ctx context.Context) (int, error)
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
	mux.HandleFunc("POST /api/groups/{fingerprint}/fix", s.handleFix)
	mux.HandleFunc("POST /api/groups/{fingerprint}/experiments", s.handleSaveExperiment)
	mux.HandleFunc("GET /api/rules", s.handleListRules)
	mux.HandleFunc("POST /api/rules/propose", s.handleProposeRules)
	mux.HandleFunc("PUT /api/rules/{id}", s.handleDecideRule)
	mux.HandleFunc("GET /api/scoreboard", s.handleScoreboard)
	return mux
}

// handleIngest menerima isi results.json di body (mentah, atau dibungkus reporter Redline
// sebagai {"playwright": ..., "test_hashes": ...}). Metadata run lewat query string:
//
//	POST /api/runs?source=local&branch=main&commit=abc123&app_version=sprint5-with-bugs&triggered_by=ega&ci_url=https://...
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
		Source:      source,
		TriggeredBy: q.Get("triggered_by"),
		CIURL:       q.Get("ci_url"),
		Branch:      q.Get("branch"),
		CommitSHA:   q.Get("commit"),
		AppVersion:  q.Get("app_version"),
	}
	res, err := s.Store.IngestReport(r.Context(), rep, meta)
	if err != nil {
		s.Log.Error("ingest gagal", "err", err)
		writeError(w, http.StatusInternalServerError, "gagal menyimpan laporan, cek log server")
		return
	}
	s.Log.Info("run tersimpan", "run", res.RunID, "total", res.Total, "failed", res.Failed,
		"new", len(res.New), "regressed", len(res.Regressed), "resolved", len(res.Resolved))
	// Embedding kegagalan baru dibuat di background, satu request untuk seluruh run. Ingest tidak menunggu:
	// Voyage bisa lambat (rate limit akun gratis), dan reporter hanya menunggu beberapa detik.
	// Analisis yang menyusul menunggu giliran (mutex) lalu memakai hasilnya.
	if len(res.New)+len(res.Regressed) > 0 && s.Analyzer != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if _, err := s.Analyzer.EmbedPending(ctx); err != nil {
				s.Log.Warn("embedding setelah ingest gagal; dicoba lagi saat analisis", "err", err)
			}
		}()
	}
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
	experiments, err := s.Store.ListExperiments(r.Context(), g.Fingerprint)
	if err != nil {
		s.serverError(w, err)
		return
	}
	resp := map[string]any{"group": g, "occurrences": occ, "experiments": experiments, "analysis": nil}
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

// handleLabel: PUT /api/groups/{fingerprint}/label {"label":"test_bug","note":"assertion salah","by":"ega"}
// Label kosong menghapus label. Label manual menimpa hasil aturan dan AI.
func (s *Server) handleLabel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label string `json:"label"`
		Note  string `json:"note"`
		By    string `json:"by"` // siapa yang memberi label; wajib kalau label diisi
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body harus JSON: {\"label\": \"...\", \"note\": \"...\", \"by\": \"...\"}")
		return
	}
	if body.Label != "" && !store.ValidCategory(body.Label) {
		writeError(w, http.StatusBadRequest, "label harus salah satu dari: "+strings.Join(store.Categories, ", "))
		return
	}
	body.By = strings.TrimSpace(body.By)
	if body.Label != "" && body.By == "" {
		writeError(w, http.StatusBadRequest, "isi \"by\" dengan nama yang memberi label")
		return
	}
	err := s.Store.SetLabel(r.Context(), r.PathValue("fingerprint"), body.Label, strings.TrimSpace(body.Note), body.By)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "kelompok kegagalan tidak ditemukan")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"label": body.Label, "note": body.Note, "by": body.By})
}

// handleFix: POST /api/groups/{fingerprint}/fix {"files": [{"path","content"}], "attempts": [...]}
// AI membuat hipotesis + patch. Patch TIDAK dijalankan di server: CLI eksperimen yang punya kode test
// menerapkannya di salinan project, menjalankan test, lalu mengirim hasilnya ke /experiments.
func (s *Server) handleFix(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Files    []analysis.SourceFile `json:"files"`
		Attempts []analysis.Attempt    `json:"attempts"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body harus JSON: {\"files\": [{\"path\": \"...\", \"content\": \"...\"}]}")
		return
	}
	fix, err := s.Analyzer.ProposeFix(r.Context(), r.PathValue("fingerprint"), body.Files, body.Attempts)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "kelompok kegagalan tidak ditemukan")
	case errors.Is(err, analysis.ErrAIDisabled):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, analysis.ErrBadFixInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		s.Log.Error("usulan patch gagal", "fingerprint", r.PathValue("fingerprint"), "err", err)
		writeError(w, http.StatusBadGateway, "usulan patch gagal, cek log server")
	default:
		writeJSON(w, http.StatusOK, fix)
	}
}

var experimentOutcomes = map[string]bool{"passed": true, "failed": true, "rejected": true, "skipped": true}

// handleSaveExperiment: POST /api/groups/{fingerprint}/experiments, hasil eksperimen dari CLI.
func (s *Server) handleSaveExperiment(w http.ResponseWriter, r *http.Request) {
	var e store.Experiment
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&e); err != nil {
		writeError(w, http.StatusBadRequest, "body harus JSON eksperimen")
		return
	}
	e.GroupID = r.PathValue("fingerprint")
	switch {
	case e.Kind != "rerun" && e.Kind != "patch":
		writeError(w, http.StatusBadRequest, "kind harus rerun atau patch")
		return
	case !experimentOutcomes[e.Outcome]:
		writeError(w, http.StatusBadRequest, "outcome harus passed, failed, rejected, atau skipped")
		return
	case e.Category != "" && !store.ValidCategory(e.Category):
		writeError(w, http.StatusBadRequest, "category harus salah satu dari: "+strings.Join(store.Categories, ", "))
		return
	case e.Runs < 1 || e.Passes < 0 || e.Passes > e.Runs:
		writeError(w, http.StatusBadRequest, "runs minimal 1 dan passes di antara 0 dan runs")
		return
	case e.Outcome == "passed" && e.Passes == 0:
		writeError(w, http.StatusBadRequest, "outcome passed butuh passes > 0")
		return
	}
	id, err := s.Store.SaveExperiment(r.Context(), e)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "kelompok kegagalan tidak ditemukan")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.Log.Info("eksperimen tersimpan", "fingerprint", e.GroupID, "kind", e.Kind, "outcome", e.Outcome)
	resp := map[string]any{"id": id, "verdicts": []store.ScoreEntry{}}
	// Eksperimen yang lulus adalah jawaban: langsung nilai tebakan aturan/AI sebelumnya.
	if e.Outcome == "passed" {
		board, err := s.Store.Scores(r.Context(), e.GroupID, 0)
		if err != nil {
			s.serverError(w, err)
			return
		}
		resp["verdicts"] = board.Entries
	}
	writeJSON(w, http.StatusCreated, resp)
}

// handleScoreboard: GET /api/scoreboard?limit=20. Akurasi tebakan aturan dan AI dibanding bukti.
func (s *Server) handleScoreboard(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 20
	}
	board, err := s.Store.Scores(r.Context(), "", limit)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, board)
}

// handleListRules: GET /api/rules?status=proposed|active|rejected (kosong = semua).
func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && !ruleStatuses[status] {
		writeError(w, http.StatusBadRequest, "status harus proposed, active, atau rejected")
		return
	}
	rules, err := s.Store.ListRules(r.Context(), status)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rules)
}

// handleProposeRules: POST /api/rules/propose. AI belajar dari kasus terbukti; hasilnya berstatus proposed.
func (s *Server) handleProposeRules(w http.ResponseWriter, r *http.Request) {
	res, err := s.Analyzer.ProposeRules(r.Context())
	switch {
	case errors.Is(err, analysis.ErrNothingToLearn):
		writeJSON(w, http.StatusOK, map[string]any{"cases": 0, "proposed": []any{}, "rejected": []any{}, "message": err.Error()})
	case errors.Is(err, analysis.ErrAIDisabled):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case err != nil:
		s.Log.Error("usulan aturan gagal", "err", err)
		writeError(w, http.StatusBadGateway, "usulan aturan gagal, cek log server")
	default:
		writeJSON(w, http.StatusOK, res)
	}
}

var ruleStatuses = map[string]bool{"proposed": true, "active": true, "rejected": true}

// handleDecideRule: PUT /api/rules/{id} {"status": "active", "by": "ega"}
func (s *Server) handleDecideRule(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id aturan harus angka")
		return
	}
	var body struct {
		Status string `json:"status"`
		By     string `json:"by"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil || !ruleStatuses[body.Status] {
		writeError(w, http.StatusBadRequest, "body harus JSON: {\"status\": \"active|rejected|proposed\", \"by\": \"...\"}")
		return
	}
	body.By = strings.TrimSpace(body.By)
	if body.By == "" {
		writeError(w, http.StatusBadRequest, "isi \"by\" dengan nama yang memutuskan")
		return
	}
	rule, err := s.Store.DecideRule(r.Context(), id, body.Status, body.By)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "aturan tidak ditemukan")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.Log.Info("aturan diputuskan", "id", id, "status", body.Status, "by", body.By)
	writeJSON(w, http.StatusOK, rule)
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

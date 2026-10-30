// Package api menyediakan HTTP API Redline.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"redline/internal/analysis"
	"redline/internal/report"
	"redline/internal/reportportal"
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
	Knowledge(ctx context.Context, fingerprint string) (*store.Knowledge, error)
	ImpactOfFile(ctx context.Context, path string) ([]store.ImpactedTest, error)
	ImpactOfEndpoint(ctx context.Context, endpoint string) ([]store.ImpactedTest, error)
}

// Analyzer menentukan penyebab kegagalan (aturan, cache, lalu AI).
type Analyzer interface {
	Analyze(ctx context.Context, fingerprint string, force bool) (*store.Analysis, bool, error)
	ProposeFix(ctx context.Context, fingerprint string, files []analysis.SourceFile, attempts []analysis.Attempt) (*analysis.Fix, error)
	ProposeRules(ctx context.Context) (*analysis.LearnResult, error)
	EmbedPending(ctx context.Context) (int, error)
	SnapshotContracts(ctx context.Context, runID int64, projects []string)
}

// ReportPortal mengisi hasil analisis ke launch ReportPortal milik sebuah run (lihat reportportal.Syncer).
type ReportPortal interface {
	Sync(ctx context.Context, runID int64, token string) (reportportal.Result, error)
}

type Server struct {
	Store        Store
	Analyzer     Analyzer
	ReportPortal ReportPortal // nil = integrasi ReportPortal mati
	Log          *slog.Logger
	MaxBodySize  int64
	APIKey       string // legacy single key; empty = no Bearer auth unless AuthEnabled
	// AuthEnabled wraps /api with the key check even when APIKey is empty (REDLINE_USERS only).
	AuthEnabled bool
	// ValidateKey checks a Bearer token. nil falls back to APIKey.
	ValidateKey func(string) bool
	// User returns the signed-in user for a session cookie (labels are attributed to it).
	User func(*http.Request) string
	// Version: versi build server, ditampilkan di /healthz dan header respons.
	Version string
	// Ready memeriksa kesiapan server (biasanya database). nil = /readyz membalas 503.
	Ready func(context.Context) error
	// Session: true when the request carries a valid UI session cookie.
	// Cookie writes also need CSRF (X-CSRF-Token matching the redline_csrf cookie).
	Session func(*http.Request) bool
	CSRF    func(*http.Request) bool
}

// APIVersion naik kalau ada perubahan yang tidak kompatibel pada /api. Paket redline-playwright
// memeriksa header X-Redline-Api-Version dan memperingatkan kalau server dan paket berbeda.
const APIVersion = 1

func (s *Server) version() string {
	if s.Version == "" {
		return "dev"
	}
	return s.Version
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok", "version": s.version(), "api": APIVersion,
			"auth": s.AuthEnabled || s.APIKey != "",
		})
	})
	mux.HandleFunc("GET /readyz", s.handleReady)
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
	mux.HandleFunc("GET /api/groups/{fingerprint}/knowledge", s.handleKnowledge)
	mux.HandleFunc("GET /api/impact", s.handleImpact)
	mux.HandleFunc("POST /api/runs/{id}/reportportal", s.handleReportPortal)
	var h http.Handler = mux
	if s.AuthEnabled || s.APIKey != "" {
		h = s.requireAPIKey(mux)
	}
	return s.versionHeaders(h)
}

// versionHeaders menandai setiap respons dengan versi server dan versi API, supaya klien bisa
// mendeteksi server dan paket yang tidak seversi tanpa memanggil endpoint tambahan.
func (s *Server) versionHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Redline-Version", s.version())
		w.Header().Set("X-Redline-Api-Version", strconv.Itoa(APIVersion))
		next.ServeHTTP(w, r)
	})
}

// handleReady memeriksa dependensi (database). Berbeda dari /healthz yang hanya menandakan proses hidup.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.Ready == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "error": "readiness check belum dikonfigurasi"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.Ready(ctx); err != nil {
		if s.Log != nil {
			s.Log.Warn("readiness gagal", "err", err)
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "error": "database tidak bisa dihubungi"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// requireAPIKey menolak request /api/... tanpa "Authorization: Bearer <APIKey>". /healthz dan /readyz tetap
// terbuka supaya pengecekan server hidup tidak butuh key. Key dibandingkan dalam waktu konstan.
func (s *Server) requireAPIKey(next http.Handler) http.Handler {
	want := []byte("Bearer " + s.APIKey)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		if s.Session != nil && s.Session(r) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead ||
				(s.CSRF != nil && s.CSRF(r)) {
				next.ServeHTTP(w, r)
				return
			}
			writeError(w, http.StatusForbidden, "CSRF token salah atau tidak ada: kirim header X-CSRF-Token")
			return
		}
		got := []byte(r.Header.Get("Authorization"))
		if s.ValidateKey != nil {
			if token, ok := strings.CutPrefix(string(got), "Bearer "); ok && s.ValidateKey(token) {
				next.ServeHTTP(w, r)
				return
			}
		} else if subtle.ConstantTimeCompare(got, want) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="redline"`)
		writeError(w, http.StatusUnauthorized, "API key salah atau tidak ada: kirim header Authorization: Bearer <REDLINE_API_KEY>")
	})
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
	// Kontrak dipotret di setiap run, termasuk yang lulus semua: run lulus adalah pembanding untuk tahu
	// apakah kontrak berubah. Tidak di background: analisis yang menyusul harus memakai kontrak run ini.
	if s.Analyzer != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		s.Analyzer.SnapshotContracts(ctx, res.RunID, projectsOf(rep))
		cancel()
	}
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

// projectsOf: project Playwright yang muncul di laporan, urut.
func projectsOf(rep *report.Report) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, o := range rep.Outcomes() {
		if !seen[o.Project] {
			seen[o.Project] = true
			out = append(out, o.Project)
		}
	}
	sort.Strings(out)
	return out
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
	// A UI session has an identity; do not trust the client's "by".
	if s.User != nil {
		if user := s.User(r); user != "" {
			body.By = user
		}
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
	if s.User != nil {
		if user := s.User(r); user != "" {
			body.By = user
		}
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

// handleKnowledge: GET /api/groups/{fingerprint}/knowledge. Pengetahuan graph (KAG) untuk satu kegagalan:
// file yang dipakai test sampai import terdalam beserta perubahannya, dan test lain di run yang sama.
func (s *Server) handleKnowledge(w http.ResponseWriter, r *http.Request) {
	k, err := s.Store.Knowledge(r.Context(), r.PathValue("fingerprint"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "kelompok tidak ditemukan, atau belum punya kemunculan gagal")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, k)
}

// handleImpact: test yang terdampak kalau sebuah file atau endpoint berubah.
//
//	GET /api/impact?file=schemas/brand.ts          (path lengkap atau akhirannya; import bertingkat ikut dihitung)
//	GET /api/impact?endpoint=GET /brands/1         (method boleh dihilangkan; ID di path menjadi :id)
func (s *Server) handleImpact(w http.ResponseWriter, r *http.Request) {
	file := strings.TrimSpace(r.URL.Query().Get("file"))
	endpoint := strings.TrimSpace(r.URL.Query().Get("endpoint"))
	if (file == "") == (endpoint == "") {
		writeError(w, http.StatusBadRequest, "isi salah satu: file=... atau endpoint=...")
		return
	}
	var tests []store.ImpactedTest
	var err error
	if file != "" {
		tests, err = s.Store.ImpactOfFile(r.Context(), file)
	} else {
		tests, err = s.Store.ImpactOfEndpoint(r.Context(), endpoint)
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": file, "endpoint": endpoint, "tests": tests})
}

var runToken = regexp.MustCompile(`^[A-Za-z0-9-]{1,100}$`)

// handleReportPortal: POST /api/runs/{id}/reportportal?token=<redline_run>. Mengisi jenis defect dan komentar
// di launch ReportPortal yang membawa atribut redline_run=<token>. Dikerjakan di background, karena agen
// Playwright baru menutup launch setelah semua reporter selesai (termasuk reporter Redline yang memanggil ini).
func (s *Server) handleReportPortal(w http.ResponseWriter, r *http.Request) {
	if s.ReportPortal == nil {
		writeError(w, http.StatusNotFound, "integrasi ReportPortal mati (REPORTPORTAL_URL kosong)")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id run tidak valid")
		return
	}
	token := r.URL.Query().Get("token")
	if !runToken.MatchString(token) {
		writeError(w, http.StatusBadRequest, "token harus huruf, angka, atau tanda hubung (maksimal 100)")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		res, err := s.ReportPortal.Sync(ctx, id, token)
		if err != nil {
			s.Log.Warn("ReportPortal tidak bisa diisi", "run", id, "err", err)
			return
		}
		s.Log.Info("ReportPortal diisi", "run", id, "launch", res.Launch, "updated", res.Updated,
			"unclear", res.Unclear, "labelled", res.Labelled, "unmatched", res.Unmatched)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted", "run": id})
}

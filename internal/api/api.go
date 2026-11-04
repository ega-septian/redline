// Package api menyediakan HTTP API Redline.
package api

import (
	"context"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"redline/internal/analysis"
	"redline/internal/contract"
	"redline/internal/report"
	"redline/internal/reportportal"
	"redline/internal/store"
)

// Store adalah bagian dari database yang dipakai API (interface supaya mudah dites).
type Store interface {
	IngestReport(ctx context.Context, rep *report.Report, meta store.RunMeta) (*store.IngestResult, error)
	ListRuns(ctx context.Context, limit int) ([]store.Run, error)
	GetRun(ctx context.Context, id int64) (store.Run, error)
	PreviousRun(ctx context.Context, id int64, branch string) (store.Run, bool, error)
	RunResults(ctx context.Context, runID int64) ([]store.RunResult, error)
	TestHealth(ctx context.Context, project string, days int) (store.TestHealthSummary, []store.TestHealthRow, error)
	TestHistory(ctx context.Context, test string, days, limit int) (store.TestHealthRow, []store.TestRunPoint, error)
	FindTest(ctx context.Context, test string) (store.TestIdentity, error)
	SetQuarantine(ctx context.Context, q store.Quarantine) (store.Quarantine, error)
	ReleaseQuarantine(ctx context.Context, key, by string) (store.Quarantine, error)
	ActiveQuarantines(ctx context.Context) ([]store.Quarantine, error)
	ContractSnapshots(ctx context.Context, project string, limit int) ([]store.ContractSnapshot, error)
	EndpointCalls(ctx context.Context, project string) ([]store.EndpointCall, error)
	ContractSources(ctx context.Context) ([]store.ContractSource, error)
	ContractSource(ctx context.Context, project string) (store.ContractSource, error)
	SetContractSource(ctx context.Context, src store.ContractSource) (store.ContractSource, error)
	DeleteContractSource(ctx context.Context, project string) error
	RecordSourceCheck(ctx context.Context, project string, record store.SourceCheckRecord) error
	NotificationSettings(ctx context.Context) (store.NotificationSettings, error)
	SetNotificationSettings(ctx context.Context, settings store.NotificationSettings) (store.NotificationSettings, error)
	SaveArtifact(ctx context.Context, a store.Artifact) (int64, error)
	ListArtifacts(ctx context.Context, runID int64) ([]store.Artifact, error)
	GetArtifact(ctx context.Context, id int64) (store.Artifact, error)
	ListGroups(ctx context.Context, statuses []string, limit int) ([]store.Group, error)
	GetGroup(ctx context.Context, fingerprint string) (store.Group, []store.Occurrence, error)
	GetAnalysis(ctx context.Context, fingerprint, promptVersion string) (store.Analysis, error)
	SetLabel(ctx context.Context, fingerprint, label, note, by string) error
	SetTriage(ctx context.Context, fingerprint, status, owner, note, by string) error
	SetTriageStatusBulk(ctx context.Context, ids []string, status, by string) (int, error)
	SetTriageOwnerBulk(ctx context.Context, ids []string, owner, by string) (int, error)
	SaveExperiment(ctx context.Context, e store.Experiment) (int64, error)
	ListExperiments(ctx context.Context, groupID string) ([]store.Experiment, error)
	ListRules(ctx context.Context, status string) ([]store.LearnedRule, error)
	ListRuleCandidates(ctx context.Context, fingerprint, status string) ([]store.RuleCandidate, error)
	DecideRule(ctx context.Context, id int64, status, by string) (store.LearnedRule, error)
	Scores(ctx context.Context, groupID string, limit int) (*store.Scoreboard, error)
	Knowledge(ctx context.Context, fingerprint string) (*store.Knowledge, error)
	ImpactOfFile(ctx context.Context, path string) ([]store.ImpactedTest, error)
	ImpactOfEndpoint(ctx context.Context, endpoint string) ([]store.ImpactedTest, error)
}

// Analyzer menentukan penyebab kegagalan (aturan, cache, lalu AI).
type Analyzer interface {
	Analyze(ctx context.Context, fingerprint string, force bool) (*store.Analysis, bool, error)
	AnalyzeLocal(ctx context.Context, fingerprint string, force bool) (*store.Analysis, error)
	PromoteCandidates(ctx context.Context, fingerprint string) (*analysis.PromotionResult, error)
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
	// ArtifactDir menyimpan trace/screenshot upload; MaxArtifactSize membatasi satu file.
	ArtifactDir     string
	MaxArtifactSize int64
	APIKey          string // legacy single key; empty = no Bearer auth unless AuthEnabled
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
	// Contracts is the runtime OpenAPI registry: Config writes are applied without a restart.
	Contracts *contract.Registry
	// EnvSpecs is the startup OPENAPI_SPECS map, used as fallback when a database source is removed.
	EnvSpecs map[string]string
	// SourceCheck validates one spec URL; nil uses contract.ValidateSource (tests stub the network).
	SourceCheck func(context.Context, string) contract.SourceCheck
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
	mux.HandleFunc("GET /api/runs/{id}/compare", s.handleCompareRun)
	mux.HandleFunc("GET /api/tests/history", s.handleTestHistory)
	mux.HandleFunc("GET /api/tests", s.handleTestHealth)
	mux.HandleFunc("GET /api/tests/quarantine", s.handleListQuarantine)
	mux.HandleFunc("PUT /api/tests/quarantine", s.handleSetQuarantine)
	mux.HandleFunc("DELETE /api/tests/quarantine", s.handleReleaseQuarantine)
	mux.HandleFunc("GET /api/coverage", s.handleCoverage)
	mux.HandleFunc("GET /api/config", s.handleGetConfig)
	mux.HandleFunc("PUT /api/config/contracts", s.handleSaveContract)
	mux.HandleFunc("DELETE /api/config/contracts", s.handleDeleteContract)
	mux.HandleFunc("POST /api/config/contracts/validate", s.handleValidateSource)
	mux.HandleFunc("POST /api/config/contracts/check", s.handleCheckContract)
	mux.HandleFunc("PUT /api/config/notifications", s.handleSaveNotifications)
	mux.HandleFunc("POST /api/config/notifications/test", s.handleTestNotification)
	mux.HandleFunc("GET /api/runs/{id}/artifacts", s.handleListArtifacts)
	mux.HandleFunc("POST /api/runs/{id}/artifacts", s.handleUploadArtifact)
	mux.HandleFunc("GET /api/artifacts/{id}", s.handleGetArtifact)
	mux.HandleFunc("GET /api/runs/{id}", s.handleGetRun)
	mux.HandleFunc("GET /api/groups", s.handleListGroups)
	mux.HandleFunc("GET /api/groups/{fingerprint}", s.handleGetGroup)
	mux.HandleFunc("POST /api/groups/{fingerprint}/analyze", s.handleAnalyze)
	mux.HandleFunc("PUT /api/groups/{fingerprint}/label", s.handleLabel)
	mux.HandleFunc("PUT /api/groups/triage", s.handleTriageBulk)
	mux.HandleFunc("PUT /api/groups/triage/owner", s.handleTriageOwnerBulk)
	mux.HandleFunc("PUT /api/groups/{fingerprint}/triage", s.handleTriage)
	mux.HandleFunc("POST /api/groups/{fingerprint}/fix", s.handleFix)
	mux.HandleFunc("POST /api/groups/{fingerprint}/experiments", s.handleSaveExperiment)
	mux.HandleFunc("GET /api/rules", s.handleListRules)
	mux.HandleFunc("GET /api/rules/candidates", s.handleListRuleCandidates)
	mux.HandleFunc("POST /api/rules/candidates/promote", s.handlePromoteCandidates)
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

func (s *Server) handleTestHealth(w http.ResponseWriter, r *http.Request) {
	days, err := queryInt(r, "days", 30, 1, 365)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	summary, tests, err := s.Store.TestHealth(r.Context(), project, days)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"summary": summary, "tests": tests})
}

func (s *Server) handleTestHistory(w http.ResponseWriter, r *http.Request) {
	test := strings.TrimSpace(r.URL.Query().Get("test"))
	if test == "" {
		writeError(w, http.StatusBadRequest, "test wajib diisi")
		return
	}
	days, err := queryInt(r, "days", 30, 1, 365)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := queryInt(r, "limit", 50, 1, 200)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	summary, runs, err := s.Store.TestHistory(r.Context(), test, days, limit)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "test tidak ditemukan pada rentang waktu ini")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"summary": summary, "runs": runs})
}

func queryInt(r *http.Request, name string, fallback, minValue, maxValue int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minValue || value > maxValue {
		return 0, fmt.Errorf("%s harus angka antara %d dan %d", name, minValue, maxValue)
	}
	return value, nil
}

func (s *Server) handleListQuarantine(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ActiveQuarantines(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleSetQuarantine(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Test   string `json:"test"`
		Owner  string `json:"owner"`
		Reason string `json:"reason"`
		By     string `json:"by"`
		Days   int    `json:"days"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body harus JSON: {\"test\": \"...\", \"owner\": \"...\", \"reason\": \"...\", \"days\": 7}")
		return
	}
	body.Test = strings.TrimSpace(body.Test)
	body.Owner = strings.TrimSpace(body.Owner)
	body.Reason = strings.TrimSpace(body.Reason)
	if body.Test == "" || body.Owner == "" || body.Reason == "" {
		writeError(w, http.StatusBadRequest, "test, owner dan reason wajib diisi")
		return
	}
	if body.Days == 0 {
		body.Days = 7
	}
	if body.Days < 1 || body.Days > 90 {
		writeError(w, http.StatusBadRequest, "days harus antara 1 dan 90")
		return
	}
	by := strings.TrimSpace(body.By)
	if s.User != nil {
		if user := s.User(r); user != "" {
			by = user
		}
	}
	if by == "" {
		by = "api"
	}
	identity, err := s.Store.FindTest(r.Context(), body.Test)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "test tidak ditemukan")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	q, err := s.Store.SetQuarantine(r.Context(), store.Quarantine{
		Key: identity.Key, TestKey: identity.TestKey, TestCaseID: identity.TestCaseID,
		Project: identity.Project, Owner: body.Owner, Reason: body.Reason, By: by,
		Until: time.Now().AddDate(0, 0, body.Days),
	})
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, q)
}

func (s *Server) handleReleaseQuarantine(w http.ResponseWriter, r *http.Request) {
	test := strings.TrimSpace(r.URL.Query().Get("test"))
	if test == "" {
		writeError(w, http.StatusBadRequest, "test wajib diisi")
		return
	}
	by := strings.TrimSpace(r.URL.Query().Get("by"))
	if s.User != nil {
		if user := s.User(r); user != "" {
			by = user
		}
	}
	if by == "" {
		by = "api"
	}
	identity, err := s.Store.FindTest(r.Context(), test)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "test tidak ditemukan")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	q, err := s.Store.ReleaseQuarantine(r.Context(), identity.Key, by)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "quarantine aktif tidak ditemukan")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, q)
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

// handleCompareRun: GET /api/runs/{id}/compare. Bandingkan run dengan run sebelumnya di branch yang sama.
func (s *Server) handleCompareRun(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id run tidak valid")
		return
	}
	current, err := s.Store.GetRun(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "run tidak ditemukan")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	previous, found, err := s.Store.PreviousRun(r.Context(), id, current.Branch)
	if err != nil {
		s.serverError(w, err)
		return
	}
	currentResults, err := s.Store.RunResults(r.Context(), id)
	if err != nil {
		s.serverError(w, err)
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, runComparison{
			Current: current, Summary: map[string]int{}, Tests: []comparedTest{},
		})
		return
	}
	previousResults, err := s.Store.RunResults(r.Context(), previous.ID)
	if err != nil {
		s.serverError(w, err)
		return
	}
	cmp := compareRuns(current, previous, currentResults, previousResults)
	writeJSON(w, http.StatusOK, cmp)
}

type runComparison struct {
	Current  store.Run      `json:"current"`
	Previous *store.Run     `json:"previous"`
	Summary  map[string]int `json:"summary"`
	Tests    []comparedTest `json:"tests"`
}

type comparedTest struct {
	Test                string `json:"test"`
	TestCaseID          string `json:"test_case_id,omitempty"`
	Status              string `json:"status"`
	PreviousStatus      string `json:"previous_status,omitempty"`
	Fingerprint         string `json:"fingerprint,omitempty"`
	PreviousFingerprint string `json:"previous_fingerprint,omitempty"`
	Change              string `json:"change"`
}

const (
	changeNew          = "new"
	changeFixed        = "fixed"
	changeStillFailing = "still_failing"
	changeChangedCause = "changed_cause"
	changeFlaky        = "flaky"
	changeRemoved      = "removed"
	changeUnchanged    = "unchanged"
)

var changePriority = map[string]int{
	changeNew: 0, changeChangedCause: 1, changeStillFailing: 2,
	changeFixed: 3, changeFlaky: 4, changeRemoved: 5, changeUnchanged: 6,
}

func compareRuns(current, previous store.Run, currentResults, previousResults []store.RunResult) runComparison {
	prev := map[string]store.RunResult{}
	for _, result := range previousResults {
		prev[result.TestKey] = result
	}
	cur := map[string]bool{}
	tests := []comparedTest{}
	summary := map[string]int{}
	for _, result := range currentResults {
		cur[result.TestKey] = true
		before, exists := prev[result.TestKey]
		change := changeUnchanged
		currentFailed := result.Status == "failed"
		previousFailed := exists && before.Status == "failed"
		switch {
		case currentFailed && (!exists || !previousFailed):
			change = changeNew
		case currentFailed && previousFailed && result.Fingerprint != before.Fingerprint:
			change = changeChangedCause
		case currentFailed && previousFailed:
			change = changeStillFailing
		case result.Status == "flaky":
			change = changeFlaky
		case previousFailed && result.Status == "passed":
			change = changeFixed
		}
		if change == changeUnchanged {
			continue
		}
		summary[change]++
		tests = append(tests, comparedTest{
			Test: result.TestKey, TestCaseID: result.TestCaseID, Status: result.Status,
			PreviousStatus: before.Status, Fingerprint: result.Fingerprint,
			PreviousFingerprint: before.Fingerprint, Change: change,
		})
	}
	for _, result := range previousResults {
		if cur[result.TestKey] || result.Status != "failed" {
			continue
		}
		summary[changeRemoved]++
		tests = append(tests, comparedTest{
			Test: result.TestKey, TestCaseID: result.TestCaseID, Status: "removed",
			PreviousStatus: result.Status, PreviousFingerprint: result.Fingerprint, Change: changeRemoved,
		})
	}
	sort.SliceStable(tests, func(i, j int) bool {
		pi, pj := changePriority[tests[i].Change], changePriority[tests[j].Change]
		if pi != pj {
			return pi < pj
		}
		return tests[i].Test < tests[j].Test
	})
	return runComparison{Current: current, Previous: &previous, Summary: summary, Tests: tests}
}

// handleGetRun: GET /api/runs/{id} mengembalikan metadata run dan semua outcome-nya.
func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id run tidak valid")
		return
	}
	run, err := s.Store.GetRun(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "run tidak ditemukan")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	tests, err := s.Store.RunResults(r.Context(), id)
	if err != nil {
		s.serverError(w, err)
		return
	}
	artifacts, err := s.Store.ListArtifacts(r.Context(), id)
	if err != nil {
		s.serverError(w, err)
		return
	}
	byTest := map[string][]store.Artifact{}
	for _, artifact := range artifacts {
		byTest[artifact.TestKey] = append(byTest[artifact.TestKey], artifact)
	}
	for i := range tests {
		tests[i].Artifacts = byTest[tests[i].TestKey]
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run, "tests": tests})
}

// handleListArtifacts: GET /api/runs/{id}/artifacts
func (s *Server) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id run tidak valid")
		return
	}
	if _, err := s.Store.GetRun(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "run tidak ditemukan")
		return
	} else if err != nil {
		s.serverError(w, err)
		return
	}
	artifacts, err := s.Store.ListArtifacts(r.Context(), id)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, artifacts)
}

// handleUploadArtifact: POST /api/runs/{id}/artifacts (multipart: test, kind, file)
func (s *Server) handleUploadArtifact(w http.ResponseWriter, r *http.Request) {
	runID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || runID <= 0 {
		writeError(w, http.StatusBadRequest, "id run tidak valid")
		return
	}
	if _, err := s.Store.GetRun(r.Context(), runID); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "run tidak ditemukan")
		return
	} else if err != nil {
		s.serverError(w, err)
		return
	}
	max := s.MaxArtifactSize
	if max <= 0 {
		max = 25 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, max+(1<<20))
	if err := r.ParseMultipartForm(max); err != nil {
		writeError(w, http.StatusBadRequest, "upload artifact tidak valid atau terlalu besar")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll() // file besar bisa ditulis ke temp; jangan tinggalkan sisa
	}
	test := r.FormValue("test")
	kind := strings.TrimSpace(r.FormValue("kind"))
	if strings.TrimSpace(test) == "" || (kind != "trace" && kind != "screenshot") {
		writeError(w, http.StatusBadRequest, "test wajib diisi dan kind harus trace atau screenshot")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file artifact wajib diisi")
		return
	}
	defer file.Close()
	if header.Size > max {
		writeError(w, http.StatusRequestEntityTooLarge, "artifact terlalu besar")
		return
	}
	dir := filepath.Join(s.artifactDir(), strconv.FormatInt(runID, 10), testHash(test))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.serverError(w, err)
		return
	}
	name := safeArtifactName(header.Filename)
	path := filepath.Join(dir, fmt.Sprintf("%s-%d-%s", kind, time.Now().UnixNano(), name))
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		s.serverError(w, err)
		return
	}
	written, copyErr := io.Copy(out, io.LimitReader(file, max+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || written > max {
		_ = os.Remove(path)
		writeError(w, http.StatusRequestEntityTooLarge, "artifact terlalu besar atau gagal ditulis")
		return
	}
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(name))
	}
	artifact := store.Artifact{RunID: runID, TestKey: test, Kind: kind, Name: name, Path: path,
		ContentType: contentType, Size: written}
	id, err := s.Store.SaveArtifact(r.Context(), artifact)
	if err != nil {
		_ = os.Remove(path)
		s.serverError(w, err)
		return
	}
	artifact.ID = id
	writeJSON(w, http.StatusCreated, artifact)
}

// handleGetArtifact: GET /api/artifacts/{id}, served from the artifact directory.
func (s *Server) handleGetArtifact(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id artifact tidak valid")
		return
	}
	artifact, err := s.Store.GetArtifact(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "artifact tidak ditemukan")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	path, err := filepath.Abs(artifact.Path)
	if err != nil {
		s.serverError(w, err)
		return
	}
	root, err := filepath.Abs(s.artifactDir())
	if err != nil || !strings.HasPrefix(path, root+string(os.PathSeparator)) {
		writeError(w, http.StatusNotFound, "artifact tidak ditemukan")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "file artifact tidak ditemukan")
		return
	}
	defer file.Close()
	if artifact.ContentType != "" {
		w.Header().Set("Content-Type", artifact.ContentType)
	}
	disposition := "inline"
	if artifact.Kind == "trace" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disposition, artifact.Name))
	http.ServeContent(w, r, artifact.Name, artifact.CreatedAt, file)
}

func (s *Server) artifactDir() string {
	if s.ArtifactDir != "" {
		return s.ArtifactDir
	}
	return "./artifacts"
}

func testHash(test string) string {
	sum := sha1.Sum([]byte(test))
	return hex.EncodeToString(sum[:8])
}

func safeArtifactName(name string) string {
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, name)
	if name == "" || name == "." {
		return "artifact"
	}
	return name
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

// handleAnalyze: POST /api/groups/{fingerprint}/analyze?force=1&ai=0
// Tanpa force, hasil cache AI dipakai kalau ada. ai=0 menjalankan aturan/label/bukti saja tanpa AI.
func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if s.Analyzer == nil {
		writeError(w, http.StatusServiceUnavailable, analysis.ErrAIDisabled.Error())
		return
	}
	fingerprint := r.PathValue("fingerprint")
	if r.URL.Query().Get("ai") == "0" {
		force := r.URL.Query().Get("force") == "1"
		res, err := s.Analyzer.AnalyzeLocal(r.Context(), fingerprint, force)
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "kelompok kegagalan tidak ditemukan")
		case err != nil:
			s.Log.Error("analisis lokal gagal", "fingerprint", fingerprint, "err", err)
			writeError(w, http.StatusBadGateway, "analisis gagal, cek log server")
		default:
			writeJSON(w, http.StatusOK, map[string]any{
				"analysis": res, "cached": false, "needs_ai": res.Source == "none",
			})
		}
		return
	}
	force := r.URL.Query().Get("force") == "1"
	res, cached, err := s.Analyzer.Analyze(r.Context(), fingerprint, force)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "kelompok kegagalan tidak ditemukan")
		return
	case errors.Is(err, analysis.ErrAIDisabled):
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	case err != nil:
		s.Log.Error("analisis gagal", "fingerprint", fingerprint, "err", err)
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
	if body.Label != "" && s.Analyzer != nil {
		if res, err := s.Analyzer.PromoteCandidates(r.Context(), r.PathValue("fingerprint")); err != nil {
			s.Log.Warn("promosi rule candidate gagal", "fingerprint", r.PathValue("fingerprint"), "err", err)
		} else if len(res.Promoted)+len(res.Rejected) > 0 {
			s.Log.Info("candidate diproses setelah label", "promoted", len(res.Promoted), "rejected", len(res.Rejected))
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"label": body.Label, "note": body.Note, "by": body.By})
}

// handleTriage: PUT /api/groups/{fingerprint}/triage
// {"status":"acknowledged","owner":"ega","note":"...", "by":"ega"}
func (s *Server) handleTriage(w http.ResponseWriter, r *http.Request) {
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
	if s.User != nil {
		if user := s.User(r); user != "" {
			body.By = user
		}
	}
	body.Status, body.Owner, body.Note, body.By = strings.TrimSpace(body.Status),
		strings.TrimSpace(body.Owner), strings.TrimSpace(body.Note), strings.TrimSpace(body.By)
	if !store.ValidTriageStatus(body.Status) {
		writeError(w, http.StatusBadRequest, "status harus salah satu dari: "+strings.Join(store.TriageStatuses, ", "))
		return
	}
	if body.By == "" {
		writeError(w, http.StatusBadRequest, "isi \"by\" dengan nama yang mengubah triage")
		return
	}
	if len(body.Owner) > 80 || len(body.Note) > 500 {
		writeError(w, http.StatusBadRequest, "owner maksimal 80 karakter dan note maksimal 500 karakter")
		return
	}
	err := s.Store.SetTriage(r.Context(), r.PathValue("fingerprint"), body.Status, body.Owner, body.Note, body.By)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "kelompok kegagalan tidak ditemukan")
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": body.Status, "owner": body.Owner, "note": body.Note, "by": body.By,
	})
}

// handleTriageBulk: PUT /api/groups/triage
// {"fingerprints":["..."],"status":"acknowledged","by":"ega"}
func (s *Server) handleTriageBulk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Fingerprints []string `json:"fingerprints"`
		Status       string   `json:"status"`
		By           string   `json:"by"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body harus JSON: {\"fingerprints\": [...], \"status\": \"...\", \"by\": \"...\"}")
		return
	}
	if s.User != nil {
		if user := s.User(r); user != "" {
			body.By = user
		}
	}
	body.Status, body.By = strings.TrimSpace(body.Status), strings.TrimSpace(body.By)
	if !store.ValidTriageStatus(body.Status) {
		writeError(w, http.StatusBadRequest, "status harus salah satu dari: "+strings.Join(store.TriageStatuses, ", "))
		return
	}
	if body.By == "" {
		writeError(w, http.StatusBadRequest, "isi \"by\" dengan nama yang mengubah triage")
		return
	}
	ids, err := cleanFingerprints(body.Fingerprints)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := s.Store.SetTriageStatusBulk(r.Context(), ids, body.Status, body.By)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": n})
}

// handleTriageOwnerBulk: PUT /api/groups/triage/owner
// {"fingerprints":["..."],"owner":"ega","by":"ega"}
func (s *Server) handleTriageOwnerBulk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Fingerprints []string `json:"fingerprints"`
		Owner        string   `json:"owner"`
		By           string   `json:"by"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body harus JSON: {\"fingerprints\": [...], \"owner\": \"...\", \"by\": \"...\"}")
		return
	}
	if s.User != nil {
		if user := s.User(r); user != "" {
			body.By = user
		}
	}
	body.Owner, body.By = strings.TrimSpace(body.Owner), strings.TrimSpace(body.By)
	if body.By == "" {
		writeError(w, http.StatusBadRequest, "isi \"by\" dengan nama yang mengubah owner")
		return
	}
	if len(body.Owner) > 80 {
		writeError(w, http.StatusBadRequest, "owner maksimal 80 karakter")
		return
	}
	ids, err := cleanFingerprints(body.Fingerprints)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := s.Store.SetTriageOwnerBulk(r.Context(), ids, body.Owner, body.By)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": n})
}

func cleanFingerprints(ids []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, errors.New("fingerprints tidak boleh kosong")
	}
	if len(out) > 200 {
		return nil, errors.New("maksimal 200 fingerprint per permintaan")
	}
	return out, nil
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
	if e.Outcome == "passed" && s.Analyzer != nil {
		if res, err := s.Analyzer.PromoteCandidates(r.Context(), e.GroupID); err != nil {
			s.Log.Warn("promosi rule candidate gagal", "fingerprint", e.GroupID, "err", err)
		} else if len(res.Promoted)+len(res.Rejected) > 0 {
			s.Log.Info("candidate diproses setelah eksperimen", "promoted", len(res.Promoted), "rejected", len(res.Rejected))
		}
	}
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

// handleListRuleCandidates: GET /api/rules/candidates?status=draft|promoted|rejected|expired
func (s *Server) handleListRuleCandidates(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status != "" && !ruleCandidateStatuses[status] {
		writeError(w, http.StatusBadRequest, "status harus draft, promoted, rejected, atau expired")
		return
	}
	candidates, err := s.Store.ListRuleCandidates(r.Context(), r.URL.Query().Get("fingerprint"), status)
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, candidates)
}

var ruleCandidateStatuses = map[string]bool{"draft": true, "promoted": true, "rejected": true, "expired": true}

// handlePromoteCandidates: POST /api/rules/candidates/promote?fingerprint=...
func (s *Server) handlePromoteCandidates(w http.ResponseWriter, r *http.Request) {
	if s.Analyzer == nil {
		writeError(w, http.StatusServiceUnavailable, "analyzer tidak aktif")
		return
	}
	res, err := s.Analyzer.PromoteCandidates(r.Context(), r.URL.Query().Get("fingerprint"))
	if err != nil {
		s.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
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

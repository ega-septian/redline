package store

// Tipe data dan fungsi bantu yang tidak bergantung pada Neo4j.

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"redline/internal/report"
	"redline/internal/triage"
)

// Categories adalah kategori penyebab kegagalan yang dipakai aturan, AI, dan label manual.
var Categories = []string{"backend_bug", "test_bug", "environment", "flaky", "unknown"}

func ValidCategory(c string) bool {
	for _, x := range Categories {
		if x == c {
			return true
		}
	}
	return false
}

// RunRef adalah ringkasan satu run untuk keperluan bukti.
type RunRef struct {
	RunID      int64     `json:"run_id"`
	At         time.Time `json:"at"`
	Source     string    `json:"source"`
	CommitSHA  string    `json:"commit"`
	AppVersion string    `json:"app_version"`
}

// Facts adalah semua bukti tentang satu kelompok kegagalan. Hanya fakta dari database,
// tanpa tafsiran; inilah yang dikirim ke aturan dan ke AI.
type Facts struct {
	Group         Group  `json:"group"`
	ErrorMessage  string `json:"error_message"` // kemunculan terakhir, sudah diredaksi
	ErrorSnippet  string `json:"error_snippet"`
	ErrorLocation string `json:"error_location"`

	// LastPass: run terakhir test ini lulus sebelum kegagalan sekarang (nil = belum pernah lulus).
	LastPass *RunRef `json:"last_pass"`
	// FirstFail: run pertama gagal dengan error ini setelah LastPass.
	FirstFail RunRef `json:"first_fail"`

	RecentStatuses []string `json:"recent_statuses"` // status test ini di 10 run terakhir, lama -> baru
	FlakyRecent    int      `json:"flaky_recent"`    // berapa kali flaky di 20 run terakhir

	// Sinyal perubahan dibanding LastPass (tanpa git):
	CurrentCalls []report.HTTPCall   `json:"current_calls"` // bentuk response saat gagal
	TestChanged  string              `json:"test_changed"`  // "ya" / "tidak" / "tidak diketahui"
	Response     triage.ResponseDiff `json:"response"`

	// FailRunID: run kemunculan gagal terakhir (yang dianalisis).
	FailRunID int64 `json:"fail_run_id"`

	// Kontrak API (OpenAPI) saat test gagal, untuk endpoint yang dipanggil. Diisi analyzer dari potret kontrak run.
	Contract           string   `json:"contract,omitempty"`
	ContractViolations []string `json:"contract_violations,omitempty"` // bagian response yang tidak sesuai kontrak
	ContractChanged    string   `json:"contract_changed,omitempty"`    // dibanding run lulus terakhir: ya / tidak / tidak diketahui
	ContractBefore     string   `json:"contract_before,omitempty"`     // kontrak saat run lulus terakhir, kalau berubah

	// Ingatan:
	Sources   []SourceFile  `json:"sources"`    // isi kode test pada kemunculan terakhir (spec dulu), sudah disamarkan
	CodeFiles []string      `json:"code_files"` // file lokal yang dipakai test ini
	CodeMap   []CodeLink    `json:"code_map"`   // test lain di run yang sama dengan endpoint/file yang sama
	Similar   []SimilarCase `json:"similar"`    // kasus terbukti yang error-nya mirip maknanya (referensi, bukan bukti)
	// Knowledge: pengetahuan graph (KAG). nil kalau tidak tersedia.
	Knowledge *Knowledge `json:"knowledge,omitempty"`
}

// DecodeCalls membaca kembali bentuk response yang disimpan (hasil CallsJSON).
func DecodeCalls(raw []byte) []report.HTTPCall {
	var calls []report.HTTPCall
	if len(raw) == 0 || json.Unmarshal(raw, &calls) != nil {
		return []report.HTTPCall{}
	}
	return calls
}

// Analysis adalah hasil analisis satu kelompok kegagalan.
type Analysis struct {
	Fingerprint   string        `json:"fingerprint"`
	PromptVersion string        `json:"prompt_version"`
	Source        string        `json:"source"` // rule, ai, human (label manual) atau experiment (bukti eksperimen); dua terakhir tidak disimpan di tabel ini
	Model         string        `json:"model,omitempty"`
	Category      string        `json:"category"`
	Confidence    string        `json:"confidence"`
	Summary       string        `json:"summary"`
	Evidence      []string      `json:"evidence"`
	NextStep      string        `json:"next_step"`
	Patch         string        `json:"patch,omitempty"`   // diff yang terbukti membuat test lulus (source experiment)
	Similar       []SimilarCase `json:"similar,omitempty"` // kasus terbukti yang mirip; dihitung ulang, tidak disimpan
	InputTokens   int           `json:"input_tokens"`      // hanya untuk analisis baru; tidak disimpan
	OutputTokens  int           `json:"output_tokens"`     // hanya untuk analisis baru; tidak disimpan
	CostUSD       float64       `json:"cost_usd"`
	CreatedAt     time.Time     `json:"created_at"`
}

// Experiment adalah satu percobaan menjalankan ulang test untuk membuktikan penyebab kegagalan.
type Experiment struct {
	ID         int64     `json:"id"`
	GroupID    string    `json:"group_id"`
	Kind       string    `json:"kind"`    // rerun / patch
	Outcome    string    `json:"outcome"` // passed / failed / rejected / skipped
	Category   string    `json:"category"`
	Hypothesis string    `json:"hypothesis"`
	Patch      string    `json:"patch"`
	Detail     string    `json:"detail"`
	Runs       int       `json:"runs"`
	Passes     int       `json:"passes"`
	CostUSD    float64   `json:"cost_usd"`
	RunBy      string    `json:"run_by"`
	CreatedAt  time.Time `json:"created_at"`
}

// RunMeta adalah informasi tambahan tentang run. Semuanya opsional.
type RunMeta struct {
	Source      string `json:"source"`       // local / ci
	TriggeredBy string `json:"triggered_by"` // siapa yang menjalankan: user, atau ci:<actor>
	CIURL       string `json:"ci_url"`       // halaman run CI (log + artifact)
	Branch      string `json:"branch"`
	CommitSHA   string `json:"commit"`
	AppVersion  string `json:"app_version"`
}

// GroupChange adalah kelompok kegagalan yang statusnya berubah karena run ini.
type GroupChange struct {
	Fingerprint string `json:"fingerprint"`
	TestKey     string `json:"test"`
	TestCaseID  string `json:"test_case_id,omitempty"` // dari tag @TC-..., kalau ada
	Error       string `json:"error,omitempty"`        // ringkasan error (sudah diredaksi)
	Occurrences int    `json:"occurrences"`
	Incident    string `json:"incident,omitempty"` // key insiden; kosong untuk resolved
}

// Incident adalah kegagalan beberapa test di run ini yang penyebabnya sama.
type Incident struct {
	Key   string `json:"key"`
	Kind  string `json:"kind"` // connection, http, error
	Label string `json:"label"`
	Tests int    `json:"tests"`
	// Representative: fingerprint yang cukup dianalisis mewakili seluruh insiden
	// (regressed lebih dulu, lalu new, lalu recurring).
	Representative string   `json:"representative"`
	Fingerprints   []string `json:"fingerprints"`
}

// IngestResult adalah ringkasan setelah satu laporan disimpan.
type IngestResult struct {
	RunID   int64 `json:"run_id"`
	Total   int   `json:"total"`
	Passed  int   `json:"passed"`
	Failed  int   `json:"failed"`
	Flaky   int   `json:"flaky"`
	Skipped int   `json:"skipped"`

	New        []GroupChange `json:"new"`       // kegagalan yang belum pernah terlihat
	Recurring  []GroupChange `json:"recurring"` // masih gagal dengan error yang sama
	Regressed  []GroupChange `json:"regressed"` // pernah beres, sekarang gagal lagi
	Resolved   []GroupChange `json:"resolved"`  // dulu gagal, sekarang lulus
	FlakyTests []string      `json:"flaky_tests"`
	Incidents  []Incident    `json:"incidents"` // kegagalan run ini, dikelompokkan per penyebab, terbesar dulu
	// SharedStatus false: run ini (misalnya lokal) tidak mengubah status bersama;
	// New/Recurring/Regressed/Resolved hanya pratinjau.
	SharedStatus bool `json:"shared_status"`
}

// MaxSourceBytes: file yang lebih besar dari ini tidak disimpan (bukan file test biasa).
const MaxSourceBytes = 50_000

// SourceID menyamarkan isi file kode test dan menghitung id-nya (md5 dari path + isi yang sudah disamarkan).
// File yang sama di run lain mendapat id yang sama, jadi disimpan sekali.
func SourceID(path, content string) (id, clean string) {
	clean = triage.Redact(content)
	sum := md5.Sum([]byte(path + "\x00" + clean))
	return hex.EncodeToString(sum[:]), clean
}

// ShapeID menghitung id bentuk response (hasil CallsJSON), supaya bentuk yang sama disimpan sekali.
func ShapeID(calls string) string {
	sum := md5.Sum([]byte(calls))
	return hex.EncodeToString(sum[:])
}

// CallsJSON menyimpan bentuk response. Path diredaksi (jaga-jaga ada email atau token di path).
func CallsJSON(calls []report.HTTPCall) string {
	clean := make([]report.HTTPCall, 0, len(calls))
	for _, c := range calls {
		if !json.Valid(c.Shape) {
			c.Shape = json.RawMessage("null")
		}
		c.Path = triage.Clean(c.Path)
		clean = append(clean, c)
	}
	b, err := json.Marshal(clean)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// Summarize meringkas pesan error menjadi satu baris: baris pertama, ditambah baris yang
// paling informatif ("Expected: 201", "Received: 500", atau baris diff dari toEqual).
func Summarize(s string) string {
	var first string
	var picked []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		if first == "" {
			first = line
			continue
		}
		if len(picked) < 3 && informative(line) {
			picked = append(picked, line)
		}
	}
	out := strings.Join(append([]string{first}, picked...), " | ")
	if r := []rune(out); len(r) > 240 {
		out = string(r[:240]) + "…"
	}
	return out
}

func informative(line string) bool {
	if strings.HasPrefix(line, "Expected") || strings.HasPrefix(line, "Received") {
		return true
	}
	// ZodError mencetak JSON; baris "message" berisi alasannya.
	if strings.HasPrefix(line, `"message":`) {
		return true
	}
	// Baris diff toEqual: "- \"status\": \"PAID\"," tapi bukan header "- Expected - 1".
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "+ ") {
		rest := line[2:]
		return !strings.HasPrefix(rest, "Expected") && !strings.HasPrefix(rest, "Received")
	}
	return false
}

// IncidentSet mengumpulkan insiden selama ingest, dengan urutan kemunculan dipertahankan.
type IncidentSet struct {
	order []string
	byKey map[string]*Incident
	rank  map[string]int // peringkat representative sekarang: 3 regressed, 2 new, 1 recurring
}

func NewIncidentSet() *IncidentSet {
	return &IncidentSet{byKey: map[string]*Incident{}, rank: map[string]int{}}
}

func (s *IncidentSet) Add(key, kind, label string) {
	if _, ok := s.byKey[key]; ok {
		return
	}
	s.order = append(s.order, key)
	s.byKey[key] = &Incident{Key: key, Kind: kind, Label: label, Fingerprints: []string{}}
}

func (s *IncidentSet) Label(key string) string {
	if inc, ok := s.byKey[key]; ok {
		return inc.Label
	}
	return ""
}

// member mencatat satu kelompok kegagalan. kind dari upsertGroup: new, recurring, regressed.
func (s *IncidentSet) Member(key, fingerprint, kind string) {
	inc, ok := s.byKey[key]
	if !ok {
		return
	}
	inc.Tests++
	inc.Fingerprints = append(inc.Fingerprints, fingerprint)
	r := map[string]int{"regressed": 3, "new": 2}[kind]
	if r == 0 {
		r = 1
	}
	if r > s.rank[key] {
		s.rank[key] = r
		inc.Representative = fingerprint
	}
}

// list: insiden terbesar dulu; yang sama besar mengikuti urutan kemunculan.
func (s *IncidentSet) List() []Incident {
	out := make([]Incident, 0, len(s.order))
	for _, k := range s.order {
		if inc := s.byKey[k]; inc.Tests > 0 {
			out = append(out, *inc)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Tests > out[j].Tests })
	return out
}

// CodeLink: test lain di run yang sama yang memakai endpoint atau file yang sama dengan test yang gagal.
// Kalau test lain itu lulus, masalahnya condong ke test ini; kalau ikut gagal, condong ke bagian bersama.
type CodeLink struct {
	Kind   string `json:"kind"` // endpoint / file
	Name   string `json:"name"` // "GET /brands" atau "tests/api/toolshop/schemas/brand.schema.ts"
	Passed int    `json:"passed"`
	Failed int    `json:"failed"`
}

// SimilarCase adalah kasus terbukti lain yang pesan error-nya mirip maknanya (embedding).
type SimilarCase struct {
	Case
	Test       string  `json:"test"`
	Similarity float64 `json:"similarity"` // cosine similarity, 0 sampai 1
}

// ScoreEntry: satu tebakan yang sudah bisa dinilai karena jawabannya sudah terbukti.
type ScoreEntry struct {
	GroupID    string     `json:"group_id"`
	Test       string     `json:"test"`
	Source     string     `json:"source"` // rule / ai
	Predicted  string     `json:"predicted"`
	Confidence string     `json:"confidence"`
	Truth      string     `json:"truth"`
	TruthBy    string     `json:"truth_by"` // label / eksperimen
	Correct    bool       `json:"correct"`
	ProvenAt   *time.Time `json:"proven_at"`
}

// Score adalah ringkasan akurasi satu sumber. Tebakan "unknown" dihitung terpisah (tidak menebak).
type Score struct {
	Source   string  `json:"source"`
	Graded   int     `json:"graded"` // tebakan selain unknown yang sudah ada jawabannya
	Correct  int     `json:"correct"`
	Unknown  int     `json:"unknown"`
	Accuracy float64 `json:"accuracy"` // correct / graded, 0 kalau graded 0
}

type Scoreboard struct {
	Sources []Score      `json:"sources"`
	Entries []ScoreEntry `json:"entries"` // terbaru dulu
}

// SourceFile adalah isi satu file kode test.
type SourceFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type Run struct {
	ID                int64      `json:"id"`
	CreatedAt         time.Time  `json:"created_at"`
	StartedAt         *time.Time `json:"started_at"`
	DurationMs        int        `json:"duration_ms"`
	Source            string     `json:"source"`
	TriggeredBy       string     `json:"triggered_by"`
	CIURL             string     `json:"ci_url"`
	Branch            string     `json:"branch"`
	CommitSHA         string     `json:"commit"`
	AppVersion        string     `json:"app_version"`
	PlaywrightVersion string     `json:"playwright_version"`
	Total             int        `json:"total"`
	Passed            int        `json:"passed"`
	Failed            int        `json:"failed"`
	Flaky             int        `json:"flaky"`
	Skipped           int        `json:"skipped"`
	ReportErrors      []string   `json:"report_errors"`
}

// RunResult is one test outcome in a run, with the failure group's latest analysis.
type RunResult struct {
	TestKey       string   `json:"test"`
	TestCaseID    string   `json:"test_case_id,omitempty"`
	Status        string   `json:"status"`
	DurationMs    int      `json:"duration_ms"`
	Retries       int      `json:"retries"`
	ErrorMessage  string   `json:"error,omitempty"`
	ErrorSnippet  string   `json:"error_snippet,omitempty"`
	ErrorLocation string   `json:"error_location,omitempty"`
	TracePath     string   `json:"trace_path,omitempty"`
	Screenshots   []string `json:"screenshots"`
	Fingerprint   string   `json:"fingerprint,omitempty"`
	Category      string   `json:"category,omitempty"`
	Confidence    string   `json:"confidence,omitempty"`
	HumanLabel    string   `json:"human_label,omitempty"`
}

type Group struct {
	Fingerprint  string     `json:"fingerprint"`
	TestKey      string     `json:"test"`
	TestCaseID   string     `json:"test_case_id,omitempty"` // dari tag @TC-..., kalau ada
	Project      string     `json:"project"`
	File         string     `json:"file"`
	Title        string     `json:"title"`
	Status       string     `json:"status"`
	Summary      string     `json:"summary"`
	SampleError  string     `json:"sample_error,omitempty"` // pesan error terakhir
	Cause        string     `json:"cause,omitempty"`        // penyebab yang bisa dibaca
	Occurrences  int        `json:"occurrences"`
	Regressions  int        `json:"regressions"`
	FirstSeenRun int64      `json:"first_seen_run"`
	LastSeenRun  int64      `json:"last_seen_run"`
	ResolvedRun  *int64     `json:"resolved_run"`
	FirstSeenAt  time.Time  `json:"first_seen_at"`
	LastSeenAt   time.Time  `json:"last_seen_at"`
	ResolvedAt   *time.Time `json:"resolved_at"`
	HumanLabel   string     `json:"human_label,omitempty"` // kategori dari label manual
	HumanNote    string     `json:"human_note,omitempty"`
	LabeledBy    string     `json:"labeled_by,omitempty"`
	LocalOnly    bool       `json:"local_only"` // hanya terlihat di run lokal; tidak masuk daftar tim

	// Latest analysis, so a list can show the category without a request per group.
	Category        string     `json:"category,omitempty"`
	Confidence      string     `json:"confidence,omitempty"`
	AnalysisSource  string     `json:"analysis_source,omitempty"`
	AnalysisModel   string     `json:"analysis_model,omitempty"`
	AnalysisSummary string     `json:"analysis_summary,omitempty"`
	AnalyzedAt      *time.Time `json:"analyzed_at,omitempty"`
}

// Occurrence adalah satu kemunculan kegagalan di satu run.
type Occurrence struct {
	RunID         int64     `json:"run_id"`
	RunCreatedAt  time.Time `json:"run_created_at"`
	Source        string    `json:"source"`
	Branch        string    `json:"branch"`
	CommitSHA     string    `json:"commit"`
	AppVersion    string    `json:"app_version"`
	Status        string    `json:"status"`
	Retries       int       `json:"retries"`
	ErrorLocation string    `json:"error_location"`
	ErrorSnippet  string    `json:"error_snippet"`
	TracePath     string    `json:"trace_path"`
	Screenshots   []string  `json:"screenshots"`
}

// PruneResult adalah jumlah baris yang dihapus oleh Prune.
type PruneResult struct {
	TestResults int64 `json:"test_results"`
	Shapes      int64 `json:"response_shapes"`
	Sources     int64 `json:"source_files"`
}

// ErrDuplicate dikembalikan kalau aturan dengan pola dan kategori yang sama sudah ada.
var ErrDuplicate = errors.New("duplicate")

// LearnedRule adalah aturan yang dipelajari dari kasus terbukti.
type LearnedRule struct {
	ID          int64      `json:"id"`
	Pattern     string     `json:"pattern"`
	Category    string     `json:"category"`
	Summary     string     `json:"summary"`
	NextStep    string     `json:"next_step"`
	Status      string     `json:"status"` // proposed / active / rejected
	LearnedFrom []string   `json:"learned_from"`
	Hits        int        `json:"hits"`
	AlsoMatches int        `json:"also_matches"`
	CostUSD     float64    `json:"cost_usd"`
	CreatedAt   time.Time  `json:"created_at"`
	DecidedBy   string     `json:"decided_by,omitempty"`
	DecidedAt   *time.Time `json:"decided_at"`
}

// Case adalah kegagalan yang penyebabnya sudah terbukti: dilabeli manusia, atau lulus setelah patch.
type Case struct {
	GroupID  string `json:"group_id"`
	Error    string `json:"error"`
	Category string `json:"category"`
	Source   string `json:"source"` // human / experiment
	Reason   string `json:"reason"` // catatan label, atau hipotesis yang terbukti
}

// GroupError adalah pesan error satu kelompok, untuk menguji aturan baru ke data lama.
type GroupError struct {
	GroupID string
	Error   string
}

// ErrNotFound dikembalikan kalau data yang diminta tidak ada.
var ErrNotFound = errors.New("not found")

// Knowledge adalah pengetahuan dari graph untuk satu kegagalan (KAG): file yang dipakai test beserta
// perubahannya sejak test terakhir lulus, dan test lain di run yang sama yang memakai file atau endpoint
// yang sama. Kosong kalau reporter tidak mengirim graph import.
type Knowledge struct {
	RunID     int64 `json:"run_id"`      // run kegagalan terakhir
	PassRunID int64 `json:"pass_run_id"` // run lulus terakhir; 0 = test belum pernah lulus
	// Compared: run lulus terakhir punya graph import, jadi FileFact.Changed bisa dipercaya.
	Compared  bool           `json:"compared"`
	Files     []FileFact     `json:"files"`
	Endpoints []EndpointFact `json:"endpoints"`
}

// FileFact adalah satu file yang dipakai test yang gagal.
type FileFact struct {
	Path    string `json:"path"`
	Direct  bool   `json:"direct"`  // file spec atau di-import langsung olehnya; false = lewat import lain
	Changed bool   `json:"changed"` // isinya berbeda dari run lulus terakhir
	// Others: test lain di run ini yang juga bergantung pada file ini. Hanya diisi untuk file yang berubah.
	Others TestSplit `json:"others"`
}

// EndpointFact adalah satu endpoint yang dipanggil test yang gagal.
type EndpointFact struct {
	Key         string `json:"key"`          // misalnya "GET /brands/1"
	SeenPassing bool   `json:"seen_passing"` // pernah dipanggil test yang lulus di run sebelumnya
	// Test lain di run ini yang memanggil endpoint yang sama, dipisah: yang memakai file yang berubah, dan yang tidak.
	WithChanged TestSplit `json:"with_changed"`
	Without     TestSplit `json:"without"`
}

// TestSplit adalah daftar test (kunci test) yang lulus dan yang gagal. Flaky dihitung lulus.
type TestSplit struct {
	Passed []string `json:"passed"`
	Failed []string `json:"failed"`
}

// ImpactedTest adalah test yang terdampak kalau sebuah file atau endpoint berubah.
type ImpactedTest struct {
	TestKey string `json:"test"`
	CaseID  string `json:"test_case_id,omitempty"`
	RunID   int64  `json:"run_id"` // run yang dipakai: lulus terakhir, atau run terakhir kalau belum pernah lulus
}

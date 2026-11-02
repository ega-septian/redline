package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"redline/internal/llm"
	"redline/internal/store"
)

// ErrAIDisabled: aturan tidak cocok dan ANTHROPIC_API_KEY belum diisi.
var ErrAIDisabled = errors.New("analisis AI belum aktif: isi ANTHROPIC_API_KEY di .env lalu restart server")

// ErrBudgetExceeded: biaya AI hari ini sudah mencapai AI_DAILY_BUDGET_USD.
var ErrBudgetExceeded = errors.New("batas biaya AI harian tercapai")

type Store interface {
	Facts(ctx context.Context, fingerprint string) (*store.Facts, error)
	Knowledge(ctx context.Context, fingerprint string) (*store.Knowledge, error)
	GetAnalysis(ctx context.Context, fingerprint, promptVersion string) (store.Analysis, error)
	SaveAnalysis(ctx context.Context, a store.Analysis) error
	LatestProof(ctx context.Context, groupID string) (store.Experiment, error)
	ActiveRules(ctx context.Context) ([]store.LearnedRule, error)
	ListRules(ctx context.Context, status string) ([]store.LearnedRule, error)
	SaveRule(ctx context.Context, r store.LearnedRule) (int64, error)
	SaveRuleCandidate(ctx context.Context, c store.RuleCandidate) (int64, error)
	ListRuleCandidates(ctx context.Context, fingerprint, status string) ([]store.RuleCandidate, error)
	MarkRuleCandidate(ctx context.Context, id int64, status, reason string, ruleID int64, hits, also int) error
	ConfirmedCases(ctx context.Context, limit int) ([]store.Case, error)
	RecentGroupErrors(ctx context.Context, limit int) ([]store.GroupError, error)
	SavePrediction(ctx context.Context, a store.Analysis) error
	PendingEmbeddings(ctx context.Context, model string, limit int) ([]store.GroupError, error)
	SaveEmbeddings(ctx context.Context, model string, ids []string, vectors [][]float32) error
	SimilarCases(ctx context.Context, groupID, model string, minSimilarity float64, limit int) ([]store.SimilarCase, error)
	SaveRunContract(ctx context.Context, runID int64, project, sourceURL, document string) (string, error)
	RunContract(ctx context.Context, runID int64, project string) (id, document string, err error)
	RecordSpend(ctx context.Context, costUSD float64) error
	SpendSince(ctx context.Context, since time.Time) (float64, error)
}

type LLM interface {
	CreateMessage(ctx context.Context, req llm.Request) (*llm.Response, error)
}

type Analyzer struct {
	Store   Store
	LLM     LLM // nil = AI mati; aturan dan label manual tetap jalan
	Model   string
	Pricing llm.Pricing
	Log     *slog.Logger

	Contracts  Contracts // nil = tanpa kontrak API
	Embed      Embedder  // nil = tanpa kasus mirip
	EmbedModel string
	embedMu    sync.Mutex // satu pemanggilan Voyage pada satu waktu (rate limit akun gratis rendah)

	// DailyBudgetUSD: batas biaya AI per hari, USD (0 = tanpa batas). Biaya dicatat di Store
	// setiap kali AI menjawab, jadi analisis ulang tetap terhitung.
	DailyBudgetUSD float64
}

// Analyze menentukan penyebab satu kelompok kegagalan. Urutannya, dari bukti terkuat:
//  1. label manual (kebenaran akhir)
//  2. bukti eksperimen: test lulus setelah patch, atau lulus saat diulang (kecuali force)
//  3. aturan: bawaan, yang dipelajari, lalu matriks perubahan. Selalu dihitung ulang (murah),
//     jadi aturan yang baru disetujui langsung berlaku
//  4. cache analisis AI untuk versi prompt sekarang (kecuali force)
//  5. AI
//
// cached bernilai true kalau hasil diambil dari cache (tidak ada biaya).
func (a *Analyzer) Analyze(ctx context.Context, fingerprint string, force bool) (*store.Analysis, bool, error) {
	facts, err := a.facts(ctx, fingerprint)
	if err != nil {
		return nil, false, err
	}
	res, cached, err := a.decide(ctx, fingerprint, facts, force)
	if res != nil {
		res.Similar = facts.Similar // supaya pengguna melihat referensi yang sama dengan yang dilihat AI
	}
	return res, cached, err
}

// AnalyzeLocal menjalankan bagian yang tidak butuh AI: label manual, bukti eksperimen, lalu aturan.
// Kalau tidak ada yang cocok, hasilnya source "none" dengan next step untuk menjalankan AI.
// Dipakai reporter tanpa REDLINE_AI=1 supaya rule tetap bekerja dan failure yang belum tertangani terlihat.
func (a *Analyzer) AnalyzeLocal(ctx context.Context, fingerprint string, force bool) (*store.Analysis, error) {
	facts, err := a.facts(ctx, fingerprint)
	if err != nil {
		return nil, err
	}
	if label := facts.Group.HumanLabel; label != "" {
		summary := facts.Group.HumanNote
		if summary == "" {
			summary = "Dilabeli manual."
		}
		return &store.Analysis{Fingerprint: fingerprint, PromptVersion: PromptVersion, Source: "human",
			Category: label, Confidence: "high", Summary: summary, Evidence: []string{}}, nil
	}
	if !force {
		if proof, err := a.Store.LatestProof(ctx, fingerprint); err == nil {
			res := fromProof(proof)
			res.Fingerprint = fingerprint
			res.PromptVersion = PromptVersion
			return res, nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	learned, err := a.Store.ActiveRules(ctx)
	if err != nil {
		return nil, err
	}
	res := ApplyRules(facts, learned...)
	if res == nil {
		// Analisis AI yang sudah ada tetap dipakai saat run tanpa AI; jangan ditimpa needs_ai.
		if hit, err := a.Store.GetAnalysis(ctx, fingerprint, PromptVersion); err == nil && hit.Source == "ai" {
			return &hit, nil
		}
		res = needsAIAnalysis(fingerprint)
	} else {
		res.Fingerprint = fingerprint
		res.PromptVersion = PromptVersion
	}
	res.CreatedAt = time.Now()
	if err := a.saveLocal(ctx, res); err != nil {
		return nil, err
	}
	return res, nil
}

func needsAIAnalysis(fingerprint string) *store.Analysis {
	return &store.Analysis{
		Fingerprint: fingerprint, PromptVersion: PromptVersion, Source: "none",
		Category: "unknown", Confidence: "none",
		Summary:  "Belum ada rule yang cocok dan AI belum menganalisis kegagalan ini.",
		Evidence: []string{},
		NextStep: "Jalankan REDLINE_AI=1 npx playwright test, atau npx redline analyze --run <id> setelah ANTHROPIC_API_KEY diisi.",
	}
}

func (a *Analyzer) saveLocal(ctx context.Context, res *store.Analysis) error {
	if err := a.Store.SaveAnalysis(ctx, *res); err != nil {
		return err
	}
	// Tebakan rule/eksperimen tetap masuk rapor; source none/human diabaikan oleh SavePrediction.
	_ = a.Store.SavePrediction(ctx, *res)
	return nil
}

func (a *Analyzer) decide(ctx context.Context, fingerprint string, facts *store.Facts, force bool) (res *store.Analysis, cached bool, err error) {
	if label := facts.Group.HumanLabel; label != "" {
		summary := facts.Group.HumanNote
		if summary == "" {
			summary = "Dilabeli manual."
		}
		return &store.Analysis{
			Fingerprint: fingerprint, Source: "human", Category: label, Confidence: "high",
			Summary: summary, Evidence: []string{},
		}, false, nil
	}
	// force (misalnya kelompok regressed): bukti lama belum tentu berlaku, jadi dilewati.
	if !force {
		if proof, err := a.Store.LatestProof(ctx, fingerprint); err == nil {
			return fromProof(proof), false, nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, false, err
		}
	}

	learned, err := a.Store.ActiveRules(ctx)
	if err != nil {
		return nil, false, err
	}
	res = ApplyRules(facts, learned...)
	// Aturan yang belum yakin (misalnya "kode test berubah") tidak menghentikan AI: AI membaca kodenya dan bisa
	// menunjuk kesalahan yang spesifik. Hasil aturan dipakai lagi kalau AI mati atau gagal.
	var fallback *store.Analysis
	if res != nil && res.Confidence != "high" && a.LLM != nil {
		fallback, res = res, nil
	}
	if res == nil && !force {
		// Hanya hasil AI yang diambil dari cache; hasil aturan lama bisa saja sudah tidak berlaku.
		if hit, err := a.Store.GetAnalysis(ctx, fingerprint, PromptVersion); err == nil && hit.Source == "ai" {
			return &hit, true, nil
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, false, err
		}
	}
	if res == nil {
		if a.LLM == nil {
			return nil, false, ErrAIDisabled
		}
		if err := a.checkBudget(ctx); err != nil {
			if fallback == nil {
				return nil, false, err
			}
			a.logger().Warn("batas biaya AI tercapai, memakai hasil aturan", "fingerprint", fingerprint, "err", err)
			res = fallback
		} else if res, err = a.ask(ctx, facts); err != nil {
			if fallback == nil {
				return nil, false, err
			}
			a.logger().Warn("AI gagal, memakai hasil aturan", "fingerprint", fingerprint, "err", err)
			res = fallback
		}
	}
	res.Fingerprint = fingerprint
	res.PromptVersion = PromptVersion
	res.CreatedAt = time.Now()
	// Biaya dicatat terpisah dari Analysis, karena SaveAnalysis menimpa analisis lama dan analisis
	// ulang (?force=1) tetap harus terhitung di batas harian.
	if res.Source == "ai" && res.CostUSD > 0 {
		if err := a.Store.RecordSpend(ctx, res.CostUSD); err != nil {
			a.logger().Warn("gagal mencatat biaya AI", "err", err)
		}
	}
	if err := a.Store.SaveAnalysis(ctx, *res); err != nil {
		return nil, false, fmt.Errorf("simpan analisis: %w", err)
	}
	// Rapor: tebakan dicatat supaya nanti bisa dibandingkan dengan bukti. Gagal mencatat tidak menggagalkan analisis.
	if err := a.Store.SavePrediction(ctx, *res); err != nil {
		a.logger().Warn("gagal mencatat tebakan", "fingerprint", fingerprint, "err", err)
	}
	// Candidate hanya draft; tidak dipakai runtime sampai terbukti dan lolos backtest.
	if res.Source == "ai" && res.RuleCandidate != nil {
		if _, err := a.Store.SaveRuleCandidate(ctx, *res.RuleCandidate); err != nil {
			a.logger().Warn("gagal menyimpan rule candidate", "fingerprint", fingerprint, "err", err)
		}
	}
	return res, false, nil
}

// checkBudget menolak panggilan AI kalau biaya hari ini sudah mencapai batas. Kalau query gagal,
// analisis tetap jalan (lebih baik daripada memblokir karena masalah hitung).
func (a *Analyzer) checkBudget(ctx context.Context) error {
	if a.DailyBudgetUSD <= 0 {
		return nil
	}
	now := time.Now()
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	spent, err := a.Store.SpendSince(ctx, since)
	if err != nil {
		a.logger().Warn("gagal menghitung biaya AI hari ini; batas dilewati", "err", err)
		return nil
	}
	if spent >= a.DailyBudgetUSD {
		return fmt.Errorf("%w: $%.4f dari $%.2f", ErrBudgetExceeded, spent, a.DailyBudgetUSD)
	}
	return nil
}

func (a *Analyzer) ask(ctx context.Context, f *store.Facts) (*store.Analysis, error) {
	factsText := BuildFacts(f)
	resp, err := a.LLM.CreateMessage(ctx, llm.Request{
		Model:     a.Model,
		MaxTokens: 1024,
		System:    systemPrompt,
		Messages:  []llm.Message{{Role: "user", Content: "FAKTA:\n\n" + factsText + References(f)}},
		Tools: []llm.Tool{{
			Name:        toolName,
			Description: "Laporkan kesimpulan penyebab kegagalan test.",
			InputSchema: verdictTool,
		}},
		ToolChoice: &llm.ToolChoice{Type: "tool", Name: toolName},
	})
	if err != nil {
		return nil, err
	}
	raw, ok := resp.ToolInput(toolName)
	if !ok {
		return nil, fmt.Errorf("AI tidak memanggil %s (stop_reason=%s)", toolName, resp.StopReason)
	}
	var v verdict
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("jawaban AI tidak valid: %w", err)
	}
	if !store.ValidCategory(v.Category) {
		v.Category = "unknown"
	}
	// Kategori harus sejalan dengan pihak yang menyimpang. Kalau bertentangan, pihak yang menyimpang dipakai
	// (AI menilainya sebelum memilih kategori) dan keyakinan diturunkan.
	if want, ok := map[string]string{"test": "test_bug", "api": "backend_bug", "environment": "environment"}[v.Deviates]; ok &&
		v.Category != want && v.Category != "flaky" {
		a.logger().Warn("kategori AI bertentangan dengan pihak yang menyimpang, dikoreksi",
			"fingerprint", f.Group.Fingerprint, "deviates", v.Deviates, "category", v.Category)
		v.Category, v.Confidence = want, "low"
	}
	if v.Confidence != "low" && v.Confidence != "medium" && v.Confidence != "high" {
		v.Confidence = "low"
	}
	if strings.TrimSpace(v.Summary) == "" {
		return nil, fmt.Errorf("AI tidak memberi summary")
	}

	// Anti-halusinasi: bukti harus benar-benar ada di fakta. Bukti karangan dibuang,
	// dan kalau tidak tersisa satu pun, tingkat keyakinan diturunkan.
	evidence := grounded(v.Evidence, factsText)
	if len(evidence) == 0 && v.Confidence != "low" {
		a.logger().Warn("bukti AI tidak ditemukan di fakta, confidence diturunkan", "fingerprint", f.Group.Fingerprint, "evidence", v.Evidence)
		v.Confidence = "low"
	}

	model := resp.Model
	if model == "" {
		model = a.Model
	}
	return &store.Analysis{
		Source:        "ai",
		Model:         model,
		Category:      v.Category,
		Confidence:    v.Confidence,
		Summary:       strings.TrimSpace(v.Summary),
		Evidence:      evidence,
		NextStep:      strings.TrimSpace(v.NextStep),
		RuleCandidate: candidateFromVerdict(v, f, model),
		InputTokens:   resp.Usage.InputTokens,
		OutputTokens:  resp.Usage.OutputTokens,
		CostUSD:       a.Pricing.Cost(resp.Usage),
	}, nil
}

// candidateFromVerdict hanya menyimpan usulan yang bentuknya aman. Kualitas pola tetap diuji
// oleh backtest saat penyebabnya terbukti; candidate sendiri tidak pernah dipakai runtime.
func candidateFromVerdict(v verdict, f *store.Facts, model string) *store.RuleCandidate {
	c := v.RuleCandidate
	if c == nil || v.Category == "unknown" || strings.TrimSpace(c.Pattern) == "" {
		return nil
	}
	re, err := regexp.Compile(c.Pattern)
	if err != nil || re.MatchString("") {
		return nil
	}
	summary := strings.TrimSpace(c.Summary)
	if summary == "" {
		summary = strings.TrimSpace(v.Summary)
	}
	next := strings.TrimSpace(c.NextStep)
	if next == "" {
		next = strings.TrimSpace(v.NextStep)
	}
	test := f.Group.TestKey
	if test == "" {
		test = strings.Join([]string{f.Group.Project, f.Group.File, f.Group.Title}, " › ")
	}
	return &store.RuleCandidate{
		Fingerprint: f.Group.Fingerprint, Test: test, Pattern: c.Pattern, Category: v.Category,
		Summary: summary, NextStep: next, Status: "draft", Source: "ai", Model: model,
		PromptVersion: PromptVersion, Confidence: v.Confidence,
	}
}

// fromProof mengubah eksperimen yang lulus menjadi kesimpulan.
func fromProof(e store.Experiment) *store.Analysis {
	res := &store.Analysis{
		Fingerprint: e.GroupID,
		Source:      "experiment",
		Category:    e.Category,
		CreatedAt:   e.CreatedAt,
	}
	if e.Kind == "patch" {
		res.Confidence = "high"
		res.Summary = "Terbukti lewat eksperimen: " + strings.TrimSpace(e.Hypothesis) +
			" Test lulus setelah patch diterapkan di salinan project."
		res.Evidence = []string{fmt.Sprintf("Eksperimen #%d: test lulus %d dari %d kali setelah patch", e.ID, e.Passes, e.Runs)}
		res.NextStep = "Review patch-nya, terapkan dengan `git apply`, lalu commit. Kalau patch ini justru menutupi bug, beri label manual."
		res.Patch = e.Patch
		return res
	}
	res.Category = "flaky"
	res.Confidence = "medium"
	res.Summary = fmt.Sprintf("Test lulus %d dari %d kali saat dijalankan ulang tanpa perubahan apa pun. "+
		"Kegagalannya tidak konsisten: kemungkinan flaky, atau kondisi sesaat seperti data, timing, atau service yang sempat bermasalah.", e.Passes, e.Runs)
	res.Evidence = []string{fmt.Sprintf("Eksperimen #%d: lulus %d dari %d kali tanpa perubahan", e.ID, e.Passes, e.Runs)}
	res.NextStep = "Cari ketergantungan pada data bersama, urutan test, atau waktu. Jalankan dengan --repeat-each=10 untuk melihat seberapa sering gagal."
	return res
}

func (a *Analyzer) logger() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.Default()
}

// grounded menyimpan hanya bukti yang muncul di teks fakta (spasi diabaikan).
func grounded(evidence []string, facts string) []string {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	haystack := norm(facts)
	out := []string{}
	for _, e := range evidence {
		e = strings.TrimSpace(e)
		if e != "" && len(out) < 4 && strings.Contains(haystack, norm(e)) {
			out = append(out, e)
		}
	}
	return out
}

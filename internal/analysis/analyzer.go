package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"redline/internal/llm"
	"redline/internal/store"
)

// ErrAIDisabled: aturan tidak cocok dan ANTHROPIC_API_KEY belum diisi.
var ErrAIDisabled = errors.New("analisis AI belum aktif: isi ANTHROPIC_API_KEY di .env lalu restart server")

type Store interface {
	Facts(ctx context.Context, fingerprint string) (*store.Facts, error)
	GetAnalysis(ctx context.Context, fingerprint, promptVersion string) (store.Analysis, error)
	SaveAnalysis(ctx context.Context, a store.Analysis) error
	LatestProof(ctx context.Context, groupID string) (store.Experiment, error)
	ActiveRules(ctx context.Context) ([]store.LearnedRule, error)
	ListRules(ctx context.Context, status string) ([]store.LearnedRule, error)
	SaveRule(ctx context.Context, r store.LearnedRule) (int64, error)
	ConfirmedCases(ctx context.Context, limit int) ([]store.Case, error)
	RecentGroupErrors(ctx context.Context, limit int) ([]store.GroupError, error)
	SavePrediction(ctx context.Context, a store.Analysis) error
	PendingEmbeddings(ctx context.Context, model string, limit int) ([]store.GroupError, error)
	SaveEmbeddings(ctx context.Context, model string, ids []string, vectors [][]float32) error
	SimilarCases(ctx context.Context, groupID, model string, minSimilarity float64, limit int) ([]store.SimilarCase, error)
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

	Embed      Embedder // nil = tanpa kasus mirip
	EmbedModel string
	embedMu    sync.Mutex // satu pemanggilan Voyage pada satu waktu (rate limit akun gratis rendah)
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
		if res, err = a.ask(ctx, facts); err != nil {
			return nil, false, err
		}
	}
	res.Fingerprint = fingerprint
	res.PromptVersion = PromptVersion
	res.CreatedAt = time.Now()
	if err := a.Store.SaveAnalysis(ctx, *res); err != nil {
		return nil, false, fmt.Errorf("simpan analisis: %w", err)
	}
	// Rapor: tebakan dicatat supaya nanti bisa dibandingkan dengan bukti. Gagal mencatat tidak menggagalkan analisis.
	if err := a.Store.SavePrediction(ctx, *res); err != nil {
		a.logger().Warn("gagal mencatat tebakan", "fingerprint", fingerprint, "err", err)
	}
	return res, false, nil
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
		Source:       "ai",
		Model:        model,
		Category:     v.Category,
		Confidence:   v.Confidence,
		Summary:      strings.TrimSpace(v.Summary),
		Evidence:     evidence,
		NextStep:     strings.TrimSpace(v.NextStep),
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		CostUSD:      a.Pricing.Cost(resp.Usage),
	}, nil
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

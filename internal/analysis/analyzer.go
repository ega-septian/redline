package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
}

// Analyze menentukan penyebab satu kelompok kegagalan. Urutannya:
//  1. label manual (kebenaran akhir)
//  2. cache untuk versi prompt sekarang (kecuali force)
//  3. aturan deterministik
//  4. AI
//
// cached bernilai true kalau hasil diambil dari cache (tidak ada biaya).
func (a *Analyzer) Analyze(ctx context.Context, fingerprint string, force bool) (res *store.Analysis, cached bool, err error) {
	facts, err := a.Store.Facts(ctx, fingerprint)
	if err != nil {
		return nil, false, err
	}
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
	if !force {
		if hit, err := a.Store.GetAnalysis(ctx, fingerprint, PromptVersion); err == nil {
			return &hit, true, nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return nil, false, err
		}
	}

	res = ApplyRules(facts)
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
	return res, false, nil
}

func (a *Analyzer) ask(ctx context.Context, f *store.Facts) (*store.Analysis, error) {
	factsText := BuildFacts(f)
	resp, err := a.LLM.CreateMessage(ctx, llm.Request{
		Model:     a.Model,
		MaxTokens: 1024,
		System:    systemPrompt,
		Messages:  []llm.Message{{Role: "user", Content: "FAKTA:\n\n" + factsText}},
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

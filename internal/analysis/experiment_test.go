package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"redline/internal/llm"
	"redline/internal/store"
)

// toolLLM menjawab tool apa pun yang diminta dengan input yang sudah disiapkan per nama tool.
type toolLLM struct {
	inputs  map[string]any
	calls   int
	lastReq llm.Request
}

func (f *toolLLM) CreateMessage(_ context.Context, req llm.Request) (*llm.Response, error) {
	f.calls++
	f.lastReq = req
	name := req.ToolChoice.Name
	input, _ := json.Marshal(f.inputs[name])
	return &llm.Response{
		Model:   "claude-haiku-5-5",
		Content: []llm.ContentBlock{{Type: "tool_use", Name: name, Input: input}},
		Usage:   llm.Usage{InputTokens: 2000, OutputTokens: 300},
	}, nil
}

func TestProposeFix(t *testing.T) {
	s, fps := setup(t)
	ctx := context.Background()
	fake := &toolLLM{inputs: map[string]any{fixToolName: map[string]any{
		"category": "test_bug", "confidence": "high", "hypothesis": "response.json() tidak di-await.",
		"edits": []map[string]string{
			{"path": "tests/order.spec.ts", "find": "parse(response.json())", "replace": "parse(await response.json())"},
			{"path": "playwright.config.ts", "find": "retries: 0", "replace": "retries: 5"}, // file yang tidak dikirim
		},
	}}}
	a := &Analyzer{Store: s, LLM: fake}
	files := []SourceFile{{Path: "tests/order.spec.ts", Content: "const token = \"Bearer abc.def\";\nparse(response.json())"}}
	attempts := []Attempt{{Hypothesis: "matcher salah", Patch: "-a\n+b", Result: "Expected: 1"}}

	fix, err := a.ProposeFix(ctx, fps["order dibayar"], files, attempts)
	if err != nil {
		t.Fatal(err)
	}
	if fix.Category != "test_bug" || len(fix.Edits) != 1 || fix.Edits[0].Path != "tests/order.spec.ts" {
		t.Fatalf("hanya edit ke file yang dikirim yang boleh lolos: %+v", fix)
	}
	prompt := fake.lastReq.Messages[0].Content
	for _, want := range []string{"=== tests/order.spec.ts ===", "PERCOBAAN SEBELUMNYA", "matcher salah", "Pesan error:"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt harus memuat %q", want)
		}
	}
	if strings.Contains(prompt, "Bearer abc.def") {
		t.Errorf("token di kode test harus disamarkan sebelum dikirim ke AI")
	}

	// Bukan salah test: patch dibuang.
	fake.inputs[fixToolName] = map[string]any{"category": "backend_bug", "confidence": "high", "hypothesis": "API berubah.",
		"edits": []map[string]string{{"path": "tests/order.spec.ts", "find": "PAID", "replace": "PENDING"}}}
	if fix, err := a.ProposeFix(ctx, fps["order dibayar"], files, nil); err != nil || len(fix.Edits) != 0 {
		t.Fatalf("backend_bug tidak boleh membawa patch: %+v %v", fix, err)
	}
	if _, err := a.ProposeFix(ctx, fps["order dibayar"], nil, nil); !errors.Is(err, ErrBadFixInput) {
		t.Errorf("tanpa file harus ErrBadFixInput, dapat %v", err)
	}
}

func TestAnalyze_ExperimentProof(t *testing.T) {
	s, fps := setup(t)
	ctx := context.Background()
	fp := fps["order dibayar"]
	fake := &fakeLLM{verdict: verdict{Category: "backend_bug", Confidence: "low", Summary: "Mungkin API.", Evidence: []string{}}}
	a := &Analyzer{Store: s, LLM: fake}

	// Rerun lulus 1 dari 2 -> flaky (medium), tanpa AI.
	if _, err := s.SaveExperiment(ctx, store.Experiment{GroupID: fp, Kind: "rerun", Outcome: "passed", Runs: 2, Passes: 1}); err != nil {
		t.Fatal(err)
	}
	res, _, err := a.Analyze(ctx, fp, false)
	if err != nil || res.Source != "experiment" || res.Category != "flaky" || fake.calls != 0 {
		t.Fatalf("rerun lulus harus jadi bukti flaky: %+v %v", res, err)
	}

	// Rerun berikutnya gagal: bukti flaky lama tidak berlaku lagi -> AI.
	if _, err := s.SaveExperiment(ctx, store.Experiment{GroupID: fp, Kind: "rerun", Outcome: "failed", Runs: 2}); err != nil {
		t.Fatal(err)
	}
	if res, _, err = a.Analyze(ctx, fp, false); err != nil || res.Source != "ai" {
		t.Fatalf("rerun gagal terbaru harus membatalkan bukti flaky: %+v %v", res, err)
	}

	// Patch terbukti -> test_bug high dengan patch, mengalahkan cache AI.
	patch := "--- a/tests/order.spec.ts\n+++ b/tests/order.spec.ts\n-parse(response.json())\n+parse(await response.json())"
	if _, err := s.SaveExperiment(ctx, store.Experiment{GroupID: fp, Kind: "patch", Outcome: "passed", Category: "test_bug",
		Hypothesis: "response.json() tidak di-await.", Patch: patch, Runs: 1, Passes: 1}); err != nil {
		t.Fatal(err)
	}
	res, _, err = a.Analyze(ctx, fp, false)
	if err != nil || res.Source != "experiment" || res.Category != "test_bug" || res.Confidence != "high" || res.Patch != patch {
		t.Fatalf("patch yang lulus harus jadi bukti terkuat: %+v %v", res, err)
	}
	// force (regressed): bukti lama dilewati.
	if res, _, err = a.Analyze(ctx, fp, true); err != nil || res.Source != "ai" {
		t.Fatalf("force harus melewati bukti lama: %+v %v", res, err)
	}
	if _, err := s.SaveExperiment(ctx, store.Experiment{GroupID: "tidakada", Kind: "rerun", Outcome: "failed", Runs: 1}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("kelompok tidak ada harus ErrNotFound, dapat %v", err)
	}
}

func TestProposeRules_LearnApproveApply(t *testing.T) {
	s, fps := setup(t)
	ctx := context.Background()
	fp := fps["order dibayar"]
	fake := &toolLLM{inputs: map[string]any{learnToolName: map[string]any{"rules": []map[string]any{
		{"pattern": `Expected: "PAID"`, "category": "test_bug", "summary": "Status order yang diharapkan sudah usang.",
			"next_step": "Sesuaikan status yang diharapkan.", "case_ids": []string{fp}},
		{"pattern": `(?i)expected`, "category": "test_bug", "summary": "Terlalu umum.", "next_step": "-", "case_ids": []string{fp}},
	}}}}
	a := &Analyzer{Store: s, LLM: fake}

	if _, err := a.ProposeRules(ctx); !errors.Is(err, ErrNothingToLearn) {
		t.Fatalf("tanpa kasus terbukti harus ErrNothingToLearn, dapat %v", err)
	}
	if _, err := s.SaveExperiment(ctx, store.Experiment{GroupID: fp, Kind: "patch", Outcome: "passed", Category: "test_bug",
		Hypothesis: "Status yang diharapkan usang.", Runs: 1, Passes: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLabel(ctx, fps["register user"], "backend_bug", "500 saat register", "ega"); err != nil {
		t.Fatal(err)
	}

	res, err := a.ProposeRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cases != 1 || len(res.Proposed) != 1 || len(res.Rejected) != 1 {
		t.Fatalf("mau 1 kasus baru (register sudah ditangani aturan 5xx), 1 usulan, 1 ditolak: %+v", res)
	}
	if !strings.Contains(res.Rejected[0].Reason, "terbukti backend_bug") {
		t.Errorf("pola yang cocok dengan kasus berkategori lain harus ditolak: %+v", res.Rejected[0])
	}
	if !strings.Contains(fake.lastReq.Messages[0].Content, "id: "+fp) {
		t.Errorf("prompt harus memuat kasus terbukti")
	}
	rule := res.Proposed[0]
	if rule.Status != "proposed" || rule.Hits != 1 || rule.LearnedFrom[0] != fp {
		t.Fatalf("usulan salah: %+v", rule)
	}

	// Belum disetujui: belum dipakai.
	facts, _ := s.Facts(ctx, fp)
	if active, _ := s.ActiveRules(ctx); ApplyRules(facts, active...) != nil {
		t.Fatalf("usulan yang belum disetujui tidak boleh dipakai")
	}
	if _, err := s.DecideRule(ctx, rule.ID, "active", "ega"); err != nil {
		t.Fatal(err)
	}
	active, _ := s.ActiveRules(ctx)
	if got := ApplyRules(facts, active...); got == nil || got.Category != "test_bug" || !strings.Contains(got.Summary, "usang") {
		t.Fatalf("aturan aktif harus memutuskan kasus ini tanpa AI: %+v", got)
	}

	// Putaran berikutnya: kasus sudah ditangani aturan -> tidak ada yang dipelajari.
	if _, err := a.ProposeRules(ctx); !errors.Is(err, ErrNothingToLearn) {
		t.Errorf("kasus yang sudah ditangani aturan tidak boleh diusulkan lagi, dapat %v", err)
	}

	// Aturan ditolak: kasusnya boleh dipelajari lagi, dan pola yang ditolak dikirim ke AI.
	if _, err := s.DecideRule(ctx, rule.ID, "rejected", "ega"); err != nil {
		t.Fatal(err)
	}
	res, err = a.ProposeRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fake.lastReq.Messages[0].Content, "SUDAH DITOLAK") || len(res.Proposed) != 0 {
		t.Errorf("pola yang ditolak harus dikirim ke AI dan tidak tersimpan dua kali: %+v", res)
	}

	// Variasi kecil dari pola yang ditolak (hasil cocoknya sama persis) juga ditolak.
	fake.inputs[learnToolName] = map[string]any{"rules": []map[string]any{
		{"pattern": `(?i)expected: "paid"`, "category": "test_bug", "summary": "Sama saja.", "next_step": "-", "case_ids": []string{fp}},
	}}
	if res, err = a.ProposeRules(ctx); err != nil || len(res.Rejected) != 1 ||
		!strings.Contains(res.Rejected[0].Reason, "sudah ditolak") {
		t.Errorf("variasi pola yang ditolak harus ditolak: %+v %v", res, err)
	}
}

package analysis

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"redline/internal/embed"
	"redline/internal/store"
)

func TestFacts_CodeMapAndSimilar(t *testing.T) {
	s, fps := setup(t)
	ctx := context.Background()

	// Run ketiga: semua test memakai schema yang sama (peta kode dari reporter).
	rep := load(t, "run1-bug.json")
	rep.TestFiles = map[string][]string{}
	for _, o := range rep.Outcomes() {
		rep.TestFiles[fmt.Sprintf("%s:%d", o.File, o.Line)] = []string{o.File, "tests/shared.schema.ts"}
	}
	if _, err := s.IngestReport(ctx, rep, store.RunMeta{}); err != nil {
		t.Fatal(err)
	}
	f, err := s.Facts(ctx, fps["order dibayar"])
	if err != nil {
		t.Fatal(err)
	}
	var shared *store.CodeLink
	for i, l := range f.CodeMap {
		if l.Name == "tests/shared.schema.ts" {
			shared = &f.CodeMap[i]
		}
	}
	// run1-bug: selain "order dibayar" ada 1 lulus, 1 flaky, 2 gagal, 1 skip (skip tidak dihitung).
	if shared == nil || shared.Passed != 2 || shared.Failed != 2 {
		t.Fatalf("peta kode salah: %+v", f.CodeMap)
	}
	if text := BuildFacts(f); !strings.Contains(text, "file tests/shared.schema.ts: 2 test lain lulus, 2 gagal") {
		t.Errorf("peta kode harus masuk fakta:\n%s", text)
	}

	if len(f.Similar) != 0 {
		t.Errorf("store tidak mengisi kasus mirip; itu tugas analyzer: %+v", f.Similar)
	}
}

// fakeEmbedder: teks yang menyebut PAID atau slug dianggap bermakna sama, sisanya berbeda.
type fakeEmbedder struct {
	calls int
	texts int
}

func (e *fakeEmbedder) Embed(_ context.Context, texts []string) (*embed.Result, error) {
	e.calls++
	e.texts += len(texts)
	res := &embed.Result{Tokens: 10 * len(texts)}
	for _, t := range texts {
		if strings.Contains(t, "PAID") || strings.Contains(t, "slug") {
			res.Vectors = append(res.Vectors, []float32{1, 0})
		} else {
			res.Vectors = append(res.Vectors, []float32{0, 1})
		}
	}
	return res, nil
}

func TestSimilarCases_Embedding(t *testing.T) {
	s, fps := setup(t)
	ctx := context.Background()
	emb := &fakeEmbedder{}
	a := &Analyzer{Store: s, Embed: emb, EmbedModel: "fake-1"}

	// Kasus terbukti: order (label manual).
	if err := s.SetLabel(ctx, fps["order dibayar"], "test_bug", "status PAID sudah usang", "ega"); err != nil {
		t.Fatal(err)
	}
	f, err := a.facts(ctx, fps["brands punya slug"])
	if err != nil {
		t.Fatal(err)
	}
	if emb.calls != 1 || emb.texts != 3 {
		t.Fatalf("semua kelompok harus di-embed dalam satu request: calls=%d texts=%d", emb.calls, emb.texts)
	}
	if len(f.Similar) != 1 || f.Similar[0].GroupID != fps["order dibayar"] || f.Similar[0].Similarity < 0.99 {
		t.Fatalf("kasus mirip salah: %+v", f.Similar)
	}
	if ref := References(f); !strings.Contains(ref, "referensi, bukan bukti") || !strings.Contains(ref, "status PAID sudah usang") {
		t.Errorf("referensi salah:\n%s", ref)
	}

	// Embedding tidak dibuat ulang; makna berbeda (register) dan kelompok itu sendiri tidak ikut.
	if f, _ = a.facts(ctx, fps["register user"]); len(f.Similar) != 0 || emb.calls != 1 {
		t.Errorf("register tidak mirip, dan tidak boleh embed ulang: %+v calls=%d", f.Similar, emb.calls)
	}
	if f, _ = a.facts(ctx, fps["order dibayar"]); len(f.Similar) != 0 {
		t.Errorf("kelompok itu sendiri tidak boleh jadi referensi: %+v", f.Similar)
	}

	// Ganti model: embedding lama tidak dibandingkan, semua di-embed ulang.
	a.EmbedModel = "fake-2"
	if sim, _ := s.SimilarCases(ctx, fps["brands punya slug"], "fake-2", minSimilarity, 3); len(sim) != 0 {
		t.Errorf("embedding model lain tidak boleh dibandingkan: %+v", sim)
	}
	if n, err := a.EmbedPending(ctx); err != nil || n != 3 || emb.calls != 2 {
		t.Errorf("ganti model harus embed ulang semua: n=%d calls=%d err=%v", n, emb.calls, err)
	}

	// Tanpa Voyage: analisis tetap jalan, tanpa kasus mirip.
	off := &Analyzer{Store: s}
	if f, err := off.facts(ctx, fps["brands punya slug"]); err != nil || len(f.Similar) != 0 {
		t.Errorf("tanpa embedder tidak ada kasus mirip: %+v %v", f.Similar, err)
	}
}

func TestScoreboard(t *testing.T) {
	s, fps := setup(t, "order dibayar") // kode test order diubah -> aturan matriks menebak test_bug
	ctx := context.Background()
	fake := &fakeLLM{verdict: verdict{Category: "backend_bug", Confidence: "high", Summary: "API salah.",
		Evidence: []string{"Kode test berubah: tidak"}}}
	a := &Analyzer{Store: s, LLM: fake}

	// Tebakan: aturan untuk order (test_bug) dan brands (backend_bug); dianalisis dua kali, dicatat sekali.
	for range 2 {
		for _, name := range []string{"order dibayar", "brands punya slug"} {
			if _, _, err := a.Analyze(ctx, fps[name], false); err != nil {
				t.Fatal(err)
			}
		}
	}
	board, err := s.Scores(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(board.Entries) != 0 {
		t.Fatalf("belum ada bukti, belum ada yang dinilai: %+v", board.Entries)
	}

	// Bukti: order terbukti test_bug (aturan benar), brands dilabeli test_bug (aturan salah).
	if _, err := s.SaveExperiment(ctx, store.Experiment{GroupID: fps["order dibayar"], Kind: "patch", Outcome: "passed",
		Category: "test_bug", Runs: 1, Passes: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLabel(ctx, fps["brands punya slug"], "test_bug", "slug memang dihapus dari kontrak", "ega"); err != nil {
		t.Fatal(err)
	}
	// Analisis sesudah bukti tidak boleh ikut dinilai (sudah tahu jawabannya).
	if _, _, err := a.Analyze(ctx, fps["brands punya slug"], true); err != nil {
		t.Fatal(err)
	}

	board, err = s.Scores(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	rule := board.Sources[0]
	if rule.Source != "rule" || rule.Graded != 2 || rule.Correct != 1 || rule.Accuracy != 0.5 {
		t.Fatalf("rapor aturan salah: %+v\n%+v", rule, board.Entries)
	}
	byGroup := map[string]store.ScoreEntry{}
	for _, e := range board.Entries {
		byGroup[e.GroupID] = e
	}
	if e := byGroup[fps["order dibayar"]]; !e.Correct || e.TruthBy != "eksperimen" {
		t.Errorf("order: %+v", e)
	}
	if e := byGroup[fps["brands punya slug"]]; e.Correct || e.Predicted != "backend_bug" || e.TruthBy != "label" {
		t.Errorf("brands: %+v", e)
	}
	one, _ := s.Scores(ctx, fps["order dibayar"], 0)
	if len(one.Entries) != 1 {
		t.Errorf("filter per kelompok: %+v", one.Entries)
	}
}

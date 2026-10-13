package store_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"redline/internal/store"
)

// TestMemory: kontrak per run, embedding dan kasus mirip, serta retensi.
func TestMemory(t *testing.T) {
	t.Run("Contracts", func(t *testing.T) { testContracts(t, newStore(t)) })
	t.Run("Embeddings", func(t *testing.T) { testEmbeddings(t, newStore(t)) })
	t.Run("SimilarCases", func(t *testing.T) { testSimilarCases(t, newStore(t)) })
	t.Run("PruneKeepsWhatAnalysisNeeds", func(t *testing.T) { testPruneKeeps(t, newStore(t)) })
	t.Run("PruneRemovesOrphans", func(t *testing.T) { testPruneOrphans(t, newStore(t)) })
}

func testContracts(t *testing.T, b *store.Store) {
	ctx := context.Background()
	run1 := ingest(t, b, shopReport(passedTest("a")), store.RunMeta{}).RunID
	run2 := ingest(t, b, shopReport(passedTest("a")), store.RunMeta{}).RunID
	v1, v2 := `{"openapi":"3.0.0","paths":{"/brands":{}}}`, `{"openapi":"3.0.0","paths":{"/brands":{},"/v2":{}}}`

	if _, _, err := b.RunContract(ctx, run1, "toolshop"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("belum ada kontrak: mau ErrNotFound, dapat %v", err)
	}
	id1, err := b.SaveRunContract(ctx, run1, "toolshop", "http://api/docs", v1)
	if err != nil {
		t.Fatal(err)
	}
	// Isi sama di run lain: id sama (disimpan sekali per isi).
	if id, err := b.SaveRunContract(ctx, run2, "toolshop", "http://api/docs", v1); err != nil || id != id1 {
		t.Fatalf("isi sama harus id sama: %s vs %s %v", id, id1, err)
	}
	// Run 2 diganti ke versi baru; run 1 tetap versi lama.
	id2, err := b.SaveRunContract(ctx, run2, "toolshop", "http://api/docs", v2)
	if err != nil || id2 == id1 {
		t.Fatalf("isi berbeda harus id berbeda: %s %v", id2, err)
	}
	if id, doc, err := b.RunContract(ctx, run1, "toolshop"); err != nil || id != id1 || doc != v1 {
		t.Errorf("kontrak run 1: %s %s %v", id, doc, err)
	}
	if id, doc, err := b.RunContract(ctx, run2, "toolshop"); err != nil || id != id2 || doc != v2 {
		t.Errorf("kontrak run 2 harus versi baru: %s %s %v", id, doc, err)
	}
	if _, _, err := b.RunContract(ctx, run1, "project-lain"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("kontrak per project: %v", err)
	}
}

func testEmbeddings(t *testing.T, b *store.Store) {
	ctx := context.Background()
	fps, _, _ := passThenFail(t, b)
	order := fps["order dibayar"]

	if err := b.SetLabel(ctx, order, "test_bug", "", "ega"); err != nil {
		t.Fatal(err)
	}
	// Kasus terbukti didahulukan, karena merekalah yang dicari.
	pending, err := b.PendingEmbeddings(ctx, "m1", 1)
	if err != nil || len(pending) != 1 || pending[0].GroupID != order || pending[0].Error == "" {
		t.Fatalf("kasus terbukti harus didahulukan: %+v %v", pending, err)
	}
	all, _ := b.PendingEmbeddings(ctx, "m1", 10)
	if len(all) != 3 {
		t.Fatalf("mau 3 kelompok tanpa embedding: %+v", all)
	}
	if err := b.SaveEmbeddings(ctx, "m1", []string{order}, nil); err == nil {
		t.Error("jumlah id dan embedding berbeda harus ditolak")
	}
	ids := []string{all[0].GroupID, all[1].GroupID, all[2].GroupID}
	if err := b.SaveEmbeddings(ctx, "m1", ids, [][]float32{{1, 0}, {0, 1}, {1, 1}}); err != nil {
		t.Fatal(err)
	}
	if left, _ := b.PendingEmbeddings(ctx, "m1", 10); len(left) != 0 {
		t.Errorf("semua sudah punya embedding m1: %+v", left)
	}
	if again, _ := b.PendingEmbeddings(ctx, "m2", 10); len(again) != 3 {
		t.Errorf("ganti model: semua di-embed ulang: %+v", again)
	}
}

func testSimilarCases(t *testing.T, b *store.Store) {
	ctx := context.Background()
	fps, _, _ := passThenFail(t, b)
	order, brands, register := fps["order dibayar"], fps["brands punya slug"], fps["register user"]

	// order dan brands terbukti; register yang sedang dicari.
	if err := b.SetLabel(ctx, order, "test_bug", "status PAID sudah usang", "ega"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SaveExperiment(ctx, store.Experiment{GroupID: brands, Kind: "patch", Outcome: "passed",
		Category: "backend_bug", Hypothesis: "slug hilang.", Runs: 1, Passes: 1}); err != nil {
		t.Fatal(err)
	}
	// register: [1,0]. order: sudut 60 derajat (cosine 0.5). brands: tegak lurus (cosine 0).
	if err := b.SaveEmbeddings(ctx, "m1", []string{register, order, brands},
		[][]float32{{1, 0}, {0.5, float32(math.Sqrt(3) / 2)}, {0, 1}}); err != nil {
		t.Fatal(err)
	}

	sim, err := b.SimilarCases(ctx, register, "m1", 0.4, 5)
	if err != nil {
		t.Fatal(err)
	}
	// Similarity harus cosine biasa (-1..1), bukan skor Neo4j (1+cos)/2: tegak lurus = 0, tidak lolos 0.4.
	if len(sim) != 1 || sim[0].GroupID != order || math.Abs(sim[0].Similarity-0.5) > 0.001 {
		t.Fatalf("mau hanya order dengan cosine 0.5: %+v", sim)
	}
	if c := sim[0]; c.Category != "test_bug" || c.Source != "human" || c.Reason != "status PAID sudah usang" ||
		c.Error == "" || c.Test == "" {
		t.Errorf("isi kasus mirip salah: %+v", c)
	}
	if sim, _ = b.SimilarCases(ctx, register, "m1", 0.6, 5); len(sim) != 0 {
		t.Errorf("cosine 0.5 tidak lolos ambang 0.6: %+v", sim)
	}
	if sim, _ = b.SimilarCases(ctx, register, "m1", -1, 5); len(sim) != 2 || sim[0].GroupID != order ||
		sim[1].GroupID != brands || math.Abs(sim[1].Similarity) > 0.001 || sim[1].Source != "experiment" {
		t.Errorf("tanpa ambang: urut paling mirip dulu: %+v", sim)
	}
	if sim, _ = b.SimilarCases(ctx, register, "m1", -1, 1); len(sim) != 1 {
		t.Errorf("limit: %+v", sim)
	}
	// Kelompok itu sendiri tidak ikut, dan kelompok yang belum terbukti tidak jadi referensi.
	if sim, _ = b.SimilarCases(ctx, order, "m1", -1, 5); len(sim) != 1 || sim[0].GroupID != brands {
		t.Errorf("hanya kasus terbukti selain dirinya: %+v", sim)
	}
	if sim, _ = b.SimilarCases(ctx, register, "m2", -1, 5); len(sim) != 0 {
		t.Errorf("embedding model lain tidak boleh dibandingkan: %+v", sim)
	}
}

// cutoff: waktu sesudah run terakhir yang sudah ada, menurut jam database (bukan jam laptop).
func cutoff(t *testing.T, b *store.Store) time.Time {
	t.Helper()
	runs, err := b.ListRuns(context.Background(), 1)
	if err != nil || len(runs) == 0 {
		t.Fatalf("run terakhir: %+v %v", runs, err)
	}
	time.Sleep(20 * time.Millisecond) // run berikutnya pasti sesudah cutoff
	return runs[0].CreatedAt.Add(time.Millisecond)
}

func testPruneKeeps(t *testing.T, b *store.Store) {
	ctx := context.Background()
	ok := `[{"method":"GET","path":"/brands","status":200,"shape":[{"id":"number","slug":"string"}]}]`
	broken := `[{"method":"GET","path":"/brands","status":200,"shape":[{"id":"number"}]}]`
	msg := "expect(received).toEqual(expected)\n- \"slug\": \"x\""

	// 5 run lulus, lalu 5 run gagal dengan bentuk response yang berubah.
	for range 5 {
		ingest(t, b, shopReport(passedWithCalls("brands", ok)), store.RunMeta{})
	}
	for range 5 {
		ingest(t, b, shopReport(failedTest("brands", msg, broken)), store.RunMeta{})
	}
	fp := fingerprint("brands", msg)
	before, err := b.Facts(ctx, fp)
	if err != nil {
		t.Fatal(err)
	}

	res, err := b.Prune(ctx, cutoff(t, b))
	if err != nil {
		t.Fatal(err)
	}
	// Tersisa 3: lulus terakhir, gagal pertama, gagal terakhir. Kedua bentuk response masih dirujuk.
	if res.TestResults != 7 || res.Shapes != 0 {
		t.Errorf("mau 7 hasil terhapus dan 0 bentuk, dapat %+v", res)
	}
	after, err := b.Facts(ctx, fp)
	if err != nil {
		t.Fatal(err)
	}
	if after.LastPass == nil || after.LastPass.RunID != before.LastPass.RunID || after.FirstFail.RunID != before.FirstFail.RunID {
		t.Errorf("pembanding hilang setelah retensi: %+v %+v", after.LastPass, after.FirstFail)
	}
	if after.Response.Verdict != before.Response.Verdict || len(after.CurrentCalls) == 0 {
		t.Errorf("perbandingan response berubah: sebelum %+v, sesudah %+v", before.Response, after.Response)
	}
	if _, occ := group(t, b, fp); len(occ) != 2 {
		t.Errorf("kemunculan tersisa gagal pertama dan terakhir: %+v", occ)
	}

	// Run baru tidak tersentuh. Gagal terakhir yang lama sekarang bukan yang terakhir lagi, jadi ikut terhapus.
	cut := cutoff(t, b)
	newRun := ingest(t, b, shopReport(failedTest("brands", msg, broken)), store.RunMeta{}).RunID
	if res, err = b.Prune(ctx, cut); err != nil || res.TestResults != 1 {
		t.Errorf("prune ulang: %+v %v", res, err)
	}
	if _, occ := group(t, b, fp); len(occ) != 2 || occ[0].RunID != newRun || occ[1].RunID != before.FirstFail.RunID {
		t.Errorf("tersisa run baru dan gagal pertama: %+v", occ)
	}
}

func testPruneOrphans(t *testing.T, b *store.Store) {
	ctx := context.Background()
	msg := "expect(received).toBe(expected)\nExpected: 200\nReceived: 500"
	run := func(version string) {
		calls := `[{"method":"GET","path":"/` + version + `/brands","status":500,"shape":{}}]`
		rep := shopReport(failedTest("brands", msg, calls))
		rep.Sources = map[string]string{"shop.spec.ts": "// kode " + version}
		rep.TestFiles = map[string][]string{}
		for _, o := range rep.Outcomes() {
			rep.TestFiles[fmt.Sprintf("%s:%d", o.File, o.Line)] = []string{o.File}
		}
		ingest(t, b, rep, store.RunMeta{})
	}

	// Tiga kegagalan dengan bentuk response dan kode test yang berbeda. Gagal pertama dan terakhir
	// dibutuhkan analisis; yang tengah tidak, jadi bentuk dan kodenya jadi yatim.
	run("v1")
	run("v2")
	run("v3")
	res, err := b.Prune(ctx, cutoff(t, b))
	if err != nil {
		t.Fatal(err)
	}
	if res.TestResults != 1 || res.Shapes != 1 || res.Sources != 1 {
		t.Errorf("hanya hasil tengah beserta bentuk dan kodenya yang terhapus: %+v", res)
	}
	f, err := b.Facts(ctx, fingerprint("brands", msg))
	if err != nil || len(f.Sources) != 1 || f.Sources[0].Content != "// kode v3" || f.CurrentCalls[0].Path != "/v3/brands" {
		t.Errorf("kemunculan terakhir tetap utuh: %+v %v", f, err)
	}
}

package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"redline/internal/store"
)

// TestProofs: eksperimen, rapor tebakan, aturan yang dipelajari, dan kasus terbukti.
func TestProofs(t *testing.T) {
	t.Run("Experiments", func(t *testing.T) { testExperiments(t, newStore(t)) })
	t.Run("Scoreboard", func(t *testing.T) { testScoreboard(t, newStore(t)) })
	t.Run("Rules", func(t *testing.T) { testRules(t, newStore(t)) })
	t.Run("ConfirmedCases", func(t *testing.T) { testConfirmedCases(t, newStore(t)) })
}

func testExperiments(t *testing.T, b *store.Store) {
	ctx := context.Background()
	fps, _, _ := passThenFail(t, b)
	fp := fps["order dibayar"]

	if _, err := b.LatestProof(ctx, fp); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("belum ada bukti: mau ErrNotFound, dapat %v", err)
	}
	save := func(e store.Experiment) int64 {
		t.Helper()
		e.GroupID = fp
		id, err := b.SaveExperiment(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	// Rerun lulus 1 dari 2: bukti flaky.
	rerun := save(store.Experiment{Kind: "rerun", Outcome: "passed", Runs: 2, Passes: 1, RunBy: "ega"})
	if p, err := b.LatestProof(ctx, fp); err != nil || p.ID != rerun || p.Kind != "rerun" || p.Category != "unknown" {
		t.Fatalf("rerun lulus harus jadi bukti (kategori default unknown): %+v %v", p, err)
	}
	// Rerun berikutnya gagal: bukti flaky lama tidak berlaku lagi.
	save(store.Experiment{Kind: "rerun", Outcome: "failed", Runs: 2, Detail: "Authorization: Bearer secret-token-123"})
	if _, err := b.LatestProof(ctx, fp); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("rerun gagal terbaru membatalkan bukti flaky: %v", err)
	}
	// Patch yang lulus: bukti terkuat, juga mengalahkan rerun lulus yang lebih baru.
	patch := save(store.Experiment{Kind: "patch", Outcome: "passed", Category: "test_bug", Hypothesis: "Status usang.",
		Patch: "-PAID\n+PENDING", Runs: 1, Passes: 1, CostUSD: 0.001})
	save(store.Experiment{Kind: "rerun", Outcome: "passed", Runs: 2, Passes: 2})
	p, err := b.LatestProof(ctx, fp)
	if err != nil || p.ID != patch || p.Kind != "patch" || p.Category != "test_bug" || p.Hypothesis != "Status usang." ||
		p.Patch != "-PAID\n+PENDING" || p.Runs != 1 || p.Passes != 1 || p.CostUSD < 0.00099 || p.CreatedAt.IsZero() {
		t.Fatalf("patch yang lulus harus jadi bukti terkuat: %+v %v", p, err)
	}

	list, err := b.ListExperiments(ctx, fp)
	if err != nil || len(list) != 4 || list[0].Kind != "rerun" || list[0].Passes != 2 || list[3].ID != rerun {
		t.Fatalf("daftar eksperimen harus terbaru dulu: %+v %v", list, err)
	}
	if strings.Contains(list[2].Detail, "secret-token-123") || list[3].RunBy != "ega" {
		t.Errorf("detail harus disamarkan, run_by tersimpan: %+v", list)
	}
	if other, _ := b.ListExperiments(ctx, fps["register user"]); len(other) != 0 {
		t.Errorf("eksperimen kelompok lain tidak boleh ikut: %+v", other)
	}
	if _, err := b.SaveExperiment(ctx, store.Experiment{GroupID: "tidakada", Kind: "rerun", Outcome: "failed", Runs: 1}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("kelompok tidak ada: mau ErrNotFound, dapat %v", err)
	}
}

func testScoreboard(t *testing.T, b *store.Store) {
	ctx := context.Background()
	fps, _, _ := passThenFail(t, b)
	order, brands, register := fps["order dibayar"], fps["brands punya slug"], fps["register user"]
	predict := func(fp, source, category string) {
		t.Helper()
		if err := b.SavePrediction(ctx, store.Analysis{Fingerprint: fp, Source: source, Category: category,
			Confidence: "high", PromptVersion: "v8", Model: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	predict(order, "rule", "test_bug")
	predict(order, "rule", "test_bug") // sama berturut-turut: dicatat sekali
	predict(brands, "rule", "backend_bug")
	predict(brands, "ai", "unknown")
	predict(register, "human", "backend_bug") // bukan tebakan: diabaikan

	board, err := b.Scores(ctx, "", 0)
	if err != nil || len(board.Entries) != 0 || len(board.Sources) != 2 {
		t.Fatalf("belum ada bukti, belum ada yang dinilai: %+v %v", board, err)
	}

	// Bukti: order terbukti test_bug lewat patch (aturan benar), brands dilabeli test_bug (aturan salah).
	if _, err := b.SaveExperiment(ctx, store.Experiment{GroupID: order, Kind: "patch", Outcome: "passed",
		Category: "test_bug", Runs: 1, Passes: 1}); err != nil {
		t.Fatal(err)
	}
	if err := b.SetLabel(ctx, brands, "test_bug", "slug dihapus dari kontrak", "ega"); err != nil {
		t.Fatal(err)
	}
	// Tebakan sesudah bukti tidak ikut dinilai (sudah tahu jawabannya).
	predict(brands, "rule", "test_bug")

	board, err = b.Scores(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	rule, ai := board.Sources[0], board.Sources[1]
	if rule.Source != "rule" || rule.Graded != 2 || rule.Correct != 1 || rule.Accuracy != 0.5 {
		t.Fatalf("rapor aturan salah: %+v\n%+v", rule, board.Entries)
	}
	if ai.Source != "ai" || ai.Graded != 0 || ai.Unknown != 1 {
		t.Errorf("rapor AI: unknown dihitung terpisah: %+v", ai)
	}
	byKey := map[string]store.ScoreEntry{}
	for _, e := range board.Entries {
		byKey[e.GroupID+" "+e.Source] = e
	}
	if e := byKey[order+" rule"]; !e.Correct || e.TruthBy != "eksperimen" || e.ProvenAt == nil || !strings.Contains(e.Test, "order dibayar") {
		t.Errorf("order: %+v", e)
	}
	if e := byKey[brands+" rule"]; e.Correct || e.Predicted != "backend_bug" || e.Truth != "test_bug" || e.TruthBy != "label" {
		t.Errorf("brands: tebakan sebelum label yang dinilai: %+v", e)
	}
	if one, _ := b.Scores(ctx, order, 0); len(one.Entries) != 1 {
		t.Errorf("filter per kelompok: %+v", one.Entries)
	}
	if limited, _ := b.Scores(ctx, "", 1); len(limited.Entries) != 1 || limited.Sources[0].Graded != 2 {
		t.Errorf("limit hanya membatasi daftar, bukan hitungan: %+v", limited)
	}
}

func testRules(t *testing.T, b *store.Store) {
	ctx := context.Background()
	r1, err := b.SaveRule(ctx, store.LearnedRule{Pattern: `received Promise`, Category: "test_bug", Summary: "Lupa await.",
		NextStep: "Tambah await.", LearnedFrom: []string{"a", "b"}, Hits: 2, AlsoMatches: 1, CostUSD: 0.0003})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.SaveRule(ctx, store.LearnedRule{Pattern: `received Promise`, Category: "test_bug", Summary: "Dobel."}); !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("pola dan kategori yang sama: mau ErrDuplicate, dapat %v", err)
	}
	r2, err := b.SaveRule(ctx, store.LearnedRule{Pattern: `received Promise`, Category: "flaky", Summary: "Kategori lain boleh."})
	if err != nil {
		t.Fatal(err)
	}
	r3, err := b.SaveRule(ctx, store.LearnedRule{Pattern: `ECONNRESET`, Category: "environment", Summary: "Koneksi putus."})
	if err != nil || !(r1 < r2 && r2 < r3) {
		t.Fatalf("id aturan harus naik: %d %d %d %v", r1, r2, r3, err)
	}

	all, err := b.ListRules(ctx, "")
	if err != nil || len(all) != 3 || all[0].ID != r3 || all[2].Status != "proposed" || all[2].DecidedAt != nil {
		t.Fatalf("daftar aturan terbaru dulu, status awal proposed: %+v %v", all, err)
	}
	first := all[2]
	if first.Pattern != `received Promise` || first.Summary != "Lupa await." || first.NextStep != "Tambah await." ||
		len(first.LearnedFrom) != 2 || first.Hits != 2 || first.AlsoMatches != 1 || first.CostUSD < 0.00029 || first.CreatedAt.IsZero() {
		t.Errorf("isi aturan salah: %+v", first)
	}

	// Aktifkan r3 lalu r1: ActiveRules mengikuti urutan id (yang lama dulu), supaya hasil stabil.
	if _, err := b.DecideRule(ctx, r3, "active", "ega"); err != nil {
		t.Fatal(err)
	}
	got, err := b.DecideRule(ctx, r1, "active", "budi")
	if err != nil || got.Status != "active" || got.DecidedBy != "budi" || got.DecidedAt == nil || got.Pattern != `received Promise` {
		t.Fatalf("aturan yang diputuskan salah: %+v %v", got, err)
	}
	active, err := b.ActiveRules(ctx)
	if err != nil || len(active) != 2 || active[0].ID != r1 || active[1].ID != r3 {
		t.Fatalf("aturan aktif harus urut id naik: %+v %v", active, err)
	}
	if proposed, _ := b.ListRules(ctx, "proposed"); len(proposed) != 1 || proposed[0].ID != r2 {
		t.Errorf("filter status: %+v", proposed)
	}
	if back, _ := b.DecideRule(ctx, r1, "proposed", "ega"); back.DecidedAt != nil || back.Status != "proposed" {
		t.Errorf("kembali ke proposed menghapus waktu keputusan: %+v", back)
	}
	if _, err := b.DecideRule(ctx, 9999, "active", "ega"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("aturan tidak ada: mau ErrNotFound, dapat %v", err)
	}
}

func testConfirmedCases(t *testing.T, b *store.Store) {
	ctx := context.Background()
	fps, _, _ := passThenFail(t, b)
	order, brands, register := fps["order dibayar"], fps["brands punya slug"], fps["register user"]

	if cases, err := b.ConfirmedCases(ctx, 10); err != nil || len(cases) != 0 {
		t.Fatalf("belum ada kasus terbukti: %+v %v", cases, err)
	}
	// order: patch yang gagal lalu yang lulus -> kasus dari eksperimen dengan hipotesis yang lulus.
	for _, e := range []store.Experiment{
		{GroupID: order, Kind: "patch", Outcome: "failed", Category: "test_bug", Hypothesis: "salah", Runs: 1},
		{GroupID: order, Kind: "patch", Outcome: "passed", Category: "test_bug", Hypothesis: "Status usang.", Runs: 1, Passes: 1},
		{GroupID: brands, Kind: "patch", Outcome: "passed", Category: "test_bug", Hypothesis: "Kalah dari label.", Runs: 1, Passes: 1},
		{GroupID: register, Kind: "rerun", Outcome: "passed", Runs: 2, Passes: 1}, // rerun bukan kasus terbukti
	} {
		if _, err := b.SaveExperiment(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	// brands: label manual menang atas eksperimen.
	if err := b.SetLabel(ctx, brands, "backend_bug", "slug memang hilang", "ega"); err != nil {
		t.Fatal(err)
	}
	cases, err := b.ConfirmedCases(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]store.Case{}
	for _, c := range cases {
		byID[c.GroupID] = c
	}
	if len(cases) != 2 {
		t.Fatalf("mau 2 kasus (order dari eksperimen, brands dari label): %+v", cases)
	}
	if c := byID[order]; c.Source != "experiment" || c.Category != "test_bug" || c.Reason != "Status usang." || c.Error == "" {
		t.Errorf("kasus order salah: %+v", c)
	}
	if c := byID[brands]; c.Source != "human" || c.Category != "backend_bug" || c.Reason != "slug memang hilang" {
		t.Errorf("label manual harus menang atas eksperimen: %+v", c)
	}
	if limited, _ := b.ConfirmedCases(ctx, 1); len(limited) != 1 {
		t.Errorf("limit: %+v", limited)
	}

	errs, err := b.RecentGroupErrors(ctx, 10)
	if err != nil || len(errs) != 3 {
		t.Fatalf("semua kelompok dengan pesan error-nya: %+v %v", errs, err)
	}
	for _, e := range errs {
		if e.GroupID == "" || e.Error == "" {
			t.Errorf("kelompok tanpa id atau error: %+v", e)
		}
	}
}

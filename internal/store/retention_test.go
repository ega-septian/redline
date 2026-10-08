package store

import (
	"context"
	"testing"
	"time"

	"redline/internal/report"
)

func passedTest(title, calls string) report.Spec {
	spec := failedTest(title, "", calls)
	spec.Tests[0].Status = "expected"
	spec.Tests[0].Results[0].Status = "passed"
	spec.Tests[0].Results[0].Error = nil
	return spec
}

func ingest(t *testing.T, s *Store, specs ...report.Spec) *IngestResult {
	t.Helper()
	r, err := s.IngestReport(context.Background(),
		&report.Report{Suites: []report.Suite{{Title: "shop.spec.ts", File: "shop.spec.ts", Specs: specs}}}, RunMeta{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func count(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIngest_DedupesResponseShapes(t *testing.T) {
	s := openTestStore(t)
	brands := `[{"method":"GET","path":"/brands","status":200,"shape":[{"id":"number","name":"string"}]}]`
	login := `[{"method":"POST","path":"/users/login","status":200,"shape":{"token":"string"}}]`

	// 10 run x 3 test, tapi hanya 2 bentuk response yang berbeda.
	for range 10 {
		ingest(t, s, passedTest("brands", brands), passedTest("brands lagi", brands), passedTest("login", login))
	}
	if n := count(t, s, `SELECT count(*) FROM response_shapes`); n != 2 {
		t.Errorf("mau 2 bentuk unik, dapat %d", n)
	}
	if n := count(t, s, `SELECT count(*) FROM test_results WHERE response_shape_id = ''`); n != 0 {
		t.Errorf("semua hasil yang punya calls harus menyimpan hash, %d kosong", n)
	}
	// Test tanpa fixture Redline: tidak membuat bentuk kosong.
	ingest(t, s, passedTest("tanpa fixture", ""))
	if n := count(t, s, `SELECT count(*) FROM response_shapes`); n != 2 {
		t.Errorf("calls kosong tidak boleh disimpan, dapat %d bentuk", n)
	}
}

func TestPrune_KeepsWhatAnalysisNeeds(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	ok := `[{"method":"GET","path":"/brands","status":200,"shape":[{"id":"number","slug":"string"}]}]`
	broken := `[{"method":"GET","path":"/brands","status":200,"shape":[{"id":"number"}]}]`
	err := "expect(received).toEqual(expected)\n- \"slug\": \"x\""

	// 5 run lulus, lalu 5 run gagal dengan bentuk response yang berubah.
	for range 5 {
		ingest(t, s, passedTest("brands", ok))
	}
	var last *IngestResult
	for range 5 {
		last = ingest(t, s, failedTest("brands", err, broken))
	}
	fp := last.Recurring[0].Fingerprint
	factsBefore, e := s.Facts(ctx, fp)
	if e != nil {
		t.Fatal(e)
	}

	// Semua run dianggap sudah 60 hari, lalu retensi 30 hari.
	if _, e := s.pool.Exec(ctx, `UPDATE runs SET created_at = now() - interval '60 days'`); e != nil {
		t.Fatal(e)
	}
	res, e := s.Prune(ctx, time.Now().AddDate(0, 0, -30))
	if e != nil {
		t.Fatal(e)
	}
	// Tersisa 3: lulus terakhir, gagal pertama, gagal terakhir.
	if n := count(t, s, `SELECT count(*) FROM test_results`); n != 3 || res.TestResults != 7 {
		t.Errorf("mau 3 tersisa dan 7 terhapus, dapat %d tersisa, %d terhapus", n, res.TestResults)
	}
	if n := count(t, s, `SELECT count(*) FROM response_shapes`); n != 2 {
		t.Errorf("bentuk yang masih dirujuk harus tetap ada, dapat %d", n)
	}

	// Analisis tetap punya pembanding yang sama setelah retensi.
	factsAfter, e := s.Facts(ctx, fp)
	if e != nil {
		t.Fatal(e)
	}
	if factsAfter.LastPass == nil || factsAfter.LastPass.RunID != factsBefore.LastPass.RunID {
		t.Errorf("lulus terakhir hilang setelah retensi: %+v", factsAfter.LastPass)
	}
	if factsAfter.Response.Verdict != factsBefore.Response.Verdict || len(factsAfter.CurrentCalls) == 0 {
		t.Errorf("perbandingan response berubah: sebelum %+v, sesudah %+v", factsBefore.Response, factsAfter.Response)
	}

	// Run baru tidak tersentuh.
	ingest(t, s, failedTest("brands", err, broken))
	if _, e := s.Prune(ctx, time.Now().AddDate(0, 0, -30)); e != nil {
		t.Fatal(e)
	}
	if n := count(t, s, `SELECT count(*) FROM test_results t JOIN runs r ON r.id = t.run_id WHERE r.created_at > now() - interval '1 day'`); n != 1 {
		t.Errorf("hasil run baru tidak boleh dihapus, tersisa %d", n)
	}
}

func TestPrune_RemovesOrphanShapes(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	old := `[{"method":"GET","path":"/v1/brands","status":200,"shape":[]}]`
	now := `[{"method":"GET","path":"/v2/brands","status":200,"shape":[]}]`

	ingest(t, s, passedTest("brands", old))
	ingest(t, s, passedTest("brands", old))
	if _, e := s.pool.Exec(ctx, `UPDATE runs SET created_at = now() - interval '60 days'`); e != nil {
		t.Fatal(e)
	}
	ingest(t, s, passedTest("brands", now)) // lulus terakhir sekarang memakai bentuk baru

	if _, e := s.Prune(ctx, time.Now().AddDate(0, 0, -30)); e != nil {
		t.Fatal(e)
	}
	if n := count(t, s, `SELECT count(*) FROM response_shapes`); n != 1 {
		t.Errorf("bentuk lama tidak dirujuk lagi, harus terhapus; tersisa %d", n)
	}
}

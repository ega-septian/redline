package store

import (
	"context"
	"os"
	"strings"
	"testing"

	"redline/internal/report"
)

// Test ini butuh Postgres sungguhan dan akan MENGHAPUS semua tabel di database tersebut.
// Jalankan dengan database khusus test, misalnya:
//
//	REDLINE_TEST_DATABASE_URL=postgres://redline:redline@localhost:5433/redline_test?sslmode=disable go test ./...
func openTestStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("REDLINE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("REDLINE_TEST_DATABASE_URL tidak diset; test database dilewati")
	}
	ctx := context.Background()
	s, err := Open(ctx, TestSchema(ctx, t, url, "store_test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func loadReport(t *testing.T, name string) *report.Report {
	t.Helper()
	f, err := os.Open("../report/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rep, err := report.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestIngest_Lifecycle(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	bug, fixed := loadReport(t, "run1-bug.json"), loadReport(t, "run2-fixed.json")

	// Run 1: API bermasalah -> 3 kelompok baru.
	r1, err := s.IngestReport(ctx, bug, RunMeta{Branch: "main", CommitSHA: "aaa111", AppVersion: "sprint5-with-bugs"})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Total != 6 || r1.Failed != 3 || r1.Flaky != 1 || r1.Skipped != 1 || r1.Passed != 1 {
		t.Fatalf("hitungan run 1 salah: %+v", r1)
	}
	if len(r1.New) != 3 || len(r1.FlakyTests) != 1 {
		t.Fatalf("mau 3 kelompok baru dan 1 flaky: %+v", r1)
	}

	// Run 2: laporan yang sama -> bukan kelompok baru, tapi berulang.
	r2, err := s.IngestReport(ctx, bug, RunMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.New) != 0 || len(r2.Recurring) != 3 {
		t.Fatalf("error yang sama harus masuk kelompok yang sama: %+v", r2)
	}
	for _, c := range r2.Recurring {
		if c.Occurrences != 2 {
			t.Errorf("occurrences harus 2: %+v", c)
		}
	}

	// Run 3: API diperbaiki -> 3 kelompok resolved, tidak ada yang terbuka.
	r3, err := s.IngestReport(ctx, fixed, RunMeta{Source: "ci"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r3.Resolved) != 3 || r3.Passed != 4 {
		t.Fatalf("mau 3 resolved: %+v", r3)
	}
	open, err := s.ListGroups(ctx, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Fatalf("tidak boleh ada kelompok terbuka setelah fix: %+v", open)
	}

	// Run 4: bug muncul lagi -> regressed.
	r4, err := s.IngestReport(ctx, bug, RunMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r4.Regressed) != 3 || len(r4.New) != 0 {
		t.Fatalf("mau 3 regressed: %+v", r4)
	}
	groups, err := s.ListGroups(ctx, []string{"regressed"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 3 || groups[0].Regressions != 1 || groups[0].Occurrences != 3 || groups[0].ResolvedRun != nil {
		t.Fatalf("kelompok regressed salah: %+v", groups)
	}

	// Detail kelompok: riwayat kemunculan lengkap dengan metadata run.
	g, occ, err := s.GetGroup(ctx, groups[0].Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if len(occ) != 3 || occ[0].RunID != r4.RunID || occ[len(occ)-1].CommitSHA != "aaa111" || occ[0].TracePath == "" {
		t.Errorf("riwayat salah: %+v", occ)
	}
	if !strings.HasPrefix(occ[0].TracePath, "test-results/") {
		t.Errorf("trace_path harus relatif terhadap project, bukan path di laptop: %q", occ[0].TracePath)
	}
	if g.SampleError == "" || g.Summary == "" {
		t.Errorf("detail harus berisi contoh error: %+v", g)
	}
	if _, _, err := s.GetGroup(ctx, "tidakada"); err != ErrNotFound {
		t.Errorf("mau ErrNotFound, dapat %v", err)
	}

	runs, err := s.ListRuns(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 4 || runs[0].ID != r4.RunID || runs[1].Source != "ci" || runs[0].PlaywrightVersion == "" {
		t.Errorf("daftar run salah: %+v", runs)
	}
}

func TestIngest_RedactsBeforeSaving(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	rep := &report.Report{Suites: []report.Suite{{
		Title: "auth.spec.ts", File: "auth.spec.ts",
		Specs: []report.Spec{{Title: "login", File: "auth.spec.ts", Tests: []report.Test{{
			ProjectName: "api", Status: "unexpected",
			Results: []report.Result{{Status: "failed", Error: &report.ErrorEntry{
				Message: "Error: 401\nAuthorization: Bearer super-secret-token",
				Snippet: `request.post("/login", { data: { password: "rahasia" } })`,
			}}},
		}}}},
	}}}
	if _, err := s.IngestReport(ctx, rep, RunMeta{}); err != nil {
		t.Fatal(err)
	}
	var msg, snippet string
	if err := s.pool.QueryRow(ctx, `SELECT error_message, error_snippet FROM test_results`).Scan(&msg, &snippet); err != nil {
		t.Fatal(err)
	}
	var sample string
	if err := s.pool.QueryRow(ctx, `SELECT last_error FROM failure_groups`).Scan(&sample); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{msg, snippet, sample} {
		if strings.Contains(v, "super-secret-token") || strings.Contains(v, "rahasia") {
			t.Errorf("rahasia bocor ke database: %q", v)
		}
	}
}

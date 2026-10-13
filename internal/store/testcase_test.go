package store

import (
	"context"
	"fmt"
	"testing"

	"redline/internal/report"
)

func tagSpecs(suites []report.Suite, n *int) {
	for i := range suites {
		for j := range suites[i].Specs {
			*n++
			suites[i].Specs[j].Tags = []string{"smoke", fmt.Sprintf("TC-BENCH-%03d", *n)}
		}
		tagSpecs(suites[i].Suites, n)
	}
}

func TestIngest_TestCaseID(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	rep := loadReport(t, "run1-bug.json")
	n := 0
	tagSpecs(rep.Suites, &n)

	res, err := s.IngestReport(ctx, rep, RunMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.New) == 0 || res.New[0].TestCaseID == "" {
		t.Fatalf("hasil ingest harus membawa test_case_id: %+v", res.New)
	}
	var missing int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM test_results WHERE test_case_id NOT LIKE 'TC-BENCH-%'`).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	if missing != 0 {
		t.Errorf("%d hasil test tanpa test_case_id", missing)
	}
	g, _, err := s.GetGroup(ctx, res.New[0].Fingerprint)
	if err != nil || g.TestCaseID != res.New[0].TestCaseID {
		t.Fatalf("kelompok harus menyimpan test_case_id: %+v %v", g, err)
	}

	// Tag diganti: kelompok yang sama (identitas tetap test_key) mengikuti tag terbaru.
	rep2 := loadReport(t, "run1-bug.json")
	for i := range rep2.Suites {
		renameTags(rep2.Suites[i:i+1], "TC-NEW-")
	}
	if _, err := s.IngestReport(ctx, rep2, RunMeta{}); err != nil {
		t.Fatal(err)
	}
	g2, _, _ := s.GetGroup(ctx, res.New[0].Fingerprint)
	if g2.TestCaseID == g.TestCaseID || g2.Fingerprint != g.Fingerprint {
		t.Errorf("tag baru harus dipakai tanpa memutus riwayat: lama %q, baru %q", g.TestCaseID, g2.TestCaseID)
	}
}

func renameTags(suites []report.Suite, prefix string) {
	for i := range suites {
		for j := range suites[i].Specs {
			suites[i].Specs[j].Tags = []string{prefix + suites[i].Specs[j].Title}
		}
		renameTags(suites[i].Suites, prefix)
	}
}

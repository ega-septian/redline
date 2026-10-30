package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"redline/internal/store"
)

// TestRunDetail: metadata run, semua outcome (bukan hanya yang gagal), dan analisis kelompoknya.
func TestRunDetail(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	run := ingest(t, s, describeReport("Brands",
		tagged{"get", "TC-BRD-001", ""},
		tagged{"create", "TC-BRD-002", "Expected: 201\nReceived: 500"},
		tagged{"untagged", "", "Expected: 200\nReceived: 404"}),
		store.RunMeta{Branch: "main", CommitSHA: "abc123", Source: "ci"})

	got, err := s.GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != run.RunID || got.Total != 3 || got.Passed != 1 || got.Failed != 2 ||
		got.Branch != "main" || got.CommitSHA != "abc123" || got.Source != "ci" {
		t.Errorf("metadata run salah: %+v", got)
	}
	if _, err := s.GetRun(ctx, 9999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("run yang tidak ada: mau ErrNotFound, dapat %v", err)
	}

	results, err := s.RunResults(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("mau 3 outcome, dapat %d: %+v", len(results), results)
	}
	byTitle := map[string]store.RunResult{}
	for _, result := range results {
		title := result.TestKey[strings.LastIndex(result.TestKey, " › ")+len(" › "):]
		byTitle[title] = result
	}
	if create := byTitle["create"]; create.Status != "failed" || create.TestCaseID != "TC-BRD-002" ||
		create.ErrorMessage == "" || create.Fingerprint != groupOf(t, run, "create").Fingerprint {
		t.Errorf("hasil gagal harus membawa status, TC ID, error dan fingerprint: %+v", create)
	}
	if get := byTitle["get"]; get.Status != "passed" || get.TestCaseID != "TC-BRD-001" || get.Fingerprint != "" {
		t.Errorf("hasil lulus harus ikut, tanpa fingerprint: %+v", get)
	}
	if untagged := byTitle["untagged"]; untagged.Status != "failed" || untagged.TestCaseID != "" {
		t.Errorf("test tanpa TC ID harus tetap muncul: %+v", untagged)
	}

	fp := groupOf(t, run, "create").Fingerprint
	if err := s.SaveAnalysis(ctx, store.Analysis{Fingerprint: fp, PromptVersion: "v1", Source: "ai",
		Category: "backend_bug", Confidence: "high", Summary: "Status berubah."}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLabel(ctx, fp, "backend_bug", "500 saat create", "ega"); err != nil {
		t.Fatal(err)
	}
	results, err = s.RunResults(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.TestCaseID != "TC-BRD-002" {
			continue
		}
		if result.Category != "backend_bug" || result.Confidence != "high" || result.HumanLabel != "backend_bug" {
			t.Errorf("hasil harus membawa analisis dan label terbaru: %+v", result)
		}
	}
}

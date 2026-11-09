package store_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"redline/internal/report"
	"redline/internal/store"
)

func healthSpec(title, tag, status string) report.Spec {
	var spec report.Spec
	switch status {
	case "failed":
		spec = failedTest(title, "boom", "")
	case "flaky":
		spec = report.Spec{Title: title, File: "shop.spec.ts", Tests: []report.Test{{
			ProjectName: "api", Status: "flaky", Results: []report.Result{
				{Status: "failed", Error: &report.ErrorEntry{Message: "boom"}},
				{Status: "passed"},
			},
		}}}
	default:
		spec = passedTest(title)
	}
	spec.Tags = []string{tag}
	return spec
}

func TestTestHealthTrend(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	ingest(t, s, shopReport(
		healthSpec("A", "TC-A", "passed"),
		healthSpec("B", "TC-B", "failed"),
	), store.RunMeta{Branch: "main", CommitSHA: "one"})
	ingest(t, s, shopReport(
		healthSpec("A", "TC-A", "passed"),
		healthSpec("B", "TC-B", "passed"),
		healthSpec("C", "TC-C", "flaky"),
	), store.RunMeta{Branch: "main", CommitSHA: "two"})

	points, err := s.TestHealthTrend(ctx, "api", 365, 10)
	if err != nil || len(points) != 2 {
		t.Fatalf("trend = %+v %v", points, err)
	}
	if points[0].RunID >= points[1].RunID || points[0].Passed != 1 || points[0].Failed != 1 {
		t.Errorf("trend run pertama salah: %+v", points)
	}
	if points[1].Passed != 2 || points[1].Flaky != 1 || math.Abs(points[1].PassRate-2.0/3.0) > 1e-9 ||
		math.Abs(points[1].FlakeRate-1.0/3.0) > 1e-9 {
		t.Errorf("trend run terakhir salah: %+v", points[1])
	}

	latest, err := s.TestHealthTrend(ctx, "api", 365, 1)
	if err != nil || len(latest) != 1 || latest[0].RunID != points[1].RunID {
		t.Errorf("trend limit salah: %+v %v", latest, err)
	}
	if other, err := s.TestHealthTrend(ctx, "other", 365, 10); err != nil || len(other) != 0 {
		t.Errorf("trend project lain harus kosong: %+v %v", other, err)
	}
}

func TestTestHealthDurationDelta(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	spec := func(title, tag string, ms float64) report.Spec {
		item := healthSpec(title, tag, "passed")
		for i := range item.Tests {
			for j := range item.Tests[i].Results {
				item.Tests[i].Results[j].Duration = ms
			}
		}
		return item
	}

	ingest(t, s, shopReport(
		spec("A", "TC-A", 1000),
		spec("B", "TC-B", 500),
	), store.RunMeta{Branch: "main", CommitSHA: "one"})
	ingest(t, s, shopReport(
		spec("A", "TC-A", 2000),
		spec("B", "TC-B", 500),
	), store.RunMeta{Branch: "main", CommitSHA: "two"})

	_, tests, err := s.TestHealth(ctx, "api", 365)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]store.TestHealthRow{}
	for _, row := range tests {
		byKey[row.Key] = row
	}
	if a := byKey["TC-A"]; a.LastDurationMs != 2000 || a.PreviousDurationMs != 1000 ||
		a.AvgDurationMs != 1500 || math.Abs(a.DurationDeltaPct-100) > 1e-9 {
		t.Errorf("durasi TC-A salah: %+v", a)
	}
	if b := byKey["TC-B"]; b.LastDurationMs != 500 || b.PreviousDurationMs != 500 ||
		b.AvgDurationMs != 500 || b.DurationDeltaPct != 0 {
		t.Errorf("durasi TC-B salah: %+v", b)
	}
}

func TestTestHealth(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	ingest(t, s, shopReport(
		healthSpec("A", "TC-A", "passed"),
		healthSpec("B", "TC-B", "failed"),
		healthSpec("C", "TC-C", "flaky"),
	), store.RunMeta{Branch: "main", CommitSHA: "one"})
	ingest(t, s, shopReport(
		healthSpec("A", "TC-A", "passed"),
		healthSpec("B", "TC-B", "passed"),
		healthSpec("C", "TC-C", "passed"),
	), store.RunMeta{Branch: "main", CommitSHA: "two"})
	ingest(t, s, shopReport(
		healthSpec("A", "TC-A", "failed"),
		healthSpec("B", "TC-B", "failed"),
		healthSpec("C", "TC-C", "flaky"),
	), store.RunMeta{Branch: "main", CommitSHA: "three"})

	summary, tests, err := s.TestHealth(ctx, "api", 365)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Runs != 3 || summary.Results != 9 || summary.Passed != 4 ||
		summary.Failed != 3 || summary.Flaky != 2 || summary.Skipped != 0 {
		t.Errorf("summary counts salah: %+v", summary)
	}
	if math.Abs(summary.PassRate-4.0/9.0) > 1e-9 || math.Abs(summary.FlakeRate-2.0/9.0) > 1e-9 {
		t.Errorf("summary rates salah: %+v", summary)
	}
	if summary.OpenFailures == 0 || summary.NewFailures == 0 {
		t.Errorf("failure counts harus terisi: %+v", summary)
	}

	byKey := map[string]store.TestHealthRow{}
	for _, row := range tests {
		byKey[row.Key] = row
	}
	if a := byKey["TC-A"]; a.Total != 3 || a.Passed != 2 || a.Failed != 1 || a.LastStatus != "failed" {
		t.Errorf("TC-A salah: %+v", a)
	} else if a.Flips != 1 {
		t.Errorf("TC-A flips = %d, mau 1: %+v", a.Flips, a)
	}
	if b := byKey["TC-B"]; b.Total != 3 || b.Passed != 1 || b.Failed != 2 || b.LastStatus != "failed" {
		t.Errorf("TC-B salah: %+v", b)
	} else if b.Flips != 2 {
		t.Errorf("TC-B flips = %d, mau 2: %+v", b.Flips, b)
	}
	if c := byKey["TC-C"]; c.Total != 3 || c.Flaky != 2 || c.LastStatus != "flaky" ||
		math.Abs(c.FlakeRate-2.0/3.0) > 1e-9 {
		t.Errorf("TC-C salah: %+v", c)
	} else if c.Flips != 2 {
		t.Errorf("TC-C flips = %d, mau 2: %+v", c.Flips, c)
	}

	if _, other, err := s.TestHealth(ctx, "other", 365); err != nil || len(other) != 0 {
		t.Errorf("project lain harus kosong: %+v %v", other, err)
	}

	row, points, err := s.TestHistory(ctx, "TC-A", 365, 10)
	if err != nil {
		t.Fatal(err)
	}
	if row.Key != "TC-A" || row.LastStatus != "failed" || len(points) != 3 {
		t.Errorf("history TC-A salah: %+v %+v", row, points)
	}
	if points[0].Status != "failed" || points[0].Branch != "main" || points[0].Commit != "three" {
		t.Errorf("titik history terbaru salah: %+v", points[0])
	}
	if _, _, err := s.TestHistory(ctx, "TC-NONE", 365, 10); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("test tidak ada: mau ErrNotFound, dapat %v", err)
	}
}

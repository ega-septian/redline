package store_test

import (
	"context"
	"strings"
	"testing"

	"redline/internal/report"
	"redline/internal/store"
)

func TestLoadErrors(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	broken := func() *report.Report {
		rep := shopReport()
		rep.Errors = []report.ErrorEntry{{Message: "Cannot find module './helpers/brands'"}}
		return rep
	}

	first := ingest(t, s, broken(), store.RunMeta{Branch: "main"})
	second := ingest(t, s, broken(), store.RunMeta{Branch: "main"})

	items, err := s.LoadErrors(ctx, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("load errors = %+v %v", items, err)
	}
	item := items[0]
	if !strings.Contains(item.Message, "helpers/brands") || item.Occurrences != 2 ||
		item.FirstSeenRun != first.RunID || item.LastSeenRun != second.RunID ||
		item.TriageStatus != "open" || item.Category != "test_bug" ||
		item.AnalysisSource != "rule" || item.Summary == "" || item.NextStep == "" ||
		item.AnalyzedAt == nil {
		t.Errorf("load error salah: %+v", item)
	}

	if err := s.SetLoadErrorTriage(ctx, item.ID, "acknowledged", "ega", "will fix the import", "ega"); err != nil {
		t.Fatal(err)
	}
	items, err = s.LoadErrors(ctx, 10)
	if err != nil || len(items) != 1 || items[0].TriageStatus != "acknowledged" ||
		items[0].TriageOwner != "ega" || items[0].TriageNote == "" {
		t.Errorf("triage load error salah: %+v %v", items, err)
	}

	if err := s.SetLoadErrorTriage(ctx, item.ID, "resolved", "ega", "", "ega"); err != nil {
		t.Fatal(err)
	}
	ingest(t, s, broken(), store.RunMeta{Branch: "main"})
	items, err = s.LoadErrors(ctx, 10)
	if err != nil || len(items) != 1 || items[0].TriageStatus != "open" {
		t.Errorf("load error harus reopen setelah muncul lagi: %+v %v", items, err)
	}
	if items[0].Occurrences != 3 {
		t.Errorf("occurrences = %d, mau 3", items[0].Occurrences)
	}

	if err := s.SetLoadErrorTriage(ctx, item.ID, "bogus", "", "", "ega"); err == nil {
		t.Error("status invalid harus error")
	}
	if err := s.SetLoadErrorTriage(ctx, "missing", "open", "", "", "ega"); err != store.ErrNotFound {
		t.Errorf("id tidak ada: mau ErrNotFound, dapat %v", err)
	}
}

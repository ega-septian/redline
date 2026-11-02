package store_test

import (
	"context"
	"errors"
	"testing"

	"redline/internal/store"
)

func TestTriage(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	run := ingest(t, s, describeReport("Brands",
		tagged{"register", "TC-USR-001", "Expected: 201\nReceived: 500"}), store.RunMeta{})
	fp := groupOf(t, run, "register").Fingerprint

	g, _ := group(t, s, fp)
	if g.TriageStatus != "open" || g.TriageOwner != "" || g.TriageUpdatedAt != nil {
		t.Errorf("triage default harus open: %+v", g)
	}
	if err := s.SetTriage(ctx, fp, "acknowledged", "ega", "check logs", "budi"); err != nil {
		t.Fatal(err)
	}
	g, _ = group(t, s, fp)
	if g.TriageStatus != "acknowledged" || g.TriageOwner != "ega" || g.TriageNote != "check logs" ||
		g.TriageUpdatedBy != "budi" || g.TriageUpdatedAt == nil {
		t.Errorf("triage tidak tersimpan: %+v", g)
	}
	if err := s.SetTriage(ctx, fp, "bukan-status", "", "", "ega"); err == nil {
		t.Error("status triage tidak valid harus ditolak")
	}
	if err := s.SetTriage(ctx, "tidakada", "open", "", "", "ega"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("kelompok tidak ada: mau ErrNotFound, dapat %v", err)
	}
}

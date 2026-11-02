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
	if n, err := s.SetTriageStatusBulk(ctx, []string{fp}, "in_progress", "budi"); err != nil || n != 1 {
		t.Errorf("bulk status: n=%d err=%v", n, err)
	}
	if g, _ = group(t, s, fp); g.TriageStatus != "in_progress" || g.TriageUpdatedBy != "budi" {
		t.Errorf("bulk status tidak tersimpan: %+v", g)
	}
	if n, err := s.SetTriageOwnerBulk(ctx, []string{fp}, "budi", "ega"); err != nil || n != 1 {
		t.Errorf("bulk owner: n=%d err=%v", n, err)
	}
	if g, _ = group(t, s, fp); g.TriageOwner != "budi" || g.TriageStatus != "in_progress" {
		t.Errorf("bulk owner tidak tersimpan: %+v", g)
	}
	if err := s.SetTriage(ctx, "tidakada", "open", "", "", "ega"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("kelompok tidak ada: mau ErrNotFound, dapat %v", err)
	}
}

package store_test

import (
	"context"
	"testing"
	"time"

	"redline/internal/store"
)

func TestArtifacts(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	run := ingest(t, s, describeReport("Brands",
		tagged{"get", "TC-BRD-001", ""}), store.RunMeta{})

	id, err := s.SaveArtifact(ctx, store.Artifact{
		RunID: run.RunID, TestKey: "toolshop › brand.spec.ts › get", Kind: "trace",
		Name: "trace.zip", Path: "/tmp/not-read-by-store", ContentType: "application/zip", Size: 123,
	})
	if err != nil || id == 0 {
		t.Fatalf("simpan artifact: id=%d err=%v", id, err)
	}
	list, err := s.ListArtifacts(ctx, run.RunID)
	if err != nil || len(list) != 1 || list[0].Kind != "trace" || list[0].Size != 123 {
		t.Fatalf("daftar artifact salah: %+v %v", list, err)
	}
	got, err := s.GetArtifact(ctx, id)
	if err != nil || got.Name != "trace.zip" || got.RunID != run.RunID {
		t.Fatalf("ambil artifact salah: %+v %v", got, err)
	}
	if _, err := s.GetArtifact(ctx, 9999); err != store.ErrNotFound {
		t.Errorf("artifact tidak ada: mau ErrNotFound, dapat %v", err)
	}

	// Retensi: run yang lebih tua dari batas ikut menghapus metadata dan mengembalikan path file.
	pruned, err := s.Prune(ctx, time.Now().Add(time.Hour))
	if err != nil || pruned.Artifacts != 1 || len(pruned.ArtifactPaths) != 1 || pruned.ArtifactPaths[0] != "/tmp/not-read-by-store" {
		t.Fatalf("retensi artifact salah: %+v %v", pruned, err)
	}
	if list, err := s.ListArtifacts(ctx, run.RunID); err != nil || len(list) != 0 {
		t.Fatalf("artifact harus terhapus: %+v %v", list, err)
	}
	if _, err := s.GetArtifact(ctx, id); err != store.ErrNotFound {
		t.Errorf("artifact terhapus: mau ErrNotFound, dapat %v", err)
	}
}

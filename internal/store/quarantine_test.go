package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"redline/internal/store"
)

func TestQuarantine(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	ingest(t, s, shopReport(healthSpec("A", "TC-A", "passed")), store.RunMeta{Branch: "main"})

	identity, err := s.FindTest(ctx, "TC-A")
	if err != nil {
		t.Fatal(err)
	}
	if identity.Key != "TC-A" || identity.TestCaseID != "TC-A" || identity.TestKey == "" {
		t.Errorf("identitas salah: %+v", identity)
	}
	byKey, err := s.FindTest(ctx, identity.TestKey)
	if err != nil || byKey.Key != "TC-A" {
		t.Errorf("cari lewat test key salah: %+v %v", byKey, err)
	}
	if _, err := s.FindTest(ctx, "TC-NONE"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("test tidak ada: mau ErrNotFound, dapat %v", err)
	}

	until := time.Now().AddDate(0, 0, 7)
	q, err := s.SetQuarantine(ctx, store.Quarantine{
		Key: identity.Key, TestKey: identity.TestKey, TestCaseID: identity.TestCaseID,
		Project: identity.Project, Owner: "ega", Reason: "flaky login", By: "ega", Until: until,
	})
	if err != nil {
		t.Fatal(err)
	}
	if q.Owner != "ega" || q.Reason != "flaky login" || q.Until.IsZero() {
		t.Errorf("quarantine tersimpan salah: %+v", q)
	}
	active, err := s.ActiveQuarantines(ctx)
	if err != nil || len(active) != 1 || active[0].Key != "TC-A" {
		t.Fatalf("active quarantine salah: %+v %v", active, err)
	}
	_, tests, err := s.TestHealth(ctx, "api", 365)
	if err != nil || len(tests) != 1 || !tests[0].Quarantined || tests[0].QuarantineOwner != "ega" {
		t.Errorf("health harus membawa quarantine: %+v %v", tests, err)
	}

	released, err := s.ReleaseQuarantine(ctx, identity.Key, "ega")
	if err != nil || released.ReleasedAt == nil || released.ReleasedBy != "ega" {
		t.Errorf("release salah: %+v %v", released, err)
	}
	if active, err := s.ActiveQuarantines(ctx); err != nil || len(active) != 0 {
		t.Errorf("quarantine released masih aktif: %+v %v", active, err)
	}
	if _, err := s.ReleaseQuarantine(ctx, identity.Key, "ega"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("release kedua: mau ErrNotFound, dapat %v", err)
	}

	// Expired quarantine is preserved as history but no longer active.
	if _, err := s.SetQuarantine(ctx, store.Quarantine{
		Key: identity.Key, TestKey: identity.TestKey, TestCaseID: identity.TestCaseID,
		Project: identity.Project, Owner: "ega", Reason: "expired", By: "ega",
		Until: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if active, err := s.ActiveQuarantines(ctx); err != nil || len(active) != 0 {
		t.Errorf("quarantine expired masih aktif: %+v %v", active, err)
	}
	if _, tests, err := s.TestHealth(ctx, "api", 365); err != nil || len(tests) != 1 || tests[0].Quarantined {
		t.Errorf("health tidak boleh menandai expired: %+v %v", tests, err)
	}
}

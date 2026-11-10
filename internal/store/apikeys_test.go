package store_test

import (
	"context"
	"errors"
	"testing"

	"redline/internal/store"
)

func TestAPIKeys(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	keys, err := s.ListAPIKeys(ctx)
	if err != nil || len(keys) != 0 {
		t.Fatalf("awal harus kosong: %+v %v", keys, err)
	}
	if count, err := s.APIKeyCount(ctx); err != nil || count != 0 {
		t.Fatalf("count awal = %d %v", count, err)
	}

	key, err := s.CreateAPIKey(ctx, store.APIKey{
		Name: "coba2-ci", Prefix: "rl_abc123", Hash: "hash-one", CreatedBy: "ega",
	})
	if err != nil {
		t.Fatal(err)
	}
	if key.ID == "" || key.Name != "coba2-ci" || key.Prefix != "rl_abc123" ||
		key.Hash != "hash-one" || key.CreatedBy != "ega" || key.CreatedAt.IsZero() {
		t.Errorf("key tersimpan salah: %+v", key)
	}
	if key.LastUsedAt != nil || key.RevokedAt != nil {
		t.Errorf("key baru harus belum dipakai dan belum dicabut: %+v", key)
	}

	if _, err := s.CreateAPIKey(ctx, store.APIKey{
		Name: "coba2-ci", Prefix: "rl_other", Hash: "hash-two",
	}); !errors.Is(err, store.ErrAPIKeyExists) {
		t.Errorf("nama duplikat: mau ErrAPIKeyExists, dapat %v", err)
	}

	name, err := s.APIKeyName(ctx, "hash-one")
	if err != nil || name != "coba2-ci" {
		t.Fatalf("lookup = %q %v", name, err)
	}
	keys, err = s.ListAPIKeys(ctx)
	if err != nil || len(keys) != 1 || keys[0].LastUsedAt == nil {
		t.Errorf("last used harus tercatat: %+v %v", keys, err)
	}

	if _, err := s.APIKeyName(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("hash tidak dikenal: mau ErrNotFound, dapat %v", err)
	}

	revoked, err := s.RevokeAPIKey(ctx, key.ID)
	if err != nil || revoked.RevokedAt == nil {
		t.Fatalf("revoke = %+v %v", revoked, err)
	}
	if _, err := s.APIKeyName(ctx, "hash-one"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("key dicabut masih valid: %v", err)
	}
	again, err := s.RevokeAPIKey(ctx, key.ID)
	if err != nil || again.RevokedAt == nil || !again.RevokedAt.Equal(*revoked.RevokedAt) {
		t.Errorf("revoke kedua harus idempoten: %+v vs %+v (%v)", again, revoked, err)
	}
	if _, err := s.RevokeAPIKey(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("revoke tidak ada: mau ErrNotFound, dapat %v", err)
	}
	if count, err := s.APIKeyCount(ctx); err != nil || count != 1 {
		t.Errorf("count setelah revoke = %d %v", count, err)
	}

	second, err := s.CreateAPIKey(ctx, store.APIKey{
		Name: "team-b", Prefix: "rl_def456", Hash: "hash-three",
	})
	if err != nil {
		t.Fatal(err)
	}
	keys, err = s.ListAPIKeys(ctx)
	if err != nil || len(keys) != 2 || keys[0].ID != second.ID {
		t.Errorf("urutan terbaru dulu salah: %+v %v", keys, err)
	}
}

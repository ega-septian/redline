package store_test

import (
	"context"
	"errors"
	"testing"

	"redline/internal/store"
)

func TestUsersAndAuthSecret(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	if count, err := s.UserCount(ctx); err != nil || count != 0 {
		t.Fatalf("awal harus kosong: %d %v", count, err)
	}
	user, err := s.CreateAdmin(ctx, "ega", "$2a$12$hash")
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "ega" || user.Role != "admin" || user.PasswordHash != "$2a$12$hash" {
		t.Errorf("user salah: %+v", user)
	}
	if count, err := s.UserCount(ctx); err != nil || count != 1 {
		t.Errorf("count = %d %v", count, err)
	}
	if _, err := s.CreateAdmin(ctx, "second", "$2a$12$hash"); !errors.Is(err, store.ErrAdminExists) {
		t.Errorf("admin kedua: mau ErrAdminExists, dapat %v", err)
	}
	got, err := s.User(ctx, "ega")
	if err != nil || got.Username != "ega" || got.Role != "admin" {
		t.Errorf("user = %+v %v", got, err)
	}
	if _, err := s.User(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing: mau ErrNotFound, dapat %v", err)
	}
	if err := s.UpdatePassword(ctx, "ega", "$2a$12$new"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.User(ctx, "ega")
	if got.PasswordHash != "$2a$12$new" {
		t.Errorf("password tidak berubah: %+v", got)
	}
	if err := s.UpdatePassword(ctx, "missing", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("update missing: mau ErrNotFound, dapat %v", err)
	}

	first, err := s.AuthSecret(ctx)
	if err != nil || first == "" {
		t.Fatalf("secret = %q %v", first, err)
	}
	second, err := s.AuthSecret(ctx)
	if err != nil || second != first {
		t.Errorf("secret harus stabil: %q vs %q (%v)", first, second, err)
	}
}

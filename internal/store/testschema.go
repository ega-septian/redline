package store

import (
	"context"
	"testing"
)

// TestSchema membuat ulang schema kosong bernama schema di database test, lalu mengembalikan
// URL yang memakai schema itu (search_path). Tiap package test memakai schema sendiri,
// jadi `go test ./...` yang berjalan paralel tidak saling menghapus data.
// Hanya untuk test: schema tersebut DIHAPUS beserta isinya.
func TestSchema(ctx context.Context, t testing.TB, url, schema string) string {
	t.Helper()
	out, err := PrepareSchema(ctx, url, schema, true)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

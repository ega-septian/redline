package store

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestSchema membuat ulang schema kosong bernama schema di database test, lalu mengembalikan
// URL yang memakai schema itu (search_path). Tiap package test memakai schema sendiri,
// jadi `go test ./...` yang berjalan paralel tidak saling menghapus data.
// Hanya untuk test: schema tersebut DIHAPUS beserta isinya.
func TestSchema(ctx context.Context, t testing.TB, url, schema string) string {
	t.Helper()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+ident+" CASCADE; CREATE SCHEMA "+ident); err != nil {
		t.Fatal(err)
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	// public ikut di search_path karena ekstensi (pgvector) dipasang di sana.
	return url + sep + "search_path=" + schema + ",public"
}

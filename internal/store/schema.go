package store

import (
	"context"
	"fmt"
	"net/url"
	"regexp"

	"github.com/jackc/pgx/v5"
)

var schemaName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// PrepareSchema menyiapkan schema Postgres terpisah (misalnya untuk benchmark atau demo) dan mengembalikan
// URL yang memakainya lewat search_path. reset true MENGHAPUS schema itu beserta isinya dulu.
// public tetap di search_path karena ekstensi (pgvector) dipasang di sana.
func PrepareSchema(ctx context.Context, databaseURL, schema string, reset bool) (string, error) {
	if !schemaName.MatchString(schema) || schema == "public" {
		return "", fmt.Errorf("DATABASE_SCHEMA %q tidak valid: huruf kecil, angka, dan _ saja, bukan public", schema)
	}
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return "", fmt.Errorf("koneksi database: %w", err)
	}
	defer conn.Close(ctx)
	ident := pgx.Identifier{schema}.Sanitize()
	if reset {
		if _, err := conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+ident+" CASCADE"); err != nil {
			return "", fmt.Errorf("hapus schema %s: %w", schema, err)
		}
	}
	if _, err := conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+ident); err != nil {
		return "", fmt.Errorf("buat schema %s: %w", schema, err)
	}
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "", fmt.Errorf("DATABASE_URL tidak valid: %w", err)
	}
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

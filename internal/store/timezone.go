package store

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
)

// WithTimezone menambahkan zona waktu sesi ke URL database (kalau belum ada), supaya semua koneksi
// server menghitung dan menampilkan waktu di zona itu.
func WithTimezone(databaseURL, tz string) (string, error) {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "", fmt.Errorf("DATABASE_URL tidak valid: %w", err)
	}
	q := u.Query()
	if q.Get("timezone") == "" {
		q.Set("timezone", tz)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// SetDatabaseTimezone menjadikan tz zona waktu bawaan database ini untuk sesi baru, termasuk
// psql atau DBeaver yang dipakai tim. Hanya tampilan: timestamptz tetap titik waktu absolut,
// jadi data lama tidak perlu dikonversi.
func (s *Store) SetDatabaseTimezone(ctx context.Context, tz string) error {
	if _, err := s.pool.Exec(ctx, `SELECT now() AT TIME ZONE $1`, tz); err != nil {
		return fmt.Errorf("zona waktu %q tidak dikenal Postgres: %w", tz, err)
	}
	var db string
	if err := s.pool.QueryRow(ctx, `SELECT current_database()`).Scan(&db); err != nil {
		return err
	}
	// ALTER DATABASE tidak menerima parameter; tz sudah divalidasi Postgres di atas.
	_, err := s.pool.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{db}.Sanitize()+
		" SET timezone TO '"+strings.ReplaceAll(tz, "'", "''")+"'")
	return err
}

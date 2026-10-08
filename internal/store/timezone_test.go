package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWithTimezone(t *testing.T) {
	cases := map[string]string{
		"postgres://u:p@h:5433/db?sslmode=disable":              "timezone=Asia%2FJakarta",
		"postgres://u:p@h:5433/db":                              "timezone=Asia%2FJakarta",
		"postgres://u:p@h:5433/db?sslmode=disable&timezone=UTC": "timezone=UTC", // yang sudah diset tidak ditimpa
	}
	for in, want := range cases {
		got, err := WithTimezone(in, "Asia/Jakarta")
		if err != nil || !strings.Contains(got, want) || !strings.Contains(got, "/db") {
			t.Errorf("WithTimezone(%q) = %q, %v; mau berisi %q", in, got, err, want)
		}
	}
}

func TestSetDatabaseTimezone(t *testing.T) {
	url := os.Getenv("REDLINE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("REDLINE_TEST_DATABASE_URL tidak diset; test database dilewati")
	}
	ctx := context.Background()
	s := openTestStore(t)
	t.Cleanup(func() {
		s.pool.Exec(ctx, `DO $$ BEGIN EXECUTE format('ALTER DATABASE %I RESET timezone', current_database()); END $$`)
	}) //nolint:errcheck

	if err := s.SetDatabaseTimezone(ctx, "Bukan/Zona"); err == nil {
		t.Error("zona waktu yang tidak dikenal harus ditolak")
	}
	if err := s.SetDatabaseTimezone(ctx, "Asia/Jakarta"); err != nil {
		t.Fatal(err)
	}
	// Sesi baru (seperti psql atau DBeaver) memakai zona waktu database.
	fresh, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	var tz string
	var now time.Time
	if err := fresh.pool.QueryRow(ctx, `SELECT current_setting('TimeZone'), now()`).Scan(&tz, &now); err != nil {
		t.Fatal(err)
	}
	if tz != "Asia/Jakarta" {
		t.Errorf("sesi baru harus Asia/Jakarta, dapat %q", tz)
	}
}

package store_test

import (
	"context"
	"os"
	"testing"

	"redline/internal/store"
	"redline/internal/storetest"
)

// TestParity_Ingest menjalankan test ingest yang sama dengan implementasi Neo4j (lihat internal/storetest).
func TestParity_Ingest(t *testing.T) {
	url := os.Getenv("REDLINE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("REDLINE_TEST_DATABASE_URL tidak diset; test database dilewati")
	}
	storetest.RunIngest(t, func(t *testing.T, sources []string) storetest.Backend {
		ctx := context.Background()
		s, err := store.Open(ctx, store.TestSchema(ctx, t, url, "store_parity_test"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		s.StatusSources = sources
		return s
	})
}

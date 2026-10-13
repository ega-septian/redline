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

// TestParity_Analysis menjalankan test fakta, analisis, dan label yang sama dengan implementasi Neo4j.
func TestParity_Analysis(t *testing.T) {
	url := os.Getenv("REDLINE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("REDLINE_TEST_DATABASE_URL tidak diset; test database dilewati")
	}
	storetest.RunAnalysis(t, func(t *testing.T) storetest.AnalysisBackend {
		ctx := context.Background()
		s, err := store.Open(ctx, store.TestSchema(ctx, t, url, "store_parity_test"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		return s
	})
}

// TestParity_Proofs menjalankan test eksperimen, rapor, dan aturan yang sama dengan implementasi Neo4j.
func TestParity_Proofs(t *testing.T) {
	url := os.Getenv("REDLINE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("REDLINE_TEST_DATABASE_URL tidak diset; test database dilewati")
	}
	storetest.RunProofs(t, func(t *testing.T) storetest.ProofBackend {
		ctx := context.Background()
		s, err := store.Open(ctx, store.TestSchema(ctx, t, url, "store_parity_test"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		return s
	})
}

// TestParity_Memory menjalankan test kontrak, kasus mirip, dan retensi yang sama dengan implementasi Neo4j.
func TestParity_Memory(t *testing.T) {
	url := os.Getenv("REDLINE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("REDLINE_TEST_DATABASE_URL tidak diset; test database dilewati")
	}
	storetest.RunMemory(t, func(t *testing.T) storetest.MemoryBackend {
		ctx := context.Background()
		s, err := store.Open(ctx, store.TestSchema(ctx, t, url, "store_parity_test"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		return s
	})
}

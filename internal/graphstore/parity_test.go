package graphstore_test

import (
	"context"
	"testing"

	"redline/internal/graphstore"
	"redline/internal/storetest"
)

// TestParity_Ingest menjalankan test ingest yang sama dengan versi Postgres (lihat internal/storetest).
func TestParity_Ingest(t *testing.T) {
	storetest.RunIngest(t, func(t *testing.T, sources []string) storetest.Backend {
		s := graphstore.TestStore(context.Background(), t, "redlinetest-parity")
		s.StatusSources = sources
		return s
	})
}

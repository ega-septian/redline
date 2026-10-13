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

// TestParity_Analysis menjalankan test fakta, analisis, dan label yang sama dengan versi Postgres.
func TestParity_Analysis(t *testing.T) {
	storetest.RunAnalysis(t, func(t *testing.T) storetest.AnalysisBackend {
		return graphstore.TestStore(context.Background(), t, "redlinetest-parity")
	})
}

// TestParity_Proofs menjalankan test eksperimen, rapor, dan aturan yang sama dengan versi Postgres.
func TestParity_Proofs(t *testing.T) {
	storetest.RunProofs(t, func(t *testing.T) storetest.ProofBackend {
		return graphstore.TestStore(context.Background(), t, "redlinetest-parity")
	})
}
